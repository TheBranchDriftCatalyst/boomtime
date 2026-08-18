// project_extras_ginkgo_test.go — ginkgo mirror of project_extras_test.go (gaka-tst-ginkgo).
// 1:1 case map (7 stdlib TestXxx):
//
//	TestProjectExtrasWriteReadSplit             → ToProjectStatistics extras > "writeSeconds/readSeconds totals + aligned dailyWriteRatio"
//	TestProjectExtrasAlignsToDailyTotal         → ToProjectStatistics extras > "daily arrays align to truncated DailyTotal (alignment bug guard)"
//	TestProjectExtrasNil                        → ToProjectStatistics extras > "nil extras still initialize non-nil arrays"
//	TestProjectExtrasBranchesCap                → ToProjectStatistics extras > "branches cap at top-12+Other; BranchesCount reports true distinct"
//	TestProjectFilesExcludesNonFileEntities     → ToProjectStatistics files > "excludes non-file entities from Files/FilesCount"
//	TestProjectLanguagesDailySumsToDailyTotal   → ToProjectStatistics languagesDaily > "sum equals DailyTotal per day; names match Languages"
//	TestProjectLanguagesDailyTopNBucketing      → ToProjectStatistics languagesDaily > "matrix caps to top-N+Other and preserves grand total"
package stats

import (
	"fmt"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ToProjectStatistics extras", func() {
	It("computes writeSeconds/readSeconds totals and aligns dailyWriteRatio index-for-index", func() {
		d1 := day(2025, 5, 1)
		d3 := day(2025, 5, 3)

		// Main rows drive the day series (d1..d3). The middle day has no main row but
		// the extras still cover it — the arrays must be length 3 and aligned.
		xs := []db.ProjectStatRow{
			{Day: d1, Language: "Go", Entity: "a.go", TotalSeconds: 100, Weekday: "4", Hour: "10"},
			{Day: d3, Language: "Go", Entity: "b.go", TotalSeconds: 60, Weekday: "6", Hour: "11"},
		}
		extras := &db.ProjectExtras{
			Daily: []db.ProjectDailyExtra{
				{Day: d1, WriteSeconds: 30, ReadSeconds: 70, DistinctEntities: 2},
				// d2 intentionally absent -> ratio 0, entities 0
				{Day: d3, WriteSeconds: 60, ReadSeconds: 0, DistinctEntities: 1},
			},
		}

		p := ToProjectStatistics(d1, d3, xs, extras)

		Expect(p.DailyTotal).To(HaveLen(3))
		Expect(p.DailyWriteRatio).To(HaveLen(3))
		Expect(p.DailyEntities).To(HaveLen(3))
		Expect(p.WriteSeconds).To(BeEquivalentTo(90))
		Expect(p.ReadSeconds).To(BeEquivalentTo(70))
		// d1: 30/(30+70)=0.3; d2: no file activity -> 0; d3: 60/60=1.0
		wantRatio := []float64{0.3, 0.0, 1.0}
		for i, w := range wantRatio {
			Expect(p.DailyWriteRatio[i]).To(BeNumerically("~", w, 1e-9), fmt.Sprintf("DailyWriteRatio[%d]", i))
		}
		wantEnt := []int64{2, 0, 1}
		for i, w := range wantEnt {
			Expect(p.DailyEntities[i]).To(Equal(w), fmt.Sprintf("DailyEntities[%d]", i))
		}
	})

	// TestProjectExtrasAlignsToDailyTotal: alignment bug guard — DailyTotal truncated
	// to last day with data, extras must match that truncated length.
	It("daily arrays align to truncated DailyTotal when main data ends early (alignment bug guard)", func() {
		d1 := day(2025, 5, 1)
		d2 := day(2025, 5, 2)
		// Main data only on d1/d2, but the requested range runs to d1+10 days.
		rangeEnd := d1.AddDate(0, 0, 10)
		xs := []db.ProjectStatRow{
			{Day: d1, Language: "Go", Entity: "a.go", TotalSeconds: 100, Weekday: "4", Hour: "10"},
			{Day: d2, Language: "Go", Entity: "b.go", TotalSeconds: 50, Weekday: "5", Hour: "11"},
		}
		extras := &db.ProjectExtras{
			Daily: []db.ProjectDailyExtra{
				{Day: d1, WriteSeconds: 10, ReadSeconds: 90, DistinctEntities: 1},
				{Day: d2, WriteSeconds: 25, ReadSeconds: 25, DistinctEntities: 1},
			},
		}
		p := ToProjectStatistics(d1, rangeEnd, xs, extras)

		// DailyTotal is truncated to the 2 days with data; the extra arrays must match.
		Expect(p.DailyTotal).To(HaveLen(2))
		Expect(p.DailyWriteRatio).To(HaveLen(len(p.DailyTotal)))
		Expect(p.DailyEntities).To(HaveLen(len(p.DailyTotal)))
		Expect(p.EntitiesCount).To(Equal(p.FilesCount))
	})

	// TestProjectExtrasNil ensures the daily arrays are always initialized (never
	// nil) and branches is empty when no extras are provided (tag path).
	It("nil extras still initialize non-nil arrays and empty branches", func() {
		d1 := day(2025, 5, 1)
		d2 := day(2025, 5, 2)
		xs := []db.ProjectStatRow{
			{Day: d1, Language: "Go", Entity: "a.go", TotalSeconds: 100, Weekday: "4", Hour: "10"},
			{Day: d2, Language: "Go", Entity: "b.go", TotalSeconds: 50, Weekday: "5", Hour: "11"},
		}
		p := ToProjectStatistics(d1, d2, xs, nil)
		Expect(p.DailyWriteRatio).NotTo(BeNil())
		Expect(p.DailyWriteRatio).To(HaveLen(2))
		Expect(p.DailyEntities).NotTo(BeNil())
		Expect(p.DailyEntities).To(HaveLen(2))
		Expect(p.Branches).NotTo(BeNil())
		Expect(p.Branches).To(HaveLen(0))
		Expect(p.WriteSeconds).To(BeEquivalentTo(0))
		Expect(p.ReadSeconds).To(BeEquivalentTo(0))
		Expect(p.BranchesCount).To(Equal(0))
	})

	// TestProjectExtrasBranchesCap: branch shaping caps at top-12 + "Other" while
	// branchesCount reports the true distinct count, and daily arrays align.
	It("branches cap at top-12+Other; BranchesCount reports true distinct count", func() {
		d1 := day(2025, 5, 1)
		d2 := day(2025, 5, 2)
		xs := []db.ProjectStatRow{
			{Day: d1, Language: "Go", Entity: "a.go", TotalSeconds: 10, Weekday: "4", Hour: "10"},
			{Day: d2, Language: "Go", Entity: "b.go", TotalSeconds: 10, Weekday: "5", Hour: "11"},
		}

		// 15 distinct branches; branch i has total i+1 seconds, split across d1/d2.
		var branchRows []db.ProjectBranchRow
		for i := 0; i < 15; i++ {
			name := fmt.Sprintf("branch-%02d", i)
			branchRows = append(branchRows,
				db.ProjectBranchRow{Day: d1, Branch: name, TotalSeconds: int64(i + 1), Pct: 0.01, DailyPct: 0.02},
				db.ProjectBranchRow{Day: d2, Branch: name, TotalSeconds: int64(i + 1), Pct: 0.01, DailyPct: 0.02},
			)
		}
		extras := &db.ProjectExtras{Branches: branchRows}

		p := ToProjectStatistics(d1, d2, xs, extras)

		Expect(p.BranchesCount).To(Equal(15))
		// capWithOther keeps 12 + 1 "Other" bucket = 13 entries.
		Expect(p.Branches).To(HaveLen(13))
		last := p.Branches[len(p.Branches)-1]
		Expect(last.Name).To(Equal("Other (3 more)"))
		// Every branch's TotalDaily aligns to the 2-day series.
		for _, b := range p.Branches {
			Expect(b.TotalDaily).To(HaveLen(2))
			Expect(b.PctDaily).To(HaveLen(2))
		}
		// The "Other" bucket sums the 3 smallest branches (totals 1+2+3 across 2 days = 12).
		Expect(last.TotalSeconds).To(BeEquivalentTo((1 + 2 + 3) * 2))
	})
})

