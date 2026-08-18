// grade_ginkgo_test.go — ginkgo mirror of grade_test.go (gaka-tst-ginkgo).
// 1:1 case map (6 stdlib TestXxx; TestGradePersonas has 3 subtests →
// DescribeTable of 3 Entries; TestLongestStreak has 5 subtests → DescribeTable
// of 5 Entries; TestGradeThresholdLadder has 11 subtests → DescribeTable of 11 Entries):
//
//	TestGradePersonas/*             → Grade > "persona entry per name"
//	TestGradeEmptyPayloadIsC        → Grade > "empty payload lands at C / percentile 100"
//	TestGradeMonotonicInVolume      → Grade > "monotonic: more coding time never worsens percentile"
//	TestGradeThresholdLadder/*      → gradeLevels ladder > entry per percentile
//	TestGradeShortRangeGuard        → Grade > "short-range guard: MinRangeDays keeps 3-day 100%-active honest"
//	TestLongestStreak/*             → longestStreak > entry per daily pattern
//	TestGradeCDFsMatchUpstream      → CDFs > "exponentialCDF and logNormalCDF match upstream at 0 and 1"
package stats

import (
	"math"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Grade personas (calibration golden tests)", func() {
	DescribeTable("known personas land at the expected level and percentile band",
		func(p *model.StatsPayload, wantLevel string, pctLo, pctHi float64) {
			g := Grade(p)
			Expect(g.Level).To(Equal(wantLevel))
			Expect(g.Percentile).To(BeNumerically(">=", pctLo))
			Expect(g.Percentile).To(BeNumerically("<=", pctHi))
			Expect(g.Subs).To(HaveLen(6))
		},
		Entry("full-timer 6h x 5d/wk, 4 langs, 3 projects",
			mkPayload(30, []bool{true, true, true, true, true, false, false}, 6*3600, 4, 3),
			"A-", 30.0, 45.0),
		Entry("casual 1h x 3d/wk, 2 langs, 1 project",
			mkPayload(30, []bool{true, true, true, false, false, false, false}, 3600, 2, 1),
			"B-", 62.5, 75.0),
		Entry("grinder 8h daily, 6 langs, 5 projects",
			mkPayload(30, []bool{true, true, true, true, true, true, true}, 8*3600, 6, 5),
			"A", 12.5, 25.0),
	)
})

var _ = Describe("Grade empty payload", func() {
	It("empty payload lands at C with percentile 100", func() {
		g := Grade(&model.StatsPayload{})
		Expect(g.Level).To(Equal("C"))
		Expect(g.Percentile).To(Equal(float64(100)))
	})
})

// More coding time must never make the grade worse (upstream property: every
// CDF is monotonically increasing, percentile = 1 - blend).
var _ = Describe("Grade volume monotonicity", func() {
	It("percentile never worsens as volume grows", func() {
		pattern := []bool{true, true, true, false, false, false, false}
		prev := 101.0
		for _, hrs := range []int64{1, 2, 4, 8} {
			g := Grade(mkPayload(30, pattern, hrs*3600, 2, 2))
			Expect(g.Percentile).To(BeNumerically("<=", prev),
				"percentile worsened as volume grew (upstream CDF monotonicity)")
			prev = g.Percentile
		}
	})
})

// The threshold ladder is upstream's: percentile <= first-matching threshold.
var _ = Describe("gradeLevels ladder", func() {
	DescribeTable("percentile <= first-matching threshold returns that level",
		func(pct float64, want string) {
			got := gradeLevels[len(gradeLevels)-1]
			for i, t2 := range gradeThresholds {
				if pct <= t2 {
					got = gradeLevels[i]
					break
				}
			}
			Expect(got).To(Equal(want))
		},
		Entry("0.5 → S", 0.5, "S"),
		Entry("1 → S", 1.0, "S"),
		Entry("1.01 → A+", 1.01, "A+"),
		Entry("12.5 → A+", 12.5, "A+"),
		Entry("25 → A", 25.0, "A"),
		Entry("37.5 → A-", 37.5, "A-"),
		Entry("50 → B+", 50.0, "B+"),
		Entry("62.5 → B", 62.5, "B"),
		Entry("75 → B-", 75.0, "B-"),
		Entry("87.5 → C+", 87.5, "C+"),
		Entry("100 → C", 100.0, "C"),
	)
})

// A 3-day range with 100% activity must NOT read as perfect consistency — the
// MinRangeDays floor keeps short ranges honest.
var _ = Describe("Grade short-range guard", func() {
	It("3-day 100%-active does not read as perfect consistency", func() {
		short := Grade(mkPayload(3, []bool{true}, 4*3600, 2, 2))
		var active SubScore
		for _, s := range short.Subs {
			if s.Metric == "activeDays" {
				active = s
			}
		}
		// 3 active days over max(3, 7) -> 42.86, not 100.
		Expect(math.Abs(active.Raw-42.857)).To(BeNumerically("<", 0.01),
			"short-range activeDays raw ~ 42.857 (floored denominator)")
	})
})

var _ = Describe("longestStreak", func() {
	DescribeTable("counts the longest run of non-zero days",
		func(daily []int64, want int) {
			Expect(longestStreak(daily)).To(Equal(want))
		},
		Entry("nil", []int64(nil), 0),
		Entry("all zero", []int64{0, 0, 0}, 0),
		Entry("all active", []int64{1, 1, 1}, 3),
		Entry("mixed with 3-run", []int64{1, 0, 1, 1, 0, 1, 1, 1}, 3),
		Entry("split by zeros", []int64{5, 5, 0, 0, 5}, 2),
	)
})

// CDFs are verbatim upstream: exponential_cdf(1) = 0.5, log_normal_cdf(1) = 0.5.
var _ = Describe("Grade CDFs match upstream", func() {
	It("exponentialCDF and logNormalCDF match upstream at 0 and 1", func() {
		Expect(math.Abs(exponentialCDF(1) - 0.5)).To(BeNumerically("<", 1e-12))
		Expect(math.Abs(logNormalCDF(1) - 0.5)).To(BeNumerically("<", 1e-12))
		Expect(exponentialCDF(0)).To(Equal(float64(0)))
		Expect(logNormalCDF(0)).To(Equal(float64(0)))
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
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
