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
// Wiring (cmd/boomtime/main.go) gates strictly on role=="worker" /
// role=="server" — NOT the inclusive IsWorkerRole()/IsServerRole() helpers,
// which also match role="all". Under role="all" a single process IS both
// server and worker sharing one LogHub already; relaying through Redis on
// top of that would inject every record a second time. Also gated on
// BrokerRabbit() — under the default inprocess broker there's no separate
// worker pod to relay from, so this stays completely inert.
package logging

import (
	"context"
	"encoding/json"
	"log/slog"

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
// A publish error is logged at Warn and dropped: a Redis hiccup must never
// slow or block the worker's own logging path, the same non-blocking
// contract LogHub.Publish itself keeps for a stalled WS subscriber.
func RelayHubToRedis(ctx context.Context, hub *LogHub, rdb redisPublisher, hostID string) {
	sub := hub.Subscribe()
	defer hub.Unsubscribe(sub)

	publish := func(e LogEntry) {
		e.Source = "worker"
		e.Host = hostID
		body, err := json.Marshal(e)
		if err != nil {
			return
		}
		if perr := rdb.Publish(ctx, LogsChannel, body).Err(); perr != nil {
			slog.Warn("logging: redis log publish failed", "err", perr)
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