var _ = Describe("ToProjectStatistics files filter (regression: 'Most active files')", func() {
	It("excludes non-file entities (domain/app) from Files list and FilesCount", func() {
		d1 := day(2025, 5, 1)
		xs := []db.ProjectStatRow{
			// Real files.
			{Day: d1, Language: "Go", Entity: "src/main.go", Ty: "file", TotalSeconds: 100, Weekday: "4", Hour: "10"},
			{Day: d1, Language: "TypeScript", Entity: "web/app.ts", Ty: "file", TotalSeconds: 60, Weekday: "4", Hour: "10"},
			// Browsing domains / apps that were leaking into "files".
			{Day: d1, Language: "Other", Entity: "github.com", Ty: "domain", TotalSeconds: 40, Weekday: "4", Hour: "10"},
			{Day: d1, Language: "Other", Entity: "https://app.vanta.com", Ty: "domain", TotalSeconds: 25, Weekday: "4", Hour: "10"},
			{Day: d1, Language: "Other", Entity: "Slack", Ty: "app", TotalSeconds: 15, Weekday: "4", Hour: "10"},
		}

		p := ToProjectStatistics(d1, d1, xs, nil)

		// Files list contains ONLY the two real files, no domains/apps.
		fileNames := map[string]bool{}
		for _, f := range p.Files {
			fileNames[f.Name] = true
		}
		for _, bad := range []string{"github.com", "https://app.vanta.com", "Slack"} {
			Expect(fileNames[bad]).To(BeFalse(), "files list leaked non-file entity "+bad)
		}
		Expect(fileNames["src/main.go"]).To(BeTrue())
		Expect(fileNames["web/app.ts"]).To(BeTrue())
		Expect(p.Files).To(HaveLen(2))

		// filesCount / entitiesCount reflect distinct file entities only.
		Expect(p.FilesCount).To(Equal(2))
		Expect(p.EntitiesCount).To(Equal(2))

		// Total-time card is UNAFFECTED: it includes domains/apps too.
		Expect(p.TotalSeconds).To(BeEquivalentTo(100 + 60 + 40 + 25 + 15))
		// Languages breakdown also still sees every entity (Other from the domains/app).
		var haveOther bool
		for _, l := range p.Languages {
			if l.Name == "Other" {
				haveOther = true
			}
		}
		Expect(haveOther).To(BeTrue())
	})
})

