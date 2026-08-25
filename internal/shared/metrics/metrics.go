// Package metrics is boomtime's Prometheus instrumentation layer. It owns a
// single process-global *prometheus.Registry that every collector registers
// into and that GET /metrics serves in the Prometheus text exposition format
// (scraped by the cluster Prometheus → Grafana). There is no bespoke in-memory
// time-series store any more: Prometheus owns retention + rate math; boomtime
// only exposes instantaneous counters/histograms.
//
// The two GENERIC RED decorators live here:
//
//   - Incoming (server router): the http_requests_total counter +
//     http_request_duration_seconds histogram, fed by internal/server's
//     metricsMiddleware for EVERY served request (labelled by matched route
//     template, not raw path, to bound cardinality).
//   - Outgoing (every external HTTP client): InstrumentTransport wraps an
//     http.RoundTripper so http_client_requests_total +
//     http_client_request_duration_seconds are recorded for every outbound
//     request, labelled by target host.
//
// On top of the generic transport metrics we keep a handful of SEMANTIC
// business counters (job-limiter outcomes, hardcover dry-run vs executed,
// amazon signed vs cookie) — dimensions the raw transport can't see.
//
// Everything registers into Registry (a dedicated registry, NOT the global
// prometheus.DefaultRegisterer) so the scrape output is limited to boomtime's
// own series plus the Go/process runtime collectors, and so the admin UI can
// Gather() a known set.
package metrics

import (
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the process-global Prometheus registry. Serve it at /metrics via
// Handler(); Gather() it for the admin UI view.
var Registry = prometheus.NewRegistry()

// ── Incoming RED (server router) ─────────────────────────────────────────────
//
// Cardinality is bounded by design: `route` is the matched echo route TEMPLATE
// (e.g. /p/:slug, never /p/abc), `method` is the small HTTP-verb set, and
// `status_class` is one of 1xx..5xx. Health/scrape probes are skipped upstream.
var (
	HTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests handled by the server router, by method, matched route template, and status class.",
	}, []string{"method", "route", "status_class"})

	HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP server request latency in seconds, by method and matched route template.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
)

// ── Outgoing RED (every external HTTP client) ────────────────────────────────
//
// Labelled by target HOST — boomtime's upstreams are a small fixed set
// (wakatime.com, api.github.com, api.audible.*, hardcover.app, the comfyui
// shim, the OIDC issuer), so host cardinality stays bounded. A transport-level
// failure (no response) is recorded with status class "error".
var (
	HTTPClientRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_client_requests_total",
		Help: "Total outbound HTTP requests, by target host, method, and status class.",
	}, []string{"host", "method", "status_class"})

	HTTPClientRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_client_request_duration_seconds",
		Help:    "Outbound HTTP request latency in seconds, by target host.",
		Buckets: prometheus.DefBuckets,
	}, []string{"host"})
)

