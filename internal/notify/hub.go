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
}

// Hub fans notification events to per-owner subscribers. The zero value is not
// usable — construct with NewHub. Mirrors jobsevents.Hub.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]struct{} // owner -> set of channels
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: map[string]map[chan Event]struct{}{}}
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
