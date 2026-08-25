package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// The gauge must actually appear on /metrics. A collector that silently emits
// nothing is worse than no metric: the dashboard shows a flat zero and everyone
// concludes the queue is empty.
func TestJobQueueDepthEmits(t *testing.T) {
	RegisterJobQueue(func() (map[string]JobQueueSample, bool) {
		return map[string]JobQueueSample{
			"books-liberate-book": {Queued: 319, Scheduled: 0, Running: 3, OldestQueuedAge: 90 * time.Minute},
		}, true
	})
	t.Cleanup(func() { RegisterJobQueue(nil) })

	got, err := testutil.GatherAndCount(Registry, "jobs_queue_depth", "jobs_queue_oldest_seconds")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	// 3 states + 1 oldest-age series.
	if got != 4 {
		t.Errorf("gathered %d series, want 4 (queued/scheduled/running + oldest)", got)
	}

	// Assert the actual exposition text, not just the series count — a gauge
	// with the right cardinality but the wrong value is the failure that would
	// make a dashboard lie.
	want := `
# HELP jobs_queue_depth Jobs in the Postgres queue by kind and state (queued=due now, scheduled=future, running).
# TYPE jobs_queue_depth gauge
jobs_queue_depth{kind="books-liberate-book",state="queued"} 319
jobs_queue_depth{kind="books-liberate-book",state="running"} 3
jobs_queue_depth{kind="books-liberate-book",state="scheduled"} 0
`
	if err := testutil.CollectAndCompare(&jobQueueCollector{}, strings.NewReader(want), "jobs_queue_depth"); err != nil {
		t.Errorf("depth exposition wrong: %v", err)
	}
}

// ok=false must degrade the scrape, not break every other metric on the endpoint.
func TestJobQueueDepthUnreachableDBDoesNotFailGather(t *testing.T) {
	RegisterJobQueue(func() (map[string]JobQueueSample, bool) { return nil, false })
	t.Cleanup(func() { RegisterJobQueue(nil) })

	if _, err := testutil.GatherAndCount(Registry, "jobs_queue_depth"); err != nil {
		t.Fatalf("a failing provider broke Gather: %v", err)
	}
}

// The outcome gauges exist because ephemeral drain pods serve no metrics
// endpoint, so per-process counters miss most execution. Pin the exposition.
func TestJobOutcomesEmit(t *testing.T) {
	RegisterJobOutcomes(func() (map[string]JobOutcomeSample, bool) {
		return map[string]JobOutcomeSample{
			"books-liberate-book": {
				ByStatus: map[string]int{"done": 40, "failed": 2},
				P50:      9 * time.Second,
				P95:      45 * time.Second,
			},
		}, true
	})
	t.Cleanup(func() { RegisterJobOutcomes(nil) })

	want := `
# HELP jobs_recent_duration_seconds Duration percentile over jobs completed in the recent window, by kind. A WINDOWED GAUGE — do not use histogram_quantile on it.
# TYPE jobs_recent_duration_seconds gauge
jobs_recent_duration_seconds{kind="books-liberate-book",quantile="0.5"} 9
jobs_recent_duration_seconds{kind="books-liberate-book",quantile="0.95"} 45
`
	if err := testutil.CollectAndCompare(&jobOutcomeCollector{}, strings.NewReader(want), "jobs_recent_duration_seconds"); err != nil {
		t.Errorf("duration percentiles wrong: %v", err)
	}

	n, err := testutil.GatherAndCount(Registry, "jobs_recent_completions")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if n != 2 {
		t.Errorf("gathered %d completion series, want 2 (done + failed)", n)
	}
}

func TestJobOutcomesUnreachableDBDoesNotFailGather(t *testing.T) {
	RegisterJobOutcomes(func() (map[string]JobOutcomeSample, bool) { return nil, false })
	t.Cleanup(func() { RegisterJobOutcomes(nil) })

	if _, err := testutil.GatherAndCount(Registry, "jobs_recent_completions"); err != nil {
		t.Fatalf("a failing provider broke Gather: %v", err)
	}
}
