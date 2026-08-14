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
)

func init() {
	Registry.MustRegister(
		HTTPRequestsTotal, HTTPRequestDuration,
		HTTPClientRequestsTotal, HTTPClientRequestDuration,
		JobLimiterTotal, HardcoverCallsTotal, AmazonCallsTotal,
	)
	// Runtime + process collectors give the standard go_* / process_* series
	// (goroutines, GC, heap, open FDs, CPU) for free — the baseline of any
	// service dashboard.
	Registry.MustRegister(collectors.NewGoCollector())
	Registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
}

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
