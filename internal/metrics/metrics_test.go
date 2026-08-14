package metrics

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// findSeries returns the named series from a snapshot, or fails.
func findSeries(t *testing.T, all []Series, name string) Series {
	t.Helper()
	for _, s := range all {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("series %q not found in snapshot %+v", name, all)
	return Series{}
}

func sumPoints(s Series) float64 {
	var total float64
	for _, p := range s.Points {
		total += p.Value
	}
	return total
}

func TestIncCountsIntoCurrentBucket(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < 5; i++ {
		r.Inc("http.requests", 1)
	}
	snap := r.Snapshot(time.Time{})
	s := findSeries(t, snap, "http.requests")

	if s.Kind != "counter" {
		t.Fatalf("kind = %q, want counter", s.Kind)
	}
	// All five landed in the same (current) minute bucket → one point of value 5.
	if got := sumPoints(s); got != 5 {
		t.Fatalf("sum = %v, want 5", got)
	}
	// The last point must be the current minute bucket.
	last := s.Points[len(s.Points)-1]
	wantBucket := time.Now().Truncate(BucketDur)
	if !last.Bucket.Equal(wantBucket) {
		t.Fatalf("last bucket = %v, want current minute %v", last.Bucket, wantBucket)
	}
	if last.Value != 5 {
		t.Fatalf("current bucket value = %v, want 5", last.Value)
	}
}

func TestObserveGaugeReplacesValue(t *testing.T) {
	r := NewRegistry()
	r.Observe("jobs.inflight", 1)
	r.Observe("jobs.inflight", 4)
	r.Observe("jobs.inflight", 2)
	s := findSeries(t, r.Snapshot(time.Time{}), "jobs.inflight")
	if s.Kind != "gauge" {
		t.Fatalf("kind = %q, want gauge", s.Kind)
	}
	last := s.Points[len(s.Points)-1]
	if last.Value != 2 {
		t.Fatalf("gauge current value = %v, want 2 (last observed)", last.Value)
	}
}

// distinctBuckets drives the ring directly (bypassing time.Now) to assert that
// events in different minutes land in different buckets and that the window
// densifies idle minutes to explicit zeros.
func TestBucketsSeparateByMinuteAndDensify(t *testing.T) {
	s := newSeries("x", Counter, "")
	base := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	s.observe(base, 2)                    // minute 0 → 2
	s.observe(base.Add(3*time.Minute), 5) // minute 3 → 5 (minutes 1,2 idle)
	now := base.Add(3 * time.Minute)

	out := s.snapshot(now, time.Time{})
	// Densified: minutes 0,1,2,3 → four points.
	if len(out.Points) != 4 {
		t.Fatalf("points = %d (%+v), want 4 densified", len(out.Points), out.Points)
	}
	want := []float64{2, 0, 0, 5}
	for i, p := range out.Points {
		if p.Value != want[i] {
			t.Fatalf("point[%d].Value = %v, want %v (points=%+v)", i, p.Value, want[i], out.Points)
		}
		wantStart := base.Add(time.Duration(i) * BucketDur)
		if !p.Bucket.Equal(wantStart) {
			t.Fatalf("point[%d].Bucket = %v, want %v", i, p.Bucket, wantStart)
		}
	}
}

// TestRingEvictionAfterWindow verifies buckets older than the window fall out of
// the ring (bounded memory) rather than accumulating forever.
func TestRingEvictionAfterWindow(t *testing.T) {
	s := newSeries("x", Counter, "")
	base := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	s.observe(base, 1) // very old bucket

	// Advance well past the full window; the old bucket must be evicted.
	later := base.Add(Window + 10*BucketDur)
	s.observe(later, 3)

	out := s.snapshot(later, time.Time{})
	if got := sumPoints(out); got != 3 {
		t.Fatalf("sum after eviction = %v, want 3 (old bucket evicted)", got)
	}
	// The ring never exceeds ringSize buckets.
	if len(s.buf) != ringSize {
		t.Fatalf("ring len = %d, want %d", len(s.buf), ringSize)
	}
	for _, p := range out.Points {
		if p.Bucket.Before(later.Add(-Window)) {
			t.Fatalf("point %v is older than the window", p.Bucket)
		}
	}
}

func TestSnapshotSinceFilter(t *testing.T) {
	s := newSeries("x", Counter, "")
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	s.observe(base, 1)
	s.observe(base.Add(5*time.Minute), 1)
	now := base.Add(5 * time.Minute)

	// since = base+3m drops the first (base) point.
	out := s.snapshot(now, base.Add(3*time.Minute))
	for _, p := range out.Points {
		if p.Bucket.Before(base.Add(3 * time.Minute)) {
			t.Fatalf("point %v predates since filter", p.Bucket)
		}
	}
	if got := sumPoints(out); got != 1 {
		t.Fatalf("sum after since filter = %v, want 1", got)
	}
}

func TestName(t *testing.T) {
	cases := []struct {
		base  string
		pairs []string
		want  string
	}{
		{"jobs.limiter.acquired", []string{"kind", "gh"}, "jobs.limiter.acquired{kind=gh}"},
		{"http.requests", []string{"method", "GET"}, "http.requests{method=GET}"},
		{"http.requests", []string{"a", "1", "b", "2"}, "http.requests{a=1,b=2}"},
		{"bare", nil, "bare"},
		{"odd", []string{"only"}, "odd"},
	}
	for _, tc := range cases {
		if got := Name(tc.base, tc.pairs...); got != tc.want {
			t.Errorf("Name(%q, %v) = %q, want %q", tc.base, tc.pairs, got, tc.want)
		}
	}
}

// TestConcurrentIncRaceFree hammers Inc from many goroutines across many series
// names; run under -race to catch data races. The final counts must be exact
// (no lost updates).
func TestConcurrentIncRaceFree(t *testing.T) {
	r := NewRegistry()
	const goroutines = 50
	const perG = 200
	const names = 8

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				r.Inc(fmt.Sprintf("series.%d", i%names), 1)
			}
		}(g)
	}
	// Concurrent readers to race Snapshot against writers.
	for g := 0; g < 5; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = r.Snapshot(time.Time{})
			}
		}()
	}
	wg.Wait()

	snap := r.Snapshot(time.Time{})
	var total float64
	for _, s := range snap {
		total += sumPoints(s)
	}
	want := float64(goroutines * perG)
	if total != want {
		t.Fatalf("total increments = %v, want %v (lost updates?)", total, want)
	}
}

func TestDefaultRegistryHelpers(t *testing.T) {
	// Uses the process-global registry; a unique name avoids cross-test bleed.
	name := fmt.Sprintf("test.default.%d", time.Now().UnixNano())
	Inc(name, 3)
	Inc(name, 2)
	s := findSeries(t, Snapshot(time.Time{}), name)
	if got := sumPoints(s); got != 5 {
		t.Fatalf("default registry sum = %v, want 5", got)
	}
}
