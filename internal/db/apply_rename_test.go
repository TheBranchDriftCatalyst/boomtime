// apply_rename_ginkgo_test.go — ginkgo mirror of apply_rename_test.go (gaka-0vp.13).
// 1:1 case map (9 stdlib TestXxx → 8 Its + 1 DescribeTable(4)):
//   TestApplyRenameRuleExactHappyPath              → It "exact happy path + preview==run SQL"
//   TestApplyRenameRuleIdempotent                  → It "idempotent: zero-match still removes mapping"
//   TestApplyRenameRuleOwnerScoped                 → It "owner-scoped: alice's apply doesn't touch bob"
//   TestApplyRenameRuleRegex                       → It "regex apply"
//   TestApplyRenamePreviewMatchesRun               → It "preview SQL == run SQL"
//   TestApplyRenameRuleRejectsHide                 → It "rejects hide rules (only 'rename')"
//   TestApplyRenameRuleTemplate                    → It "template apply via regexp_replace"
//   TestApplyRenameRuleRollbackOnConstraintViolation → It "atomicity: FK violation rolls back UPDATE + DELETE"
//   TestInlineParams                                → DescribeTable "InlineParams" (4 entries)
package db

import (
	"context"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("ApplyRenameRule", func() {
	ginkgo.It("exact happy path: rewrites matching rows + removes mapping; preview SQL == run SQL", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "applyexact")
		sender := f.Sender()

		base := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, ctx, sender, "old-project", "keep", "new-project")
		for i := 0; i < 3; i++ {
			seedHBG(d, ctx, sender, "old-project", "Go", base.Add(time.Duration(i)*time.Minute))
		}
		for i := 0; i < 2; i++ {
			seedHBG(d, ctx, sender, "keep", "Go", base.Add(time.Duration(10+i)*time.Minute))
		}

		ruleID := createRenameG(d, ctx, sender, "project", "old-project", "new-project")
		rule, _, err := d.GetCurationRule(ctx, ruleID)
		Expect(err).NotTo(HaveOccurred())

		updSQL, delSQL, diff, total, err := d.ApplyRenamePreview(ctx, sender, rule, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(BeEquivalentTo(3))
		Expect(diff).To(HaveLen(3))
		for _, dd := range diff {
			Expect(dd.Before).To(Equal("old-project"))
			Expect(dd.After).To(Equal("new-project"))
		}
		Expect(updSQL).To(ContainSubstring("UPDATE heartbeats"))
		Expect(delSQL).To(ContainSubstring("DELETE FROM curation_rules"))

		rows, sqlUpd, sqlDel, err := d.ApplyRenameRule(ctx, sender, rule)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEquivalentTo(3))
		Expect(sqlUpd).To(Equal(updSQL))
		Expect(sqlDel).To(Equal(delSQL))

		Expect(rawCountG(d, ctx, sender, "project", "new-project")).To(Equal(3))
		Expect(rawCountG(d, ctx, sender, "project", "old-project")).To(Equal(0))
		Expect(rawCountG(d, ctx, sender, "project", "keep")).To(Equal(2))
		rule2, _, err := d.GetCurationRule(ctx, ruleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(rule2).To(BeNil())
	})

	ginkgo.It("idempotent: zero-match still removes the mapping row", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "applynoop")
		sender := f.Sender()

		ensureProjectsG(d, ctx, sender, "keep", "spectre")
		base := time.Date(2025, 6, 2, 10, 0, 0, 0, time.UTC)
		seedHBG(d, ctx, sender, "keep", "Go", base)

		ruleID := createRenameG(d, ctx, sender, "project", "ghost", "spectre")
		rule, _, err := d.GetCurationRule(ctx, ruleID)
		Expect(err).NotTo(HaveOccurred())

		rows, _, _, err := d.ApplyRenameRule(ctx, sender, rule)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEquivalentTo(0))
		rule2, _, err := d.GetCurationRule(ctx, ruleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(rule2).To(BeNil())
	})

	ginkgo.It("owner-scoped: alice's apply doesn't touch bob's rows", func() {
		d := openTestDBG()
		ctx := context.Background()
		alice := newSenderG(d, "applyalice")
		bob := newSenderG(d, "applybob")
		base := time.Date(2025, 6, 3, 10, 0, 0, 0, time.UTC)

		ensureProjectsG(d, ctx, alice.Sender(), "shared", "alice-only")
		ensureProjectsG(d, ctx, bob.Sender(), "shared", "alice-only")
		for i := 0; i < 2; i++ {
			seedHBG(d, ctx, alice.Sender(), "shared", "Go", base.Add(time.Duration(i)*time.Minute))
			seedHBG(d, ctx, bob.Sender(), "shared", "Go", base.Add(time.Duration(i)*time.Minute))
		}

		ruleID := createRenameG(d, ctx, alice.Sender(), "project", "shared", "alice-only")
		rule, _, err := d.GetCurationRule(ctx, ruleID)
		Expect(err).NotTo(HaveOccurred())

		rows, _, _, err := d.ApplyRenameRule(ctx, alice.Sender(), rule)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEquivalentTo(2))
		Expect(rawCountG(d, ctx, alice.Sender(), "project", "alice-only")).To(Equal(2))
		Expect(rawCountG(d, ctx, bob.Sender(), "project", "shared")).To(Equal(2), "bob rows should be UNTOUCHED")
		Expect(rawCountG(d, ctx, bob.Sender(), "project", "alice-only")).To(Equal(0))
	})

	ginkgo.It("regex rename apply rewrites every matching row to the fixed newValue", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "applyregex")
		sender := f.Sender()

		base := time.Date(2025, 6, 4, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, ctx, sender, "acme-alpha", "acme-beta", "other", "acme")
		seedHBG(d, ctx, sender, "acme-alpha", "Go", base)
		seedHBG(d, ctx, sender, "acme-beta", "Go", base.Add(time.Minute))
		seedHBG(d, ctx, sender, "other", "Go", base.Add(2*time.Minute))

		ruleID := createRegexRenameG(d, ctx, sender, "project", "^acme-", "acme")
		rule, _, err := d.GetCurationRule(ctx, ruleID)
		Expect(err).NotTo(HaveOccurred())
		rows, _, _, err := d.ApplyRenameRule(ctx, sender, rule)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEquivalentTo(2))
		Expect(rawCountG(d, ctx, sender, "project", "acme")).To(Equal(2))
		Expect(rawCountG(d, ctx, sender, "project", "other")).To(Equal(1))
	})

	ginkgo.It("preview SQL is byte-identical to what apply runs", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "applyparity")
		sender := f.Sender()

		base := time.Date(2025, 6, 5, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, ctx, sender, "x", "y")
		seedHBG(d, ctx, sender, "x", "Go", base)

		ruleID := createRenameG(d, ctx, sender, "project", "x", "y")
		rule, _, err := d.GetCurationRule(ctx, ruleID)
		Expect(err).NotTo(HaveOccurred())
		previewUpd, previewDel, _, _, err := d.ApplyRenamePreview(ctx, sender, rule, 10)
		Expect(err).NotTo(HaveOccurred())
		_, runUpd, runDel, err := d.ApplyRenameRule(ctx, sender, rule)
		Expect(err).NotTo(HaveOccurred())
		Expect(runUpd).To(Equal(previewUpd))
		Expect(runDel).To(Equal(previewDel))
	})

	ginkgo.It("rejects hide rules (only rename can be applied)", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "applyhide")
		sender := f.Sender()

		hideRule, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "secret", nil)
		Expect(err).NotTo(HaveOccurred())
		_, _, _, err = d.ApplyRenameRule(ctx, sender, hideRule)
		Expect(err).To(HaveOccurred())
		Expect(strings.Contains(err.Error(), "only rename")).To(BeTrue())

		still, _, err := d.GetCurationRule(ctx, hideRule.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(still).NotTo(BeNil(), "hide rule was deleted by a failed apply — transaction leak")
	})

	ginkgo.It("template rename apply rewrites every matching row via regexp_replace", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "applytmpl")
		sender := f.Sender()

		base := time.Date(2025, 6, 6, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, ctx, sender, "@drogon", "@swarm-graph", "plain", "drogon", "swarm-graph")
		seedHBG(d, ctx, sender, "@drogon", "Go", base)
		seedHBG(d, ctx, sender, "@swarm-graph", "Go", base.Add(time.Minute))
		seedHBG(d, ctx, sender, "plain", "Go", base.Add(2*time.Minute))

		ruleID := createTemplateRenameG(d, ctx, sender, "project", `^@(.*)$`, `$1`)
		rule, _, err := d.GetCurationRule(ctx, ruleID)
		Expect(err).NotTo(HaveOccurred())
		rows, _, _, err := d.ApplyRenameRule(ctx, sender, rule)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEquivalentTo(2))
		Expect(rawCountG(d, ctx, sender, "project", "drogon")).To(Equal(1))
		Expect(rawCountG(d, ctx, sender, "project", "swarm-graph")).To(Equal(1))
		Expect(rawCountG(d, ctx, sender, "project", "plain")).To(Equal(1))
	})

	ginkgo.It("atomicity: FK violation rolls back UPDATE + DELETE", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "applyrollback")
		sender := f.Sender()

		base := time.Date(2025, 6, 7, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, ctx, sender, "old")
		seedHBG(d, ctx, sender, "old", "Go", base)

		ruleID := createRenameG(d, ctx, sender, "project", "old", "missing")
		rule, _, err := d.GetCurationRule(ctx, ruleID)
		Expect(err).NotTo(HaveOccurred())

		_, _, _, err = d.ApplyRenameRule(ctx, sender, rule)
		Expect(err).To(HaveOccurred(), "expected FK violation")
		Expect(rawCountG(d, ctx, sender, "project", "old")).To(Equal(1))
		still, _, err := d.GetCurationRule(ctx, ruleID)
		Expect(err).NotTo(HaveOccurred())
		Expect(still).NotTo(BeNil(), "mapping row was deleted despite UPDATE failure")
	})

	ginkgo.DescribeTable("InlineParams",
		func(sql string, args []any, want string) {
			Expect(InlineParams(sql, args)).To(Equal(want))
		},
		ginkgo.Entry("basic", "UPDATE t SET c = $2 WHERE sender = $1", []any{"alice", "hi"},
			"UPDATE t SET c = 'hi' WHERE sender = 'alice'"),
		ginkgo.Entry("escaped quote", "SET c = $1", []any{"o'brien"}, "SET c = 'o''brien'"),
		ginkgo.Entry("int", "WHERE id = $1", []any{42}, "WHERE id = 42"),
		ginkgo.Entry("double-digit ($10 vs $1)", "$10 $1",
			[]any{"a", "b", "c", "d", "e", "f", "g", "h", "i", "TEN"}, "'TEN' 'a'"),
	)
})
