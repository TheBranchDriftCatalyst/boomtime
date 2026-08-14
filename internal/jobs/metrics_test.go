package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
)

func seriesSum(t *testing.T, name string) float64 {
	t.Helper()
	for _, s := range metrics.Snapshot(time.Time{}) {
		if s.Name == name {
			var total float64
			for _, p := range s.Points {
				total += p.Value
			}
			return total
		}
	}
	return 0
}

// TestLimiterAcquireBumpsSeries drives the real in-process limiter (max=1) plus
// recordAcquire — the exact pairing runJob uses — and asserts the acquired /
// atlimit rate-limiter series advance. Delta-based so it is robust against any
// series the process already holds.
func TestLimiterAcquireBumpsSeries(t *testing.T) {
	const kind = "test-limiter-kind"
	acquired := metrics.Name("jobs.limiter.acquired", "kind", kind)
	atlimit := metrics.Name("jobs.limiter.atlimit", "kind", kind)

	beforeAcq := seriesSum(t, acquired)
	beforeAtl := seriesSum(t, atlimit)

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

	if got := seriesSum(t, acquired) - beforeAcq; got != 1 {
		t.Errorf("jobs.limiter.acquired{kind=%s} delta = %v, want 1", kind, got)
	}
	if got := seriesSum(t, atlimit) - beforeAtl; got != 1 {
		t.Errorf("jobs.limiter.atlimit{kind=%s} delta = %v, want 1", kind, got)
	}
}