var _ = Describe("ToProjectStatistics languagesDaily", func() {
	It("summing across every series per day equals DailyTotal; series names match Languages exactly", func() {
		d1 := day(2025, 5, 1)
		d2 := day(2025, 5, 2)
		d3 := day(2025, 5, 3)
		xs := []db.ProjectStatRow{
			{Day: d1, Language: "Go", Entity: "a.go", Ty: "file", TotalSeconds: 100, Weekday: "4", Hour: "10"},
			{Day: d1, Language: "TypeScript", Entity: "app.ts", Ty: "file", TotalSeconds: 40, Weekday: "4", Hour: "10"},
			// d2 has only TypeScript.
			{Day: d2, Language: "TypeScript", Entity: "app.ts", Ty: "file", TotalSeconds: 60, Weekday: "5", Hour: "11"},
			// d3 has Go + a browsing "Other" (still counts toward totals).
			{Day: d3, Language: "Go", Entity: "b.go", Ty: "file", TotalSeconds: 30, Weekday: "6", Hour: "12"},
			{Day: d3, Language: "Other", Entity: "github.com", Ty: "domain", TotalSeconds: 20, Weekday: "6", Hour: "12"},
		}

		p := ToProjectStatistics(d1, d3, xs, nil)

		n := len(p.DailyTotal)
		Expect(n).To(Equal(3))
		Expect(p.LanguagesDaily).NotTo(BeEmpty())

		// Names must match the Languages breakdown exactly (same order / same set,
		// including any "Other (N more)" bucket).
		Expect(p.LanguagesDaily).To(HaveLen(len(p.Languages)))
		for i := range p.Languages {
			Expect(p.LanguagesDaily[i].Name).To(Equal(p.Languages[i].Name),
				fmt.Sprintf("LanguagesDaily[%d].Name mismatches Languages[%d].Name", i, i))
			Expect(p.LanguagesDaily[i].Daily).To(HaveLen(n),
				fmt.Sprintf("LanguagesDaily[%d] must be aligned to DailyTotal", i))
		}

		// The stacked-column invariant: per-day sum over languages == DailyTotal[day].
		for di := 0; di < n; di++ {
			var s int64
			for _, ld := range p.LanguagesDaily {
				s += ld.Daily[di]
			}
			Expect(s).To(Equal(p.DailyTotal[di]),
				fmt.Sprintf("day %d: sum(LanguagesDaily) != DailyTotal", di))
		}
	})

	It("caps LanguagesDaily to top-N+Other and preserves grand total", func() {
		d1 := day(2025, 5, 1)
		// resourceTopN+3 distinct languages; language i has i+1 seconds on d1.
		var xs []db.ProjectStatRow
		numLangs := resourceTopN + 3
		var grand int64
		for i := 0; i < numLangs; i++ {
			secs := int64(i + 1)
			grand += secs
			xs = append(xs, db.ProjectStatRow{
				Day:          d1,
				Language:     fmt.Sprintf("lang-%02d", i),
				Entity:       fmt.Sprintf("f%02d.x", i),
				Ty:           "file",
				TotalSeconds: secs,
				Weekday:      "4",
				Hour:         "10",
			})
		}

		p := ToProjectStatistics(d1, d1, xs, nil)

		// Capped to top-N + one "Other (N more)" bucket.
		Expect(p.LanguagesDaily).To(HaveLen(resourceTopN + 1))
		Expect(p.LanguagesDaily[len(p.LanguagesDaily)-1].Name).To(
			Equal(fmt.Sprintf("Other (%d more)", numLangs-resourceTopN)))

		// Sum across every series on d1 equals the grand total (nothing dropped).
		var s int64
		for _, ld := range p.LanguagesDaily {
			s += ld.Daily[0]
		}
		Expect(s).To(Equal(grand))
		Expect(s).To(Equal(p.DailyTotal[0]))
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
