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
