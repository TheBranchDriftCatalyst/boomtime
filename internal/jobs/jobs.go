// Package jobs is catalyst-go-jobs (gaka-hney): a small, generic, DB-backed job
// queue + periodic scheduler. It is transport-agnostic and domain-agnostic —
// callers register a Handler per job kind, enqueue work (or a Scheduler enqueues
// it periodically), and Workers claim + run it with retry/backoff.
//
// Design goals: no new infra (Postgres is the broker via FOR UPDATE SKIP
// LOCKED), safe with many concurrent workers, and additive — it stands beside
// the RabbitMQ image path rather than replacing it (that migration is a later,
// gated stage). The package is intentionally free of boomtime-specific imports
// so it can be lifted into a standalone module.
package jobs

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// Status is a job's lifecycle state.
type Status string

const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Job is one unit of work.
type Job struct {
	ID          int64
	Kind        string
	Payload     json.RawMessage
	Status      Status
	Attempts    int
	MaxAttempts int
	Error       string
	RunAt       time.Time
	CreatedAt   time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
}

// Schedule is a periodic enqueue registration.
type Schedule struct {
	Kind     string
	Interval time.Duration
	NextRun  time.Time
	LastRun  *time.Time
}

// Handler runs one job of a kind. A nil return marks it done; a non-nil error
// marks it failed and re-queues with backoff until MaxAttempts is reached.
type Handler interface {
	Handle(ctx context.Context, job Job) error
}

// HandlerFunc adapts a func to a Handler.
type HandlerFunc func(ctx context.Context, job Job) error

// Handle implements Handler.
func (f HandlerFunc) Handle(ctx context.Context, job Job) error { return f(ctx, job) }

// Registry maps a job kind to its handler. Safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{handlers: map[string]Handler{}} }

// Register binds a handler to a kind (last write wins).
func (r *Registry) Register(kind string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[kind] = h
}

// Handler returns the handler for a kind, if registered.
func (r *Registry) Handler(kind string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[kind]
	return h, ok
}

// Kinds returns the registered kinds, sorted.
func (r *Registry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
