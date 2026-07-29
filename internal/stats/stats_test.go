// stats_ginkgo_test.go — ginkgo mirror of stats_test.go (gaka-tst-ginkgo).
// 1:1 case map (3 stdlib TestXxx with 6 subtests → 1 DescribeTable of 6 Entries
// plus 2 Its):
//   TestCompoundDuration/*        → CompoundDuration > entry per name
//   TestToStatsPayloadShaping     → ToStatsPayload > "shapes projects/languages/dailies"
//   TestToLeaderboardsPayload     → ToLeaderboardsPayload > "filters <60 and buckets by language"
package stats

import (
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CompoundDuration", func() {
	DescribeTable("formats seconds like upstream Utils.compoundDuration",
		func(in *int64, want string) {
			Expect(CompoundDuration(in)).To(Equal(want))
		},
		Entry("nil", (*int64)(nil), "no data"),
		Entry("zero", ptr(0), "no data"),
		// 2h 15m 30s = 8130s -> drop the seconds unit -> "2 hrs 15 min".
		Entry("hours+min", ptr(2*3600+15*60+30), "2 hrs 15 min"),
		// 90s = 1 min 30 sec -> drop sec -> "1 min".
		Entry("just-min", ptr(90), "1 min"),
		// 45s -> only seconds -> init drops it -> "".
		Entry("just-sec", ptr(45), ""),
		// 26h = 1 day 2 hrs; compoundDuration drops the LAST non-zero unit
		// (init), leaving "1 day" (matches Utils.compoundDuration exactly).
		Entry("day", ptr(24*3600+2*3600), "1 day"),
	)
})

var _ = Describe("ToStatsPayload", func() {
	It("shapes projects/languages/daily arrays from stat rows", func() {
		t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		t1 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC) // 2 days inclusive

		day1 := t0
		day2 := t1
		rows := []db.StatRow{
			{Day: day1, Project: "alpha", Language: "Go", Editor: "vim", Platform: "linux", Machine: "m1", Entity: "a.go", TotalSeconds: 100, Pct: 0.5, DailyPct: 1.0},
			{Day: day1, Project: "beta", Language: "Go", Editor: "vim", Platform: "linux", Machine: "m1", Entity: "b.go", TotalSeconds: 50, Pct: 0.25, DailyPct: 0.5},
			{Day: day2, Project: "alpha", Language: "Rust", Editor: "code", Platform: "mac", Machine: "m2", Entity: "c.rs", TotalSeconds: 50, Pct: 0.25, DailyPct: 1.0},
		}

		p := ToStatsPayload(t0, t1, rows, nil)

		Expect(p.TotalSeconds).To(BeEquivalentTo(200))
		Expect(p.DailyTotal).To(HaveLen(2))
		Expect(p.DailyTotal[0]).To(BeEquivalentTo(150))
		Expect(p.DailyTotal[1]).To(BeEquivalentTo(50))
		Expect(p.DailyAvg).To(Equal(float64(100)))

		// Projects: alpha appears both days (100 + 50 = 150), beta only day1 (50, 0).
		var alpha *int64
		for i := range p.Projects {
			if p.Projects[i].Name == "alpha" {
				v := p.Projects[i].TotalSeconds
				alpha = &v
				Expect(p.Projects[i].TotalDaily).To(HaveLen(2))
				Expect(p.Projects[i].TotalDaily[0]).To(BeEquivalentTo(100))
				Expect(p.Projects[i].TotalDaily[1]).To(BeEquivalentTo(50))
			}
		}
		Expect(alpha).NotTo(BeNil())
		Expect(*alpha).To(BeEquivalentTo(150))

		// Languages: Go (day1 only, len-2 daily) and Rust (day2).
		Expect(p.Languages).To(HaveLen(2))
	})
})

var _ = Describe("ToLeaderboardsPayload", func() {
	It("filters senders with totals <60 and buckets remainder by language", func() {
		rows := []db.LeaderboardRow{
			{Project: "p", Language: "Go", Sender: "alice", TotalSeconds: 500},
			{Project: "p", Language: "Go", Sender: "bob", TotalSeconds: 40}, // < 60, filtered
			{Project: "q", Language: "Rust", Sender: "alice", TotalSeconds: 300},
		}
		lb := ToLeaderboardsPayload(rows)
		// alice total = 800 global; bob filtered out.
		Expect(lb.Global).To(HaveLen(1))
		Expect(lb.Global[0].Name).To(Equal("alice"))
		Expect(lb.Global[0].Value).To(BeEquivalentTo(800))
		Expect(lb.Lang).To(HaveKey("Go"))
		// Go has alice 500 (bob filtered).
		Expect(lb.Lang["Go"]).To(HaveLen(1))
		Expect(lb.Lang["Go"][0].Value).To(BeEquivalentTo(500))
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
func ptr(v int64) *int64 { return &v }

