package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/logctx"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
)

// outcome is what execute did with a job — providers use it to decide any
// transport follow-up (e.g. the AMQP provider re-publishes on outcomeRetry).
type outcome int

const (
	outcomeDone outcome = iota
	outcomeRetry
	outcomeFailed
)

// heartbeatInterval is how often a running job refreshes heartbeat_at. It must be
// comfortably shorter than the reaper's lease TTL (default 120s) so a live handler
// always renews before it could be judged stale, even across a missed tick.
const heartbeatInterval = 30 * time.Second

// startHeartbeat spins a goroutine that stamps store.Heartbeat(id) every
// heartbeatInterval while a job's handler runs, keeping a LONG but live job (e.g. a
// 20-min hardcover-match) from being reaped. The returned stop() ends it; call it
// when the handler returns. The ticker also exits if ctx is cancelled (shutdown /
// admin-cancel), so no goroutine leaks past the run.
func startHeartbeat(ctx context.Context, store *Store, id int64, log *slog.Logger) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(heartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if err := store.Heartbeat(ctx, id); err != nil && ctx.Err() == nil {
					log.Warn("jobs: heartbeat write failed", "id", id, "err", err)
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

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
func execute(ctx context.Context, reg *Registry, store *Store, job Job, log *slog.Logger, n Notifier, capture *LogCapture) outcome {
	// Durable log capture (gaka-hney): subscribe to the LogHub BEFORE the first
	// line so a long job's early lines are caught, and flush this job's entries to
	// object storage on return (the deferred finish runs AFTER the terminal
	// done/failed line below, so it's included). No-op when capture is nil.
	defer capture.begin(job.ID)()

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

	// Keep this row's heartbeat_at fresh for the whole handler run so the stale-job
	// reaper never reclaims a long-but-live job; stop the ticker once Handle returns.
	stopHeartbeat := startHeartbeat(hctx, store, job.ID, jl)
	err := func() (e error) {
		defer func() {
			if r := recover(); r != nil {
				e = fmt.Errorf("panic: %v", r)
			}
		}()
		return h.Handle(hctx, job)
	}()
	stopHeartbeat()

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
