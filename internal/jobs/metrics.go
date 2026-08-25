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

// outcomeWindow is how far back RecentOutcomes looks. 15 minutes is long enough
// that a low-frequency kind still shows something between scrapes, and short
// enough that the numbers describe "now" rather than the last deploy.
const outcomeWindow = 15 * time.Minute

// RecentOutcomes reads terminal counts + duration percentiles per kind for the
// scrape-time gauges (metrics.RegisterJobOutcomes).
//
// This exists because most execution happens on ephemeral drain pods that serve
// no metrics endpoint, so the per-process counters miss it. Reading the table
// covers every pod. See the RegisterJobOutcomes doc for the counter-vs-gauge
// tradeoff this accepts.
func (s *Store) RecentOutcomes(ctx context.Context) (map[string]metrics.JobOutcomeSample, bool) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind, status, count(*) AS n,
		       COALESCE(percentile_cont(0.5)  WITHIN GROUP (
		                ORDER BY EXTRACT(EPOCH FROM (finished_at - started_at))), 0) AS p50,
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (
		                ORDER BY EXTRACT(EPOCH FROM (finished_at - started_at))), 0) AS p95
		  FROM jobs
		 WHERE finished_at IS NOT NULL
		   AND finished_at > now() - $1::interval
		 GROUP BY kind, status`,
		outcomeWindow.String())
	if err != nil {
		return nil, false
	}
	defer rows.Close()

	out := map[string]metrics.JobOutcomeSample{}
	for rows.Next() {
		var kind, status string
		var n int
		var p50, p95 float64
		if err := rows.Scan(&kind, &status, &n, &p50, &p95); err != nil {
			return nil, false
		}
		e, ok := out[kind]
		if !ok {
			e = metrics.JobOutcomeSample{ByStatus: map[string]int{}}
		}
		e.ByStatus[status] = n
		// Percentiles come back per (kind,status); keep the widest observed for
		// the kind. A kind's p95 should reflect its slowest real work, and a
		// failure that took four minutes before erroring is exactly the kind of
		// slow run an operator wants surfaced, not averaged away.
		if d := time.Duration(p50 * float64(time.Second)); d > e.P50 {
			e.P50 = d
		}
		if d := time.Duration(p95 * float64(time.Second)); d > e.P95 {
			e.P95 = d
		}
		out[kind] = e
	}
	if rows.Err() != nil {
		return nil, false
	}
	return out, true
}