// ── Semantic business counters (layered on top of the transport metrics) ─────
var (
	// JobLimiterTotal counts background-job concurrency-limiter events per kind
	// and outcome. Only limited kinds (max>0) reach the limiter, so `kind`
	// cardinality is bounded by the registered limited kinds.
	//   outcome=acquired — a slot was reserved (throughput)
	//   outcome=atlimit  — the kind was at its fleet-wide cap; job requeued (back-pressure)
	//   outcome=error    — the limiter's broker/Redis call failed
	JobLimiterTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_limiter_events_total",
		Help: "Background-job concurrency-limiter events by kind and outcome (acquired|atlimit|error).",
	}, []string{"kind", "outcome"})

	// HardcoverCallsTotal counts Hardcover GraphQL calls by outcome
	// (executed | dryrun_blocked). The generic transport metric already counts
	// the wire calls; this adds the dry-run dimension the transport can't see.
	HardcoverCallsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hardcover_calls_total",
		Help: "Hardcover GraphQL calls by outcome (executed|dryrun_blocked).",
	}, []string{"outcome"})

	// AmazonCallsTotal counts Amazon/Audible/Kindle calls by the transport used
	// (signed device-cred vs cookie Cloud-Reader). Complements the generic
	// per-host outbound metric with the boomtime-side transport dimension.
	AmazonCallsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "amazon_calls_total",
		Help: "Amazon/Audible/Kindle calls by boomtime transport (signed|cookie).",
	}, []string{"transport"})

	// HeartbeatsIngestedTotal counts heartbeats successfully persisted by the
	// ingest hot path — the app's core throughput signal. Unlabelled to keep it
	// a single cheap series (per-owner would be unbounded).
	HeartbeatsIngestedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "boomtime_heartbeats_ingested_total",
		Help: "Total coding heartbeats persisted via the ingest endpoint.",
	})

	// JobsRunTotal counts background-job terminal outcomes by kind and status
	// (done|failed|retry|cancelled). `kind` is bounded by the registered job
	// kinds.
	JobsRunTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_run_total",
		Help: "Background jobs executed, by kind and terminal status (done|failed|retry|cancelled).",
	}, []string{"kind", "status"})

	// JobLimiterInflight is THIS pod's current in-flight count for a limited
	// kind (incremented on a successful slot acquire, decremented on release).
	// Paired with JobLimiterMax it shows headroom vs the fleet-wide cap.
	JobLimiterInflight = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "jobs_limiter_in_flight",
		Help: "In-flight concurrency slots held by this pod, by kind.",
	}, []string{"kind"})

	// JobLimiterMax is the configured fleet-wide concurrency cap for a kind
	// (the denominator for headroom).
	JobLimiterMax = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "jobs_limiter_max",
		Help: "Configured fleet-wide concurrency cap, by kind.",
	}, []string{"kind"})

	// HTTPRatelimitDecisionsTotal counts the API-level (per-user/per-IP) HTTP
	// rate limiter's decisions. This makes throttling visible AS A LIMITER,
	// distinct from the generic http_requests_total 429s.
	//   decision = allowed | throttled
	//   scope    = user | ip   (which bucket keyed the decision)
	HTTPRatelimitDecisionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_ratelimit_decisions_total",
		Help: "API rate-limiter decisions, by decision (allowed|throttled) and bucket scope (user|ip).",
	}, []string{"decision", "scope"})

	// CacheRequestsTotal counts in-memory TTL-cache lookups by cache name and
	// result (hit|miss) — cache effectiveness. `cache` is bounded by the small
	// set of named caches.
	CacheRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cache_requests_total",
		Help: "In-memory TTL cache lookups, by cache name and result (hit|miss).",
	}, []string{"cache", "result"})

	// WSActiveConnections is the current number of open websocket streams by
	// stream type (logs, jobs, notify, …) — incremented on accept, decremented
	// on close.
	WSActiveConnections = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ws_active_connections",
		Help: "Currently-open websocket connections, by stream type.",
	}, []string{"stream"})

	// AMQPDeliveriesTotal counts RabbitMQ job deliveries handled by the AMQP
	// provider's consume loop, by queue and outcome (processed|requeued). Only
	// bumped when the rabbitmq jobs provider is active — absent (no series) on
	// the default local provider. Live queue DEPTH is already exported by the
	// RabbitMQ operator's own scrape (rabbitmq PodMonitor); this is the
	// app-side consume signal on top of it.
	AMQPDeliveriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "boomtime_amqp_deliveries_total",
		Help: "RabbitMQ job deliveries handled by the AMQP provider, by queue and outcome (processed|requeued).",
	}, []string{"queue", "outcome"})
)

