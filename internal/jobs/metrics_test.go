package jobs

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
)

// TestLimiterAcquireBumpsCounter drives the real in-process limiter (max=1) plus
// recordAcquire — the exact pairing runJob uses — and asserts the acquired /
// atlimit outcomes of jobs_limiter_events_total advance. Delta-based so it is
// robust against any counts the process already holds.
func TestLimiterAcquireBumpsCounter(t *testing.T) {
	const kind = "test-limiter-kind"
	acquired := metrics.JobLimiterTotal.WithLabelValues(kind, "acquired")
	atlimit := metrics.JobLimiterTotal.WithLabelValues(kind, "atlimit")

	beforeAcq := testutil.ToFloat64(acquired)
	beforeAtl := testutil.ToFloat64(atlimit)

	lim := newMemLimiter()
	ctx := context.Background()

	// First acquire on a max=1 kind succeeds → acquired++.
	rel, ok, err := lim.Acquire(ctx, kind, "holderA", 1)
	recordAcquire(kind, ok, err)
	if err != nil || !ok {
		t.Fatalf("first Acquire: ok=%v err=%v, want ok=true", ok, err)
	}

	// Second acquire while the slot is held is at-limit → atlimit++.
	_, ok2, err2 := lim.Acquire(ctx, kind, "holderB", 1)
	recordAcquire(kind, ok2, err2)
	if err2 != nil || ok2 {
		t.Fatalf("second Acquire: ok=%v err=%v, want ok=false (at limit)", ok2, err2)
	}

	rel() // free the slot

	if got := testutil.ToFloat64(acquired) - beforeAcq; got != 1 {
		t.Errorf("jobs_limiter_events_total{kind=%s,outcome=acquired} delta = %v, want 1", kind, got)
	}
	if got := testutil.ToFloat64(atlimit) - beforeAtl; got != 1 {
		t.Errorf("jobs_limiter_events_total{kind=%s,outcome=atlimit} delta = %v, want 1", kind, got)
	}
}
