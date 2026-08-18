// template_rename_ginkgo_test.go — ginkgo mirror of template_rename_test.go (gaka-0vp.13).
// 1:1 case map (5 stdlib TestXxx → 5 Its; NormalizeTemplate uses DescribeTable Entries):
//
//	TestNormalizeTemplate                     → DescribeTable "NormalizeTemplate" 10 entries
//	TestTemplateRenameStripsPrefix            → It "template rename strips leading @ across raw + rollup"
//	TestTemplateAffectedMappedTo              → It "affected values include mappedTo preview per raw value"
//	TestExactAndRegexAffectedMappedToFixed    → It "exact and regex mappedTo is the fixed new_value"
//	TestTemplateValidation                    → It "ValidateTemplate: good ok, bad backref/pattern rejected"
package db

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("template rename", func() {
	ginkgo.DescribeTable("NormalizeTemplate",
		func(in, want string) {
			Expect(NormalizeTemplate(in)).To(Equal(want))
		},
		ginkgo.Entry("already Postgres form", `\1`, `\1`),
		ginkgo.Entry("shell form -> Postgres", `$1`, `\1`),
		ginkgo.Entry("multiple groups", `$1-$2`, `\1-\2`),
		ginkgo.Entry("prefix preserved", `pre-$1`, `pre-\1`),
		ginkgo.Entry("$$ escape", `$$1`, `$1`),
		ginkgo.Entry("$ not followed by digit is literal", `a$b`, `a$b`),
		ginkgo.Entry("whole match", `$0`, `\0`),
		ginkgo.Entry("no refs", `x`, `x`),
		ginkgo.Entry("empty", ``, ``),
		ginkgo.Entry("mixed forms", `\1 and $2`, `\1 and \2`),
	)

	ginkgo.It("template rename '^@(.*)$ -> \\1' strips '@' across raw scan, rollup, projects list, and is reversible", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "tmpl")
		sender := f.Sender()

		base := time.Date(2025, 5, 3, 10, 0, 0, 0, time.UTC)
		swarm, _ := f.Block(hbSeed{project: "@swarm-graph", language: "Go", editor: "vim"}, base, 4, 120)
		drogon, _ := f.Block(hbSeed{project: "@drogon", language: "Go", editor: "vim"}, base.Add(time.Hour), 3, 120)
		plain, _ := f.Block(hbSeed{project: "plain-project", language: "Go", editor: "vim"}, base.Add(2*time.Hour), 2, 120)
		f.RefreshRollup(base.AddDate(0, 0, -1))

		t0 := base.AddDate(0, 0, -1)
		t1 := base.AddDate(0, 0, 1)

		baseRows, err := d.GetUserActivity(ctx, sender, t0, t1, 15, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		baseProj := axisTotals(baseRows, "project")
		Expect(baseProj["@swarm-graph"]).To(Equal(swarm))
		Expect(baseProj["@drogon"]).To(Equal(drogon))

		id := createTemplateRenameG(d, ctx, sender, "project", "^@(.*)$", `\1`)
		rs := loadRenamesG(d, ctx, sender)
		Expect(rs.HasAxis("project")).To(BeTrue())

		assertMerged := func(label string, rows []StatRow) {
			p := axisTotals(rows, "project")
			_, ok := p["@swarm-graph"]
			Expect(ok).To(BeFalse(), "%s: '@swarm-graph' should be relabeled away; got %+v", label, p)
			_, ok = p["@drogon"]
			Expect(ok).To(BeFalse(), "%s: '@drogon' should be relabeled away", label)
			Expect(p["swarm-graph"]).To(Equal(swarm), "%s: 'swarm-graph'", label)
			Expect(p["drogon"]).To(Equal(drogon), "%s: 'drogon'", label)
			Expect(p["plain-project"]).To(Equal(plain), "%s: 'plain-project' unaffected", label)
			Expect(grandTotal(rows)).To(Equal(swarm+drogon+plain), "%s: grand total conserved", label)
		}

		rawRows, err := d.GetUserActivity(ctx, sender, t0, t1, 30, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		assertMerged("GetUserActivity", rawRows)

		rollRows, err := d.GetUserActivityRollup(ctx, sender, t0, t1, HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		assertMerged("GetUserActivityRollup", rollRows)

		projs, err := d.GetAllProjects(ctx, sender, t0, t1, HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		set := map[string]bool{}
		for _, p := range projs {
			set[p] = true
		}
		Expect(set["swarm-graph"]).To(BeTrue())
		Expect(set["drogon"]).To(BeTrue())
		Expect(set["plain-project"]).To(BeTrue())
		Expect(set["@swarm-graph"]).To(BeFalse())
		Expect(set["@drogon"]).To(BeFalse())

		Expect(rawCountG(d, ctx, sender, "project", "@swarm-graph")).To(Equal(5), "1 break + 4 attributed, unchanged")
		Expect(rawCountG(d, ctx, sender, "project", "swarm-graph")).To(Equal(0))

		_, err = d.DeleteCurationRule(ctx, sender, id)
		Expect(err).NotTo(HaveOccurred())
		rs2 := loadRenamesG(d, ctx, sender)
		revRows, err := d.GetUserActivity(ctx, sender, t0, t1, 15, "UTC", HiddenSets{}, rs2, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		rev := axisTotals(revRows, "project")
		Expect(rev["@swarm-graph"]).To(Equal(swarm))
		Expect(rev["@drogon"]).To(Equal(drogon))
		_, ok := rev["swarm-graph"]
		Expect(ok).To(BeFalse(), "after delete, merged 'swarm-graph' should be gone")
	})

	ginkgo.It("template affected values include mappedTo preview per raw value", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "tmplaff")
		sender := f.Sender()

		day := time.Date(2025, 6, 13, 9, 0, 0, 0, time.UTC)
		f.Seed(hbSeed{project: "@swarm-graph", language: "Go", entity: "a.go", ts: day, gap: 60})
		f.Seed(hbSeed{project: "@swarm-graph", language: "Go", entity: "a.go", ts: day.Add(time.Minute), gap: 60})
		f.Seed(hbSeed{project: "@drogon", language: "Go", entity: "b.go", ts: day.Add(2 * time.Minute), gap: 60})
		f.Seed(hbSeed{project: "plain", language: "Go", entity: "c.go", ts: day.Add(3 * time.Minute), gap: 60})

		id := createTemplateRenameG(d, ctx, sender, "project", "^@(.*)$", `\1`)
		rule, _, err := d.GetCurationRule(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		vals, _, err := d.CurationAffectedValues(ctx, sender, rule, 200)
		Expect(err).NotTo(HaveOccurred())
		mapped := map[string]string{}
		counts := map[string]int64{}
		for _, v := range vals {
			mapped[v.Value] = v.MappedTo
			counts[v.Value] = v.Count
		}
		Expect(mapped["@swarm-graph"]).To(Equal("swarm-graph"))
		Expect(mapped["@drogon"]).To(Equal("drogon"))
		Expect(counts["@swarm-graph"]).To(BeEquivalentTo(2))
		Expect(counts["@drogon"]).To(BeEquivalentTo(1))
		_, ok := mapped["plain"]
		Expect(ok).To(BeFalse(), "'plain' does not match ^@ and must not appear in affected values")
	})

	ginkgo.It("exact + regex rules return a FIXED mappedTo (not per-value template result)", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "fixmap")
		sender := f.Sender()

		day := time.Date(2025, 6, 14, 9, 0, 0, 0, time.UTC)
		f.Seed(hbSeed{project: "Meet - Standup", language: "Go", entity: "a.go", ts: day, gap: 60})
		f.Seed(hbSeed{project: "Meet - Planning", language: "Go", entity: "b.go", ts: day.Add(time.Minute), gap: 60})
		f.Seed(hbSeed{project: "solo", language: "Go", entity: "c.go", ts: day.Add(2 * time.Minute), gap: 60})

		rxID := createRegexRenameG(d, ctx, sender, "project", "^Meet", "Meeting")
		rxRule, _, _ := d.GetCurationRule(ctx, rxID)
		rxVals, _, err := d.CurationAffectedValues(ctx, sender, rxRule, 200)
		Expect(err).NotTo(HaveOccurred())
		for _, v := range rxVals {
			Expect(v.MappedTo).To(Equal("Meeting"), "regex mappedTo[%q]", v.Value)
		}

		exID := createRenameG(d, ctx, sender, "project", "solo", "Misc")
		exRule, _, _ := d.GetCurationRule(ctx, exID)
		exVals, _, err := d.CurationAffectedValues(ctx, sender, exRule, 200)
		Expect(err).NotTo(HaveOccurred())
		Expect(exVals).To(HaveLen(1))
		Expect(exVals[0].MappedTo).To(Equal("Misc"))
	})

	ginkgo.It("ValidateTemplate accepts good templates and rejects bad backref/pattern", func() {
		d := openTestDBG()
		ctx := context.Background()

		Expect(d.ValidateTemplate(ctx, "^@(.*)$", `\1`)).To(Succeed())
		Expect(d.ValidateTemplate(ctx, "^@(.*)$", `\9`)).To(HaveOccurred(), "bad backref \\9 (only 1 group)")
		Expect(d.ValidateTemplate(ctx, "(unterminated", `\1`)).To(HaveOccurred(), "uncompilable pattern")
	})
})
