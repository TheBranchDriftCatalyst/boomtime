// case_fold_ginkgo_test.go — ginkgo mirror of case_fold_test.go (boom-0vp.13).
// 1:1 case map (6 stdlib TestXxx incl 7+2 subtests → 5 Its + 1 DescribeTable(7) + 1 DescribeTable(2)):
//
//	TestCaseFoldAggregationAcrossAxes → DescribeTable "case-fold aggregation across axes" (7 axes)
//	TestCaseFoldCategoryDaily          → It "GetCategoryDaily collapses 'coding' case variants"
//	TestCaseFoldRollupPath             → It "rollup path collapses MyProject/myproject case variants"
//	TestCaseFoldEntity                 → It "entity list + active files fold file path case variants"
//	TestCaseFoldHideCatchesVariants    → It "hide rule catches every case variant"
//	TestCaseFoldMultiDayCanonicalPick  → DescribeTable "multi-day canonical pick" (category + project)
package db

import (
	"context"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("case folding", func() {
	// Reusable seed helper for the per-axis case-fold block.
	// Returns the attributed seconds (200: 2× 100s beats).
	seedCaseBlockG := func(d *DB, sender, axis, low, mid, up string, start time.Time) int64 {
		ctx := seedTwoUserBlockCtx()
		tmpl := hbSeed{
			project: "P", language: "Go", editor: "vim", plugin: "pl",
			machine: "m", platform: "linux", branch: "main", category: "Coding",
			entity: "a.go",
		}
		set := func(tmpl *hbSeed, v string) {
			switch axis {
			case "project":
				tmpl.project = v
			case "language":
				tmpl.language = v
			case "editor":
				tmpl.editor = v
			case "plugin":
				tmpl.plugin = v
			case "machine":
				tmpl.machine = v
			case "platform":
				tmpl.platform = v
			case "branch":
				tmpl.branch = v
			case "category":
				tmpl.category = v
			case "entity":
				tmpl.entity = v
			}
		}
		brk := tmpl
		set(&brk, low)
		brk.ts = start
		brk.gap = 999999
		if axis == "project" {
			ensureProjectsG(d, ctx, sender, low)
		}
		insertSeedG(d, ctx, sender, brk)
		for i, v := range []string{mid, up} {
			h := tmpl
			set(&h, v)
			h.ts = start.Add(time.Duration(i+1) * time.Minute)
			h.gap = 100
			if axis == "project" {
				ensureProjectsG(d, ctx, sender, v)
			}
			insertSeedG(d, ctx, sender, h)
		}
		return 200
	}

	day := time.Date(2025, 6, 1, 8, 0, 0, 0, time.UTC)
	start := day.AddDate(0, 0, -1)
	end := day.AddDate(0, 0, 1)

	ginkgo.DescribeTable("aggregation across every axis collapses three case variants to ONE row with summed total",
		func(axis, low, mid, up string) {
			d := openTestDBG()
			f := newSenderG(d, "cfax_"+axis)
			ctx := f.Ctx()
			sender := f.Sender()
			ensureProjectsG(d, ctx, sender, "P")

			expected := seedCaseBlockG(d, sender, axis, low, mid, up, day)

			if axis == "category" {
				cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15, "UTC",
					HiddenSets{}, RenameSets{}, MemberSets{}, false)
				Expect(err).NotTo(HaveOccurred())
				var seen []string
				var total int64
				for _, c := range cats {
					if strings.EqualFold(c.Category, low) {
						seen = append(seen, c.Category)
						total += c.TotalSeconds
					}
				}
				Expect(seen).To(HaveLen(1), "[category] expected one folded row, got %v", seen)
				Expect(total).To(Equal(expected))
				return
			}
			rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC",
				HiddenSets{}, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			totals := axisTotals(rows, axis)
			var seen []string
			for k := range totals {
				if strings.EqualFold(k, low) {
					seen = append(seen, k)
				}
			}
			Expect(seen).To(HaveLen(1), "[%s] expected one folded row for %q variants", axis, low)
			Expect(totals[seen[0]]).To(Equal(expected))
			label := seen[0]
			Expect(label == low || label == mid || label == up).To(BeTrue(),
				"[%s] display label %q not one of raw inputs (%q/%q/%q)", axis, label, low, mid, up)
		},
		ginkgo.Entry("category", "category", "writing docs", "Writing Docs", "WRITING DOCS"),
		ginkgo.Entry("project", "project", "myproject", "MyProject", "MYPROJECT"),
		ginkgo.Entry("language", "language", "go", "Go", "GO"),
		ginkgo.Entry("editor", "editor", "vscode", "VSCode", "VSCODE"),
		ginkgo.Entry("platform", "platform", "linux", "Linux", "LINUX"),
		ginkgo.Entry("machine", "machine", "laptop", "Laptop", "LAPTOP"),
		ginkgo.Entry("branch", "branch", "main", "Main", "MAIN"),
	)

	ginkgo.It("GetCategoryDaily collapses 'coding' case variants to one row with summed total (200)", func() {
		d := openTestDBG()
		f := newSenderG(d, "casefoldcat")
		ctx := f.Ctx()
		sender := f.Sender()
		ensureProjectsG(d, ctx, sender, "P")

		day := time.Date(2025, 7, 1, 10, 0, 0, 0, time.UTC)
		insertSeedG(d, ctx, sender, hbSeed{project: "P", language: "Go", editor: "vim", entity: "a.go", category: "coding", ts: day, gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", language: "Go", editor: "vim", entity: "a.go", category: "Coding", ts: day.Add(time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", language: "Go", editor: "vim", entity: "a.go", category: "CODING", ts: day.Add(2 * time.Minute), gap: 100})

		start := day.AddDate(0, 0, -1)
		end := day.AddDate(0, 0, 1)

		cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var seen []string
		var total int64
		for _, c := range cats {
			if strings.EqualFold(c.Category, "coding") {
				seen = append(seen, c.Category)
				total += c.TotalSeconds
			}
		}
		Expect(seen).To(HaveLen(1))
		Expect(total).To(BeEquivalentTo(200))
		Expect(seen[0] == "coding" || seen[0] == "Coding" || seen[0] == "CODING").To(BeTrue())
		Expect(strings.EqualFold(seen[0], "coding")).To(BeTrue())
	})

	ginkgo.It("rollup fast path merges MyProject/myproject case variants (200s conserved)", func() {
		d := openTestDBG()
		f := newSenderG(d, "casefoldroll")
		ctx := f.Ctx()
		sender := f.Sender()

		day := time.Date(2025, 8, 1, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, ctx, sender, "MyProject", "myproject")
		insertSeedG(d, ctx, sender, hbSeed{project: "MyProject", language: "Go", editor: "vim", entity: "a.go", ts: day, gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "MyProject", language: "Go", editor: "vim", entity: "a.go", ts: day.Add(time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "myproject", language: "Go", editor: "vim", entity: "a.go", ts: day.Add(2 * time.Minute), gap: 100})
		f.RefreshRollup(day.AddDate(0, 0, -1))

		start := day.AddDate(0, 0, -1)
		end := day.AddDate(0, 0, 1)

		roll, err := d.GetUserActivityRollup(ctx, sender, start, end, HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		totals := axisTotals(roll, "project")
		var seen []string
		for k := range totals {
			if strings.EqualFold(k, "myproject") {
				seen = append(seen, k)
			}
		}
		Expect(seen).To(HaveLen(1))
		Expect(totals[seen[0]]).To(BeEquivalentTo(200))
	})

	ginkgo.It("entity list + active files fold file path case variants (src/main.go)", func() {
		d := openTestDBG()
		f := newSenderG(d, "casefoldent")
		ctx := f.Ctx()
		sender := f.Sender()

		day := time.Date(2025, 9, 1, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, ctx, sender, "P")

		insertSeedG(d, ctx, sender, hbSeed{project: "P", language: "Go", entity: "src/main.go", ts: day, gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", language: "Go", entity: "src/Main.go", ts: day.Add(time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", language: "Go", entity: "SRC/MAIN.GO", ts: day.Add(2 * time.Minute), gap: 100})

		list, _, err := d.ListEntitiesByType(ctx, sender, "file", 100)
		Expect(err).NotTo(HaveOccurred())
		var seen []EntitySummary
		for _, e := range list {
			if strings.EqualFold(e.Entity, "src/main.go") {
				seen = append(seen, e)
			}
		}
		Expect(seen).To(HaveLen(1))
		Expect(seen[0].Count).To(BeNumerically(">=", 2))

		files, _, err := d.GetActiveFiles(ctx, sender, day.AddDate(0, 0, -1), day.AddDate(0, 0, 1), 15, 100,
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var mainMatches int
		for _, af := range files {
			if strings.EqualFold(af.Entity, "src/main.go") {
				mainMatches++
				Expect(af.Seconds).To(BeEquivalentTo(200))
			}
		}
		Expect(mainMatches).To(Equal(1))
	})

	ginkgo.It("hide rule authored in one casing catches every case variant", func() {
		d := openTestDBG()
		f := newSenderG(d, "casefoldhide")
		ctx := f.Ctx()
		sender := f.Sender()

		day := time.Date(2025, 10, 1, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, ctx, sender, "MyProject", "myproject", "Keep")

		insertSeedG(d, ctx, sender, hbSeed{project: "MyProject", entity: "a.go", ts: day, gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "MyProject", entity: "a.go", ts: day.Add(time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "myproject", entity: "a.go", ts: day.Add(2 * time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "Keep", entity: "a.go", ts: day.Add(3 * time.Minute), gap: 100})

		_, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "MYPROJECT", nil)
		Expect(err).NotTo(HaveOccurred())
		hs, err := d.LoadHiddenSets(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(hs.AnyHidden()).To(BeTrue())

		start := day.AddDate(0, 0, -1)
		end := day.AddDate(0, 0, 1)
		rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", hs, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		totals := axisTotals(rows, "project")
		for k := range totals {
			Expect(strings.EqualFold(k, "MyProject")).To(BeFalse(), "case-variant %q survived the case-insensitive hide", k)
		}
		found := false
		for k := range totals {
			if strings.EqualFold(k, "Keep") {
				found = true
			}
		}
		Expect(found).To(BeTrue())
	})

	ginkgo.It("[category] multi-day canonical pick: ONE display casing across all days (boom-5db)", func() {
		d := openTestDBG()
		f := newSenderG(d, "cfmulticat")
		ctx := f.Ctx()
		sender := f.Sender()
		ensureProjectsG(d, ctx, sender, "P")

		day1 := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)
		day2 := day1.AddDate(0, 0, 1)

		// Day1: 3× "Writing Docs" + 1× "writing docs" attributed
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "Writing Docs", ts: day1, gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "Writing Docs", ts: day1.Add(time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "Writing Docs", ts: day1.Add(2 * time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "Writing Docs", ts: day1.Add(3 * time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "writing docs", ts: day1.Add(4 * time.Minute), gap: 100})
		// Day2: 3× "writing docs" + 1× "Writing Docs" attributed
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "writing docs", ts: day2, gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "writing docs", ts: day2.Add(time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "writing docs", ts: day2.Add(2 * time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "writing docs", ts: day2.Add(3 * time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "Writing Docs", ts: day2.Add(4 * time.Minute), gap: 100})

		start := day1.AddDate(0, 0, -1)
		end := day2.AddDate(0, 0, 1)

		cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		seen := map[string]int64{}
		for _, c := range cats {
			if strings.EqualFold(c.Category, "writing docs") {
				seen[c.Category] += c.TotalSeconds
			}
		}
		Expect(seen).To(HaveLen(1), "expected one canonical casing across all days")
		var total int64
		var pick string
		for k, v := range seen {
			pick = k
			total = v
		}
		Expect(total).To(BeEquivalentTo(800))
		Expect(pick == "Writing Docs" || pick == "writing docs").To(BeTrue())
	})

	ginkgo.It("[project] multi-day canonical pick: ONE display casing across all days (StatRow path)", func() {
		d := openTestDBG()
		f := newSenderG(d, "cfmultiproj")
		ctx := f.Ctx()
		sender := f.Sender()
		ensureProjectsG(d, ctx, sender, "MyProject", "myproject", "MYPROJECT")

		day1 := time.Date(2025, 7, 1, 10, 0, 0, 0, time.UTC)
		day2 := day1.AddDate(0, 0, 1)

		insertSeedG(d, ctx, sender, hbSeed{project: "MyProject", entity: "a.go", ts: day1, gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "MyProject", entity: "a.go", ts: day1.Add(time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "MyProject", entity: "a.go", ts: day1.Add(2 * time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "MyProject", entity: "a.go", ts: day1.Add(3 * time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "myproject", entity: "a.go", ts: day1.Add(4 * time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "myproject", entity: "a.go", ts: day2, gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "myproject", entity: "a.go", ts: day2.Add(time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "myproject", entity: "a.go", ts: day2.Add(2 * time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "myproject", entity: "a.go", ts: day2.Add(3 * time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "MYPROJECT", entity: "a.go", ts: day2.Add(4 * time.Minute), gap: 100})

		start := day1.AddDate(0, 0, -1)
		end := day2.AddDate(0, 0, 1)

		rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		totals := axisTotals(rows, "project")
		seen := []string{}
		for k := range totals {
			if strings.EqualFold(k, "myproject") {
				seen = append(seen, k)
			}
		}
		Expect(seen).To(HaveLen(1), "expected one canonical project casing across days")
	})
})

// seedTwoUserBlockCtx returns a background context (the seedCaseBlockG helper
// needs one).
func seedTwoUserBlockCtx() context.Context { return context.Background() }
