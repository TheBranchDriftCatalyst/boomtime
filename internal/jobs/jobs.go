// Package jobs is catalyst-go-jobs (boom-hney): a small, generic, DB-backed job
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
	// StatusCancelled is a terminal state set by an admin cancel (Store.MarkCancelled).
	// A queued job flipped to cancelled is never claimed (ClaimNext filters on
	// 'queued'); a running job is stamped terminal while LocalProvider.Cancel
	// interrupts its context.
	StatusCancelled Status = "cancelled"
)

// Job is one unit of work.
type Job struct {
	ID   int64
	Kind string
	// Owner is the user a job belongs to ("" = a system job). Terminal events
	// for owned jobs route to that user's push notifications (boom-hney.6).
	Owner       string
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

// JobEvent is a terminal status transition (done/failed) delivered to a
// Notifier for push notifications (boom-hney.6).
type JobEvent struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Owner  string `json:"owner"`
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Notifier receives terminal job events. boomtime's impl fans them to a WS so
// the FE can toast on completion. Optional — a nil notifier just means no push.
type Notifier interface {
	Notify(ev JobEvent)
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
	// concurrency is the per-kind fleet-wide cap consulted by the KindLimiter.
	// A kind absent from the map (or set to 0) is unlimited. Register does NOT
	// touch this — limits are policy set separately via SetConcurrency.
	concurrency map[string]int
	// offload marks the heavy/"drainable" kinds that belong on a scale-to-zero
	// worker (e.g. avatar-render, label-image), NOT the always-on server. The
	// provider kind-filter is derived from this (see DeriveKindFilter) so a new
	// scheduled/server-resident kind is server-only by DEFAULT and can never be
	// silently orphaned by a worker scaling down mid-run. Policy, like concurrency.
	offload map[string]bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{handlers: map[string]Handler{}, concurrency: map[string]int{}, offload: map[string]bool{}}
}

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

// SetConcurrency sets the fleet-wide max number of jobs of kind that may run at
// once (across all pods + users), enforced by the KindLimiter in front of the
// queue. max <= 0 (or a kind never set) means unlimited. Policy, not wiring —
// call it near where handlers are registered.
func (r *Registry) SetConcurrency(kind string, max int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.concurrency[kind] = max
}

// Concurrency returns a copy of the per-kind limits, for the KindLimiter to
// consult without holding the registry lock. Kinds absent here are unlimited.
func (r *Registry) Concurrency() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]int, len(r.concurrency))
	for k, v := range r.concurrency {
		out[k] = v
	}
	return out
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

// SetOffload marks a kind as belonging to a scale-to-zero worker rather than the
// always-on server (see the offload field). Policy, not wiring — call it near
// SetConcurrency. The worker's include-filter and the server's exclude-filter are
// both derived from this set (DeriveKindFilter), so routing lives in ONE place
// and a new kind is server-resident by default.
func (r *Registry) SetOffload(kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.offload[kind] = true
}

// OffloadKinds returns the offloadable (worker-resident) kinds, sorted.
func (r *Registry) OffloadKinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.offload))
	for k := range r.offload {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DeriveKindFilter computes the (include, exclude) kind-filter for a provider from
// its role and the registry's offload set. An explicit env override (either
// envInclude or envExclude non-empty) wins entirely — an operator escape hatch.
// Otherwise the filter is DERIVED so routing can't drift:
//   - role "worker" (a dedicated, KEDA-scaled pod): claim ONLY offload kinds, so
//     a worker scaling to zero can never grab — and orphan — a server-resident or
//     scheduled kind (the reading-monitor-orphan bug, boom-caxl);
//   - role "server" (always-on): claim everything EXCEPT offload kinds (those are
//     drained by the worker);
//   - any other role ("all" / single-pod dev): no filter, claim everything.
//
// A worker with an EMPTY offload set is a misconfiguration (it would claim
// everything via an empty include); the caller logs the derived filter so that's
// visible. Pure — unit-tested independently of main wiring.
func DeriveKindFilter(role string, offload, envInclude, envExclude []string) (include, exclude []string) {
	if len(envInclude) > 0 || len(envExclude) > 0 {
		return envInclude, envExclude
	}
	switch role {
	case "worker":
		return offload, nil
	case "server":
		return nil, offload
	default:
		return nil, nil
	}
}
