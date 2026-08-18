package jobs

import (
	"context"
	"log/slog"
	"time"
)

// Scheduler enqueues periodic jobs. It's safe to run on every replica: the
// enqueue decision lives in ClaimDueSchedules (an atomic UPDATE ... RETURNING),
// so each due schedule fires exactly once per period regardless of how many
// schedulers are ticking. It enqueues through an Enqueuer, so periodic jobs
// ride whichever provider is active (local or rabbitmq).
type Scheduler struct {
	store *Store
	enq   Enqueuer
	log   *slog.Logger
	tick  time.Duration
	// maxAttempts for the jobs the scheduler enqueues (the periodic work itself
	// retries on transient failure, e.g. a rate-limit).
	maxAttempts int
}

// NewScheduler builds a scheduler ticking once a minute that enqueues via enq.
func NewScheduler(store *Store, enq Enqueuer, log *slog.Logger) *Scheduler {
	return &Scheduler{store: store, enq: enq, log: log, tick: time.Minute, maxAttempts: 3}
}

// Register upserts a periodic schedule for kind at interval. Call before Run.
func (s *Scheduler) Register(ctx context.Context, kind string, interval time.Duration) error {
	return s.store.UpsertSchedule(ctx, kind, interval)
}

// Run enqueues due schedules on each tick until ctx is done.
func (s *Scheduler) Run(ctx context.Context) {
	s.log.Info("jobs scheduler started", "tick", s.tick.String())
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("jobs scheduler stopped")
			return
		case <-t.C:
			s.fire(ctx)
		}
	}
}

func (s *Scheduler) fire(ctx context.Context) {
	kinds, err := s.store.ClaimDueSchedules(ctx)
	if err != nil {
		s.log.Warn("jobs scheduler: claim due failed", "err", err)
		return
	}
	for _, k := range kinds {
		// Coalesce: never stack a second periodic row for a kind that already has
		// one queued or running. A transient hang (e.g. books-reading-monitor,
		// scheduled every minute) would otherwise pile up hundreds of duplicate
		// rows behind the stuck one. The reaper reclaims a genuinely-dead run; this
		// just avoids fan-out while a run is legitimately in flight or pending.
		if pending, err := s.store.HasPendingKind(ctx, k); err != nil {
			s.log.Warn("jobs scheduler: pending check failed; enqueuing anyway", "kind", k, "err", err)
		} else if pending {
			s.log.Info("jobs scheduler: skip enqueue, one already queued/running", "kind", k)
			continue
		}
		if _, err := s.enq.Enqueue(ctx, k, nil, MaxAttempts(s.maxAttempts)); err != nil {
			s.log.Warn("jobs scheduler: enqueue failed", "kind", k, "err", err)
			continue
		}
		s.log.Info("jobs scheduler: enqueued periodic job", "kind", k)
	}
}
