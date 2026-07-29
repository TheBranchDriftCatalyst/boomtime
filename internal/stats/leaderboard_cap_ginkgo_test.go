// leaderboard_cap_ginkgo_test.go — ginkgo mirror of leaderboard_cap_test.go (gaka-tst-ginkgo).
// 1:1 case map (3 stdlib TestXxx):
//   TestToLeaderboardsPayloadGlobalCapAndSort              → ToLeaderboardsPayload > "top-20 cap and desc sort"
//   TestToLeaderboardsPayloadTieBreakByName                → ToLeaderboardsPayload > "tie-break by name ascending"
//   TestToLeaderboardsPayloadFiltersUnder60AndEmptyLangBuckets
//                                                          → ToLeaderboardsPayload > "filters <=60 and omits empty lang buckets"
package stats

import (
	"fmt"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ToLeaderboardsPayload cap/sort/filter", func() {
	It("caps global to top-20 and sorts descending by value", func() {
		// 25 distinct senders, all > 60s, with strictly descending totals so the
		// top-20 cap and value-desc ordering are unambiguous.
		var rows []db.LeaderboardRow
		for i := 0; i < 25; i++ {
			rows = append(rows, db.LeaderboardRow{
				Sender:       fmt.Sprintf("user%02d", i),
				Language:     "Go",
				TotalSeconds: int64(10000 - i*100), // 10000 down to 7600, all > 60
			})
		}

		p := ToLeaderboardsPayload(rows)

		Expect(p.Global).To(HaveLen(20))
		// Highest total is user00 (10000); descending thereafter.
		Expect(p.Global[0].Name).To(Equal("user00"))
		Expect(p.Global[0].Value).To(BeEquivalentTo(10000))
		for i := 1; i < len(p.Global); i++ {
			Expect(p.Global[i-1].Value).To(BeNumerically(">=", p.Global[i].Value),
				fmt.Sprintf("Global not sorted desc at %d", i))
		}
		// The 21st..25th (lowest totals) must be dropped: user20..user24 absent.
		for _, ut := range p.Global {
			Expect(ut.Name).NotTo(Equal("user24"), "user24 (lowest) should have been dropped by top-20 cap")
		}
	})

	It("tie-breaks equal totals by sender name ascending", func() {
		rows := []db.LeaderboardRow{
			{Sender: "charlie", Language: "Go", TotalSeconds: 500},
			{Sender: "alice", Language: "Go", TotalSeconds: 500},
			{Sender: "bob", Language: "Go", TotalSeconds: 500},
		}
		p := ToLeaderboardsPayload(rows)
		got := []string{p.Global[0].Name, p.Global[1].Name, p.Global[2].Name}
		Expect(got).To(Equal([]string{"alice", "bob", "charlie"}))
	})

	It("filters totals <=60 and omits empty language buckets", func() {
		rows := []db.LeaderboardRow{
			{Sender: "keepGlobal", Language: "Go", TotalSeconds: 120},
			// Exactly 60 is filtered out (filter is v > 60, strictly).
			{Sender: "borderline", Language: "Go", TotalSeconds: 60},
			// Sub-60 sender in Python: its only total is 30 -> Python bucket empty -> omitted.
			{Sender: "tiny", Language: "Python", TotalSeconds: 30},
		}
		p := ToLeaderboardsPayload(rows)

		// Global: only keepGlobal survives (120 > 60); borderline (==60) and tiny (30) dropped.
		Expect(p.Global).To(HaveLen(1))
		Expect(p.Global[0].Name).To(Equal("keepGlobal"))

		// Go bucket has keepGlobal (>60); borderline (==60) filtered but bucket non-empty.
		Expect(p.Lang).To(HaveKey("Go"))
		goList := p.Lang["Go"]
		Expect(goList).To(HaveLen(1))
		Expect(goList[0].Name).To(Equal("keepGlobal"))

		// Python bucket only had a sub-60 total -> empty list -> must be omitted entirely.
		Expect(p.Lang).NotTo(HaveKey("Python"))
	})
})
