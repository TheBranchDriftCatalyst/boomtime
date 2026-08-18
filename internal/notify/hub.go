// Package notify is boomtime's domain-agnostic per-user notification hub. It
// generalizes internal/jobsevents (which fans catalyst-go-jobs terminal events
// to per-owner WS subscribers) into a subsystem any domain can publish through:
// a caller hands the hub a self-describing Event envelope and every WebSocket
// subscriber watching that event's owner receives it, so the FE can toast.
//
// In-process for now — same posture as jobsevents: the default deploy runs the
// worker inside the server process (role=all), so a Publish from any in-process
// producer reaches the server's WS subscribers directly. A cross-pod
// Redis/Dragonfly relay is the follow-up for the split worker topology.
//
// notify is additive and does NOT replace jobsevents — the jobs toasts keep
// their own hub + /api/v1/jobs/ws stream. jobs MAY later publish through notify,
// but that migration is deliberately out of scope here.
package notify

import (
	"context"
	"sync"
	"time"
)

// Event is the self-describing notification envelope fanned to subscribers.
// Type identifies the notification kind (domain-defined, e.g. "book-sync");
// Owner scopes delivery to a single user; Title/Body are the human-facing toast
// strings; Data carries arbitrary structured payload the FE may read; At is the
// server-side timestamp.
type Event struct {
	Type  string         `json:"type"`
	Owner string         `json:"owner"`
	Title string         `json:"title"`
	Body  string         `json:"body,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
	At    time.Time      `json:"at"`
	// Durable marks an event that must NOT be dropped on the floor when the user
	// has no open WS session: it is written to the persister (a DB row) as well as
	// fanned out live, so the FE can replay it on the next session. Ephemeral
	// (Durable=false) events are toast-only. Not serialized to WS subscribers.
	Durable bool `json:"-"`
}

// Persister durably stores a notification so it survives a missing session. The
// signature is primitives-only (no Event) so implementers (e.g. *db.DB) need not
// import this package. A nil persister on the Hub disables durability (events still
// fan out live).
type Persister interface {
	SaveNotification(ctx context.Context, owner, typ, title, body string, data map[string]any, at time.Time) error
}

// Hub fans notification events to per-owner subscribers. The zero value is not
// usable — construct with NewHub. Mirrors jobsevents.Hub.
type Hub struct {
	mu        sync.RWMutex
	subs      map[string]map[chan Event]struct{} // owner -> set of channels
	persister Persister                          // nil → durability disabled
	onErr     func(error)                        // optional durable-save error sink
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: map[string]map[chan Event]struct{}{}}
}

// SetPersister wires durable storage (call once at startup). onErr, if non-nil, is
// invoked when a durable save fails (for logging); a failed save does NOT block the
// live fan-out.
func (h *Hub) SetPersister(p Persister, onErr func(error)) {
	h.mu.Lock()
	h.persister = p
	h.onErr = onErr
	h.mu.Unlock()
}

// Publish delivers the event to the owner's subscribers. Ownerless events
// (owner "") have no user to toast, so they're dropped. Non-blocking: a slow
// subscriber drops events rather than stalling the publisher. If At is zero it
// is stamped with the current time.
func (h *Hub) Publish(ev Event) {
	if ev.Owner == "" {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	// Durable events are stored BEFORE the live fan-out so a subscriber that reacts
	// by refetching always sees the row. Best-effort + bounded: a save failure is
	// reported via onErr but never blocks or drops the live toast.
	h.mu.RLock()
	persister, onErr := h.persister, h.onErr
	h.mu.RUnlock()
	if ev.Durable && persister != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := persister.SaveNotification(ctx, ev.Owner, ev.Type, ev.Title, ev.Body, ev.Data, ev.At); err != nil && onErr != nil {
			onErr(err)
		}
		cancel()
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[ev.Owner] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe returns a buffered channel of events for owner plus an unsubscribe
// func the caller MUST invoke (it removes the subscription and closes the
// channel).
func (h *Hub) Subscribe(owner string) (<-chan Event, func()) {
	ch := make(chan Event, 16)
	h.mu.Lock()
	if h.subs[owner] == nil {
		h.subs[owner] = map[chan Event]struct{}{}
	}
	h.subs[owner][ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			if set := h.subs[owner]; set != nil {
				delete(set, ch)
				if len(set) == 0 {
					delete(h.subs, owner)
				}
			}
			h.mu.Unlock()
			close(ch)
		})
	}
}