// ── Persistent reading-monitor (catalyst-books §5.1) ─────────────────────────
//
// The server-side two-level Kindle reading-monitor's cadence signals. A Grafana
// agent BUILDS TO THESE NAMES — treat them as a PINNED contract; if one must
// change, flag it loudly. The only label is `source` (currently just "kindle"),
// bounded by the small fixed set of reading sources — NEVER per-title/ASIN (that
// would explode cardinality: a library is unbounded).
//
// advance_interval_seconds + sec_per_location share the same explicit buckets
// (5,15,30,60,120,300,600 s) so the panels align — these are the empirical
// cadence numbers the design's T1/T2/G recommendation reads off (§5.1).
var (
	// ReadingMonitorAdvancesTotal counts detected last-page-read advances (a
	// book's furthest position moved forward), by source. The raw "reading is
	// happening" event rate.
	ReadingMonitorAdvancesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "boomtime_reading_monitor_advances_total",
		Help: "Detected reading-position advances observed by the persistent monitor, by source.",
	}, []string{"source"})

	// ReadingMonitorAdvanceInterval is the wall-clock gap (seconds) between two
	// CONSECUTIVE advances of the same book — the intra-session cadence the fine
	// L2 poll exists to capture. Observed only when a prior advance exists.
	ReadingMonitorAdvanceInterval = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "boomtime_reading_monitor_advance_interval_seconds",
		Help:    "Seconds between consecutive advances of the same book, by source.",
		Buckets: []float64{5, 15, 30, 60, 120, 300, 600},
	}, []string{"source"})

	// ReadingMonitorActiveBooks is the current count of books in L2 (fine
	// capture) across all users, by source — the fleet-wide "actively being read
	// right now" gauge (set each pass).
	ReadingMonitorActiveBooks = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "boomtime_reading_monitor_active_books",
		Help: "Books currently in fine-capture (L2, actively advancing) across all users, by source.",
	}, []string{"source"})

	// ReadingMonitorSecPerLocation is the implied seconds-per-location-unit for an
	// advance (advance interval / Δlocation) — the "reading speed" signal that
	// turns an opaque location delta into a time estimate. Observed only when a
	// prior advance exists and Δlocation > 0.
	ReadingMonitorSecPerLocation = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "boomtime_reading_monitor_sec_per_location",
		Help:    "Implied seconds per location unit (advance interval / Δlocation), by source.",
		Buckets: []float64{5, 15, 30, 60, 120, 300, 600},
	}, []string{"source"})

	// ReadingActivitySecondsTotal counts reading-seconds LANDED into
	// reading_activity by source — the high-level Domain-board throughput metric.
	// Incremented per newly-observed in-session advance interval (the seconds
	// composeSessions attributes to that pair), so it stays MONOTONIC despite the
	// idempotent bucket overwrite of UpsertReadingActivity.
	ReadingActivitySecondsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "boomtime_reading_activity_seconds_total",
		Help: "Reading-seconds landed into reading_activity, by source (the Domain-board throughput metric).",
	}, []string{"source"})
)

func init() {
	Registry.MustRegister(
		HTTPRequestsTotal, HTTPRequestDuration,
		HTTPClientRequestsTotal, HTTPClientRequestDuration,
		JobLimiterTotal, HardcoverCallsTotal, AmazonCallsTotal,
		HeartbeatsIngestedTotal, JobsRunTotal,
		JobLimiterInflight, JobLimiterMax, JobDurationSeconds,
		HTTPRatelimitDecisionsTotal, CacheRequestsTotal,
		WSActiveConnections, AMQPDeliveriesTotal,
		ReadingMonitorAdvancesTotal, ReadingMonitorAdvanceInterval,
		ReadingMonitorActiveBooks, ReadingMonitorSecPerLocation,
		ReadingActivitySecondsTotal,
	)
	// Runtime + process collectors give the standard go_* / process_* series
	// (goroutines, GC, heap, open FDs, CPU) for free — the baseline of any
	// service dashboard.
	Registry.MustRegister(collectors.NewGoCollector())
	Registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	// Scrape-time pool collectors (DB + Redis). Unchecked collectors: they emit
	// nothing until a provider is wired via RegisterDBPool / RegisterRedisPool
	// (done from cmd at boot), so they're inert in tests that never register.
	Registry.MustRegister(&dbPoolCollector{}, &redisPoolCollector{}, &amqpQueueCollector{}, &jobQueueCollector{}, &jobOutcomeCollector{})
}

