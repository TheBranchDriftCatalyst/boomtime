// rename_apply_test.go — the ingest-time rename applier (boom-scrub). Pure specs
// construct rules directly (same package); the parity + loader specs use the
// live test DB. The parity spec is the load-bearing one: it proves the Go
// applier agrees with Postgres regexp_replace (first-match-only), so a rule
// behaves identically whether applied at ingest (Go) or query-time (SQL).
package db

import (
	"context"
	"regexp"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

func tmplRule(pattern, storedTemplate string) compiledRenameRule {
	return compiledRenameRule{
		matchType:  MatchTemplate,
		re:         regexp.MustCompile("(?i)" + pattern),
		goTemplate: convertTemplateToGo(storedTemplate),
	}
}

var _ = ginkgo.Describe("convertTemplateToGo", func() {
	ginkgo.DescribeTable("Postgres \\N template → Go ${N}",
		func(stored, want string) { Expect(convertTemplateToGo(stored)).To(Equal(want)) },
		ginkgo.Entry("single backref", `\1`, `${1}`),
		ginkgo.Entry("backref then literal digit", `\12`, `${1}2`),
		ginkgo.Entry("whole match", `\0`, `${0}`),
		ginkgo.Entry("mixed", `pre-\1-post`, `pre-${1}-post`),
		ginkgo.Entry("escaped backslash", `\\`, `\`),
		ginkgo.Entry("literal dollar is escaped", `a$b`, `a$$b`),
		ginkgo.Entry("empty (prefix strip)", ``, ``),
	)
})

var _ = ginkgo.Describe("compiledRenameRule.apply", func() {
	ginkgo.It("exact: case-insensitive whole-value replace", func() {
		r := compiledRenameRule{matchType: MatchExact, matchValue: "Writing Docs", newValue: "docs"}
		Expect(r.apply("writing docs")).To(Equal("docs"))
		Expect(r.apply("something else")).To(Equal("something else"))
	})
	ginkgo.It("regex: match → whole-value replace (case-insensitive)", func() {
		r := compiledRenameRule{matchType: MatchRegex, re: regexp.MustCompile("(?i)^meet"), newValue: "meetings"}
		Expect(r.apply("Meet-standup")).To(Equal("meetings"))
		Expect(r.apply("coding")).To(Equal("coding"))
	})
	ginkgo.It("template: strips a path prefix (first-match)", func() {
		r := tmplRule(`^/Users/hdaniels/protecht_devspace/`, ``)
		Expect(r.apply("/Users/hdaniels/protecht_devspace/api/main.go")).To(Equal("api/main.go"))
	})
	ginkgo.It("template: FIRST match only (mirrors regexp_replace without 'g')", func() {
		r := tmplRule(`x`, `y`)
		Expect(r.apply("xax")).To(Equal("yax")) // NOT "yay"
	})
	ginkgo.It("template: capture-group backref", func() {
		r := tmplRule(`^@(.*)$`, `\1`)
		Expect(r.apply("@handle")).To(Equal("handle"))
	})
})

var _ = ginkgo.Describe("IngestRenameSet.Apply", func() {
	ginkgo.It("nil-guards *string fields and scrubs value fields", func() {
		set := IngestRenameSet{byAxis: map[string][]compiledRenameRule{
			"entity":  {tmplRule(`^/secret/`, ``)},
			"project": {{matchType: MatchExact, matchValue: "old", newValue: "new"}},
		}}
		hb := &model.HeartbeatPayload{Entity: "/secret/a.go", Project: nil}
		set.Apply(hb)
		Expect(hb.Entity).To(Equal("a.go"))
		Expect(hb.Project).To(BeNil()) // nil stays nil, no panic
	})
	ginkgo.It("empty set is a no-op", func() {
		var set IngestRenameSet
		Expect(set.Empty()).To(BeTrue())
		hb := &model.HeartbeatPayload{Entity: "/untouched"}
		set.Apply(hb)
		Expect(hb.Entity).To(Equal("/untouched"))
	})
})

var _ = ginkgo.Describe("Go applier ↔ Postgres regexp_replace PARITY (boom-scrub)", func() {
	ginkgo.It("template output matches regexp_replace(input, pat, tmpl, 'i') for every case", func() {
		d := openTestDBG()
		ctx := context.Background()
		cases := []struct{ pattern, storedTemplate, input string }{
			{`^/Users/[^/]+/`, ``, `/Users/dj/src/a.go`},
			{`(\d+)`, `X\1X`, `a1b2`},              // first-match only
			{`foo`, `bar`, `foofoo`},               // first-match only
			{`^@(.*)$`, `\1`, `@handle`},           // strip leading @
			{`^(.*)-(.*)$`, `\2/\1`, `alpha-beta`}, // swap groups
			{`MEET`, `sync`, `meet-standup-meet`},  // case-insensitive, first
			{`x`, ``, `axbxc`},                     // delete first x
		}
		for _, c := range cases {
			var pg string
			err := d.Pool.QueryRow(ctx, `SELECT regexp_replace($1, $2, $3, 'i')`,
				c.input, c.pattern, c.storedTemplate).Scan(&pg)
			Expect(err).NotTo(HaveOccurred())
			got := tmplRule(c.pattern, c.storedTemplate).apply(c.input)
			Expect(got).To(Equal(pg), "pattern=%q tmpl=%q input=%q: Go=%q PG=%q",
				c.pattern, c.storedTemplate, c.input, got, pg)
		}
	})
})

var _ = ginkgo.Describe("LoadIngestRenameRules + query-time exclusion", func() {
	ginkgo.It("loads only enabled apply_at_ingest rename rules; excludes them from LoadRenameSets", func() {
		d := openTestDBG()
		ctx := context.Background()
		owner := newSenderG(d, "scrubload").Sender()
		strip := ""

		// An apply_at_ingest template rule on entity.
		_, err := d.CreateCurationRuleWithIngest(ctx, owner, "entity", "rename", MatchTemplate, `^/secret/`, &strip, true)
		Expect(err).NotTo(HaveOccurred())
		// A normal (query-time) rename rule on project.
		newp := "public"
		_, err = d.CreateCurationRuleWithIngest(ctx, owner, "project", "rename", MatchExact, "private", &newp, false)
		Expect(err).NotTo(HaveOccurred())
		// A disabled apply_at_ingest rule — must NOT load.
		off, err := d.CreateCurationRuleWithIngest(ctx, owner, "branch", "rename", MatchExact, "wip", &newp, true)
		Expect(err).NotTo(HaveOccurred())
		_, _, _ = d.ToggleCurationRule(ctx, owner, off.ID) // disable it

		set, err := d.LoadIngestRenameRules(ctx, owner)
		Expect(err).NotTo(HaveOccurred())
		Expect(set.byAxis).To(HaveKey("entity"))
		Expect(set.byAxis).NotTo(HaveKey("project"), "query-time rule must not load into the ingest set")
		Expect(set.byAxis).NotTo(HaveKey("branch"), "disabled ingest rule must not load")
		hb := &model.HeartbeatPayload{Entity: "/secret/x.go"}
		set.Apply(hb)
		Expect(hb.Entity).To(Equal("x.go"))

		// The query-time remap must EXCLUDE the apply_at_ingest rule (no double-apply)
		// but still carry the normal project rule.
		rs, err := d.LoadRenameSets(ctx, owner)
		Expect(err).NotTo(HaveOccurred())
		Expect(rs.byAxis).To(HaveKey("project"))
		Expect(rs.byAxis).NotTo(HaveKey("entity"), "apply_at_ingest rule must be excluded from query-time remap")
	})
})
