package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/logctx"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
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
	// Job-scoped logger: EVERY lifecycle line (and, once handlers pull it from
	// ctx, every handler line) carries job_id/kind/owner as structured attrs, so
	// the Admin log viewer can filter the stream down to a single job's run
	// (gaka-f0is). teeHandler flattens these into LogEntry.Attrs automatically.
	jl := log.With("job_id", job.ID, "kind", job.Kind, "owner", job.Owner)
	started := time.Now()

	notify := func(status Status, errMsg string) {
		if n != nil {
			n.Notify(JobEvent{ID: job.ID, Kind: job.Kind, Owner: job.Owner, Status: status, Error: errMsg})
		}
	}

	h, ok := reg.Handler(job.Kind)
	if !ok {
		_ = store.Fail(ctx, job.ID, "no handler registered for kind "+job.Kind, nil)
		jl.Warn("jobs: no handler for kind")
		notify(StatusFailed, "no handler for kind "+job.Kind)
		metrics.JobsRunTotal.WithLabelValues(job.Kind, "failed").Inc()
		return outcomeFailed
	}

	// "started" is logged for EVERY job kind, DRY — a running job is no longer
	// silent in the viewer, and the side panel has an opening marker.
	jl.Info("jobs: started", "attempt", job.Attempts, "of", job.MaxAttempts)

	// Carry the job-scoped logger on ctx so handlers (and the domain log helpers
	// they call) resolve it via logctx.FromContext — EVERY handler line then
	// inherits job_id/kind/owner and the Admin viewer can filter to one job's run.
	hctx := logctx.NewContext(ctx, jl)

	err := func() (e error) {
		defer func() {
			if r := recover(); r != nil {
				e = fmt.Errorf("panic: %v", r)
			}
		}()
		return h.Handle(hctx, job)
	}()

	// Cancelled mid-run (admin cancel via LocalProvider.Cancel, or shutdown): do
	// NOT record a Complete/Fail that would clobber the terminal 'cancelled' status
	// the admin path already stamped via Store.MarkCancelled — a Fail-with-retry
	// would even flip the row back to 'queued' and re-run it. The store write is
	// also on the cancelled ctx and would fail anyway; just stop, no notify.
	if ctx.Err() != nil {
		jl.Info("jobs: run stopped by context cancellation", "dur_ms", time.Since(started).Milliseconds())
		metrics.JobsRunTotal.WithLabelValues(job.Kind, "cancelled").Inc()
		return outcomeFailed
	}

	if err == nil {
		if cerr := store.Complete(ctx, job.ID); cerr != nil {
			jl.Warn("jobs: complete failed", "err", cerr)
		}
		jl.Info("jobs: done", "attempt", job.Attempts, "dur_ms", time.Since(started).Milliseconds())
		notify(StatusDone, "")
		metrics.JobsRunTotal.WithLabelValues(job.Kind, "done").Inc()
		return outcomeDone
	}

	if job.Attempts < job.MaxAttempts {
		retryAt := time.Now().Add(retryDelay(job.Attempts))
		if ferr := store.Fail(ctx, job.ID, err.Error(), &retryAt); ferr != nil {
			jl.Warn("jobs: fail-state write failed", "err", ferr)
		}
		jl.Warn("jobs: retry scheduled",
			"attempt", job.Attempts, "of", job.MaxAttempts, "dur_ms", time.Since(started).Milliseconds(), "err", err)
		metrics.JobsRunTotal.WithLabelValues(job.Kind, "retry").Inc()
		return outcomeRetry // not terminal — no notify
	}
	if ferr := store.Fail(ctx, job.ID, err.Error(), nil); ferr != nil {
		jl.Warn("jobs: fail-state write failed", "err", ferr)
	}
	jl.Error("jobs: failed (attempts exhausted)",
		"attempts", job.Attempts, "dur_ms", time.Since(started).Milliseconds(), "err", err)
	notify(StatusFailed, err.Error())
	metrics.JobsRunTotal.WithLabelValues(job.Kind, "failed").Inc()
	return outcomeFailed
}
