package jobs

import "github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"

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