// RegisterBuildInfo publishes a boomtime_build_info{version,commit,go_version}
// gauge fixed at 1 (the Prometheus build-info idiom — the value is constant, the
// facts live in labels). Call once at startup from cmd with the ldflags-stamped
// Config values. Idempotent: a duplicate registration is ignored.
func RegisterBuildInfo(version, commit string) {
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "boomtime_build_info",
		Help: "Boomtime build metadata (constant 1; version/commit/go_version in labels).",
		ConstLabels: prometheus.Labels{
			"version":    nonEmpty(version),
			"commit":     nonEmpty(commit),
			"go_version": runtime.Version(),
		},
	})
	g.Set(1)
	_ = Registry.Register(g) // ignore AlreadyRegisteredError (idempotent)
}

func nonEmpty(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// ── DB pool collector (pgx) ──────────────────────────────────────────────────
//
// DBPoolSample mirrors *pgxpool.Stat so this package stays free of the pgx
// dependency; cmd adapts pool.Stat() into it. Registered from cmd (not db.New)
// so unit tests — which open + close many isolated pools — never scrape a
// closed pool.
type DBPoolSample struct {
	AcquiredConns          int32
	IdleConns              int32
	TotalConns             int32
	MaxConns               int32
	ConstructingConns      int32
	AcquireCount           int64
	CanceledAcquireCount   int64
	EmptyAcquireCount      int64
	AcquireDurationSeconds float64
}

var (
	dbPoolMu       sync.RWMutex
	dbPoolProvider func() DBPoolSample
)

// RegisterDBPool wires a scrape-time reader of the pgx pool's Stat(). Pass nil
// to clear it (test cleanup). Only the most recently registered provider is
// read.
func RegisterDBPool(sample func() DBPoolSample) {
	dbPoolMu.Lock()
	dbPoolProvider = sample
	dbPoolMu.Unlock()
}

var (
	dbAcquiredDesc     = prometheus.NewDesc("db_pool_acquired_conns", "Connections currently acquired from the pool.", nil, nil)
	dbIdleDesc         = prometheus.NewDesc("db_pool_idle_conns", "Idle connections in the pool.", nil, nil)
	dbTotalDesc        = prometheus.NewDesc("db_pool_total_conns", "Total connections in the pool (acquired + idle + constructing).", nil, nil)
	dbMaxDesc          = prometheus.NewDesc("db_pool_max_conns", "Maximum size of the pool.", nil, nil)
	dbConstructingDesc = prometheus.NewDesc("db_pool_constructing_conns", "Connections currently being constructed.", nil, nil)
	dbAcquireCntDesc   = prometheus.NewDesc("db_pool_acquire_count", "Cumulative successful acquires from the pool.", nil, nil)
	dbAcquireDurDesc   = prometheus.NewDesc("db_pool_acquire_duration_seconds_total", "Cumulative time spent blocking on acquires.", nil, nil)
	dbCanceledDesc     = prometheus.NewDesc("db_pool_canceled_acquire_count", "Cumulative acquires canceled by context.", nil, nil)
	dbEmptyDesc        = prometheus.NewDesc("db_pool_empty_acquire_count", "Cumulative acquires that had to wait for a new/free conn.", nil, nil)
)

type dbPoolCollector struct{}

// Describe sends no descriptors → an "unchecked" collector, so Collect may emit
// nothing when no provider is wired.
func (*dbPoolCollector) Describe(chan<- *prometheus.Desc) {}

func (*dbPoolCollector) Collect(ch chan<- prometheus.Metric) {
	dbPoolMu.RLock()
	p := dbPoolProvider
	dbPoolMu.RUnlock()
	if p == nil {
		return
	}
	s := p()
	g := func(d *prometheus.Desc, v float64) { ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v) }
	c := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}
	g(dbAcquiredDesc, float64(s.AcquiredConns))
	g(dbIdleDesc, float64(s.IdleConns))
	g(dbTotalDesc, float64(s.TotalConns))
	g(dbMaxDesc, float64(s.MaxConns))
	g(dbConstructingDesc, float64(s.ConstructingConns))
	c(dbAcquireCntDesc, float64(s.AcquireCount))
	c(dbAcquireDurDesc, s.AcquireDurationSeconds)
	c(dbCanceledDesc, float64(s.CanceledAcquireCount))
	c(dbEmptyDesc, float64(s.EmptyAcquireCount))
}

