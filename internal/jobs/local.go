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

// Run drains due work then polls, until ctx is cancelled.
func (p *LocalProvider) Run(ctx context.Context, reg *Registry) error {
	p.log.Info("jobs: local provider running", "worker", p.id, "poll", p.poll.String())
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		job, ok, err := p.store.ClaimNext(ctx, p.id)
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
