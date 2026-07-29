// goals_ginkgo_test.go — ginkgo mirror of goals_test.go (gaka-0vp.13).
// 1:1 case map (13 stdlib TestXxx → 13 Its):
//   TestGoalsCRUDAndSpecRoundtrip           → It "CRUD + spec round-trip"
//   TestGoalsUpdateSpecClearsProgress       → It "spec PATCH clears cached progress"
//   TestGoalsInvalidateForOwner             → It "InvalidateGoalsForOwner is owner-scoped"
//   TestGoalsOwnerScoping                   → It "owner scoping on Get/Update/Delete/Toggle"
//   TestGoalsToggle                         → It "Toggle: flip semantics + idempotent set"
//   TestGoalsDuplicateName                  → It "UNIQUE (owner, name) constraint"
//   TestGoalsUpdateAllPatchFields           → It "every UpdateGoal PATCH branch + updated_at bump"
//   TestGoalsUpdateNoOpPatch                → It "empty patch is a GET-like no-op"
//   TestGoalsUpdateMissingID                → It "missing id: (nil,nil)/(false,nil)/(_,false,nil)"
//   TestGoalsListOwnerScoping               → It "ListGoals is owner-scoped"
//   TestGoalsToggleExactSetOppositeValue    → It "exact-set toggle with opposite value flips"
//   TestGoalsInvalidateEmptyOwner           → It "InvalidateGoalsForOwner: empty owner is a no-op"
//   TestGoalsUpdateProgressNilClears        → It "UpdateGoalProgress(nil) explicitly clears"
package db

