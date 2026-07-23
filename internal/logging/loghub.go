package logging

import (
	"sync"
	"sync/atomic"
	"time"
)

// LogEntry is one captured server log record. ID is a process-monotonic cursor
// so clients can resume (afterId) after a reload or a dropped connection.
type LogEntry struct {
	ID    int64     `json:"id"`
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`
	// Attrs holds any structured slog attributes (flattened key->string).
	Attrs map[string]string `json:"attrs,omitempty"`
}

// LogHub is an in-process ring buffer + fan-out for the server's own log
// records. It mirrors importer.Hub: publishing is NON-BLOCKING so a slow (or
// stalled) WebSocket subscriber can never block the server's logging path.
//
// The ring buffer holds the last `capacity` entries, which lets a reconnecting
// client backfill the recent tail (via afterId) so the viewer is durable across
// reloads. It is safe for concurrent use.
type LogHub struct {
	mu       sync.Mutex
	capacity int
	ring     []LogEntry // ordered oldest->newest, len <= capacity
	nextID   atomic.Int64
	subs     map[chan LogEntry]struct{}
}

// DefaultLogHubCapacity is the number of recent log entries retained for
// reload/backfill.
const DefaultLogHubCapacity = 1000

// NewLogHub creates a hub with the given ring-buffer capacity (<=0 uses the
// default).
func NewLogHub(capacity int) *LogHub {
	if capacity <= 0 {
		capacity = DefaultLogHubCapacity
	}
	return &LogHub{
		capacity: capacity,
		ring:     make([]LogEntry, 0, capacity),
		subs:     make(map[chan LogEntry]struct{}),
	}
}

// Publish assigns the entry a monotonic ID, appends it to the ring buffer, and
// fans it out to all current subscribers. Delivery is non-blocking: a full
// subscriber buffer drops the event (the client catches up via afterId). Nil
// receiver is a safe no-op so logging never depends on a hub being wired.
func (h *LogHub) Publish(e LogEntry) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	e.ID = h.nextID.Add(1)
	if len(h.ring) < h.capacity {
		h.ring = append(h.ring, e)
	} else {
		// Ring is full: drop the oldest, keep order, append newest.
		copy(h.ring, h.ring[1:])
		h.ring[len(h.ring)-1] = e
	}

	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Subscribe returns a buffered channel receiving live entries. Call Unsubscribe
// to release it.
func (h *LogHub) Subscribe() chan LogEntry {
	ch := make(chan LogEntry, 256)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs[ch] = struct{}{}
	return ch
}

// Unsubscribe removes and closes a subscriber channel. Safe to call once.
func (h *LogHub) Unsubscribe(ch chan LogEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
}

// Backfill returns retained entries with ID > afterId, oldest first. Pass 0 to
// get the full retained tail. This is what makes reload/resume work.
func (h *LogHub) Backfill(afterID int64) []LogEntry {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]LogEntry, 0, len(h.ring))
	for _, e := range h.ring {
		if e.ID > afterID {
			out = append(out, e)
		}
	}
	return out
}

// OwnerAttrKey is the slog attribute key used to tag a log record with the
// user it pertains to. Handlers emit e.g. `slog.Info(..., "user", owner)` and
// the tee handler flattens that into LogEntry.Attrs["user"]. Records with no
// value under this key are treated as server-scope and are visible to any
// authenticated viewer of the Logs tab. See FilterForUser for the fail-closed
// semantics enforced when a requester is not identifiable.
const OwnerAttrKey = "user"

// FilterForUser is the per-record owner gate that fixes gaka-awh.2 — LogHub
// used to fan every record out to every authenticated viewer, leaking cross-
// tenant activity metadata (who saved a wakatime key, whose password rotated,
// which owner's import job just finished). Records tagged with an owner via
// OwnerAttrKey are only visible to that user. Records with no owner
// attribution (server-scope events like healthz, migrations, DB tracer output
// whose args are already redacted) pass through to everyone.
//
// Fail-closed on an empty requester: an anonymous/unresolved viewer gets ONLY
// the un-tagged (server-scope) records. This matters because a future change
// that forgets to guard the endpoint should not turn this into an
// unauthenticated leak.
//
// A nil records slice returns nil (not an empty slice) so callers that use
// the nil-check to decide whether to emit `[]` vs `null` keep their prior
// behavior.
func FilterForUser(records []LogEntry, requester string) []LogEntry {
	if records == nil {
		return nil
	}
	out := make([]LogEntry, 0, len(records))
	for _, e := range records {
		owner, tagged := e.Attrs[OwnerAttrKey]
		if !tagged {
			// server-scope: everyone sees these.
			out = append(out, e)
			continue
		}
		if requester != "" && owner == requester {
			out = append(out, e)
		}
	}
	return out
}
