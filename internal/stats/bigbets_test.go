// bigbets_ginkgo_test.go — ginkgo mirror of bigbets_test.go (gaka-tst-ginkgo).
// 1:1 case map (7 stdlib TestXxx):
//
//	TestCategoriesFoldIn      → ToStatsPayload categories > "folds category-daily rows aligned to day series"
//	TestCategoriesNil         → ToStatsPayload categories > "nil cats yields empty non-nil breakdown + zero count"
//	TestPunchcardPayload      → ToPunchcardPayload > "passes cells through and computes totals/max"
//	TestPunchcardEmpty        → ToPunchcardPayload > "nil cells yield all-zero payload"
//	TestSessionsPayload       → ToSessionsPayload > "summarizes, gap-fills daily, buckets histogram"
//	TestSessionsEmpty         → ToSessionsPayload > "empty input still gap-fills daily and 5 histogram bins"
//	TestMomentumPayload       → ToMomentumPayload > "picks top-N and aligns weekly series with gap-fill"
//	TestIsoWeekStart          → isoWeekStart > "maps a weekday back to the ISO week's Monday"
package stats

import (
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ---- Categories fold-in ----

var _ = Describe("ToStatsPayload categories fold-in", func() {
	It("folds category-daily rows aligned to the day series", func() {
		d1 := day(2025, 5, 1)
		d2 := day(2025, 5, 2)
		xs := []db.StatRow{
			{Day: d1, Project: "p", Language: "Go", Editor: "vim", Platform: "linux", Machine: "m", Entity: "a.go", TotalSeconds: 100},
			{Day: d2, Project: "p", Language: "Go", Editor: "vim", Platform: "linux", Machine: "m", Entity: "b.go", TotalSeconds: 50},
		}
		cats := []db.CategoryDailyRow{
			{Day: d1, Category: "coding", TotalSeconds: 80, Pct: 0.5, DailyPct: 0.8},
			{Day: d1, Category: "debugging", TotalSeconds: 20, Pct: 0.1, DailyPct: 0.2},
			{Day: d2, Category: "coding", TotalSeconds: 50, Pct: 0.3, DailyPct: 1.0},
		}
		p := ToStatsPayload(d1, d2, xs, cats)

		Expect(p.CategoriesCount).To(Equal(2))
		// Each category's TotalDaily aligns to the 2-day series.
		var coding *int64
		for i := range p.Categories {
			c := p.Categories[i]
			Expect(c.TotalDaily).To(HaveLen(2))
			if c.Name == "coding" {
				v := c.TotalSeconds
				coding = &v
				Expect(c.TotalDaily[0]).To(BeEquivalentTo(80))
				Expect(c.TotalDaily[1]).To(BeEquivalentTo(50))
			}
		}
		Expect(coding).NotTo(BeNil())
		Expect(*coding).To(BeEquivalentTo(130))
	})

	It("nil cats yields empty non-nil breakdown + zero count", func() {
		d1 := day(2025, 5, 1)
		xs := []db.StatRow{
			{Day: d1, Project: "p", Language: "Go", Editor: "vim", Platform: "linux", Machine: "m", Entity: "a.go", TotalSeconds: 100},
		}
		p := ToStatsPayload(d1, d1, xs, nil)
		Expect(p.Categories).NotTo(BeNil())
		Expect(p.Categories).To(HaveLen(0))
		Expect(p.CategoriesCount).To(Equal(0))
	})
})

// ---- Punchcard ----

var _ = Describe("ToPunchcardPayload", func() {
	It("passes cells through and computes totals/max", func() {
		cells := []db.PunchcardCell{
			{Dow: 1, Hour: 9, Seconds: 300},
			{Dow: 1, Hour: 10, Seconds: 900},
			{Dow: 3, Hour: 14, Seconds: 600},
		}
		p := ToPunchcardPayload(cells)
		Expect(p.TotalSeconds).To(BeEquivalentTo(1800))
		Expect(p.MaxSeconds).To(BeEquivalentTo(900))
		Expect(p.Cells).To(HaveLen(3))
		Expect(p.Cells[1].Dow).To(BeEquivalentTo(1))
		Expect(p.Cells[1].Hour).To(BeEquivalentTo(10))
		Expect(p.Cells[1].Seconds).To(BeEquivalentTo(900))
	})

	It("nil cells yield an all-zero payload", func() {
		p := ToPunchcardPayload(nil)
		Expect(p.MaxSeconds).To(BeEquivalentTo(0))
		Expect(p.TotalSeconds).To(BeEquivalentTo(0))
		Expect(p.Cells).To(HaveLen(0))
	})
})

// ---- Sessions ----

var _ = Describe("ToSessionsPayload", func() {
	It("summarizes rows, gap-fills daily to range, and buckets histogram", func() {
		d1 := day(2025, 5, 1)
		d2 := day(2025, 5, 2)
		// Range d1..d3 (3 days); d3 has no sessions -> gap-filled zero row.
		d3 := day(2025, 5, 3)

		rows := []db.SessionRow{
			{Day: d1, Seconds: 600},   // 10m -> "0–15m"
			{Day: d1, Seconds: 2000},  // ~33m -> "30–60m"
			{Day: d2, Seconds: 5400},  // 90m -> "1–2h"
			{Day: d2, Seconds: 100},   // <15m -> "0–15m"
			{Day: d2, Seconds: 10000}, // ~2.7h -> "2h+"
		}
		p := ToSessionsPayload(d1, d3, rows)

		Expect(p.Summary.Count).To(BeEquivalentTo(5))
		Expect(p.Summary.TotalSeconds).To(BeEquivalentTo(18100))
		Expect(p.Summary.MaxSeconds).To(BeEquivalentTo(10000))
		// avg = 18100/5 = 3620.
		Expect(p.Summary.AvgSeconds).To(BeEquivalentTo(3620))
		// sorted: [100,600,2000,5400,10000] -> median 2000.
		Expect(p.Summary.MedianSeconds).To(BeEquivalentTo(2000))

		// Daily gap-filled to 3 days.
		Expect(p.Daily).To(HaveLen(3))
		Expect(p.Daily[0].Date).To(Equal("2025-05-01"))
		Expect(p.Daily[0].Sessions).To(BeEquivalentTo(2))
		Expect(p.Daily[0].TotalSeconds).To(BeEquivalentTo(2600))
		Expect(p.Daily[0].LongestSeconds).To(BeEquivalentTo(2000))
		Expect(p.Daily[2].Date).To(Equal("2025-05-03"))
		Expect(p.Daily[2].Sessions).To(BeEquivalentTo(0))
		Expect(p.Daily[2].TotalSeconds).To(BeEquivalentTo(0))

		// Histogram: 5 fixed bins, in order.
		want := map[string]int64{"0–15m": 2, "15–30m": 0, "30–60m": 1, "1–2h": 1, "2h+": 1}
		Expect(p.Histogram).To(HaveLen(5))
		for _, b := range p.Histogram {
			Expect(b.Count).To(BeEquivalentTo(want[b.Label]), "bin "+b.Label)
		}
	})

	It("empty input still gap-fills daily and 5 histogram bins", func() {
		d1 := day(2025, 5, 1)
		d2 := day(2025, 5, 2)
		p := ToSessionsPayload(d1, d2, nil)
		Expect(p.Summary.Count).To(BeEquivalentTo(0))
		Expect(p.Summary.MedianSeconds).To(BeEquivalentTo(0))
		Expect(p.Summary.AvgSeconds).To(BeEquivalentTo(0))
		Expect(p.Daily).To(HaveLen(2))
		Expect(p.Histogram).To(HaveLen(5))
	})
})

// ---- Momentum ----

var _ = Describe("ToMomentumPayload", func() {
	It("picks top-N projects and aligns weekly series with gap-fill", func() {
		// Range spanning 3 ISO weeks. Pick Mondays.
		w1 := time.Date(2025, 5, 5, 0, 0, 0, 0, time.UTC)  // Mon
		w2 := time.Date(2025, 5, 12, 0, 0, 0, 0, time.UTC) // Mon
		w3 := time.Date(2025, 5, 19, 0, 0, 0, 0, time.UTC) // Mon

		rows := []db.MomentumRow{
			{Project: "alpha", WeekStart: w1, Seconds: 3600},
			{Project: "alpha", WeekStart: w3, Seconds: 7200}, // skips w2 -> gap-filled 0
			{Project: "beta", WeekStart: w2, Seconds: 1800},
			{Project: "gamma", WeekStart: w1, Seconds: 100},
		}
		// top=2 keeps alpha(10800) and beta(1800); gamma(100) dropped.
		p := ToMomentumPayload(w1, w3, rows, 2)

		Expect(p.Weeks).To(HaveLen(3))
		Expect(p.Weeks[0]).To(Equal("2025-05-05"))
		Expect(p.Weeks[1]).To(Equal("2025-05-12"))
		Expect(p.Weeks[2]).To(Equal("2025-05-19"))
		Expect(p.Projects).To(HaveLen(2), "top-2")
		// Ranked by total desc: alpha first.
		Expect(p.Projects[0].Name).To(Equal("alpha"))
		Expect(p.Projects[0].TotalSeconds).To(BeEquivalentTo(10800))
		// alpha weekly aligned to weeks: [3600, 0, 7200].
		Expect(p.Projects[0].Weekly).To(HaveLen(3))
		Expect(p.Projects[0].Weekly[0]).To(BeEquivalentTo(3600))
		Expect(p.Projects[0].Weekly[1]).To(BeEquivalentTo(0))
		Expect(p.Projects[0].Weekly[2]).To(BeEquivalentTo(7200))
		Expect(p.Projects[1].Name).To(Equal("beta"))
	})
})

var _ = Describe("isoWeekStart", func() {
	It("maps a weekday back to the ISO week's Monday", func() {
		// A Wednesday should map back to that week's Monday.
		wed := time.Date(2025, 5, 7, 15, 30, 0, 0, time.UTC)
		Expect(isoWeekStart(wed).Format("2006-01-02")).To(Equal("2025-05-05"))
		// A Monday maps to itself.
		mon := time.Date(2025, 5, 5, 0, 0, 0, 0, time.UTC)
		Expect(isoWeekStart(mon).Equal(mon)).To(BeTrue())
		// A Sunday maps back to the previous Monday.
		sun := time.Date(2025, 5, 11, 23, 0, 0, 0, time.UTC)
		Expect(isoWeekStart(sun).Format("2006-01-02")).To(Equal("2025-05-05"))
	})
})