import (
	"context"
	"encoding/json"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// cleanupGoalsG mirrors cleanupGoals for ginkgo.
func cleanupGoalsG(d *DB, ctx context.Context, sender string) {
	ginkgo.DeferCleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM goals WHERE owner=$1`, sender)
	})
}

var _ = ginkgo.Describe("goals (gaka-wpb)", func() {
	ginkgo.It("CRUD happy path + spec JSONB round-trips semantically through create/read/PATCH", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_crud")
		cleanupGoalsG(d, ctx, fx.Sender())

		desc := "one hour weekly on Go"
		g, err := d.CreateGoal(ctx, fx.Sender(), "weekly-go", &desc, json.RawMessage(plantedSpec))
		Expect(err).NotTo(HaveOccurred())
		Expect(g).NotTo(BeNil())
		Expect(g.Owner).To(Equal(fx.Sender()))
		Expect(g.Name).To(Equal("weekly-go"))
		Expect(g.Description).NotTo(BeNil())
		Expect(*g.Description).To(Equal(desc))
		Expect(g.Enabled).To(BeTrue())
		Expect(semanticGoalsDiff(plantedSpec, string(g.Spec))).To(BeEmpty())

		got, err := d.GetGoal(ctx, fx.Sender(), g.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
		Expect(semanticGoalsDiff(plantedSpec, string(got.Spec))).To(BeEmpty())

		list, err := d.ListGoals(ctx, fx.Sender())
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].ID).To(Equal(g.ID))

		newDesc := "at least 60 minutes / week on Go"
		updated, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{Description: &newDesc})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).NotTo(BeNil())
		Expect(updated.Description).NotTo(BeNil())
		Expect(*updated.Description).To(Equal(newDesc))
		Expect(semanticGoalsDiff(plantedSpec, string(updated.Spec))).To(BeEmpty())

		ok, err := d.DeleteGoal(ctx, fx.Sender(), g.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		ok, err = d.DeleteGoal(ctx, fx.Sender(), g.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	ginkgo.It("PATCHing spec clears cached last_progress + last_evaluated_at atomically", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_specclear")
		cleanupGoalsG(d, ctx, fx.Sender())

		g, err := d.CreateGoal(ctx, fx.Sender(), "clear-me", nil, json.RawMessage(plantedSpec))
		Expect(err).NotTo(HaveOccurred())
		planted := json.RawMessage(`{"hit":true,"progress":1,"sub_conditions":[]}`)
		Expect(d.UpdateGoalProgress(ctx, fx.Sender(), g.ID, planted)).To(Succeed())
		before, err := d.GetGoal(ctx, fx.Sender(), g.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(before.LastProgress)).To(BeNumerically(">", 0))
		Expect(before.LastEvaluatedAt).NotTo(BeNil())

		newSpec := json.RawMessage(`{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":7200,"window":"week"}`)
		after, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{Spec: &newSpec})
		Expect(err).NotTo(HaveOccurred())
		Expect(after.LastProgress).To(HaveLen(0))
		Expect(after.LastEvaluatedAt).To(BeNil())
	})

	ginkgo.It("InvalidateGoalsForOwner clears all owner's cached progress + is owner-scoped", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_invalidate")
		cleanupGoalsG(d, ctx, fx.Sender())

		g1, _ := d.CreateGoal(ctx, fx.Sender(), "g1", nil, json.RawMessage(plantedSpec))
		g2, _ := d.CreateGoal(ctx, fx.Sender(), "g2", nil, json.RawMessage(plantedSpec))
		Expect(g1).NotTo(BeNil())
		Expect(g2).NotTo(BeNil())
		planted := json.RawMessage(`{"hit":false,"progress":0.5,"sub_conditions":[]}`)
		Expect(d.UpdateGoalProgress(ctx, fx.Sender(), g1.ID, planted)).To(Succeed())
		Expect(d.UpdateGoalProgress(ctx, fx.Sender(), g2.ID, planted)).To(Succeed())

		other := newSenderG(d, "goals_invalidate_other")
		cleanupGoalsG(d, ctx, other.Sender())
		og, err := d.CreateGoal(ctx, other.Sender(), "og", nil, json.RawMessage(plantedSpec))
		Expect(err).NotTo(HaveOccurred())
		Expect(d.UpdateGoalProgress(ctx, other.Sender(), og.ID, planted)).To(Succeed())

		Expect(d.InvalidateGoalsForOwner(ctx, fx.Sender())).To(Succeed())

		after1, _ := d.GetGoal(ctx, fx.Sender(), g1.ID)
		after2, _ := d.GetGoal(ctx, fx.Sender(), g2.ID)
		afterO, _ := d.GetGoal(ctx, other.Sender(), og.ID)
		Expect(after1.LastProgress).To(HaveLen(0))
		Expect(after1.LastEvaluatedAt).To(BeNil())
		Expect(after2.LastProgress).To(HaveLen(0))
		Expect(after2.LastEvaluatedAt).To(BeNil())
		Expect(len(afterO.LastProgress)).To(BeNumerically(">", 0), "other-owner goal wrongly invalidated")
		Expect(afterO.LastEvaluatedAt).NotTo(BeNil())
	})

	ginkgo.It("owner scoping: alice cannot Get/Update/Delete/Toggle bob's goal (nil/false — not-found sentinel)", func() {
		d := openTestDBG()
		ctx := context.Background()
		alice := newSenderG(d, "goals_alice")
		bob := newSenderG(d, "goals_bob")
		cleanupGoalsG(d, ctx, alice.Sender())
		cleanupGoalsG(d, ctx, bob.Sender())

		bg, err := d.CreateGoal(ctx, bob.Sender(), "bob-goal", nil, json.RawMessage(plantedSpec))
		Expect(err).NotTo(HaveOccurred())

		got, err := d.GetGoal(ctx, alice.Sender(), bg.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())

		name := "hijacked"
		patched, err := d.UpdateGoal(ctx, alice.Sender(), bg.ID, GoalPatch{Name: &name})
		Expect(err).NotTo(HaveOccurred())
		Expect(patched).To(BeNil())
		still, _ := d.GetGoal(ctx, bob.Sender(), bg.ID)
		Expect(still).NotTo(BeNil())
		Expect(still.Name).To(Equal("bob-goal"))

		ok, err := d.DeleteGoal(ctx, alice.Sender(), bg.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		still, _ = d.GetGoal(ctx, bob.Sender(), bg.ID)
		Expect(still).NotTo(BeNil())

		_, found, err := d.ToggleGoal(ctx, alice.Sender(), bg.ID, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
	})

	ginkgo.It("ToggleGoal flip semantics + idempotent exact-set at current value", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_toggle")
		cleanupGoalsG(d, ctx, fx.Sender())

		g, err := d.CreateGoal(ctx, fx.Sender(), "toggle-me", nil, json.RawMessage(plantedSpec))
		Expect(err).NotTo(HaveOccurred())
		Expect(g.Enabled).To(BeTrue())

		newEnabled, found, err := d.ToggleGoal(ctx, fx.Sender(), g.ID, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(newEnabled).To(BeFalse())

		newEnabled, found, err = d.ToggleGoal(ctx, fx.Sender(), g.ID, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(newEnabled).To(BeTrue())

		desired := true
		_, found, err = d.ToggleGoal(ctx, fx.Sender(), g.ID, &desired)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
	})

	ginkgo.It("UNIQUE (owner, name): duplicate name for same owner fails; different owners OK", func() {
		d := openTestDBG()
		ctx := context.Background()
		alice := newSenderG(d, "goals_dup_a")
		bob := newSenderG(d, "goals_dup_b")
		cleanupGoalsG(d, ctx, alice.Sender())
		cleanupGoalsG(d, ctx, bob.Sender())

		_, err := d.CreateGoal(ctx, alice.Sender(), "shared-name", nil, json.RawMessage(plantedSpec))
		Expect(err).NotTo(HaveOccurred())
		_, err = d.CreateGoal(ctx, alice.Sender(), "shared-name", nil, json.RawMessage(plantedSpec))
		Expect(err).To(HaveOccurred(), "second create with same (owner,name) must fail")
		_, err = d.CreateGoal(ctx, bob.Sender(), "shared-name", nil, json.RawMessage(plantedSpec))
		Expect(err).NotTo(HaveOccurred())
	})

	ginkgo.It("UpdateGoal covers every dynamic PATCH branch + always bumps updated_at on non-empty patch", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_patchfields")
		cleanupGoalsG(d, ctx, fx.Sender())

		desc0 := "initial"
		g, err := d.CreateGoal(ctx, fx.Sender(), "orig-name", &desc0, json.RawMessage(plantedSpec))
		Expect(err).NotTo(HaveOccurred())
		origUpdatedAt := g.UpdatedAt

		newName := "renamed-goal"
		g2, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{Name: &newName})
		Expect(err).NotTo(HaveOccurred())
		Expect(g2.Name).To(Equal(newName))
		Expect(g2.Description).NotTo(BeNil())
		Expect(*g2.Description).To(Equal(desc0))
		Expect(g2.Enabled).To(BeTrue())
		Expect(g2.UpdatedAt.After(origUpdatedAt)).To(BeTrue())
		Expect(semanticGoalsDiff(plantedSpec, string(g2.Spec))).To(BeEmpty())

		falseVal := false
		g3, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{Enabled: &falseVal})
		Expect(err).NotTo(HaveOccurred())
		Expect(g3.Enabled).To(BeFalse())
		Expect(g3.Name).To(Equal(newName))

		desc2 := "second desc"
		g4, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{Description: &desc2})
		Expect(err).NotTo(HaveOccurred())
		Expect(g4.Description).NotTo(BeNil())
		Expect(*g4.Description).To(Equal(desc2))
		Expect(g4.Enabled).To(BeFalse())

		nName := "combined"
		nDesc := "combined desc"
		g5, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{Name: &nName, Description: &nDesc})
		Expect(err).NotTo(HaveOccurred())
		Expect(g5.Name).To(Equal(nName))
		Expect(g5.Description).NotTo(BeNil())
		Expect(*g5.Description).To(Equal(nDesc))
	})

	ginkgo.It("empty patch returns the row unchanged (GET-like no-op, no updated_at bump)", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_noop")
		cleanupGoalsG(d, ctx, fx.Sender())

		desc := "keep me"
		g, err := d.CreateGoal(ctx, fx.Sender(), "noop", &desc, json.RawMessage(plantedSpec))
		Expect(err).NotTo(HaveOccurred())
		origUpdatedAt := g.UpdatedAt

		back, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{})
		Expect(err).NotTo(HaveOccurred())
		Expect(back).NotTo(BeNil())
		Expect(back.ID).To(Equal(g.ID))
		Expect(back.UpdatedAt.Equal(origUpdatedAt)).To(BeTrue())
		Expect(back.Description).NotTo(BeNil())
		Expect(*back.Description).To(Equal(desc))
	})

	ginkgo.It("missing id: UpdateGoal→(nil,nil); DeleteGoal→(false,nil); ToggleGoal→(_,false,nil); GetGoal→(nil,nil)", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_missing")
		cleanupGoalsG(d, ctx, fx.Sender())

		fakeID := "00000000-0000-0000-0000-000000000000"
		nm := "ghost"
		got, err := d.UpdateGoal(ctx, fx.Sender(), fakeID, GoalPatch{Name: &nm})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())

		ok, err := d.DeleteGoal(ctx, fx.Sender(), fakeID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())

		_, found, err := d.ToggleGoal(ctx, fx.Sender(), fakeID, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
		setTrue := true
		_, found, err = d.ToggleGoal(ctx, fx.Sender(), fakeID, &setTrue)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())

		g, err := d.GetGoal(ctx, fx.Sender(), fakeID)
		Expect(err).NotTo(HaveOccurred())
		Expect(g).To(BeNil())
	})

	ginkgo.It("ListGoals is owner-scoped: alice/bob get exactly their own rows", func() {
		d := openTestDBG()
		ctx := context.Background()
		alice := newSenderG(d, "goals_list_a")
		bob := newSenderG(d, "goals_list_b")
		cleanupGoalsG(d, ctx, alice.Sender())
		cleanupGoalsG(d, ctx, bob.Sender())

		a1, _ := d.CreateGoal(ctx, alice.Sender(), "a1", nil, json.RawMessage(plantedSpec))
		a2, _ := d.CreateGoal(ctx, alice.Sender(), "a2", nil, json.RawMessage(plantedSpec))
		b1, _ := d.CreateGoal(ctx, bob.Sender(), "b1", nil, json.RawMessage(plantedSpec))
		b2, _ := d.CreateGoal(ctx, bob.Sender(), "b2", nil, json.RawMessage(plantedSpec))
		b3, _ := d.CreateGoal(ctx, bob.Sender(), "b3", nil, json.RawMessage(plantedSpec))
		Expect(a1).NotTo(BeNil())
		Expect(a2).NotTo(BeNil())
		Expect(b1).NotTo(BeNil())
		Expect(b2).NotTo(BeNil())
		Expect(b3).NotTo(BeNil())

		aList, err := d.ListGoals(ctx, alice.Sender())
		Expect(err).NotTo(HaveOccurred())
		Expect(aList).To(HaveLen(2))
		aIDs := map[string]bool{}
		for _, g := range aList {
			aIDs[g.ID] = true
			Expect(g.Owner).To(Equal(alice.Sender()))
		}
		Expect(aIDs[a1.ID]).To(BeTrue())
		Expect(aIDs[a2.ID]).To(BeTrue())

		bList, err := d.ListGoals(ctx, bob.Sender())
		Expect(err).NotTo(HaveOccurred())
		Expect(bList).To(HaveLen(3))
		for _, g := range bList {
			Expect(g.Owner).To(Equal(bob.Sender()))
		}
	})

	ginkgo.It("exact-set toggle with opposite value performs a REAL flip (not just an idempotent set)", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_exactset")
		cleanupGoalsG(d, ctx, fx.Sender())

		g, err := d.CreateGoal(ctx, fx.Sender(), "flipme", nil, json.RawMessage(plantedSpec))
		Expect(err).NotTo(HaveOccurred())
		Expect(g.Enabled).To(BeTrue())

		desiredFalse := false
		newEnabled, found, err := d.ToggleGoal(ctx, fx.Sender(), g.ID, &desiredFalse)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(newEnabled).To(BeFalse())
		after, _ := d.GetGoal(ctx, fx.Sender(), g.ID)
		Expect(after.Enabled).To(BeFalse())

		desiredTrue := true
		newEnabled, found, err = d.ToggleGoal(ctx, fx.Sender(), g.ID, &desiredTrue)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(newEnabled).To(BeTrue())
		after, _ = d.GetGoal(ctx, fx.Sender(), g.ID)
		Expect(after.Enabled).To(BeTrue())
	})

	ginkgo.It("InvalidateGoalsForOwner: empty-owner call is a no-op that doesn't touch other owners", func() {
		d := openTestDBG()
		ctx := context.Background()
		empty := newSenderG(d, "goals_inv_empty")
		other := newSenderG(d, "goals_inv_other")
		cleanupGoalsG(d, ctx, empty.Sender())
		cleanupGoalsG(d, ctx, other.Sender())

		og, _ := d.CreateGoal(ctx, other.Sender(), "og", nil, json.RawMessage(plantedSpec))
		Expect(og).NotTo(BeNil())
		planted := json.RawMessage(`{"hit":true,"progress":1,"sub_conditions":[]}`)
		Expect(d.UpdateGoalProgress(ctx, other.Sender(), og.ID, planted)).To(Succeed())

		Expect(d.InvalidateGoalsForOwner(ctx, empty.Sender())).To(Succeed())
		after, _ := d.GetGoal(ctx, other.Sender(), og.ID)
		Expect(len(after.LastProgress)).To(BeNumerically(">", 0))
		Expect(after.LastEvaluatedAt).NotTo(BeNil())
	})

	ginkgo.It("UpdateGoalProgress(nil) explicitly clears last_progress + last_evaluated_at", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_progclear")
		cleanupGoalsG(d, ctx, fx.Sender())

		g, err := d.CreateGoal(ctx, fx.Sender(), "clr", nil, json.RawMessage(plantedSpec))
		Expect(err).NotTo(HaveOccurred())
		planted := json.RawMessage(`{"hit":false,"progress":0.1,"sub_conditions":[]}`)
		Expect(d.UpdateGoalProgress(ctx, fx.Sender(), g.ID, planted)).To(Succeed())
		mid, _ := d.GetGoal(ctx, fx.Sender(), g.ID)
		Expect(len(mid.LastProgress)).To(BeNumerically(">", 0))
		Expect(mid.LastEvaluatedAt).NotTo(BeNil())

		Expect(d.UpdateGoalProgress(ctx, fx.Sender(), g.ID, nil)).To(Succeed())
		after, _ := d.GetGoal(ctx, fx.Sender(), g.ID)
		Expect(after).NotTo(BeNil())
		Expect(after.LastProgress).To(HaveLen(0))
		Expect(after.LastEvaluatedAt).To(BeNil())
	})
})
