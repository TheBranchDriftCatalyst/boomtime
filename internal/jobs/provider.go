package jobs

import (
	"context"
	"time"
)

// The provider boundary — the ONE seam boomtime swaps to choose where jobs run.
//
//	Enqueuer — put work on the queue (server side + the Scheduler).
//	Runner   — pull work off and dispatch it to the Registry (worker side).
//	Provider — both, named, for logs/metrics/admin.
//
// Two providers ship in this package: LocalProvider (Postgres is the broker via
// FOR UPDATE SKIP LOCKED) and AMQPProvider (RabbitMQ, celery-style). Both record
// every job in the `jobs` table via the shared Store, so the admin UI reads one
// place regardless of transport — the provider only decides HOW a worker is
// woken (poll the table vs consume an AMQP delivery).

// Enqueuer puts a job of a kind on the queue and returns its id.
type Enqueuer interface {
	Enqueue(ctx context.Context, kind string, payload []byte, opts ...EnqueueOption) (int64, error)
}

// Runner consumes jobs and dispatches them to reg until ctx is cancelled.
type Runner interface {
	Run(ctx context.Context, reg *Registry) error
}

// Provider is a complete job backend.
type Provider interface {
	Enqueuer
	Runner
	// Name identifies the backend ("local", "rabbitmq") for logs + admin.
	Name() string
	// SetNotifier wires an optional terminal-event sink (gaka-hney.6). nil-safe.
	SetNotifier(Notifier)
}

// EnqueueOption tunes a single enqueue.
type EnqueueOption func(*enqueueConfig)

type enqueueConfig struct {
	maxAttempts int
	runAt       time.Time
	owner       string
}

func resolveEnqueue(opts []EnqueueOption) enqueueConfig {
	cfg := enqueueConfig{maxAttempts: 1}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// Owner scopes a job to a user, so its terminal event routes to that user's
// push notifications. Empty = a system job.
func Owner(username string) EnqueueOption {
	return func(c *enqueueConfig) { c.owner = username }
}

// MaxAttempts sets how many times a job is tried before it's terminal (>=1).
func MaxAttempts(n int) EnqueueOption {
	return func(c *enqueueConfig) { c.maxAttempts = n }
}

// Delay runs the job no earlier than d from now.
func Delay(d time.Duration) EnqueueOption {
	return func(c *enqueueConfig) { c.runAt = time.Now().Add(d) }
}

// At runs the job no earlier than t.
func At(t time.Time) EnqueueOption {
	return func(c *enqueueConfig) { c.runAt = t }
}
