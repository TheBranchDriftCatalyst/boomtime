// streaks_test.go — pins the streak / active-day helpers (streaks.go) against
// fixed fixtures. These feed the SVG stat-tile widgets (Part B Stage 1) and
// MUST stay in lockstep with the FE reference (grade.ts currentStreak /
// longestStreakInRange and the WidgetRenderer active-days-stat ratio) — each
// Entry's expectation is what the FE computes for the same series.
package stats

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Streak / active-day helpers (Part B Stage 1)", func() {
	DescribeTable("CurrentStreak — consecutive active days ending at the last day",
		func(daily []int64, want int) {
			Expect(CurrentStreak(daily)).To(Equal(want))
		},
		Entry("empty series", []int64{}, 0),
		Entry("nil series", []int64(nil), 0),
		Entry("all zero", []int64{0, 0, 0}, 0),
		Entry("trailing zero kills the streak", []int64{3600, 3600, 0}, 0),
		Entry("active tail after a gap", []int64{3600, 0, 1800, 3600}, 2),
		Entry("all active", []int64{1, 1, 1, 1}, 4),
		Entry("single active day", []int64{3600}, 1),
	)

	DescribeTable("LongestStreak — longest run of active days anywhere",
		func(daily []int64, want int) {
			Expect(LongestStreak(daily)).To(Equal(want))
		},
		Entry("empty series", []int64{}, 0),
		Entry("all zero", []int64{0, 0, 0}, 0),
		Entry("mid run beats the tail", []int64{1, 1, 1, 0, 1}, 3),
		Entry("trailing zero keeps the earlier run", []int64{3600, 3600, 0}, 2),
		Entry("all active", []int64{1, 1, 1, 1}, 4),
	)

	DescribeTable("ActiveDays — count of active days + range length",
		func(daily []int64, wantActive, wantTotal int) {
			active, total := ActiveDays(daily)
			Expect(active).To(Equal(wantActive))
			Expect(total).To(Equal(wantTotal))
		},
		Entry("empty series", []int64{}, 0, 0),
		Entry("all zero", []int64{0, 0, 0}, 0, 3),
		Entry("mixed", []int64{3600, 0, 1800, 0}, 2, 4),
		Entry("all active", []int64{1, 2}, 2, 2),
	)

	// The exported twin must be the SAME function the grade blend consumes —
	// a drift between them would let the widget disagree with the grade.
	It("LongestStreak matches the unexported longestStreak the grade uses", func() {
		for _, daily := range [][]int64{{}, {0}, {1}, {1, 0, 1, 1}, {3600, 3600, 0, 3600}} {
			Expect(LongestStreak(daily)).To(Equal(longestStreak(daily)))
		}
	})
})
