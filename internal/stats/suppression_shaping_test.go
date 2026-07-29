// suppression_shaping_ginkgo_test.go — ginkgo mirror of suppression_shaping_test.go (gaka-tst-ginkgo).
// 1:1 case map (1 stdlib TestXxx):
//
//	TestSuppressionShapingExcluded → ToStatsPayload > "shaping doesn't reintroduce SUPPRESS across breakdowns"
package stats

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ToStatsPayload suppression shaping (gaka-tst-ginkgo)", func() {
	It("shaping never reintroduces a SUPPRESS row into any breakdown", func() {
		d1 := day(2025, 6, 1)
		// Rows as the DB would return them AFTER excluding SUPPRESS: only KEEP values.
		rows := []db.StatRow{
			{Day: d1, Project: "keepProj", Language: "Go", Editor: "vim", Platform: "linux", Machine: "laptop", Entity: "a.go", TotalSeconds: 300},
		}
		// Categories as GetCategoryDaily would return them after excluding SUPPRESS.
		cats := []db.CategoryDailyRow{
			{Day: d1, Category: "Coding", TotalSeconds: 300, Pct: 1, DailyPct: 1},
		}

		p := ToStatsPayload(d1, d1, rows, cats)

		// SUPPRESS must not appear in any breakdown.
		for _, r := range p.Projects {
			Expect(r.Name).NotTo(Equal("SUPPRESS"), "projects breakdown leaked SUPPRESS")
		}
		for _, r := range p.Languages {
			Expect(r.Name).NotTo(Equal("SUPPRESS"), "languages breakdown leaked SUPPRESS")
		}
		for _, r := range p.Categories {
			Expect(r.Name).NotTo(Equal("SUPPRESS"), "categories breakdown leaked SUPPRESS")
		}
		// KEEP present and totals conserved (no time invented/lost by shaping).
		Expect(p.TotalSeconds).To(BeEquivalentTo(300))
		Expect(p.Projects).To(HaveLen(1))
		Expect(p.Projects[0].Name).To(Equal("keepProj"))
		Expect(p.Categories).To(HaveLen(1))
		Expect(p.Categories[0].Name).To(Equal("Coding"))
	})
})
