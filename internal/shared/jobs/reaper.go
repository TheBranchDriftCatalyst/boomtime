package jobs

import (
	"context"
	"log/slog"
	"time"
)

// reaperInterval is how often the stale-job reaper sweeps. Independent of the
// lease TTL: the sweep is cheap (a single guarded UPDATE over the small set of
// running rows) so ~1/min keeps post-crash reclaim latency low without churn.
const reaperInterval = 60 * time.Second

// RunReaper reclaims zombie 'running' rows left by dead workers (pod restarts) —
// the durable half of the heartbeat mechanism. It sweeps ONCE immediately (so a
// deploy's leftover backlog clears on the very next boot, not a minute later) then
// every reaperInterval until ctx is done. ttl is the lease: a running row whose
// heartbeat_at (COALESCEd onto locked_at/started_at for pre-heartbeat rows) is
// older than ttl is considered lost and reset. It's idempotent + concurrency-safe,
// so running it on every server/worker pod is fine. Blocks until ctx is cancelled
// — call it in its own goroutine.
func RunReaper(ctx context.Context, store *Store, ttl time.Duration, log *slog.Logger) {
	sweep := func() {
		n, err := store.ReapStaleRunning(ctx, ttl)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("jobs reaper: sweep failed", "err", err)
			}
			return
		}
		if n > 0 {
			log.Info("jobs reaper: reclaimed stale running jobs", "count", n, "lease", ttl.String())
		}
	}

	log.Info("jobs reaper started", "interval", reaperInterval.String(), "lease", ttl.String())
	sweep() // immediate: clear the current post-deploy backlog now
	t := time.NewTicker(reaperInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("jobs reaper stopped")
			return
		case <-t.C:
			sweep()
		}
	}
}
