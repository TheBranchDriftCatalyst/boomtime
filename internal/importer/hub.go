package importer

import (
	"sync"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
)

// Event is a live update published for a job. Type is one of "log", "progress",
// or "state". Log carries a LogLine; progress/state carry the Job snapshot.
type Event struct {
	Type string      `json:"type"`
	Job  *db.Job     `json:"job,omitempty"`
	Log  *db.LogLine `json:"log,omitempty"`
}

// Hub is an in-process pub/sub for job events. Subscribers receive live events;
// the DB provides the durable backlog for reconnection.
type Hub struct {
	mu   sync.Mutex
	subs map[int]map[chan Event]struct{}
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[int]map[chan Event]struct{})}
}

// Subscribe returns a buffered channel receiving events for the given job.
func (h *Hub) Subscribe(jobID int) chan Event {
	ch := make(chan Event, 64)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[jobID] == nil {
		h.subs[jobID] = make(map[chan Event]struct{})
	}
	h.subs[jobID][ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber channel and closes it.
func (h *Hub) Unsubscribe(jobID int, ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.subs[jobID]; ok {
		if _, ok := set[ch]; ok {
			delete(set, ch)
			close(ch)
		}
		if len(set) == 0 {
			delete(h.subs, jobID)
		}
	}
}

// Publish delivers an event to all current subscribers of a job. Delivery is
// non-blocking: a full subscriber buffer drops the event (the DB remains the
// source of truth, and clients can catch up via afterId).
func (h *Hub) Publish(jobID int, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[jobID] {
		select {
		case ch <- ev:
		default:
		}
	}
}
