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
// outcome lets a push-based provider (AMQP) re-deliver a retry.
func execute(ctx context.Context, reg *Registry, store *Store, job Job, log *slog.Logger) outcome {
	h, ok := reg.Handler(job.Kind)
	if !ok {
		_ = store.Fail(ctx, job.ID, "no handler registered for kind "+job.Kind, nil)
		log.Warn("jobs: no handler for kind", "kind", job.Kind, "id", job.ID)
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

	if err == nil {
		if cerr := store.Complete(ctx, job.ID); cerr != nil {
			log.Warn("jobs: complete failed", "id", job.ID, "err", cerr)
		}
		log.Info("jobs: done", "kind", job.Kind, "id", job.ID, "attempt", job.Attempts)
		return outcomeDone
	}

	if job.Attempts < job.MaxAttempts {
		retryAt := time.Now().Add(retryDelay(job.Attempts))
		_ = store.Fail(ctx, job.ID, err.Error(), &retryAt)
		log.Warn("jobs: retry scheduled", "kind", job.Kind, "id", job.ID,
			"attempt", job.Attempts, "of", job.MaxAttempts, "err", err)
		return outcomeRetry
	}
	_ = store.Fail(ctx, job.ID, err.Error(), nil)
	log.Error("jobs: failed (attempts exhausted)", "kind", job.Kind, "id", job.ID,
		"attempts", job.Attempts, "err", err)
	return outcomeFailed
}
