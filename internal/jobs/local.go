package jobs

import (
	"context"
	"log/slog"
	"time"
)

// LocalProvider runs jobs with Postgres as the broker: Enqueue inserts a row,
// Run polls + claims due rows via FOR UPDATE SKIP LOCKED. No extra infra, safe
// with many replicas, and retries "just work" (a retry is a queued row with a
// future run_at that the next poll picks up). This is boomtime's default.
type LocalProvider struct {
	store    *Store
	log      *slog.Logger
	id       string
	poll     time.Duration
	notifier Notifier
	// Kind-routing (gaka-hney): include = only these kinds (empty = any);
	// exclude = skip these kinds. Lets the always-on server drain light kinds
	// and a ScaledJob drain the heavy ones off the same queue.
	include []string
	exclude []string
}

// SetKindFilter restricts which kinds this provider claims (gaka-hney).
func (p *LocalProvider) SetKindFilter(include, exclude []string) {
	p.include = include
	p.exclude = exclude
}

// NewLocalProvider builds the Postgres-backed provider. workerID is stamped
// into locked_by for observability.
func NewLocalProvider(store *Store, log *slog.Logger, workerID string) *LocalProvider {
	return &LocalProvider{store: store, log: log, id: workerID, poll: 5 * time.Second}
}

// Name implements Provider.
func (p *LocalProvider) Name() string { return "local" }

// SetNotifier implements Provider.
func (p *LocalProvider) SetNotifier(n Notifier) { p.notifier = n }

// Enqueue implements Enqueuer.
func (p *LocalProvider) Enqueue(ctx context.Context, kind string, payload []byte, opts ...EnqueueOption) (int64, error) {
	c := resolveEnqueue(opts)
	return p.store.Enqueue(ctx, kind, c.owner, payload, c.maxAttempts, c.runAt)
}

// Drain claims + runs every currently-due job, then returns (ScaledJob mode,
// gaka-hney). A KEDA ScaledJob creates one pod per pending job; each pod drains
// what it can and exits. A long job runs to completion — a ScaledJob Job is
// never killed on scale-down, which removes the mid-job-kill + redelivery
// amplification at the root. Any extra pod that finds the queue already emptied
// simply exits. Per-job errors are recorded by execute() and don't stop the
// drain. Retries (future run_at) are left for a later pod once they come due.
func (p *LocalProvider) Drain(ctx context.Context, reg *Registry) error {
	p.log.Info("jobs: draining due queue", "worker", p.id)
	n := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		job, ok, err := p.store.ClaimNext(ctx, p.id, p.include, p.exclude)
		if err != nil {
			return err
		}
		if !ok {
			p.log.Info("jobs: drain complete", "worker", p.id, "processed", n)
			return nil
		}
		execute(ctx, reg, p.store, *job, p.log, p.notifier)
		n++
	}
}

// Run drains due work then polls, until ctx is cancelled.
func (p *LocalProvider) Run(ctx context.Context, reg *Registry) error {
	p.log.Info("jobs: local provider running", "worker", p.id, "poll", p.poll.String())
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		job, ok, err := p.store.ClaimNext(ctx, p.id, p.include, p.exclude)
		if err != nil {
			p.log.Warn("jobs: claim error", "err", err)
		}
		if ok {
			// LocalProvider ignores the outcome: a retry is a re-queued row the
			// next poll re-claims once run_at is due.
			execute(ctx, reg, p.store, *job, p.log, p.notifier)
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.poll):
		}
	}
}
