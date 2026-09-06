// redis_relay.go — cross-pod log relay for the split worker topology.
//
// Since the worker-topology cutover, image-job processing runs in a SEPARATE
// boomtime-worker pod; that pod's own LogHub (built by logging.Setup like
// every process's) already collects its slog records locally, but nothing
// ever reads it — role=worker binds no HTTP API, so the Admin Logs viewer
// only ever saw the server pod's logs. RelayHubToRedis fans a worker pod's
// hub entries out over Dragonfly/Redis pub/sub; SubscribeRedisIntoHub is the
// server-side counterpart that injects them into the server's own LogHub, so
// the existing Admin Logs WS (see internal/meta/logs.go) picks them up for
// free with no protocol change — only a new "source" field on LogEntry.
//
// Mirrors imagejobs.RedisEventBus (internal/queue/imagejobs/redis_bus.go):
// same JSON-over-pub/sub shape, same "malformed frame is dropped, never
// crashes the relay" contract. Kept in this package rather than imagejobs
// because it's a LogHub concern, not an image-job one.
//
// CURRENTLY UNWIRED (audit 2026-09-06). The cmd/boomtime/main.go wiring this
// file used to describe was deleted along with the RabbitMQ broker arm in
// 08f2ecc, and the BrokerRabbit() gate it referenced no longer exists on
// config.Config. Nothing in the repo calls RelayHubToRedis or
// SubscribeRedisIntoHub, so worker-pod logs do NOT reach the Admin Logs
// viewer today — do not read the paragraph below as a description of running
// behaviour. The pair is kept (rather than deleted) because the split
// topology it was written for IS deployed (--role=worker + the KEDA drain
// pods), so re-wiring it is a live option; the self-feedback hazard that
// would have made that re-wiring dangerous is fixed in publishFailureLogger
// below.
//
// WHEN RE-WIRED, gate strictly on role=="worker" / role=="server" — NOT the
// inclusive IsWorkerRole()/IsServerRole() helpers, which also match
// role="all". Under role="all" a single process IS both server and worker
// sharing one LogHub already; relaying through Redis on top of that would
// inject every record a second time.
package logging

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// LogsChannel is the Dragonfly/Redis pub/sub channel worker log records
// travel on — sibling to imagejobs.EventsChannel.
const LogsChannel = "boomtime:logs"

// redisPublisher is the subset of *redis.Client RelayHubToRedis needs.
// Narrowed to an interface so tests can substitute a fake and exercise the
// publish path without a live Redis/Dragonfly instance.
type redisPublisher interface {
	Publish(ctx context.Context, channel string, message interface{}) *redis.IntCmd
}

// RelayHubToRedis subscribes to hub's entries and best-effort PUBLISHes each
// one, JSON-encoded, on LogsChannel. Every outgoing record is stamped
// Source="worker" and Host=hostID regardless of what was already set — this
// is the only place that tags a record "worker" before it hits the wire.
//
// Subscribes to the hub BEFORE reading its backfill (same ordering
// ServerLogsWS uses) so no live entry is missed in the gap; the ring-buffer
// tail is then sent first so early boot lines (e.g. "migrations applied",
// "labelimages worker enabled") aren't lost just because this goroutine
// started after they were logged. Runs until ctx is cancelled.
//
// A publish error is dropped (and reported at most once per
// publishFailureLogInterval — see publishFailureLogger for why the rate limit
// is load-bearing rather than cosmetic): a Redis hiccup must never slow or
// block the worker's own logging path, the same non-blocking contract
// LogHub.Publish itself keeps for a stalled WS subscriber.
func RelayHubToRedis(ctx context.Context, hub *LogHub, rdb redisPublisher, hostID string) {
	sub := hub.Subscribe()
	defer hub.Unsubscribe(sub)

	reportFailure := publishFailureLogger()
	publish := func(e LogEntry) {
		e.Source = "worker"
		e.Host = hostID
		body, err := json.Marshal(e)
		if err != nil {
			return
		}
		if perr := rdb.Publish(ctx, LogsChannel, body).Err(); perr != nil {
			reportFailure(perr)
		}
	}

	for _, e := range hub.Backfill(0) {
		publish(e)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub:
			if !ok {
				return
			}
			publish(e)
		}
	}
}

// publishFailureLogInterval bounds how often a failing relay may log about it.
// One line a minute is enough to diagnose "Redis is down"; see
// publishFailureLogger for why any higher rate is unsafe.
const publishFailureLogInterval = time.Minute

// publishFailureLogger returns a rate-limited reporter for Redis publish
// failures, and it exists to break a SELF-FEEDBACK LOOP, not to reduce noise.
//
// The relay subscribes to the process LogHub. The process's default slog
// logger is a teeHandler that publishes EVERY record it handles into that same
// hub. So a bare slog.Warn on a failed publish is re-delivered to the relay,
// which tries to publish it, which fails (Redis is still down), which logs
// another Warn — a tight unbounded loop that pins a CPU and floods stdout for
// as long as Redis is unreachable. Rate-limiting the log caps the cycle: the
// one warn that does get emitted costs exactly one extra (failed) publish, and
// every re-entrant record for the next interval is dropped silently, so the
// loop terminates instead of spinning.
//
// Returned closure is used from a single goroutine, but the mutex keeps it
// safe if a future caller fans out.
func publishFailureLogger() func(error) {
	var (
		mu   sync.Mutex
		last time.Time
	)
	return func(err error) {
		mu.Lock()
		now := time.Now()
		if !last.IsZero() && now.Sub(last) < publishFailureLogInterval {
			mu.Unlock()
			return
		}
		last = now
		mu.Unlock()
		slog.Warn("logging: redis log publish failed (further failures suppressed for a minute)", "err", err)
	}
}

// decodeWorkerLogRecord parses one LogsChannel payload into a LogEntry,
// forcing Source="worker" regardless of the wire value. Only worker pods
// ever publish on this channel, but Source drives a UI trust cue (which pod
// to blame for a line), so this stays defense-in-depth against a payload
// that omitted or misreported the field rather than trusting the wire.
// Returns ok=false on malformed JSON; callers drop the record.
func decodeWorkerLogRecord(payload []byte) (LogEntry, bool) {
	var e LogEntry
	if err := json.Unmarshal(payload, &e); err != nil {
		return LogEntry{}, false
	}
	e.Source = "worker"
	return e, true
}

// SubscribeRedisIntoHub subscribes to LogsChannel and injects every
// well-formed record into hub via decodeWorkerLogRecord. A malformed frame
// is dropped rather than tearing down the subscriber — same contract as
// imagejobs.RedisEventBus.Subscribe. Runs until ctx is cancelled; go-redis
// reconnects the underlying subscription transparently on a dropped
// connection, so this never needs its own retry loop.
func SubscribeRedisIntoHub(ctx context.Context, hub *LogHub, rdb *redis.Client) {
	psub := rdb.Subscribe(ctx, LogsChannel)
	defer psub.Close()
	ch := psub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if e, ok := decodeWorkerLogRecord([]byte(msg.Payload)); ok {
				hub.Publish(e)
			}
		}
	}
}
