// monitor_recommend_test.go — pure (no DB) tests of the interval RECOMMENDATION
// heuristic. Table-driven over synthetic advance samples so every branch
// (too-few-samples, the p50/p90 floored derivation) is pinned.
package books

import (
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// samples builds n advance pairs each with the given interval (seconds).
func samples(intervalSecs float64, n int) []db.KindleAdvancePair {
	out := make([]db.KindleAdvancePair, n)
	for i := range out {
		out[i] = db.KindleAdvancePair{IntervalSecs: intervalSecs, DLoc: 50}
	}
	return out
}

func TestRecommendIntervals_NilBelowMinSamples(t *testing.T) {
	cfg := MonitorConfig{}
	// 4 usable intervals < the 5-sample floor → nil.
	if got := RecommendIntervals(samples(45, 4), cfg); got != nil {
		t.Fatalf("want nil below min samples, got %+v", got)
	}
	// Non-positive intervals don't count toward the floor.
	junk := append(samples(45, 4), db.KindleAdvancePair{IntervalSecs: 0, DLoc: 0})
	if got := RecommendIntervals(junk, cfg); got != nil {
		t.Fatalf("want nil when usable intervals < min, got %+v", got)
	}
}

func TestRecommendIntervals_FidelityFloorCapsCapture(t *testing.T) {
	// Fast advances: p50 = p90 = 20s. capture floors at 60s; idle floors at 180s
	// (round(20*2)=40 < 180); detect = max(2*60, round(20*3)=60) = 120.
	rec := RecommendIntervals(samples(20, 20), MonitorConfig{})
	if rec == nil {
		t.Fatal("want a recommendation, got nil")
	}
	if rec.MedianAdvanceSecs != 20 || rec.P90AdvanceSecs != 20 {
		t.Errorf("median/p90 = %d/%d, want 20/20 (raw observed)", rec.MedianAdvanceSecs, rec.P90AdvanceSecs)
	}
	if rec.CaptureSecs != 60 {
		t.Errorf("captureSecs = %d, want 60 (fidelity floor)", rec.CaptureSecs)
	}
	if rec.IdleSecs != 180 {
		t.Errorf("idleSecs = %d, want 180 (floor: 20*2 < 180)", rec.IdleSecs)
	}
	if rec.DetectSecs != 120 {
		t.Errorf("detectSecs = %d, want 120 (max(2*60, 20*3))", rec.DetectSecs)
	}
	if rec.SampleCount != 20 {
		t.Errorf("sampleCount = %d, want 20", rec.SampleCount)
	}
}

func TestRecommendIntervals_DerivesFromPercentiles(t *testing.T) {
	// A spread where p90 dominates the derived idle/detect. 18 samples at 90s + 2
	// at 600s → sorted, nearest-rank p90 (rank ceil(.9*20)-1 = 17) = 90 here since
	// only the last 2 are 600; make the top decile 600 by using 2 highs at index
	// 18,19 → p90 index 17 = 90. Use a clearer split: 10×120 + 10×600.
	pairs := append(samples(120, 10), samples(600, 10)...)
	rec := RecommendIntervals(pairs, MonitorConfig{})
	if rec == nil {
		t.Fatal("want a recommendation, got nil")
	}
	// sorted: ten 120s then ten 600s. p50 rank = ceil(.5*20)-1 = 9 → 120.
	// p90 rank = ceil(.9*20)-1 = 17 → 600.
	if rec.MedianAdvanceSecs != 120 {
		t.Errorf("medianAdvanceSecs = %d, want 120 (p50)", rec.MedianAdvanceSecs)
	}
	if rec.P90AdvanceSecs != 600 {
		t.Errorf("p90AdvanceSecs = %d, want 600 (p90)", rec.P90AdvanceSecs)
	}
	if rec.CaptureSecs != 120 { // max(60, 120)
		t.Errorf("captureSecs = %d, want 120", rec.CaptureSecs)
	}
	if rec.IdleSecs != 1200 { // max(180, 600*2)
		t.Errorf("idleSecs = %d, want 1200 (p90*2)", rec.IdleSecs)
	}
	if rec.DetectSecs != 1800 { // max(2*120, 600*3)
		t.Errorf("detectSecs = %d, want 1800 (p90*3)", rec.DetectSecs)
	}
}

func TestPercentile_NearestRank(t *testing.T) {
	s := []float64{10, 20, 30, 40, 50}
	if got := percentile(s, 50); got != 30 {
		t.Errorf("p50 = %v, want 30", got)
	}
	if got := percentile(s, 90); got != 50 {
		t.Errorf("p90 = %v, want 50", got)
	}
}
