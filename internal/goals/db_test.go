// goals_ginkgo_test.go — ginkgo mirror of goals_test.go (gaka-0vp.13).
// 1:1 case map (13 stdlib TestXxx → 13 Its):
//
//	TestGoalsCRUDAndSpecRoundtrip           → It "CRUD + spec round-trip"
//	TestGoalsUpdateSpecClearsProgress       → It "spec PATCH clears cached progress"
//	TestGoalsInvalidateForOwner             → It "InvalidateGoalsForOwner is owner-scoped"
//	TestGoalsOwnerScoping                   → It "owner scoping on Get/Update/Delete/Toggle"
//	TestGoalsToggle                         → It "Toggle: flip semantics + idempotent set"
//	TestGoalsDuplicateName                  → It "UNIQUE (owner, name) constraint"
//	TestGoalsUpdateAllPatchFields           → It "every UpdateGoal PATCH branch + updated_at bump"
//	TestGoalsUpdateNoOpPatch                → It "empty patch is a GET-like no-op"
//	TestGoalsUpdateMissingID                → It "missing id: (nil,nil)/(false,nil)/(_,false,nil)"
//	TestGoalsListOwnerScoping               → It "ListGoals is owner-scoped"
//	TestGoalsToggleExactSetOppositeValue    → It "exact-set toggle with opposite value flips"
//	TestGoalsInvalidateEmptyOwner           → It "InvalidateGoalsForOwner: empty owner is a no-op"
//	TestGoalsUpdateProgressNilClears        → It "UpdateGoalProgress(nil) explicitly clears"
package goals_test

