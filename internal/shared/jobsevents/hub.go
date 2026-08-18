// Package jobsevents is boomtime's push-notification hub for catalyst-go-jobs
// terminal events (gaka-hney.6). It implements jobs.Notifier: a completed or
// failed job fans out to every WebSocket subscriber watching that job's owner,
// so the FE can toast on completion.
//
// In-process for now — the default deploy runs the worker inside the server
// process (role=all), so a Notify from the worker reaches the server's WS
// subscribers directly. A cross-pod Redis/Dragonfly relay (mirroring the
// worker-log relay) is the follow-up for the split worker topology.
package jobsevents

import (
	"sync"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/jobs"
)

// Hub fans terminal job events to per-owner subscribers. The zero value is not
// usable — construct with NewHub.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan jobs.JobEvent]struct{} // owner -> set of channels
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: map[string]map[chan jobs.JobEvent]struct{}{}}
}

// Notify implements jobs.Notifier — deliver the event to the owner's
// subscribers. System jobs (owner "") have no user to toast, so they're
// dropped. Non-blocking: a slow subscriber drops events rather than stalling
// the worker.
func (h *Hub) Notify(ev jobs.JobEvent) {
	if ev.Owner == "" {
		return
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
func (h *Hub) Subscribe(owner string) (<-chan jobs.JobEvent, func()) {
	ch := make(chan jobs.JobEvent, 16)
	h.mu.Lock()
	if h.subs[owner] == nil {
		h.subs[owner] = map[chan jobs.JobEvent]struct{}{}
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
