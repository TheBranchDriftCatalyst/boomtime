// monitor_recommend.go — turns the observed advance INTERVALS into the interval
// RECOMMENDATION + sync-pattern CLASSIFICATION the admin panel states
// (catalyst-books §5.1). Pure function over the persisted advance samples
// (db.KindleAdvancePair, from kindle_reading_monitor_advances) + the MonitorConfig
// coefficients — no DB, no clock — so the heuristic is exhaustively unit-tested.
//
// THE DERIVATION (documented so the numbers are auditable):
//
//	p50 / p90   = percentiles of the observed advance intervals (seconds between
//	              consecutive intra-session furthest-page-read advances).
//	captureSecs = max(FidelityFloorSecs, round(p50))  — the fine L2 cadence ≈ the
//	              typical advance interval, FLOORED so polling never goes sub-fidelity.
//	idleSecs    = max(IdleFloorSecs, round(p90 * IdleMultiplier))  — the idle gap G.
//	detectSecs  = max(2*captureSecs, round(p90 * DetectMultiplier))  — coarse L1.
//	medianAdvanceSecs = round(p50);  p90AdvanceSecs = round(p90).
//
// THE CLASSIFICATION (§5.1 table — decides the composition method, never guessed):
//
//	continuous       — frequent (p50 interval < ContinuousMaxIntervalSecs) AND small
//	                   (p50 dloc < MaxDloc) ⇒ gap-sum works.
//	session-boundary — sparse (p50 interval >= SessionBoundaryMinIntervalSecs) AND
//	                   large (p50 dloc >= SessionBoundaryMinDloc) ⇒ position-delta.
//	unknown          — anything else (too few / ambiguous) ⇒ fall back to gap-sum.
//
// Returns nil (JSON null) when fewer than cfg.MinSamples advances are observed, so
// the FE shows "not enough data — read a book with the monitor on to calibrate".
package reading

import (
	"fmt"
	"math"
	"sort"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// Sync-pattern classification values (the syncPattern field). Public so the FE
// contract + tests reference the same strings.
const (
	SyncPatternContinuous      = "continuous"
	SyncPatternSessionBoundary = "session-boundary"
	SyncPatternUnknown         = "unknown"
)

// Implied composition-method values (the impliedMethod field). These name the two
// reading_time.go composition methods the classification selects between.
const (
	MethodGapSum        = "gap-sum"
	MethodPositionDelta = "position-delta"
	MethodUnknown       = "unknown"
)

// Recommendation is the derived optimal-interval answer + sync-pattern
// classification the admin panel renders. Interval fields are whole seconds. nil
// means "not enough data yet".
type Recommendation struct {
	DetectSecs        int `json:"detectSecs"`
	CaptureSecs       int `json:"captureSecs"`
	IdleSecs          int `json:"idleSecs"`
	MedianAdvanceSecs int `json:"medianAdvanceSecs"`
	P90AdvanceSecs    int `json:"p90AdvanceSecs"`
	SampleCount       int `json:"sampleCount"`
	// Classification (PART 3): which whispersync sync-pattern the observed advances
	// fit, the composition method it implies, and a plain-English sentence the FE
	// renders verbatim.
	SyncPattern   string `json:"syncPattern"`
	ImpliedMethod string `json:"impliedMethod"`
	Rationale     string `json:"rationale"`
}

// RecommendIntervals derives the interval recommendation + sync-pattern
// classification from observed advance samples, using the coefficients in cfg.
// Returns nil when there are fewer than cfg.MinSamples usable intervals.
func RecommendIntervals(pairs []db.KindleAdvancePair, cfg MonitorConfig) *Recommendation {
	cfg = cfg.withDefaults()

	intervals := make([]float64, 0, len(pairs))
	dlocs := make([]float64, 0, len(pairs))
	for _, p := range pairs {
		if p.IntervalSecs > 0 {
			intervals = append(intervals, p.IntervalSecs)
			dlocs = append(dlocs, float64(p.DLoc))
		}
	}
	if len(intervals) < cfg.MinSamples {
		return nil
	}
	sort.Float64s(intervals)
	sort.Float64s(dlocs)

	p50 := percentile(intervals, 50)
	p90 := percentile(intervals, 90)
	p50dloc := percentile(dlocs, 50)

	capture := maxInt(cfg.FidelityFloorSecs, roundToInt(p50))
	idle := maxInt(cfg.IdleFloorSecs, roundToInt(p90*cfg.IdleMultiplier))
	detect := maxInt(2*capture, roundToInt(p90*cfg.DetectMultiplier))

	pattern, method, rationale := classify(p50, p50dloc, cfg)

	return &Recommendation{
		DetectSecs:        detect,
		CaptureSecs:       capture,
		IdleSecs:          idle,
		MedianAdvanceSecs: roundToInt(p50),
		P90AdvanceSecs:    roundToInt(p90),
		SampleCount:       len(intervals),
		SyncPattern:       pattern,
		ImpliedMethod:     method,
		Rationale:         rationale,
	}
}

// classify buckets the observed cadence (median interval + median Δlocation) into
// one of the three §5.1 sync patterns and returns the pattern, the composition
// method it implies, and a plain-English rationale the FE renders.
func classify(p50interval, p50dloc float64, cfg MonitorConfig) (pattern, method, rationale string) {
	frequent := p50interval < float64(cfg.ContinuousMaxIntervalSecs)
	small := p50dloc < float64(cfg.MaxDloc)
	sparse := p50interval >= float64(cfg.SessionBoundaryMinIntervalSecs)
	large := p50dloc >= float64(cfg.SessionBoundaryMinDloc)

	switch {
	case frequent && small:
		return SyncPatternContinuous, MethodGapSum, fmt.Sprintf(
			"Advances are frequent (median %ds apart) and small (median +%d loc), so whispersync is writing position continuously while reading — temporal gap-sum of the poll samples measures reading time directly.",
			roundToInt(p50interval), roundToInt(p50dloc))
	case sparse && large:
		return SyncPatternSessionBoundary, MethodPositionDelta, fmt.Sprintf(
			"Advances are sparse (median %ds apart) and large (median +%d loc), so whispersync is only writing position at session boundaries (device close/open) — reading time is reconstructed from position-delta × reading-speed, anchored at each advance.",
			roundToInt(p50interval), roundToInt(p50dloc))
	default:
		return SyncPatternUnknown, MethodGapSum, fmt.Sprintf(
			"The observed cadence is ambiguous (median %ds apart, +%d loc) — not clearly continuous nor session-boundary yet. Falling back to temporal gap-sum; run a calibration burst to classify with high-fidelity samples.",
			roundToInt(p50interval), roundToInt(p50dloc))
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
