package jobs

import "github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"

// recordAcquire feeds the per-kind rate-limiter observability series that the
// admin Metrics dashboard renders — THE thing an operator wants to see when a
// kind is saturating its fleet-wide concurrency cap:
//
//   - jobs.limiter.acquired{kind=…} — throughput: slots successfully reserved
//   - jobs.limiter.atlimit{kind=…}  — back-pressure: the kind was at its cap and
//     the job was requeued (a lost slot-race), i.e. the limiter did its job
//
// Only limited kinds (max>0) reach here, so cardinality is bounded by the
// registered limited kinds. Errors are counted separately so a spike in
// atlimit isn't conflated with a broker/Redis failure.
func recordAcquire(kind string, ok bool, err error) {
	switch {
	case err != nil:
		metrics.Inc(metrics.Name("jobs.limiter.error", "kind", kind), 1)
	case ok:
		metrics.Inc(metrics.Name("jobs.limiter.acquired", "kind", kind), 1)
	default:
		metrics.Inc(metrics.Name("jobs.limiter.atlimit", "kind", kind), 1)
	}
}
