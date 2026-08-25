package jobs

import (
	"context"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
)

// recordAcquire feeds the per-kind rate-limiter Prometheus counter
// (jobs_limiter_events_total{kind,outcome}) that the admin Metrics dashboard +
// Grafana render — THE thing an operator wants to see when a kind is saturating
// its fleet-wide concurrency cap:
//
//   - outcome=acquired — throughput: slots successfully reserved
//   - outcome=atlimit  — back-pressure: the kind was at its cap and the job was
//     requeued (a lost slot-race), i.e. the limiter did its job
//   - outcome=error    — the limiter's broker/Redis call failed (counted
//     separately so a spike in atlimit isn't conflated with an infra failure)
//
// Only limited kinds (max>0) reach here, so `kind` cardinality is bounded by
// the registered limited kinds.
func recordAcquire(kind string, ok bool, err error) {
	switch {
	case err != nil:
		metrics.JobLimiterTotal.WithLabelValues(kind, "error").Inc()
	case ok:
		metrics.JobLimiterTotal.WithLabelValues(kind, "acquired").Inc()
	default:
		metrics.JobLimiterTotal.WithLabelValues(kind, "atlimit").Inc()
	}
}

// recordRun stamps BOTH the terminal counter and the duration histogram for one
// job run. A single helper because there are five terminal paths in exec.go and
// incrementing the counter at each while remembering to observe the histogram
// too is exactly how the two drift — which is how dur_ms ended up in the logs
// but never in a metric in the first place.
func recordRun(kind, status string, started time.Time) {
	metrics.JobsRunTotal.WithLabelValues(kind, status).Inc()
	metrics.JobDurationSeconds.WithLabelValues(kind, status).Observe(time.Since(started).Seconds())
}

// QueueDepth reads per-kind queue levels for the scrape-time gauge
// (metrics.RegisterJobQueue). One query, three counts plus the oldest DUE age.
//
// Split queued (due now) from scheduled (run_at in the future) deliberately: a
// backlog alert must not fire on work that is merely scheduled for later, which
// is the normal steady state for every periodic kind.
func (s *Store) QueueDepth(ctx context.Context) (map[string]metrics.JobQueueSample, bool) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind,
		       count(*) FILTER (WHERE status = 'queued' AND run_at <= now())        AS queued,
		       count(*) FILTER (WHERE status = 'queued' AND run_at >  now())        AS scheduled,
		       count(*) FILTER (WHERE status = 'running')                           AS running,
		       COALESCE(EXTRACT(EPOCH FROM (now() - min(run_at))
		                FILTER (WHERE status = 'queued' AND run_at <= now())), 0)   AS oldest_secs
		  FROM jobs
		 WHERE status IN ('queued', 'running')
		 GROUP BY kind`)
	if err != nil {
		// ok=false: degrade the scrape rather than failing Gather for every
		// other metric on the endpoint.
		return nil, false
	}
	defer rows.Close()

	out := map[string]metrics.JobQueueSample{}
	for rows.Next() {
		var kind string
		var queued, scheduled, running int
		var oldestSecs float64
		if err := rows.Scan(&kind, &queued, &scheduled, &running, &oldestSecs); err != nil {
			return nil, false
		}
		out[kind] = metrics.JobQueueSample{
			Queued:          queued,
			Scheduled:       scheduled,
			Running:         running,
			OldestQueuedAge: time.Duration(oldestSecs * float64(time.Second)),
		}
	}
	if rows.Err() != nil {
		return nil, false
	}
	return out, true
}
