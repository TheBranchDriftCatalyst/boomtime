// grade.go computes the letter grade shown on the stats-card-with-grade widget
// (gaka-hsj). It is a faithful port of github-readme-stats' rank algorithm
// (src/calculateRank.js): the same two CDFs, the same weighted-blend shape, the
// same percentile thresholds and level ladder. The ONLY local adaptation is the
// metric set — GitHub's commits/prs/issues/reviews/stars/followers are swapped
// for coding-time equivalents, each with a tunable median. Lower percentile is
// better, exactly like upstream (S ≈ top 1%).
package stats

import (
	"math"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// gradeLevels and gradeThresholds are verbatim from github-readme-stats:
// the level at the first threshold with percentile <= t.
var (
	gradeLevels     = []string{"S", "A+", "A", "A-", "B+", "B", "B-", "C+", "C"}
	gradeThresholds = []float64{1, 12.5, 25, 37.5, 50, 62.5, 75, 87.5, 100}
)

// exponentialCDF is upstream's exponential_cdf: 1 - 2^-x.
func exponentialCDF(x float64) float64 { return 1 - math.Pow(2, -x) }

// logNormalCDF is upstream's log_normal_cdf: x / (1 + x).
func logNormalCDF(x float64) float64 { return x / (1 + x) }

// GradeConfig holds the medians + weights for each metric. Weights mirror the
// upstream slot each metric replaces (commits=2, prs=3, issues=1, reviews=1,
// stars=4, followers=1). Medians are the local tuning knobs — "a median coder
// scores 50th percentile" is the calibration intent.
type GradeConfig struct {
	StreakMedian float64 // days; upstream commits slot (weight 2, exponential)
	StreakWeight float64

	ActiveMedian float64 // active-day ratio x100; upstream prs slot (weight 3, exponential)
	ActiveWeight float64

	LanguagesMedian float64 // distinct languages; upstream issues slot (weight 1, exponential)
	LanguagesWeight float64

	ProjectsMedian float64 // distinct projects; upstream reviews slot (weight 1, exponential)
	ProjectsWeight float64

	DailyAvgMedian float64 // seconds/day; upstream stars slot (weight 4, log-normal)
	DailyAvgWeight float64

	HoursMedian float64 // total hours in range; upstream followers slot (weight 1, log-normal)
	HoursWeight float64

	// MinRangeDays floors the active-ratio denominator so a 1-day range can't
	// score 100% consistency (short-range inflation guard).
	MinRangeDays int
}

// DefaultGradeConfig is the shipped calibration (see gaka-hsj plan). Kept as a
// package-level var so cmd/boomtime (via ApplyGradeConfigFromEnv) can tune it
// once at boot without threading a config value through every renderer.
var DefaultGradeConfig = GradeConfig{
	StreakMedian: 5, StreakWeight: 2,
	ActiveMedian: 50, ActiveWeight: 3,
	LanguagesMedian: 3, LanguagesWeight: 1,
	ProjectsMedian: 3, ProjectsWeight: 1,
	DailyAvgMedian: 7200, DailyAvgWeight: 4,
	HoursMedian: 40, HoursWeight: 1,
	MinRangeDays: 7,
}

// SubScore is one metric's contribution — kept on the result for debugging and
// future tuning (a widget tooltip or a /grade debug endpoint can surface it).
type SubScore struct {
	Metric string  `json:"metric"`
	Raw    float64 `json:"raw"`    // metric value before normalization
	Median float64 `json:"median"` // config median it was divided by
	Score  float64 `json:"score"`  // CDF output, 0..1
	Weight float64 `json:"weight"`
}

// GradeResult is the computed grade for one StatsPayload.
type GradeResult struct {
	Level      string     `json:"level"`      // S, A+, A, A-, B+, B, B-, C+, C
	Percentile float64    `json:"percentile"` // 0..100, LOWER is better (upstream convention)
	Subs       []SubScore `json:"subs"`
}

// Grade computes the letter grade for a stats payload with the default config.
func Grade(p *model.StatsPayload) GradeResult { return GradeWith(p, DefaultGradeConfig) }

// GradeWith is Grade with an explicit config — pure, payload-only, no DB.
func GradeWith(p *model.StatsPayload, cfg GradeConfig) GradeResult {
	rangeDays := len(p.DailyTotal)
	activeDays := 0
	for _, s := range p.DailyTotal {
		if s > 0 {
			activeDays++
		}
	}
	denom := max(rangeDays, cfg.MinRangeDays)
	activeRatio := float64(activeDays) / float64(denom) * 100

	subs := []SubScore{
		{Metric: "streak", Raw: float64(longestStreak(p.DailyTotal)), Median: cfg.StreakMedian, Weight: cfg.StreakWeight},
		{Metric: "activeDays", Raw: activeRatio, Median: cfg.ActiveMedian, Weight: cfg.ActiveWeight},
		{Metric: "languages", Raw: float64(p.LanguagesCount), Median: cfg.LanguagesMedian, Weight: cfg.LanguagesWeight},
		{Metric: "projects", Raw: float64(p.ProjectsCount), Median: cfg.ProjectsMedian, Weight: cfg.ProjectsWeight},
		{Metric: "dailyAvg", Raw: p.DailyAvg, Median: cfg.DailyAvgMedian, Weight: cfg.DailyAvgWeight},
		{Metric: "hours", Raw: float64(p.TotalSeconds) / 3600, Median: cfg.HoursMedian, Weight: cfg.HoursWeight},
	}
	// First four use the exponential CDF (count-like, saturate fast); the two
	// volume metrics use log-normal (heavy-tailed) — the same split upstream
	// applies to commits/prs/issues/reviews vs stars/followers.
	var weightSum, blend float64
	for i := range subs {
		x := 0.0
		if subs[i].Median > 0 {
			x = subs[i].Raw / subs[i].Median
		}
		switch subs[i].Metric {
		case "dailyAvg", "hours":
			subs[i].Score = logNormalCDF(x)
		default:
			subs[i].Score = exponentialCDF(x)
		}
		blend += subs[i].Weight * subs[i].Score
		weightSum += subs[i].Weight
	}

	percentile := 100.0
	if weightSum > 0 {
		percentile = (1 - blend/weightSum) * 100
	}
	level := gradeLevels[len(gradeLevels)-1]
	for i, t := range gradeThresholds {
		if percentile <= t {
			level = gradeLevels[i]
			break
		}
	}
	return GradeResult{Level: level, Percentile: percentile, Subs: subs}
}

// longestStreak is the longest consecutive run of active days in the series.
func longestStreak(daily []int64) int {
	best, cur := 0, 0
	for _, s := range daily {
		if s > 0 {
			cur++
			best = max(best, cur)
		} else {
			cur = 0
		}
	}
	return best
}