// ── Redis/Dragonfly pool collector (go-redis) ────────────────────────────────
//
// RedisPoolSample mirrors *redis.PoolStats so this package stays free of the
// go-redis dependency; cmd adapts client.PoolStats() into it. Multiple clients
// register under a bounded `purpose` label (limiter, logs, …).
type RedisPoolSample struct {
	Hits       uint32
	Misses     uint32
	Timeouts   uint32
	TotalConns uint32
	IdleConns  uint32
	StaleConns uint32
}

var (
	redisMu        sync.RWMutex
	redisProviders = map[string]func() RedisPoolSample{}
)

// RegisterRedisPool wires a scrape-time reader of a go-redis client's
// PoolStats() under a bounded purpose label. Pass nil to remove it.
func RegisterRedisPool(purpose string, sample func() RedisPoolSample) {
	redisMu.Lock()
	if sample == nil {
		delete(redisProviders, purpose)
	} else {
		redisProviders[purpose] = sample
	}
	redisMu.Unlock()
}

var (
	redisHitsDesc    = prometheus.NewDesc("redis_pool_hits_total", "Free connection found in the pool.", []string{"purpose"}, nil)
	redisMissesDesc  = prometheus.NewDesc("redis_pool_misses_total", "No free connection found in the pool.", []string{"purpose"}, nil)
	redisTimeoutDesc = prometheus.NewDesc("redis_pool_timeouts_total", "Waits for a connection that timed out.", []string{"purpose"}, nil)
	redisTotalDesc   = prometheus.NewDesc("redis_pool_total_conns", "Total connections in the pool.", []string{"purpose"}, nil)
	redisIdleDesc    = prometheus.NewDesc("redis_pool_idle_conns", "Idle connections in the pool.", []string{"purpose"}, nil)
	redisStaleDesc   = prometheus.NewDesc("redis_pool_stale_conns", "Stale connections removed from the pool.", []string{"purpose"}, nil)
)

type redisPoolCollector struct{}

func (*redisPoolCollector) Describe(chan<- *prometheus.Desc) {}

func (*redisPoolCollector) Collect(ch chan<- prometheus.Metric) {
	redisMu.RLock()
	providers := make(map[string]func() RedisPoolSample, len(redisProviders))
	for k, v := range redisProviders {
		providers[k] = v
	}
	redisMu.RUnlock()
	for purpose, p := range providers {
		s := p()
		ch <- prometheus.MustNewConstMetric(redisHitsDesc, prometheus.CounterValue, float64(s.Hits), purpose)
		ch <- prometheus.MustNewConstMetric(redisMissesDesc, prometheus.CounterValue, float64(s.Misses), purpose)
		ch <- prometheus.MustNewConstMetric(redisTimeoutDesc, prometheus.CounterValue, float64(s.Timeouts), purpose)
		ch <- prometheus.MustNewConstMetric(redisTotalDesc, prometheus.GaugeValue, float64(s.TotalConns), purpose)
		ch <- prometheus.MustNewConstMetric(redisIdleDesc, prometheus.GaugeValue, float64(s.IdleConns), purpose)
		ch <- prometheus.MustNewConstMetric(redisStaleDesc, prometheus.CounterValue, float64(s.StaleConns), purpose)
	}
}

