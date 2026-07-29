// toggle_curation_ginkgo_test.go — ginkgo mirror of toggle_curation_test.go (gaka-0vp.13).
// 1:1 case map (7 stdlib TestXxx → 7 Its):
//   TestToggleCurationRuleHappyPath          → It "ToggleCurationRule: happy path"
//   TestToggleCurationRuleOwnerScoped        → It "ToggleCurationRule: owner scoped"
//   TestSetCurationRuleEnabledIdempotent     → It "SetCurationRuleEnabled: idempotent"
//   TestSetCurationRuleEnabledMissing        → It "SetCurationRuleEnabled: missing id returns found=false"
//   TestLoadHiddenSetsSkipsDisabled          → It "LoadHiddenSets: skips disabled hide rules"
//   TestLoadRenameSetsSkipsDisabled          → It "LoadRenameSets: skips disabled rename rules; ListCurationRules keeps them"
//   TestCreateCurationRuleReEnablesOnUpsert  → It "CreateCurationRule: upsert re-enables paused rule"
package db

import (
	"context"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("curation rule toggle (gaka-dfd)", func() {
	ginkgo.It("ToggleCurationRule flips enabled and returns the new value each time", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "toggle_hp")
		sender := f.Sender()

		ensureProjectsG(d, ctx, sender, "keep")
		id := createRenameG(d, ctx, sender, "project", "keep", "kept")

		rule, _, err := d.GetCurationRule(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(rule.Enabled).To(BeTrue(), "newly-created rule should be enabled=true")

		got, found, err := d.ToggleCurationRule(ctx, sender, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(got).To(BeFalse(), "first toggle should have returned enabled=false")

		got, found, err = d.ToggleCurationRule(ctx, sender, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(got).To(BeTrue(), "second toggle should have returned enabled=true")
	})

	ginkgo.It("ToggleCurationRule is owner-scoped (wrong sender: found=false, rule unchanged)", func() {
		d := openTestDBG()
		ctx := context.Background()

		fA := newSenderG(d, "toggle_ownerA")
		fB := newSenderG(d, "toggle_ownerB")
		senderA, senderB := fA.Sender(), fB.Sender()

		ensureProjectsG(d, ctx, senderA, "aProj")
		id := createRenameG(d, ctx, senderA, "project", "aProj", "aProjRenamed")

		_, found, err := d.ToggleCurationRule(ctx, senderB, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse(), "cross-owner toggle must report found=false (never leak existence)")

		rule, _, err := d.GetCurationRule(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(rule.Enabled).To(BeTrue(), "cross-owner toggle attempt mutated the rule (owner-scope violation)")
	})

	ginkgo.It("SetCurationRuleEnabled is idempotent (setting current value still returns found=true)", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "toggle_idem")
		sender := f.Sender()

		ensureProjectsG(d, ctx, sender, "proj")
		id := createRenameG(d, ctx, sender, "project", "proj", "projRenamed")

		found, err := d.SetCurationRuleEnabled(ctx, sender, id, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue(), "idempotent set-to-current-value must still return found=true")

		found, err = d.SetCurationRuleEnabled(ctx, sender, id, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		rule, _, err := d.GetCurationRule(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(rule.Enabled).To(BeFalse(), "SetCurationRuleEnabled(false) did not persist")

		found, err = d.SetCurationRuleEnabled(ctx, sender, id, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue(), "second idempotent set must still return found=true")
	})

	ginkgo.It("SetCurationRuleEnabled on a missing id returns found=false (not an error)", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "toggle_missing")
		sender := f.Sender()

		found, err := d.SetCurationRuleEnabled(ctx, sender, 999999999, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
	})

	ginkgo.It("LoadHiddenSets skips disabled hide rules (only enabled rows contribute)", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "toggle_hide_skip")
		sender := f.Sender()

		ensureProjectsG(d, ctx, sender, "hideA", "hideB")
		_, err := d.Pool.Exec(ctx,
			`INSERT INTO curation_rules (sender, axis, action, match_type, match_value, enabled)
			 VALUES ($1, 'project', 'hide', 'exact', 'hideA', true),
			        ($1, 'project', 'hide', 'exact', 'hideB', false)`, sender)
		Expect(err).NotTo(HaveOccurred())

		hs, err := d.LoadHiddenSets(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(hs.AnyHidden()).To(BeTrue())
		got := hs.byAxis["project"]
		Expect(got).To(HaveLen(1), "disabled rule must be skipped")
		Expect(got[0]).To(Equal("hidea"), "values are pre-lowered")
	})

	ginkgo.It("LoadRenameSets skips disabled rename rules while ListCurationRules keeps them (UI needs it)", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "toggle_rename_skip")
		sender := f.Sender()

		ensureProjectsG(d, ctx, sender, "src1", "src2", "dst1", "dst2")
		idOn := createRenameG(d, ctx, sender, "project", "src1", "dst1")
		idOff := createRenameG(d, ctx, sender, "project", "src2", "dst2")

		_, err := d.SetCurationRuleEnabled(ctx, sender, idOff, false)
		Expect(err).NotTo(HaveOccurred())

		rs, err := d.LoadRenameSets(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		a := rs.byAxis["project"]
		Expect(a.exact).To(HaveLen(1))
		_, ok := a.exact["src1"]
		Expect(ok).To(BeTrue(), "expected the ENABLED rename 'src1' to survive")
		_, ok = a.exact["src2"]
		Expect(ok).To(BeFalse(), "DISABLED rename 'src2' leaked into RenameSets")

		rules, err := d.ListCurationRules(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		var seenDisabled bool
		for _, r := range rules {
			if r.ID == idOff {
				seenDisabled = true
				Expect(r.Enabled).To(BeFalse(), "disabled rule returned with enabled=true — scan drift")
			}
			if r.ID == idOn {
				Expect(r.Enabled).To(BeTrue(), "enabled rule reported as disabled — scan drift")
			}
		}
		Expect(seenDisabled).To(BeTrue(), "ListCurationRules must still return disabled rules (UI depends on it)")
	})

	ginkgo.It("CreateCurationRule upserts a matching key: re-enables paused rule and updates new_value", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "toggle_upsert")
		sender := f.Sender()

		ensureProjectsG(d, ctx, sender, "px", "pxNew", "pxNewer")
		id := createRenameG(d, ctx, sender, "project", "px", "pxNew")

		_, err := d.SetCurationRuleEnabled(ctx, sender, id, false)
		Expect(err).NotTo(HaveOccurred())

		newTarget := "pxNewer"
		_, err = d.CreateCurationRule(ctx, sender, "project", "rename", "exact", "px", &newTarget)
		Expect(err).NotTo(HaveOccurred())
		rule, _, err := d.GetCurationRule(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(rule.Enabled).To(BeTrue(), "upsert of a matching key must re-enable a paused rule")
		Expect(rule.NewValue).NotTo(BeNil())
		Expect(*rule.NewValue).To(Equal("pxNewer"))
	})
})
