package stats

import (
	"math"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// mkPayload builds a synthetic StatsPayload from a repeating weekly activity
// pattern. secondsPerActiveDay fills each active slot; pattern is a 7-day
// on/off template tiled across rangeDays.
func mkPayload(rangeDays int, pattern []bool, secondsPerActiveDay int64, langs, projects int) *model.StatsPayload {
	daily := make([]int64, rangeDays)
	var total int64
	for i := range daily {
		if pattern[i%len(pattern)] {
			daily[i] = secondsPerActiveDay
			total += secondsPerActiveDay
		}
	}
	return &model.StatsPayload{
		TotalSeconds:   total,
		DailyAvg:       float64(total) / float64(rangeDays),
		DailyTotal:     daily,
		LanguagesCount: langs,
		ProjectsCount:  projects,
	}
}

// The three calibration personas from the gaka-hsj plan. These are golden
// tests: if a config change moves a persona's letter, the diff shows exactly
// which band shifted.
func TestGradePersonas(t *testing.T) {
	cases := []struct {
		name      string
		p         *model.StatsPayload
		wantLevel string
		pctLo     float64 // expected percentile band (guards against silent
		pctHi     float64 // formula drift without asserting exact floats)
	}{
		{
			name:      "full-timer 6h x 5d/wk, 4 langs, 3 projects",
			p:         mkPayload(30, []bool{true, true, true, true, true, false, false}, 6*3600, 4, 3),
			wantLevel: "A-",
			pctLo:     30, pctHi: 45,
		},
		{
			name:      "casual 1h x 3d/wk, 2 langs, 1 project",
			p:         mkPayload(30, []bool{true, true, true, false, false, false, false}, 3600, 2, 1),
			wantLevel: "B-",
			pctLo:     62.5, pctHi: 75,
		},
		{
			name:      "grinder 8h daily, 6 langs, 5 projects",
			p:         mkPayload(30, []bool{true, true, true, true, true, true, true}, 8*3600, 6, 5),
			wantLevel: "A",
			pctLo:     12.5, pctHi: 25,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := Grade(tc.p)
			if g.Level != tc.wantLevel {
				t.Errorf("Level = %q (percentile %.2f), want %q", g.Level, g.Percentile, tc.wantLevel)
			}
			if g.Percentile < tc.pctLo || g.Percentile > tc.pctHi {
				t.Errorf("Percentile = %.2f, want in [%.1f, %.1f]", g.Percentile, tc.pctLo, tc.pctHi)
			}
			if len(g.Subs) != 6 {
				t.Errorf("len(Subs) = %d, want 6", len(g.Subs))
			}
		})
	}
}

func TestGradeEmptyPayloadIsC(t *testing.T) {
	g := Grade(&model.StatsPayload{})
	if g.Level != "C" {
		t.Errorf("empty payload Level = %q, want C", g.Level)
	}
	if g.Percentile != 100 {
		t.Errorf("empty payload Percentile = %v, want 100", g.Percentile)
	}
}

// More coding time must never make the grade worse (upstream property: every
// CDF is monotonically increasing, percentile = 1 - blend).
func TestGradeMonotonicInVolume(t *testing.T) {
	pattern := []bool{true, true, true, false, false, false, false}
	prev := 101.0
	for _, hrs := range []int64{1, 2, 4, 8} {
		g := Grade(mkPayload(30, pattern, hrs*3600, 2, 2))
		if g.Percentile > prev {
			t.Errorf("percentile worsened as volume grew: %d h/day -> %.2f (prev %.2f)", hrs, g.Percentile, prev)
		}
		prev = g.Percentile
	}
}

// The threshold ladder is upstream's: percentile <= first-matching threshold.
func TestGradeThresholdLadder(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{
		{0.5, "S"}, {1, "S"}, {1.01, "A+"}, {12.5, "A+"}, {25, "A"},
		{37.5, "A-"}, {50, "B+"}, {62.5, "B"}, {75, "B-"}, {87.5, "C+"}, {100, "C"},
	}
	for _, tc := range cases {
		got := gradeLevels[len(gradeLevels)-1]
		for i, t2 := range gradeThresholds {
			if tc.pct <= t2 {
				got = gradeLevels[i]
				break
			}
		}
		if got != tc.want {
			t.Errorf("percentile %.2f -> %q, want %q", tc.pct, got, tc.want)
		}
	}
}

// A 3-day range with 100% activity must NOT read as perfect consistency — the
// MinRangeDays floor keeps short ranges honest.
func TestGradeShortRangeGuard(t *testing.T) {
	short := Grade(mkPayload(3, []bool{true}, 4*3600, 2, 2))
	var active SubScore
	for _, s := range short.Subs {
		if s.Metric == "activeDays" {
			active = s
		}
	}
	// 3 active days over max(3, 7) -> 42.86, not 100.
	if math.Abs(active.Raw-42.857) > 0.01 {
		t.Errorf("short-range activeDays raw = %.3f, want ~42.857 (floored denominator)", active.Raw)
	}
}

func TestLongestStreak(t *testing.T) {
	cases := []struct {
		daily []int64
		want  int
	}{
		{nil, 0},
		{[]int64{0, 0, 0}, 0},
		{[]int64{1, 1, 1}, 3},
		{[]int64{1, 0, 1, 1, 0, 1, 1, 1}, 3},
		{[]int64{5, 5, 0, 0, 5}, 2},
	}
	for _, tc := range cases {
		if got := longestStreak(tc.daily); got != tc.want {
			t.Errorf("longestStreak(%v) = %d, want %d", tc.daily, got, tc.want)
		}
	}
}

// CDFs are verbatim upstream: exponential_cdf(1) = 0.5, log_normal_cdf(1) = 0.5.
func TestGradeCDFsMatchUpstream(t *testing.T) {
	if got := exponentialCDF(1); math.Abs(got-0.5) > 1e-12 {
		t.Errorf("exponentialCDF(1) = %v, want 0.5", got)
	}
	if got := logNormalCDF(1); math.Abs(got-0.5) > 1e-12 {
		t.Errorf("logNormalCDF(1) = %v, want 0.5", got)
	}
	if got := exponentialCDF(0); got != 0 {
		t.Errorf("exponentialCDF(0) = %v, want 0", got)
	}
	if got := logNormalCDF(0); got != 0 {
		t.Errorf("logNormalCDF(0) = %v, want 0", got)
	}
}
