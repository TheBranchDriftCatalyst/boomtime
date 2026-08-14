// monitor_recommend.go — turns the observed advance INTERVALS into the interval
// RECOMMENDATION the admin panel states (catalyst-books §5.1). Pure function over
// the persisted advance samples (db.KindleAdvancePair, from
// kindle_reading_monitor_advances) — no DB, no clock — so the heuristic is
// exhaustively unit-tested.
//
// THE DERIVATION (documented so the numbers are auditable):
//
//	p50 / p90   = percentiles of the observed advance intervals (seconds between
//	              consecutive intra-session furthest-page-read advances).
//	captureSecs = max(60, round(p50))  — the fine L2 cadence ≈ the typical advance
//	              interval, FLOORED at 60s: reading-time is minute-level analytics,
//	              so polling finer only adds Amazon load for no fidelity gain.
//	idleSecs    = max(180, round(p90 * 2))  — the idle gap G ≈ twice the longest
//	              intra-session gap (p90), floored so a normal pause doesn't end a
//	              session.
//	detectSecs  = max(2*captureSecs, round(p90 * 3))  — coarse L1: detection can be
//	              as slow as a few × the session gap because Amazon's creationTime
//	              backfills the true event time, so a larger T1 costs toast
//	              LATENCY, not data.
//	medianAdvanceSecs = round(p50);  p90AdvanceSecs = round(p90).
//
// Returns nil (JSON null) when fewer than recommendMinSamples advances are
// observed, so the FE shows "not enough data — read a book with the monitor on
// to calibrate".
package books

import (
	"math"
	"sort"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// RecommendLookback bounds how far back the recommendation reads advance samples.
const RecommendLookback = 30 * 24 * time.Hour

const (
	recommendMinSamples = 5  // below this → nil ("not enough data yet")
	fidelityFloorSecs   = 60 // never recommend polling finer — minute-level analytics
	idleFloorSecs       = 180
)

// Recommendation is the derived optimal-interval answer the admin panel renders.
// All fields are whole seconds. nil means "not enough data yet".
type Recommendation struct {
	DetectSecs        int `json:"detectSecs"`
	CaptureSecs       int `json:"captureSecs"`
	IdleSecs          int `json:"idleSecs"`
	MedianAdvanceSecs int `json:"medianAdvanceSecs"`
	P90AdvanceSecs    int `json:"p90AdvanceSecs"`
	SampleCount       int `json:"sampleCount"`
}

// RecommendIntervals derives the interval recommendation from observed advance
// samples. Returns nil when there are fewer than recommendMinSamples usable
// intervals.
func RecommendIntervals(pairs []db.KindleAdvancePair) *Recommendation {
	intervals := make([]float64, 0, len(pairs))
	for _, p := range pairs {
		if p.IntervalSecs > 0 {
			intervals = append(intervals, p.IntervalSecs)
		}
	}
	if len(intervals) < recommendMinSamples {
		return nil
	}
	sort.Float64s(intervals)

	p50 := percentile(intervals, 50)
	p90 := percentile(intervals, 90)

	capture := maxInt(fidelityFloorSecs, roundToInt(p50))
	idle := maxInt(idleFloorSecs, roundToInt(p90*2))
	detect := maxInt(2*capture, roundToInt(p90*3))

	return &Recommendation{
		DetectSecs:        detect,
		CaptureSecs:       capture,
		IdleSecs:          idle,
		MedianAdvanceSecs: roundToInt(p50),
		P90AdvanceSecs:    roundToInt(p90),
		SampleCount:       len(intervals),
	}
}

// percentile returns the nearest-rank p-th percentile of an already-sorted slice.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	rank := int(math.Ceil(p/100*float64(n))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= n {
		rank = n - 1
	}
	return sorted[rank]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func roundToInt(f float64) int { return int(math.Round(f)) }