// ── RabbitMQ queue collector (AMQP, conditional) ─────────────────────────────
//
// AMQPQueueSample is one queue's live depth + consumer count, read at scrape
// time via QueueDeclarePassive. Registered ONLY when the rabbitmq jobs provider
// is wired (broker=rabbitmq); the deployed default local provider never
// registers, so these series are simply absent — a clean no-op, not an error.
//
// The provider func returns ok=false on any transient failure (connection
// closed mid-shutdown, passive-declare error) so a scrape degrades to "no
// sample" instead of panicking Gather. Note QueueDeclarePassive exposes the
// READY message count + consumers; the ready/unacked split is only in the
// RabbitMQ management API, already scraped via the operator's own PodMonitor —
// so state is fixed to "ready" here.
type AMQPQueueSample struct {
	Messages  int
	Consumers int
}

var (
	amqpMu        sync.RWMutex
	amqpProviders = map[string]func() (AMQPQueueSample, bool){}
)

// RegisterAMQPQueue wires a scrape-time reader of one queue's depth/consumers.
// Pass nil to remove it. Guard the provider to return ok=false when the broker
// is unreachable so scrapes never fail.
func RegisterAMQPQueue(queue string, sample func() (AMQPQueueSample, bool)) {
	amqpMu.Lock()
	if sample == nil {
		delete(amqpProviders, queue)
	} else {
		amqpProviders[queue] = sample
	}
	amqpMu.Unlock()
}

var (
	amqpMessagesDesc  = prometheus.NewDesc("rabbitmq_queue_messages", "Messages in the queue by state (ready via passive declare).", []string{"queue", "state"}, nil)
	amqpConsumersDesc = prometheus.NewDesc("rabbitmq_queue_consumers", "Consumers currently attached to the queue.", []string{"queue"}, nil)
)

type amqpQueueCollector struct{}

func (*amqpQueueCollector) Describe(chan<- *prometheus.Desc) {}

func (*amqpQueueCollector) Collect(ch chan<- prometheus.Metric) {
	amqpMu.RLock()
	providers := make(map[string]func() (AMQPQueueSample, bool), len(amqpProviders))
	for k, v := range amqpProviders {
		providers[k] = v
	}
	amqpMu.RUnlock()
	for queue, p := range providers {
		s, ok := p()
		if !ok {
			continue // broker unreachable this scrape — skip, don't fail Gather
		}
		ch <- prometheus.MustNewConstMetric(amqpMessagesDesc, prometheus.GaugeValue, float64(s.Messages), queue, "ready")
		ch <- prometheus.MustNewConstMetric(amqpConsumersDesc, prometheus.GaugeValue, float64(s.Consumers), queue)
	}
}

// --- job queue depth (boom-piig) -------------------------------------------
//
// The gap this closes: there was a scrape-time depth reader for the RabbitMQ
// queue but NONE for the Postgres `jobs` table — which is the queue actually in
// use. A 300-deep liberation backlog was completely invisible in Grafana while
// the queue we are deleting had a depth gauge. Backlog is the single most
// alertable property of a job system, so it gets a first-class metric.
//
// Scrape-time rather than event-counted, for the same reason the DB-pool and
// AMQP collectors are: depth is a LEVEL, not a rate. Counting enqueues and
// dequeues and subtracting would drift on every crash, reap and manual delete.

// JobQueueSample is the queue's live state for one kind, read at scrape time.
type JobQueueSample struct {
	// Queued is rows due to run now (status='queued' AND run_at <= now()).
	Queued int
	// Scheduled is rows queued for the FUTURE (run_at > now()). Split out so a
	// backlog alert is not tripped by work that is merely scheduled.
	Scheduled int
	// Running is rows currently claimed.
	Running int
	// OldestQueuedAge is how long the oldest DUE row has waited. This is the
	// staleness signal — depth alone cannot distinguish "1000 jobs draining
	// briskly" from "3 jobs wedged for an hour", and the second is the outage.
	OldestQueuedAge time.Duration
}

