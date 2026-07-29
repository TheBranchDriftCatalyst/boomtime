// spaces_ginkgo_test.go — ginkgo mirror of spaces_test.go (gaka-0vp.13).
// 1:1 case map (9 stdlib TestXxx → 7 Its + 1 DescribeTable(19)):
//
//	TestInclusionPredicateShape        → It "inclusionPredicate SQL/arg shape"
//	TestRewriteAnchoredRegex           → DescribeTable "rewriteAnchoredRegex" 19 entries
//	TestSpaceScopePredicateEmpty       → It "spaceScopePredicate: empty requested → AND FALSE; unrequested no-op"
//	TestHasMemberOutside               → It "HasMemberOutside: rollup vs non-rollup axis"
//	TestSpaceInclusionUnionAcrossAxes  → It "space inclusion is UNION across axes"
//	TestSpaceMultiRuleOR               → It "multiple exact rules on same axis OR together"
//	TestSpaceEmptyMatchesNothing       → It "empty space requested matches nothing"
//	TestSpaceCRUDAndLoadMemberSets     → It "space CRUD + LoadMemberSets round-trip + owner isolation"
//	TestSpacePreviewValues             → It "SpacePreviewValues returns matching raw values + counts"
package db

import (
	"context"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// seedAxisBlock2G — ginkgo variant of seedAxisBlock2.
func seedAxisBlock2G(d *DB, ctx context.Context, sender string, tmpl hbSeed, startTS time.Time, n int, each int64) (int64, int) {
	if tmpl.project != "" {
		ensureProjectsG(d, ctx, sender, tmpl.project)
	}
	f := &SenderFixtureG{db: d, ctx: ctx, name: sender}
	return f.Block(tmpl, startTS, n, each)
}

// newSpaceSenderG — ginkgo variant of newSpaceSender.
func newSpaceSenderG(d *DB, prefix string) (context.Context, string) {
	ctx := context.Background()
	sender := mkSender(prefix)
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSenderG(d, ctx, sender)
	return ctx, sender
}

var _ = ginkgo.Describe("spaces (inclusion predicate)", func() {
	ginkgo.It("inclusionPredicate produces the expected SQL/arg shape (project ANY + editor ILIKE/~)", func() {
		ms := mkMembers(
			map[string][]string{"project": {"a", "b"}},
			map[string][]string{"editor": {"^vim", "code"}},
		)
		args := []any{"sender", time.Now(), time.Now(), int64(15)}
		sql, outArgs, next := inclusionPredicate(ms, rawHeartbeatCols, "", 5, args)

		want := " AND (lower(project) = ANY($5) OR editor ILIKE $6 OR editor ~* $7)"
		Expect(sql).To(Equal(want))
		Expect(next).To(Equal(8))
		Expect(outArgs).To(HaveLen(7))
		Expect(outArgs[5]).To(Equal("vim%"))
		Expect(outArgs[6]).To(Equal("code"))
	})

	ginkgo.DescribeTable("rewriteAnchoredRegex",
		func(pat, wantOp, wantBound string) {
			op, bound := rewriteAnchoredRegex(pat)
			Expect(op).To(Equal(wantOp))
			Expect(bound).To(Equal(wantBound))
		},
		ginkgo.Entry("^foo → LIKE foo%", "^foo", "LIKE", "foo%"),
		ginkgo.Entry("^svc-", "^svc-", "LIKE", "svc-%"),
		ginkgo.Entry("^catalyst-web", "^catalyst-web", "LIKE", "catalyst-web%"),
		ginkgo.Entry("^some/path", "^some/path", "LIKE", "some/path%"),
		ginkgo.Entry("^ns:name", "^ns:name", "LIKE", "ns:name%"),
		ginkgo.Entry("^foo$ → =", "^foo$", "=", "foo"),
		ginkgo.Entry("^svc-auth$", "^svc-auth$", "=", "svc-auth"),
		ginkgo.Entry("no anchor foo", "foo", "~", "foo"),
		ginkgo.Entry("no anchor teak", "teak", "~", "teak"),
		ginkgo.Entry("no anchor protecht", "protecht", "~", "protecht"),
		ginkgo.Entry("^foo.bar (metachar)", "^foo.bar", "~", "^foo.bar"),
		ginkgo.Entry("^foo\\.bar", "^foo\\.bar", "~", "^foo\\.bar"),
		ginkgo.Entry("^svc-(a|b)", "^svc-(a|b)", "~", "^svc-(a|b)"),
		ginkgo.Entry("^foo*", "^foo*", "~", "^foo*"),
		ginkgo.Entry("^foo+", "^foo+", "~", "^foo+"),
		ginkgo.Entry("^foo?", "^foo?", "~", "^foo?"),
		ginkgo.Entry("^foo[a-z]", "^foo[a-z]", "~", "^foo[a-z]"),
		ginkgo.Entry("^foo bar (space)", "^foo bar", "~", "^foo bar"),
		ginkgo.Entry("bare ^", "^", "~", "^"),
		ginkgo.Entry("bare ^$", "^$", "~", "^$"),
	)

	ginkgo.It("spaceScopePredicate: empty+requested → AND FALSE; empty+unrequested → no-op", func() {
		sql, _, next := spaceScopePredicate(MemberSets{}, rawHeartbeatCols, "", 5, []any{"x"}, true)
		Expect(sql).To(Equal(" AND FALSE"))
		Expect(next).To(Equal(5))

		sql2, _, _ := spaceScopePredicate(MemberSets{}, rawHeartbeatCols, "", 5, []any{"x"}, false)
		Expect(sql2).To(Equal(""))
		Expect((MemberSets{}).AnyMember()).To(BeFalse())
	})

	ginkgo.It("HasMemberOutside: rollup-axis rules return false; non-rollup (entity) returns true", func() {
		inRollup := mkMembers(map[string][]string{"project": {"p"}}, nil)
		Expect(inRollup.HasMemberOutside(RollupAxes)).To(BeFalse())
		outside := mkMembers(map[string][]string{"entity": {"main.go"}}, nil)
		Expect(outside.HasMemberOutside(RollupAxes)).To(BeTrue())
	})

	ginkgo.It("space inclusion is UNION across axes (project exact OR editor regex)", func() {
		d := openTestDBG()
		ctx, sender := newSpaceSenderG(d, "spc_union")
		ensureProjectsG(d, ctx, sender, "catalyst-web", "catalyst-api", "personal")

		day := time.Date(2025, 9, 1, 9, 0, 0, 0, time.UTC)
		web, _ := seedAxisBlock2G(d, ctx, sender, hbSeed{project: "catalyst-web", editor: "vim", language: "Go"}, day, 2, 100)
		code, _ := seedAxisBlock2G(d, ctx, sender, hbSeed{project: "personal", editor: "code", language: "Go"}, day.Add(20*time.Minute), 3, 100)
		_, _ = seedAxisBlock2G(d, ctx, sender, hbSeed{project: "personal", editor: "emacs", language: "Go"}, day.Add(40*time.Minute), 1, 100)

		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)
		ms := mkMembers(
			map[string][]string{"project": {"catalyst-web"}},
			map[string][]string{"editor": {"^code"}},
		)
		rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, RenameSets{}, ms, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(rows)).To(Equal(web + code))
		secs := axisTotals(rows, "editor")
		_, ok := secs["emacs"]
		Expect(ok).To(BeFalse(), "emacs rows should be excluded (match no rule)")
	})

	ginkgo.It("multiple exact rules on the same axis OR together", func() {
		d := openTestDBG()
		ctx, sender := newSpaceSenderG(d, "spc_or")
		ensureProjectsG(d, ctx, sender, "alpha", "beta", "gamma")

		day := time.Date(2025, 9, 2, 9, 0, 0, 0, time.UTC)
		a, _ := seedAxisBlock2G(d, ctx, sender, hbSeed{project: "alpha", language: "Go"}, day, 2, 100)
		b, _ := seedAxisBlock2G(d, ctx, sender, hbSeed{project: "beta", language: "Go"}, day.Add(20*time.Minute), 3, 100)
		_, _ = seedAxisBlock2G(d, ctx, sender, hbSeed{project: "gamma", language: "Go"}, day.Add(40*time.Minute), 1, 100)
		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

		ms := mkMembers(map[string][]string{"project": {"alpha", "beta"}}, nil)
		rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, RenameSets{}, ms, true)
		Expect(err).NotTo(HaveOccurred())
		secs := axisTotals(rows, "project")
		Expect(secs["alpha"]).To(Equal(a))
		Expect(secs["beta"]).To(Equal(b))
		_, ok := secs["gamma"]
		Expect(ok).To(BeFalse(), "gamma should be excluded (no rule)")
	})

	ginkgo.It("empty space requested matches nothing; unrequested returns full dashboard", func() {
		d := openTestDBG()
		ctx, sender := newSpaceSenderG(d, "spc_empty")
		ensureProjectsG(d, ctx, sender, "alpha")

		day := time.Date(2025, 9, 3, 9, 0, 0, 0, time.UTC)
		seedAxisBlock2G(d, ctx, sender, hbSeed{project: "alpha", language: "Go"}, day, 2, 100)
		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

		rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(0))

		unscoped, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(unscoped)).To(BeEquivalentTo(200))
	})

	ginkgo.It("space CRUD + LoadMemberSets round-trip + owner isolation + rename + cascade-on-delete", func() {
		d := openTestDBG()
		ctx, owner := newSpaceSenderG(d, "spc_crud")
		_, other := newSpaceSenderG(d, "spc_other")

		sp, err := d.CreateSpace(ctx, owner, "Work")
		Expect(err).NotTo(HaveOccurred())
		Expect(sp.ID).NotTo(BeZero())
		Expect(sp.Name).To(Equal("Work"))

		_, err = d.AddSpaceRule(ctx, owner, sp.ID, "project", "catalyst-web", "exact")
		Expect(err).NotTo(HaveOccurred())
		_, err = d.AddSpaceRule(ctx, owner, sp.ID, "project", "^svc-", "regex")
		Expect(err).NotTo(HaveOccurred())

		_, err = d.AddSpaceRule(ctx, owner, sp.ID, "bogus", "x", "exact")
		Expect(err).To(HaveOccurred(), "unknown axis should be rejected")
		_, err = d.AddSpaceRule(ctx, owner, sp.ID, "project", "x", "template")
		Expect(err).To(HaveOccurred(), "template matchType should be rejected")
		_, err = d.AddSpaceRule(ctx, owner, sp.ID, "project", "(unterminated", "regex")
		Expect(err).To(HaveOccurred(), "invalid regex should be rejected")

		ms, err := d.LoadMemberSets(ctx, sp.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ms.AnyMember()).To(BeTrue())
		a := ms.byAxis["project"]
		Expect(a.exact).To(Equal([]string{"catalyst-web"}))
		Expect(a.regex).To(Equal([]string{"^svc-"}))

		gotSp, rules, err := d.GetSpace(ctx, owner, sp.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(gotSp).NotTo(BeNil())
		Expect(rules).To(HaveLen(2))
		list, err := d.ListSpaces(ctx, owner)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].RuleCount).To(BeEquivalentTo(2))

		// Owner isolation.
		sp2, _, _ := d.GetSpace(ctx, other, sp.ID)
		Expect(sp2).To(BeNil())
		rule, err := d.AddSpaceRule(ctx, other, sp.ID, "project", "x", "exact")
		Expect(err).NotTo(HaveOccurred())
		Expect(rule).To(BeNil())
		n, _ := d.DeleteSpace(ctx, other, sp.ID)
		Expect(n).To(BeEquivalentTo(0))
		list2, _ := d.ListSpaces(ctx, other)
		Expect(list2).To(HaveLen(0))

		// Delete a rule (owner-scoped).
		n, err = d.DeleteSpaceRule(ctx, owner, sp.ID, rules[0].ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(1))

		newName := "Job"
		pos := 5
		n, err = d.RenameSpace(ctx, owner, sp.ID, &newName, &pos)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(1))
		gotSp, _, _ = d.GetSpace(ctx, owner, sp.ID)
		Expect(gotSp.Name).To(Equal("Job"))
		Expect(gotSp.Position).To(Equal(5))

		n, err = d.DeleteSpace(ctx, owner, sp.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(1))
		var ruleCount int
		_ = d.Pool.QueryRow(ctx, `SELECT count(*) FROM space_rules WHERE space_id=$1`, sp.ID).Scan(&ruleCount)
		Expect(ruleCount).To(Equal(0), "rules should cascade on space delete")
	})

	ginkgo.It("SpacePreviewValues returns matching raw values with counts", func() {
		d := openTestDBG()
		ctx, owner := newSpaceSenderG(d, "spc_prev")
		ensureProjectsG(d, ctx, owner, "svc-auth", "svc-billing", "web")

		day := time.Date(2025, 9, 4, 9, 0, 0, 0, time.UTC)
		for i := 0; i < 3; i++ {
			insertSeedG(d, ctx, owner, hbSeed{project: "svc-auth", entity: "a.go", ts: day.Add(time.Duration(i) * time.Minute), gap: 60})
		}
		for i := 0; i < 2; i++ {
			insertSeedG(d, ctx, owner, hbSeed{project: "svc-billing", entity: "b.go", ts: day.Add(time.Duration(10+i) * time.Minute), gap: 60})
		}
		insertSeedG(d, ctx, owner, hbSeed{project: "web", entity: "c.go", ts: day.Add(30 * time.Minute), gap: 60})

		vals, trunc, err := d.SpacePreviewValues(ctx, owner, "project", "^svc-", "regex", 200)
		Expect(err).NotTo(HaveOccurred())
		Expect(trunc).To(BeFalse())
		byVal := map[string]int64{}
		for _, v := range vals {
			byVal[v.Value] = v.Count
		}
		Expect(byVal["svc-auth"]).To(BeEquivalentTo(3))
		Expect(byVal["svc-billing"]).To(BeEquivalentTo(2))
		_, ok := byVal["web"]
		Expect(ok).To(BeFalse(), "web must not match ^svc-")
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
func mkMembers(exact map[string][]string, regex map[string][]string) MemberSets {
	ms := MemberSets{byAxis: map[string]axisMembers{}}
	for axis, vals := range exact {
		a := ms.byAxis[axis]
		a.exact = append(a.exact, vals...)
		ms.byAxis[axis] = a
	}
	for axis, vals := range regex {
		a := ms.byAxis[axis]
		a.regex = append(a.regex, vals...)
		ms.byAxis[axis] = a
	}
	return ms
}

func newSpaceSender(t *testing.T, d *DB, prefix string) (context.Context, string) {
	t.Helper()
	ctx := context.Background()
	sender := mkSender(prefix)
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	return ctx, sender
}

func seedAxisBlock2(t *testing.T, d *DB, ctx context.Context, sender string, tmpl hbSeed, startTS time.Time, n int, each int64) (int64, int) {
	t.Helper()
	if tmpl.project != "" {
		ensureProjects(t, d, ctx, sender, tmpl.project)
	}
	f := &SenderFixture{t: t, db: d, ctx: ctx, name: sender}
	return f.Block(tmpl, startTS, n, each)
}
