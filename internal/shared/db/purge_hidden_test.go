// purge_hidden_ginkgo_test.go — ginkgo mirror of purge_hidden_test.go (boom-0vp.13).
// 1:1 case map (7 stdlib TestXxx → 7 Its):
//
//	TestPurgeHiddenRuleExactHappyPath           → It "exact happy path: deletes matching rows + rule row"
//	TestPurgeHiddenRuleIdempotent               → It "idempotent: zero-match still deletes rule"
//	TestPurgeHiddenRuleOwnerScoped              → It "owner-scoped: alice's purge doesn't touch bob"
//	TestPurgeHiddenRuleRegex                    → It "regex hide purges every matching row"
//	TestPurgeHiddenPreviewMatchesRun            → It "preview SQL == run SQL"
//	TestPurgeHiddenRuleRejectsRename            → It "rejects rename rules (only 'hide')"
//	TestPurgeHiddenRuleRollbackOnCommitFail     → It "atomicity: cancelled ctx leaves rows + rule intact"
package db

import (
	"context"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("PurgeHiddenRule", func() {
	ginkgo.It("exact happy path: deletes matching rows + rule row; preview SQL == run SQL", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "purgexact")
		sender := f.Sender()

		base := time.Date(2025, 6, 8, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, ctx, sender, "secret", "keep")
		for i := 0; i < 3; i++ {
			seedHBG(d, ctx, sender, "secret", "Go", base.Add(time.Duration(i)*time.Minute))
		}
		for i := 0; i < 2; i++ {
			seedHBG(d, ctx, sender, "keep", "Go", base.Add(time.Duration(10+i)*time.Minute))
		}

		hideRule, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "secret", nil)
		Expect(err).NotTo(HaveOccurred())

		delRows, delRule, diff, total, err := d.PurgeHiddenPreview(ctx, sender, hideRule, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(BeEquivalentTo(3))
		Expect(diff).To(HaveLen(3))
		for _, dd := range diff {
			Expect(dd.Deleted["project"]).To(Equal("secret"))
		}
		Expect(delRows).To(ContainSubstring("DELETE FROM heartbeats"))
		Expect(delRule).To(ContainSubstring("DELETE FROM curation_rules"))

		rows, sqlDelRows, sqlDelRule, err := d.PurgeHiddenRule(ctx, sender, hideRule)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEquivalentTo(3))
		Expect(sqlDelRows).To(Equal(delRows), "purge sqlDeleteRows diverged from preview")
		Expect(sqlDelRule).To(Equal(delRule), "purge sqlDeleteRule diverged from preview")

		Expect(rawCountG(d, ctx, sender, "project", "secret")).To(Equal(0))
		Expect(rawCountG(d, ctx, sender, "project", "keep")).To(Equal(2), "untouched")
		rule2, _, err := d.GetCurationRule(ctx, hideRule.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(rule2).To(BeNil())
	})

	ginkgo.It("no-op: a rule matching zero rows still succeeds and deletes the rule row", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "purgenoop")
		sender := f.Sender()

		ensureProjectsG(d, ctx, sender, "keep")
		base := time.Date(2025, 6, 9, 10, 0, 0, 0, time.UTC)
		seedHBG(d, ctx, sender, "keep", "Go", base)

		hideRule, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "ghost", nil)
		Expect(err).NotTo(HaveOccurred())

		rows, _, _, err := d.PurgeHiddenRule(ctx, sender, hideRule)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEquivalentTo(0))
		rule2, _, err := d.GetCurationRule(ctx, hideRule.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(rule2).To(BeNil())
		Expect(rawCountG(d, ctx, sender, "project", "keep")).To(Equal(1))
	})

	ginkgo.It("owner-scoped: alice's purge doesn't touch bob's rows on the same project name", func() {
		d := openTestDBG()
		ctx := context.Background()
		alice := newSenderG(d, "purgealice")
		bob := newSenderG(d, "purgebob")
		base := time.Date(2025, 6, 10, 10, 0, 0, 0, time.UTC)

		ensureProjectsG(d, ctx, alice.Sender(), "shared")
		ensureProjectsG(d, ctx, bob.Sender(), "shared")
		for i := 0; i < 2; i++ {
			seedHBG(d, ctx, alice.Sender(), "shared", "Go", base.Add(time.Duration(i)*time.Minute))
			seedHBG(d, ctx, bob.Sender(), "shared", "Go", base.Add(time.Duration(i)*time.Minute))
		}

		hideRule, err := d.CreateCurationRule(ctx, alice.Sender(), "project", "hide", "exact", "shared", nil)
		Expect(err).NotTo(HaveOccurred())
		rows, _, _, err := d.PurgeHiddenRule(ctx, alice.Sender(), hideRule)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEquivalentTo(2), "alice's only")
		Expect(rawCountG(d, ctx, alice.Sender(), "project", "shared")).To(Equal(0))
		Expect(rawCountG(d, ctx, bob.Sender(), "project", "shared")).To(Equal(2), "BOB rows should be UNTOUCHED")
	})

	ginkgo.It("regex hide purges every matching row (case-insensitive)", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "purgeregex")
		sender := f.Sender()

		base := time.Date(2025, 6, 11, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, ctx, sender, "secret-alpha", "secret-beta", "other")
		seedHBG(d, ctx, sender, "secret-alpha", "Go", base)
		seedHBG(d, ctx, sender, "secret-beta", "Go", base.Add(time.Minute))
		seedHBG(d, ctx, sender, "other", "Go", base.Add(2*time.Minute))

		hideRule, err := d.CreateCurationRule(ctx, sender, "project", "hide", "regex", "^secret-", nil)
		Expect(err).NotTo(HaveOccurred())
		rows, _, _, err := d.PurgeHiddenRule(ctx, sender, hideRule)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEquivalentTo(2))
		Expect(rawCountG(d, ctx, sender, "project", "secret-alpha")).To(Equal(0))
		Expect(rawCountG(d, ctx, sender, "project", "other")).To(Equal(1), "untouched")
	})

	ginkgo.It("preview SQL is byte-identical to what /purge runs", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "purgeparity")
		sender := f.Sender()

		base := time.Date(2025, 6, 12, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, ctx, sender, "x")
		seedHBG(d, ctx, sender, "x", "Go", base)

		hideRule, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "x", nil)
		Expect(err).NotTo(HaveOccurred())
		previewRows, previewRule, _, _, err := d.PurgeHiddenPreview(ctx, sender, hideRule, 10)
		Expect(err).NotTo(HaveOccurred())
		_, runRows, runRule, err := d.PurgeHiddenRule(ctx, sender, hideRule)
		Expect(err).NotTo(HaveOccurred())
		Expect(runRows).To(Equal(previewRows))
		Expect(runRule).To(Equal(previewRule))
	})

	ginkgo.It("rejects rename rules (only 'hide' can be purged) and leaves rule intact", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "purgerename")
		sender := f.Sender()

		renameRule, err := d.CreateCurationRule(ctx, sender, "project", "rename", "exact", "old", ptrStr("new"))
		Expect(err).NotTo(HaveOccurred())
		_, _, _, err = d.PurgeHiddenRule(ctx, sender, renameRule)
		Expect(err).To(HaveOccurred())
		Expect(strings.Contains(err.Error(), "only hide")).To(BeTrue(), "error should mention hide-only")

		still, _, err := d.GetCurationRule(ctx, renameRule.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(still).NotTo(BeNil(), "rename rule was deleted by a failed purge — transaction leak")
	})

	ginkgo.It("atomicity: a cancelled context leaves heartbeats + rule intact (tx rollback)", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "purgerollback")
		sender := f.Sender()

		base := time.Date(2025, 6, 13, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, ctx, sender, "will-purge")
		seedHBG(d, ctx, sender, "will-purge", "Go", base)

		hideRule, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "will-purge", nil)
		Expect(err).NotTo(HaveOccurred())

		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		_, _, _, err = d.PurgeHiddenRule(cancelledCtx, sender, hideRule)
		Expect(err).To(HaveOccurred(), "expected context.Canceled from PurgeHiddenRule")
		Expect(rawCountG(d, ctx, sender, "project", "will-purge")).To(Equal(1), "unchanged")
		still, _, err := d.GetCurationRule(ctx, hideRule.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(still).NotTo(BeNil(), "rule row was deleted despite tx failure — atomicity broken")
	})
})

// -- helpers restored from stdlib partner (boom-0vp.17) --
func ptrStr(s string) *string { return &s }