import (
	"context"
	"encoding/json"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/goals"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// cleanupGoalsG mirrors cleanupGoals for ginkgo.
func cleanupGoalsG(d *db.DB, ctx context.Context, sender string) {
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
		g, err := goals.CreateGoal(d, ctx, fx.Sender(), "weekly-go", &desc, json.RawMessage(plantedSpec), false)
		Expect(err).NotTo(HaveOccurred())
		Expect(g).NotTo(BeNil())
		Expect(g.Owner).To(Equal(fx.Sender()))
		Expect(g.Name).To(Equal("weekly-go"))
		Expect(g.Description).NotTo(BeNil())
		Expect(*g.Description).To(Equal(desc))
		Expect(g.Enabled).To(BeTrue())
		Expect(semanticGoalsDiff(plantedSpec, string(g.Spec))).To(BeEmpty())

		got, err := goals.GetGoal(d, ctx, fx.Sender(), g.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
		Expect(semanticGoalsDiff(plantedSpec, string(got.Spec))).To(BeEmpty())

		list, err := goals.ListGoals(d, ctx, fx.Sender())
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].ID).To(Equal(g.ID))

		newDesc := "at least 60 minutes / week on Go"
		updated, err := goals.UpdateGoal(d, ctx, fx.Sender(), g.ID, goals.GoalPatch{Description: &newDesc})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).NotTo(BeNil())
		Expect(updated.Description).NotTo(BeNil())
		Expect(*updated.Description).To(Equal(newDesc))
		Expect(semanticGoalsDiff(plantedSpec, string(updated.Spec))).To(BeEmpty())

		ok, err := goals.DeleteGoal(d, ctx, fx.Sender(), g.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		ok, err = goals.DeleteGoal(d, ctx, fx.Sender(), g.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	ginkgo.It("PATCHing spec clears cached last_progress + last_evaluated_at atomically", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_specclear")
		cleanupGoalsG(d, ctx, fx.Sender())

		g, err := goals.CreateGoal(d, ctx, fx.Sender(), "clear-me", nil, json.RawMessage(plantedSpec), false)
		Expect(err).NotTo(HaveOccurred())
		planted := json.RawMessage(`{"hit":true,"progress":1,"sub_conditions":[]}`)
		Expect(goals.UpdateGoalProgress(d, ctx, fx.Sender(), g.ID, planted)).To(Succeed())
		before, err := goals.GetGoal(d, ctx, fx.Sender(), g.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(before.LastProgress)).To(BeNumerically(">", 0))
		Expect(before.LastEvaluatedAt).NotTo(BeNil())

		newSpec := json.RawMessage(`{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":7200,"window":"week"}`)
		after, err := goals.UpdateGoal(d, ctx, fx.Sender(), g.ID, goals.GoalPatch{Spec: &newSpec})
		Expect(err).NotTo(HaveOccurred())
		Expect(after.LastProgress).To(HaveLen(0))
		Expect(after.LastEvaluatedAt).To(BeNil())
	})

	ginkgo.It("InvalidateGoalsForOwner clears all owner's cached progress + is owner-scoped", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_invalidate")
		cleanupGoalsG(d, ctx, fx.Sender())

		g1, _ := goals.CreateGoal(d, ctx, fx.Sender(), "g1", nil, json.RawMessage(plantedSpec), false)
		g2, _ := goals.CreateGoal(d, ctx, fx.Sender(), "g2", nil, json.RawMessage(plantedSpec), false)
		Expect(g1).NotTo(BeNil())
		Expect(g2).NotTo(BeNil())
		planted := json.RawMessage(`{"hit":false,"progress":0.5,"sub_conditions":[]}`)
		Expect(goals.UpdateGoalProgress(d, ctx, fx.Sender(), g1.ID, planted)).To(Succeed())
		Expect(goals.UpdateGoalProgress(d, ctx, fx.Sender(), g2.ID, planted)).To(Succeed())

		other := newSenderG(d, "goals_invalidate_other")
		cleanupGoalsG(d, ctx, other.Sender())
		og, err := goals.CreateGoal(d, ctx, other.Sender(), "og", nil, json.RawMessage(plantedSpec), false)
		Expect(err).NotTo(HaveOccurred())
		Expect(goals.UpdateGoalProgress(d, ctx, other.Sender(), og.ID, planted)).To(Succeed())

		Expect(goals.InvalidateGoalsForOwner(d, ctx, fx.Sender())).To(Succeed())

		after1, _ := goals.GetGoal(d, ctx, fx.Sender(), g1.ID)
		after2, _ := goals.GetGoal(d, ctx, fx.Sender(), g2.ID)
		afterO, _ := goals.GetGoal(d, ctx, other.Sender(), og.ID)
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

		bg, err := goals.CreateGoal(d, ctx, bob.Sender(), "bob-goal", nil, json.RawMessage(plantedSpec), false)
		Expect(err).NotTo(HaveOccurred())

		got, err := goals.GetGoal(d, ctx, alice.Sender(), bg.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())

		name := "hijacked"
		patched, err := goals.UpdateGoal(d, ctx, alice.Sender(), bg.ID, goals.GoalPatch{Name: &name})
		Expect(err).NotTo(HaveOccurred())
		Expect(patched).To(BeNil())
		still, _ := goals.GetGoal(d, ctx, bob.Sender(), bg.ID)
		Expect(still).NotTo(BeNil())
		Expect(still.Name).To(Equal("bob-goal"))

		ok, err := goals.DeleteGoal(d, ctx, alice.Sender(), bg.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		still, _ = goals.GetGoal(d, ctx, bob.Sender(), bg.ID)
		Expect(still).NotTo(BeNil())

		_, found, err := goals.ToggleGoal(d, ctx, alice.Sender(), bg.ID, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
	})

	ginkgo.It("ToggleGoal flip semantics + idempotent exact-set at current value", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_toggle")
		cleanupGoalsG(d, ctx, fx.Sender())

		g, err := goals.CreateGoal(d, ctx, fx.Sender(), "toggle-me", nil, json.RawMessage(plantedSpec), false)
		Expect(err).NotTo(HaveOccurred())
		Expect(g.Enabled).To(BeTrue())

		newEnabled, found, err := goals.ToggleGoal(d, ctx, fx.Sender(), g.ID, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(newEnabled).To(BeFalse())

		newEnabled, found, err = goals.ToggleGoal(d, ctx, fx.Sender(), g.ID, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(newEnabled).To(BeTrue())

		desired := true
		_, found, err = goals.ToggleGoal(d, ctx, fx.Sender(), g.ID, &desired)
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

		_, err := goals.CreateGoal(d, ctx, alice.Sender(), "shared-name", nil, json.RawMessage(plantedSpec), false)
		Expect(err).NotTo(HaveOccurred())
		_, err = goals.CreateGoal(d, ctx, alice.Sender(), "shared-name", nil, json.RawMessage(plantedSpec), false)
		Expect(err).To(HaveOccurred(), "second create with same (owner,name) must fail")
		_, err = goals.CreateGoal(d, ctx, bob.Sender(), "shared-name", nil, json.RawMessage(plantedSpec), false)
		Expect(err).NotTo(HaveOccurred())
	})

	ginkgo.It("UpdateGoal covers every dynamic PATCH branch + always bumps updated_at on non-empty patch", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_patchfields")
		cleanupGoalsG(d, ctx, fx.Sender())

		desc0 := "initial"
		g, err := goals.CreateGoal(d, ctx, fx.Sender(), "orig-name", &desc0, json.RawMessage(plantedSpec), false)
		Expect(err).NotTo(HaveOccurred())
		origUpdatedAt := g.UpdatedAt

		newName := "renamed-goal"
		g2, err := goals.UpdateGoal(d, ctx, fx.Sender(), g.ID, goals.GoalPatch{Name: &newName})
		Expect(err).NotTo(HaveOccurred())
		Expect(g2.Name).To(Equal(newName))
		Expect(g2.Description).NotTo(BeNil())
		Expect(*g2.Description).To(Equal(desc0))
		Expect(g2.Enabled).To(BeTrue())
		Expect(g2.UpdatedAt.After(origUpdatedAt)).To(BeTrue())
		Expect(semanticGoalsDiff(plantedSpec, string(g2.Spec))).To(BeEmpty())

		falseVal := false
		g3, err := goals.UpdateGoal(d, ctx, fx.Sender(), g.ID, goals.GoalPatch{Enabled: &falseVal})
		Expect(err).NotTo(HaveOccurred())
		Expect(g3.Enabled).To(BeFalse())
		Expect(g3.Name).To(Equal(newName))

		desc2 := "second desc"
		g4, err := goals.UpdateGoal(d, ctx, fx.Sender(), g.ID, goals.GoalPatch{Description: &desc2})
		Expect(err).NotTo(HaveOccurred())
		Expect(g4.Description).NotTo(BeNil())
		Expect(*g4.Description).To(Equal(desc2))
		Expect(g4.Enabled).To(BeFalse())

		nName := "combined"
		nDesc := "combined desc"
		g5, err := goals.UpdateGoal(d, ctx, fx.Sender(), g.ID, goals.GoalPatch{Name: &nName, Description: &nDesc})
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
		g, err := goals.CreateGoal(d, ctx, fx.Sender(), "noop", &desc, json.RawMessage(plantedSpec), false)
		Expect(err).NotTo(HaveOccurred())
		origUpdatedAt := g.UpdatedAt

		back, err := goals.UpdateGoal(d, ctx, fx.Sender(), g.ID, goals.GoalPatch{})
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
		got, err := goals.UpdateGoal(d, ctx, fx.Sender(), fakeID, goals.GoalPatch{Name: &nm})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())

		ok, err := goals.DeleteGoal(d, ctx, fx.Sender(), fakeID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())

		_, found, err := goals.ToggleGoal(d, ctx, fx.Sender(), fakeID, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
		setTrue := true
		_, found, err = goals.ToggleGoal(d, ctx, fx.Sender(), fakeID, &setTrue)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())

		g, err := goals.GetGoal(d, ctx, fx.Sender(), fakeID)
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

		a1, _ := goals.CreateGoal(d, ctx, alice.Sender(), "a1", nil, json.RawMessage(plantedSpec), false)
		a2, _ := goals.CreateGoal(d, ctx, alice.Sender(), "a2", nil, json.RawMessage(plantedSpec), false)
		b1, _ := goals.CreateGoal(d, ctx, bob.Sender(), "b1", nil, json.RawMessage(plantedSpec), false)
		b2, _ := goals.CreateGoal(d, ctx, bob.Sender(), "b2", nil, json.RawMessage(plantedSpec), false)
		b3, _ := goals.CreateGoal(d, ctx, bob.Sender(), "b3", nil, json.RawMessage(plantedSpec), false)
		Expect(a1).NotTo(BeNil())
		Expect(a2).NotTo(BeNil())
		Expect(b1).NotTo(BeNil())
		Expect(b2).NotTo(BeNil())
		Expect(b3).NotTo(BeNil())

		aList, err := goals.ListGoals(d, ctx, alice.Sender())
		Expect(err).NotTo(HaveOccurred())
		Expect(aList).To(HaveLen(2))
		aIDs := map[string]bool{}
		for _, g := range aList {
			aIDs[g.ID] = true
			Expect(g.Owner).To(Equal(alice.Sender()))
		}
		Expect(aIDs[a1.ID]).To(BeTrue())
		Expect(aIDs[a2.ID]).To(BeTrue())

		bList, err := goals.ListGoals(d, ctx, bob.Sender())
		Expect(err).NotTo(HaveOccurred())
		Expect(bList).To(HaveLen(3))
		for _, g := range bList {
			Expect(g.Owner).To(Equal(bob.Sender()))
		}
	})

	ginkgo.It("ListPublicGoals returns ONLY enabled&&public goals (Part B Stage 4 SQL privacy gate)", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_list_public")
		cleanupGoalsG(d, ctx, fx.Sender())

		pub, err := goals.CreateGoal(d, ctx, fx.Sender(), "public-enabled", nil, json.RawMessage(plantedSpec), true)
		Expect(err).NotTo(HaveOccurred())
		_, err = goals.CreateGoal(d, ctx, fx.Sender(), "private-enabled", nil, json.RawMessage(plantedSpec), false)
		Expect(err).NotTo(HaveOccurred())
		pubDisabled, err := goals.CreateGoal(d, ctx, fx.Sender(), "public-disabled", nil, json.RawMessage(plantedSpec), true)
		Expect(err).NotTo(HaveOccurred())
		disabled := false
		_, err = goals.UpdateGoal(d, ctx, fx.Sender(), pubDisabled.ID, goals.GoalPatch{Enabled: &disabled})
		Expect(err).NotTo(HaveOccurred())

		got, err := goals.ListPublicGoals(d, ctx, fx.Sender())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1), "only the enabled&&public goal should be returned")
		Expect(got[0].ID).To(Equal(pub.ID))
		Expect(got[0].Enabled).To(BeTrue())
		Expect(got[0].Public).To(BeTrue())
	})

	ginkgo.It("ListPublicGoals is owner-scoped and empty-owner rejects like ListGoals", func() {
		d := openTestDBG()
		ctx := context.Background()
		alice := newSenderG(d, "goals_pub_a")
		bob := newSenderG(d, "goals_pub_b")
		cleanupGoalsG(d, ctx, alice.Sender())
		cleanupGoalsG(d, ctx, bob.Sender())

		_, err := goals.CreateGoal(d, ctx, alice.Sender(), "alice-pub", nil, json.RawMessage(plantedSpec), true)
		Expect(err).NotTo(HaveOccurred())
		_, err = goals.CreateGoal(d, ctx, bob.Sender(), "bob-pub", nil, json.RawMessage(plantedSpec), true)
		Expect(err).NotTo(HaveOccurred())

		aList, err := goals.ListPublicGoals(d, ctx, alice.Sender())
		Expect(err).NotTo(HaveOccurred())
		Expect(aList).To(HaveLen(1))
		Expect(aList[0].Name).To(Equal("alice-pub"))

		_, err = goals.ListPublicGoals(d, ctx, "")
		Expect(err).To(HaveOccurred())
	})

	ginkgo.It("exact-set toggle with opposite value performs a REAL flip (not just an idempotent set)", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_exactset")
		cleanupGoalsG(d, ctx, fx.Sender())

		g, err := goals.CreateGoal(d, ctx, fx.Sender(), "flipme", nil, json.RawMessage(plantedSpec), false)
		Expect(err).NotTo(HaveOccurred())
		Expect(g.Enabled).To(BeTrue())

		desiredFalse := false
		newEnabled, found, err := goals.ToggleGoal(d, ctx, fx.Sender(), g.ID, &desiredFalse)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(newEnabled).To(BeFalse())
		after, _ := goals.GetGoal(d, ctx, fx.Sender(), g.ID)
		Expect(after.Enabled).To(BeFalse())

		desiredTrue := true
		newEnabled, found, err = goals.ToggleGoal(d, ctx, fx.Sender(), g.ID, &desiredTrue)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(newEnabled).To(BeTrue())
		after, _ = goals.GetGoal(d, ctx, fx.Sender(), g.ID)
		Expect(after.Enabled).To(BeTrue())
	})

	ginkgo.It("InvalidateGoalsForOwner: empty-owner call is a no-op that doesn't touch other owners", func() {
		d := openTestDBG()
		ctx := context.Background()
		empty := newSenderG(d, "goals_inv_empty")
		other := newSenderG(d, "goals_inv_other")
		cleanupGoalsG(d, ctx, empty.Sender())
		cleanupGoalsG(d, ctx, other.Sender())

		og, _ := goals.CreateGoal(d, ctx, other.Sender(), "og", nil, json.RawMessage(plantedSpec), false)
		Expect(og).NotTo(BeNil())
		planted := json.RawMessage(`{"hit":true,"progress":1,"sub_conditions":[]}`)
		Expect(goals.UpdateGoalProgress(d, ctx, other.Sender(), og.ID, planted)).To(Succeed())

		Expect(goals.InvalidateGoalsForOwner(d, ctx, empty.Sender())).To(Succeed())
		after, _ := goals.GetGoal(d, ctx, other.Sender(), og.ID)
		Expect(len(after.LastProgress)).To(BeNumerically(">", 0))
		Expect(after.LastEvaluatedAt).NotTo(BeNil())
	})

	ginkgo.It("UpdateGoalProgress(nil) explicitly clears last_progress + last_evaluated_at", func() {
		d := openTestDBG()
		ctx := context.Background()
		fx := newSenderG(d, "goals_progclear")
		cleanupGoalsG(d, ctx, fx.Sender())

		g, err := goals.CreateGoal(d, ctx, fx.Sender(), "clr", nil, json.RawMessage(plantedSpec), false)
		Expect(err).NotTo(HaveOccurred())
		planted := json.RawMessage(`{"hit":false,"progress":0.1,"sub_conditions":[]}`)
		Expect(goals.UpdateGoalProgress(d, ctx, fx.Sender(), g.ID, planted)).To(Succeed())
		mid, _ := goals.GetGoal(d, ctx, fx.Sender(), g.ID)
		Expect(len(mid.LastProgress)).To(BeNumerically(">", 0))
		Expect(mid.LastEvaluatedAt).NotTo(BeNil())

		Expect(goals.UpdateGoalProgress(d, ctx, fx.Sender(), g.ID, nil)).To(Succeed())
		after, _ := goals.GetGoal(d, ctx, fx.Sender(), g.ID)
		Expect(after).NotTo(BeNil())
		Expect(after.LastProgress).To(HaveLen(0))
		Expect(after.LastEvaluatedAt).To(BeNil())
	})
})

const plantedSpec = `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":3600,"window":"week"}`

func semanticGoalsDiff(a, b string) string {
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		return "left is not valid JSON: " + err.Error()
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		return "right is not valid JSON: " + err.Error()
	}
	an, _ := json.Marshal(av)
	bn, _ := json.Marshal(bv)
	if string(an) != string(bn) {
		return "normalized forms differ"
	}
	return ""
}
