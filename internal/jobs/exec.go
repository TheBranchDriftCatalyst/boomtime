package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// outcome is what execute did with a job — providers use it to decide any
// transport follow-up (e.g. the AMQP provider re-publishes on outcomeRetry).
type outcome int

const (
	outcomeDone outcome = iota
	outcomeRetry
	outcomeFailed
)

// retryDelay backs off a retry linearly (attempt × 30s), capped at 10m.
func retryDelay(attempt int) time.Duration {
	d := time.Duration(attempt) * 30 * time.Second
	if d > 10*time.Minute {
		d = 10 * time.Minute
	}
	return d
}

// execute runs one already-claimed job through its handler and records the
// result in the store — Complete on success, or Fail with a backoff-retry until
// MaxAttempts is exhausted. Shared by every provider's Run loop so the retry +
// terminal semantics are identical regardless of transport. The returned
// outcome lets a push-based provider (AMQP) re-deliver a retry. On a TERMINAL
// outcome (done/failed) it fires n.Notify (gaka-hney.6) so the FE can toast.
func execute(ctx context.Context, reg *Registry, store *Store, job Job, log *slog.Logger, n Notifier) outcome {
	notify := func(status Status, errMsg string) {
		if n != nil {
			n.Notify(JobEvent{ID: job.ID, Kind: job.Kind, Owner: job.Owner, Status: status, Error: errMsg})
		}
	}

	h, ok := reg.Handler(job.Kind)
	if !ok {
		_ = store.Fail(ctx, job.ID, "no handler registered for kind "+job.Kind, nil)
		log.Warn("jobs: no handler for kind", "kind", job.Kind, "id", job.ID)
		notify(StatusFailed, "no handler for kind "+job.Kind)
		return outcomeFailed
	}

	err := func() (e error) {
		defer func() {
			if r := recover(); r != nil {
				e = fmt.Errorf("panic: %v", r)
			}
		}()
		return h.Handle(ctx, job)
	}()

	// Cancelled mid-run (admin cancel via LocalProvider.Cancel, or shutdown): do
	// NOT record a Complete/Fail that would clobber the terminal 'cancelled' status
	// the admin path already stamped via Store.MarkCancelled — a Fail-with-retry
	// would even flip the row back to 'queued' and re-run it. The store write is
	// also on the cancelled ctx and would fail anyway; just stop, no notify.
	if ctx.Err() != nil {
		log.Info("jobs: run stopped by context cancellation", "kind", job.Kind, "id", job.ID)
		return outcomeFailed
	}

	if err == nil {
		if cerr := store.Complete(ctx, job.ID); cerr != nil {
			log.Warn("jobs: complete failed", "id", job.ID, "err", cerr)
		}
		log.Info("jobs: done", "kind", job.Kind, "id", job.ID, "attempt", job.Attempts)
		notify(StatusDone, "")
		return outcomeDone
	}

	if job.Attempts < job.MaxAttempts {
		retryAt := time.Now().Add(retryDelay(job.Attempts))
		_ = store.Fail(ctx, job.ID, err.Error(), &retryAt)
		log.Warn("jobs: retry scheduled", "kind", job.Kind, "id", job.ID,
			"attempt", job.Attempts, "of", job.MaxAttempts, "err", err)
		return outcomeRetry // not terminal — no notify
	}
	_ = store.Fail(ctx, job.ID, err.Error(), nil)
	log.Error("jobs: failed (attempts exhausted)", "kind", job.Kind, "id", job.ID,
		"attempts", job.Attempts, "err", err)
	notify(StatusFailed, err.Error())
	return outcomeFailed
}