var (
	jobQueueMu       sync.RWMutex
	jobQueueProvider func() (map[string]JobQueueSample, bool)
)

// RegisterJobQueue wires a scrape-time reader of per-kind queue depth. Pass nil
// to remove it. The provider must return ok=false when the DB is unreachable so
// a scrape degrades rather than failing Gather.
func RegisterJobQueue(sample func() (map[string]JobQueueSample, bool)) {
	jobQueueMu.Lock()
	jobQueueProvider = sample
	jobQueueMu.Unlock()
}

var (
	jobQueueDepthDesc = prometheus.NewDesc("jobs_queue_depth",
		"Jobs in the Postgres queue by kind and state (queued=due now, scheduled=future, running).",
		[]string{"kind", "state"}, nil)
	jobQueueOldestDesc = prometheus.NewDesc("jobs_queue_oldest_seconds",
		"Age of the oldest DUE queued job, by kind. The staleness signal — depth alone cannot distinguish a brisk backlog from a wedged one.",
		[]string{"kind"}, nil)
)

type jobQueueCollector struct{}

func (*jobQueueCollector) Describe(chan<- *prometheus.Desc) {}

func (*jobQueueCollector) Collect(ch chan<- prometheus.Metric) {
	jobQueueMu.RLock()
	p := jobQueueProvider
	jobQueueMu.RUnlock()
	if p == nil {
		return
	}
	byKind, ok := p()
	if !ok {
		return // DB unreachable this scrape — skip, don't fail Gather
	}
	for kind, s := range byKind {
		ch <- prometheus.MustNewConstMetric(jobQueueDepthDesc, prometheus.GaugeValue, float64(s.Queued), kind, "queued")
		ch <- prometheus.MustNewConstMetric(jobQueueDepthDesc, prometheus.GaugeValue, float64(s.Scheduled), kind, "scheduled")
		ch <- prometheus.MustNewConstMetric(jobQueueDepthDesc, prometheus.GaugeValue, float64(s.Running), kind, "running")
		ch <- prometheus.MustNewConstMetric(jobQueueOldestDesc, prometheus.GaugeValue, s.OldestQueuedAge.Seconds(), kind)
	}
}

// JobOutcomeSample is one kind's recent terminal activity, read at scrape time.
type JobOutcomeSample struct {
	// ByStatus counts jobs that reached each terminal status in the window.
	ByStatus map[string]int
	// P50 / P95 are duration percentiles over completed jobs in the window.
	P50, P95 time.Duration
}

var (
	jobOutcomeMu       sync.RWMutex
	jobOutcomeProvider func() (map[string]JobOutcomeSample, bool)
)

// RegisterJobOutcomes wires a scrape-time reader of recent per-kind terminal
// outcomes and durations, derived from the jobs table.
//
// WHY FROM SQL RATHER THAN IN-PROCESS COUNTERS. Most job execution now happens
// on KEDA ScaledJob drain pods, which deliberately run no HTTP server (see the
// drain branch in cmd/boomtime) and are short-lived — a pod that lives 90
// seconds may never be scraped at all. So jobs_run_total and
// jobs_duration_seconds, which are per-process, systematically undercount
// exactly the work that matters most.
//
// Reading the jobs table instead gives COMPLETE coverage regardless of which pod
// ran the job, from the always-up server. The tradeoff is honest and worth
// stating: these are WINDOWED GAUGES, not counters and histograms. Do not
// rate() them. They answer "what happened recently" rather than "how much ever".
func RegisterJobOutcomes(sample func() (map[string]JobOutcomeSample, bool)) {
	jobOutcomeMu.Lock()
	jobOutcomeProvider = sample
	jobOutcomeMu.Unlock()
}

var (
	jobRecentDesc = prometheus.NewDesc("jobs_recent_completions",
		"Jobs reaching a terminal status in the recent window, by kind and status. A WINDOWED GAUGE derived from the jobs table — do not rate() it.",
		[]string{"kind", "status"}, nil)
	jobRecentDurDesc = prometheus.NewDesc("jobs_recent_duration_seconds",
		"Duration percentile over jobs completed in the recent window, by kind. A WINDOWED GAUGE — do not use histogram_quantile on it.",
		[]string{"kind", "quantile"}, nil)
)

type jobOutcomeCollector struct{}

func (*jobOutcomeCollector) Describe(chan<- *prometheus.Desc) {}

func (*jobOutcomeCollector) Collect(ch chan<- prometheus.Metric) {
	jobOutcomeMu.RLock()
	p := jobOutcomeProvider
	jobOutcomeMu.RUnlock()
	if p == nil {
		return
	}
	byKind, ok := p()
	if !ok {
		return // DB unreachable this scrape — skip, don't fail Gather
	}
	for kind, s := range byKind {
		for status, n := range s.ByStatus {
			ch <- prometheus.MustNewConstMetric(jobRecentDesc, prometheus.GaugeValue, float64(n), kind, status)
		}
		ch <- prometheus.MustNewConstMetric(jobRecentDurDesc, prometheus.GaugeValue, s.P50.Seconds(), kind, "0.5")
		ch <- prometheus.MustNewConstMetric(jobRecentDurDesc, prometheus.GaugeValue, s.P95.Seconds(), kind, "0.95")
	}
}

// JobDurationSeconds observes how long each job took. dur_ms was already in the
// structured log but never exported, so there was no p50/p95 — and "jobs are
// slow" is not answerable from a counter.
var JobDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name: "jobs_duration_seconds",
	Help: "Wall-clock duration of a job run, by kind and terminal status.",
	// Buckets span the real range: a books-liberate-sweep finishes in
	// milliseconds, a liberation download+remux takes minutes. Default buckets
	// top out at 10s and would put every liberation in +Inf.
	Buckets: []float64{0.1, 0.5, 1, 5, 15, 60, 180, 600, 1800},
}, []string{"kind", "status"})

// Handler returns the promhttp handler that serves Registry in the Prometheus
// text exposition format. Mount it at GET /metrics (unauthenticated, off the
// rate-limit + request-log paths — standard for intra-cluster scrape).
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}

// StatusClass maps an HTTP status code to its class label. A code of 0 (a
// transport error before any response) maps to "error", so a failed dial is
// still counted with bounded cardinality rather than exploding label space.
func StatusClass(code int) string {
	switch {
	case code >= 100 && code < 200:
		return "1xx"
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	default:
		return "error"
	}
}

// instrumentedTransport wraps a base RoundTripper and records the generic
// outgoing RED metrics for every request it carries. It never mutates the
// request and always returns the base's (response, error) verbatim —
// instrumentation must not alter outcomes.
type instrumentedTransport struct {
	base http.RoundTripper
}

func (t *instrumentedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	HTTPClientRequestDuration.WithLabelValues(host).Observe(time.Since(start).Seconds())
	code := 0
	if resp != nil {
		code = resp.StatusCode
	}
	HTTPClientRequestsTotal.WithLabelValues(host, req.Method, StatusClass(code)).Inc()
	return resp, err
}

// InstrumentTransport wraps base so every request it carries is recorded in the
// outgoing RED metrics. base==nil defaults to http.DefaultTransport. This is
// the "outgoing decorator": wire every external http.Client through it, either
// directly —
//
//	&http.Client{Transport: metrics.InstrumentTransport(nil)}
//
// or by layering it UNDER an existing transport (set that transport's base to
// the instrumented one) so header-mangling wrappers (UA setters, etc.) stay in
// place while the wire call is still measured.
func InstrumentTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &instrumentedTransport{base: base}
}
