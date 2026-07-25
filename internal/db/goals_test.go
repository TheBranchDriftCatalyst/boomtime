// goals_test.go — schema + accessor tests for the goals table (gaka-wpb).
//
// The non-tautological anchors here are:
//
//   - JSONB spec ROUND-TRIP (semantic): what we write is what we read back,
//     even after a PATCH-and-reread cycle. Guards against a future storage
//     swap (JSONB → normalized rows) that would drop keys or reorder
//     arrays. We compare structurally (json.Marshal of decoded values)
//     rather than byte-identically because Postgres JSONB doesn't preserve
//     object key order.
//
//   - Spec change CLEARS cached progress. If UpdateGoal ever forgets to
//     null last_progress on a spec write, the read path would serve stale
//     numbers under a new definition. A hand-written cache row is planted,
//     the spec is PATCHed, and we assert the cache is empty afterwards.
//
//   - InvalidateGoalsForOwner nulls every goal's cache in one shot — the
//     hook the ingest path calls. Test with two goals so we don't accept
//     a "cleared one" false positive.
//
//   - Owner scoping: alice can never see, edit, delete, or toggle bob's
//     goals — GetGoal / UpdateGoal / DeleteGoal / ToggleGoal all return
//     the not-found sentinel for a cross-owner id.
package db

import (
	"context"
	"encoding/json"
	"testing"
)

// cleanupGoals wipes every goal row for `sender`. Kept local so the
// package-wide deleteSenderRows doesn't need to know about goals (the
// harness deletes users CASCADE, but running deleteSenderRows during a
// test that shares a DB with parallel binaries would racily nuke other
// packages' rows — we explicitly clean up here).
func cleanupGoals(t *testing.T, d *DB, ctx context.Context, sender string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM goals WHERE owner=$1`, sender)
	})
}

// planted returns a plausible spec JSON for a "one hour a week on Go" leaf
// goal. Kept as a raw string so keys stay literal and any accidental
// reformat pass would show up in a diff.
const plantedSpec = `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":3600,"window":"week"}`

// TestGoalsCRUDAndSpecRoundtrip covers the happy path end-to-end + the
// non-tautological round-trip anchor. Any storage-shape regression will
// surface as a semanticJSONDiff mismatch here.
func TestGoalsCRUDAndSpecRoundtrip(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	fx := newSender(t, d, "goals_crud")
	cleanupGoals(t, d, ctx, fx.Sender())

	// Create.
	desc := "one hour weekly on Go"
	g, err := d.CreateGoal(ctx, fx.Sender(), "weekly-go", &desc, json.RawMessage(plantedSpec))
	if err != nil || g == nil {
		t.Fatalf("CreateGoal: %v goal=%v", err, g)
	}
	if g.Owner != fx.Sender() || g.Name != "weekly-go" || g.Description == nil || *g.Description != desc {
		t.Fatalf("created row mismatch: %+v", g)
	}
	if !g.Enabled {
		t.Errorf("expected enabled=true default, got false")
	}
	if diff := semanticGoalsDiff(plantedSpec, string(g.Spec)); diff != "" {
		t.Errorf("spec round-trip on create: %s\n  sent: %s\n   got: %s", diff, plantedSpec, string(g.Spec))
	}

	// Get by id — should match.
	got, err := d.GetGoal(ctx, fx.Sender(), g.ID)
	if err != nil || got == nil {
		t.Fatalf("GetGoal: %v got=%v", err, got)
	}
	if diff := semanticGoalsDiff(plantedSpec, string(got.Spec)); diff != "" {
		t.Errorf("spec round-trip on read-back: %s", diff)
	}

	// List — should contain our goal.
	list, err := d.ListGoals(ctx, fx.Sender())
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list count = %d, want 1", len(list))
	}
	if list[0].ID != g.ID {
		t.Errorf("list[0].ID = %s, want %s", list[0].ID, g.ID)
	}

	// Update the description; spec MUST stay intact through the PATCH.
	newDesc := "at least 60 minutes / week on Go"
	updated, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{Description: &newDesc})
	if err != nil || updated == nil {
		t.Fatalf("UpdateGoal(description): %v updated=%v", err, updated)
	}
	if updated.Description == nil || *updated.Description != newDesc {
		t.Fatalf("description not updated: %+v", updated.Description)
	}
	if diff := semanticGoalsDiff(plantedSpec, string(updated.Spec)); diff != "" {
		t.Errorf("spec drifted after non-spec PATCH: %s", diff)
	}

	// Delete.
	ok, err := d.DeleteGoal(ctx, fx.Sender(), g.ID)
	if err != nil || !ok {
		t.Fatalf("DeleteGoal: %v ok=%v", err, ok)
	}
	// Re-delete is a no-op returning false — never 404 the caller.
	ok, err = d.DeleteGoal(ctx, fx.Sender(), g.ID)
	if err != nil || ok {
		t.Fatalf("re-delete idempotency: err=%v ok=%v (want ok=false)", err, ok)
	}
}

// TestGoalsUpdateSpecClearsProgress is the load-bearing invariant for the
// cache freshness policy: writing a new spec MUST null out last_progress
// so the next read recomputes under the new definition.
func TestGoalsUpdateSpecClearsProgress(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	fx := newSender(t, d, "goals_specclear")
	cleanupGoals(t, d, ctx, fx.Sender())

	g, err := d.CreateGoal(ctx, fx.Sender(), "clear-me", nil, json.RawMessage(plantedSpec))
	if err != nil || g == nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	// Plant a cache row so we can prove the PATCH nukes it.
	planted := json.RawMessage(`{"hit":true,"progress":1,"sub_conditions":[]}`)
	if err := d.UpdateGoalProgress(ctx, fx.Sender(), g.ID, planted); err != nil {
		t.Fatalf("UpdateGoalProgress: %v", err)
	}
	before, err := d.GetGoal(ctx, fx.Sender(), g.ID)
	if err != nil || before == nil {
		t.Fatalf("GetGoal pre-patch: %v", err)
	}
	if len(before.LastProgress) == 0 || before.LastEvaluatedAt == nil {
		t.Fatalf("cache didn't land: last_progress=%s last_evaluated_at=%v",
			string(before.LastProgress), before.LastEvaluatedAt)
	}

	// PATCH the spec — this must clear the cache columns atomically.
	newSpec := json.RawMessage(`{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":7200,"window":"week"}`)
	after, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{Spec: &newSpec})
	if err != nil || after == nil {
		t.Fatalf("UpdateGoal(spec): %v", err)
	}
	if len(after.LastProgress) != 0 {
		t.Errorf("spec PATCH did NOT clear last_progress: %s", string(after.LastProgress))
	}
	if after.LastEvaluatedAt != nil {
		t.Errorf("spec PATCH did NOT clear last_evaluated_at: %v", *after.LastEvaluatedAt)
	}
}

// TestGoalsInvalidateForOwner covers the hook the ingest path calls:
// after a heartbeat arrives we clear cached progress for every goal the
// owner has. Two goals in play so we don't accept a "cleared one" false
// positive.
func TestGoalsInvalidateForOwner(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	fx := newSender(t, d, "goals_invalidate")
	cleanupGoals(t, d, ctx, fx.Sender())

	// Two goals, both with planted cache rows.
	g1, _ := d.CreateGoal(ctx, fx.Sender(), "g1", nil, json.RawMessage(plantedSpec))
	g2, _ := d.CreateGoal(ctx, fx.Sender(), "g2", nil, json.RawMessage(plantedSpec))
	if g1 == nil || g2 == nil {
		t.Fatalf("create goals: g1=%v g2=%v", g1, g2)
	}
	planted := json.RawMessage(`{"hit":false,"progress":0.5,"sub_conditions":[]}`)
	if err := d.UpdateGoalProgress(ctx, fx.Sender(), g1.ID, planted); err != nil {
		t.Fatalf("plant g1: %v", err)
	}
	if err := d.UpdateGoalProgress(ctx, fx.Sender(), g2.ID, planted); err != nil {
		t.Fatalf("plant g2: %v", err)
	}

	// Also plant a goal for a DIFFERENT owner to make sure invalidate is
	// scoped (this owner's goal survives the sweep).
	other := newSender(t, d, "goals_invalidate_other")
	cleanupGoals(t, d, ctx, other.Sender())
	og, err := d.CreateGoal(ctx, other.Sender(), "og", nil, json.RawMessage(plantedSpec))
	if err != nil || og == nil {
		t.Fatalf("create other-owner goal: %v", err)
	}
	if err := d.UpdateGoalProgress(ctx, other.Sender(), og.ID, planted); err != nil {
		t.Fatalf("plant other-owner cache: %v", err)
	}

	if err := d.InvalidateGoalsForOwner(ctx, fx.Sender()); err != nil {
		t.Fatalf("InvalidateGoalsForOwner: %v", err)
	}

	after1, _ := d.GetGoal(ctx, fx.Sender(), g1.ID)
	after2, _ := d.GetGoal(ctx, fx.Sender(), g2.ID)
	afterO, _ := d.GetGoal(ctx, other.Sender(), og.ID)
	if len(after1.LastProgress) != 0 || after1.LastEvaluatedAt != nil {
		t.Errorf("g1 not invalidated: %s / %v", string(after1.LastProgress), after1.LastEvaluatedAt)
	}
	if len(after2.LastProgress) != 0 || after2.LastEvaluatedAt != nil {
		t.Errorf("g2 not invalidated: %s / %v", string(after2.LastProgress), after2.LastEvaluatedAt)
	}
	if len(afterO.LastProgress) == 0 || afterO.LastEvaluatedAt == nil {
		t.Errorf("other-owner goal wrongly invalidated: %s / %v",
			string(afterO.LastProgress), afterO.LastEvaluatedAt)
	}
}

// TestGoalsOwnerScoping is the no-oracle contract for the DB layer: alice
// cannot Get / Update / Delete / Toggle bob's goal (all return the
// not-found sentinel — nil goal / found=false — never a distinguishable
// error).
func TestGoalsOwnerScoping(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	alice := newSender(t, d, "goals_alice")
	bob := newSender(t, d, "goals_bob")
	cleanupGoals(t, d, ctx, alice.Sender())
	cleanupGoals(t, d, ctx, bob.Sender())

	// Bob's goal.
	bg, err := d.CreateGoal(ctx, bob.Sender(), "bob-goal", nil, json.RawMessage(plantedSpec))
	if err != nil || bg == nil {
		t.Fatalf("CreateGoal(bob): %v", err)
	}

	// Alice tries to Get bob's goal — must be nil, no error.
	got, err := d.GetGoal(ctx, alice.Sender(), bg.ID)
	if err != nil {
		t.Errorf("GetGoal(alice, bob's id): unexpected err %v", err)
	}
	if got != nil {
		t.Errorf("GetGoal(alice, bob's id) leaked: %+v", got)
	}

	// Alice tries to PATCH bob's goal — nil, no error.
	name := "hijacked"
	patched, err := d.UpdateGoal(ctx, alice.Sender(), bg.ID, GoalPatch{Name: &name})
	if err != nil {
		t.Errorf("UpdateGoal(alice, bob's id): unexpected err %v", err)
	}
	if patched != nil {
		t.Errorf("UpdateGoal(alice, bob's id) leaked: %+v", patched)
	}
	// Bob's row must be unchanged.
	still, _ := d.GetGoal(ctx, bob.Sender(), bg.ID)
	if still == nil || still.Name != "bob-goal" {
		t.Errorf("bob's goal name changed: %+v", still)
	}

	// Alice tries to DELETE — ok=false, no error, bob's row survives.
	ok, err := d.DeleteGoal(ctx, alice.Sender(), bg.ID)
	if err != nil || ok {
		t.Errorf("DeleteGoal(alice, bob's id): err=%v ok=%v (want false,nil)", err, ok)
	}
	still, _ = d.GetGoal(ctx, bob.Sender(), bg.ID)
	if still == nil {
		t.Errorf("bob's goal vanished after alice's DELETE attempt")
	}

	// Alice tries to TOGGLE — found=false.
	_, found, err := d.ToggleGoal(ctx, alice.Sender(), bg.ID, nil)
	if err != nil || found {
		t.Errorf("ToggleGoal(alice, bob's id): err=%v found=%v (want false,nil)", err, found)
	}
}

// TestGoalsToggle covers both flip semantics and the exact-set idempotent
// path. Two rapid flips must land on the original value.
func TestGoalsToggle(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	fx := newSender(t, d, "goals_toggle")
	cleanupGoals(t, d, ctx, fx.Sender())

	g, err := d.CreateGoal(ctx, fx.Sender(), "toggle-me", nil, json.RawMessage(plantedSpec))
	if err != nil || g == nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if !g.Enabled {
		t.Fatalf("expected enabled=true default")
	}

	newEnabled, found, err := d.ToggleGoal(ctx, fx.Sender(), g.ID, nil)
	if err != nil || !found || newEnabled {
		t.Fatalf("first flip: err=%v found=%v enabled=%v (want true,true,false)", err, found, newEnabled)
	}
	newEnabled, found, err = d.ToggleGoal(ctx, fx.Sender(), g.ID, nil)
	if err != nil || !found || !newEnabled {
		t.Fatalf("second flip: err=%v found=%v enabled=%v (want true,true,true)", err, found, newEnabled)
	}

	// Exact-set idempotent: setting enabled=true on a row already at true.
	desired := true
	_, found, err = d.ToggleGoal(ctx, fx.Sender(), g.ID, &desired)
	if err != nil || !found {
		t.Errorf("idempotent set: err=%v found=%v (want true,nil)", err, found)
	}
}

// TestGoalsDuplicateName exercises the UNIQUE (owner, name) constraint —
// creating two goals with the same name for one owner must fail, but
// distinct owners with the same name work fine.
func TestGoalsDuplicateName(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	alice := newSender(t, d, "goals_dup_a")
	bob := newSender(t, d, "goals_dup_b")
	cleanupGoals(t, d, ctx, alice.Sender())
	cleanupGoals(t, d, ctx, bob.Sender())

	if _, err := d.CreateGoal(ctx, alice.Sender(), "shared-name", nil, json.RawMessage(plantedSpec)); err != nil {
		t.Fatalf("alice/shared-name first create: %v", err)
	}
	if _, err := d.CreateGoal(ctx, alice.Sender(), "shared-name", nil, json.RawMessage(plantedSpec)); err == nil {
		t.Errorf("alice/shared-name second create: want error, got nil")
	}
	// Different owner with the same name is fine.
	if _, err := d.CreateGoal(ctx, bob.Sender(), "shared-name", nil, json.RawMessage(plantedSpec)); err != nil {
		t.Errorf("bob/shared-name create: %v (want nil — different owner)", err)
	}
}

// TestGoalsUpdateAllPatchFields exercises EVERY branch of the dynamic
// UPDATE builder — name-only, description-only, enabled-only, and
// name+description together — so a subtle regression in one branch
// (wrong column, typo in the SET fragment, missed arg-index bump) is
// caught. TestGoalsCRUDAndSpecRoundtrip only covers Description; the
// other three columns' write paths were uncovered.
//
// Also anchors the updated_at invariant: any non-empty patch MUST bump
// updated_at strictly (>= previous). Comment out the
// `sets = append(sets, "updated_at = now()")` line in UpdateGoal and
// this test fails on the equality check.
func TestGoalsUpdateAllPatchFields(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	fx := newSender(t, d, "goals_patchfields")
	cleanupGoals(t, d, ctx, fx.Sender())

	desc0 := "initial"
	g, err := d.CreateGoal(ctx, fx.Sender(), "orig-name", &desc0, json.RawMessage(plantedSpec))
	if err != nil || g == nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	origUpdatedAt := g.UpdatedAt

	// Name-only PATCH. The updated row's name must change to the exact
	// value we wrote; every OTHER column must be unchanged.
	newName := "renamed-goal"
	g2, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{Name: &newName})
	if err != nil || g2 == nil {
		t.Fatalf("UpdateGoal(name): %v g2=%v", err, g2)
	}
	if g2.Name != newName {
		t.Errorf("name = %q, want %q", g2.Name, newName)
	}
	if g2.Description == nil || *g2.Description != desc0 {
		t.Errorf("description drifted on name-only patch: %v", g2.Description)
	}
	if !g2.Enabled {
		t.Errorf("enabled flipped on name-only patch: got false")
	}
	if !g2.UpdatedAt.After(origUpdatedAt) {
		t.Errorf("updated_at didn't tick on name patch: %v -> %v", origUpdatedAt, g2.UpdatedAt)
	}
	if diff := semanticGoalsDiff(plantedSpec, string(g2.Spec)); diff != "" {
		t.Errorf("spec drifted on name-only patch: %s", diff)
	}

	// Enabled-only PATCH — sets enabled=false. Read-back reflects it and
	// the DB shows the exact boolean we wrote.
	falseVal := false
	g3, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{Enabled: &falseVal})
	if err != nil || g3 == nil {
		t.Fatalf("UpdateGoal(enabled): %v g3=%v", err, g3)
	}
	if g3.Enabled {
		t.Errorf("enabled not written: still true after Enabled=&false patch")
	}
	if g3.Name != newName {
		t.Errorf("name drifted on enabled-only patch: %q", g3.Name)
	}

	// Description-only PATCH re-established (make sure it survives the
	// prior mutations).
	desc2 := "second desc"
	g4, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{Description: &desc2})
	if err != nil || g4 == nil {
		t.Fatalf("UpdateGoal(desc): %v g4=%v", err, g4)
	}
	if g4.Description == nil || *g4.Description != desc2 {
		t.Errorf("description = %v, want %q", g4.Description, desc2)
	}
	if g4.Enabled { // should still be false from prior patch
		t.Errorf("enabled unexpectedly true after desc-only patch")
	}

	// Combined name+description PATCH — two fields at once exercises the
	// arg-index-bump path in the builder. A bug that reused the same
	// $N would send "renamed" to BOTH columns and the assertion below
	// (different values per column) would fail.
	nName := "combined"
	nDesc := "combined desc"
	g5, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{Name: &nName, Description: &nDesc})
	if err != nil || g5 == nil {
		t.Fatalf("UpdateGoal(name+desc): %v g5=%v", err, g5)
	}
	if g5.Name != nName {
		t.Errorf("name = %q, want %q", g5.Name, nName)
	}
	if g5.Description == nil || *g5.Description != nDesc {
		t.Errorf("desc = %v, want %q", g5.Description, nDesc)
	}
}

// TestGoalsUpdateNoOpPatch verifies the empty-patch branch: an
// UpdateGoal with zero non-nil fields returns the CURRENT row unchanged
// (idempotent GET-like behavior). Load-bearing: the branch at
// `len(sets) == 0` returns d.GetGoal — if a future refactor turned
// that into "always emit `updated_at = now()`" (an UPDATE-with-no-SET
// syntax error), this test would fail on either the error or the
// updated_at bump.
func TestGoalsUpdateNoOpPatch(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	fx := newSender(t, d, "goals_noop")
	cleanupGoals(t, d, ctx, fx.Sender())

	desc := "keep me"
	g, err := d.CreateGoal(ctx, fx.Sender(), "noop", &desc, json.RawMessage(plantedSpec))
	if err != nil || g == nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	origUpdatedAt := g.UpdatedAt

	// Zero-field patch — expect the same row back (updated_at NOT bumped).
	back, err := d.UpdateGoal(ctx, fx.Sender(), g.ID, GoalPatch{})
	if err != nil || back == nil {
		t.Fatalf("UpdateGoal(empty): %v back=%v", err, back)
	}
	if back.ID != g.ID {
		t.Errorf("id changed on no-op patch: %s -> %s", g.ID, back.ID)
	}
	if !back.UpdatedAt.Equal(origUpdatedAt) {
		t.Errorf("no-op patch bumped updated_at: %v -> %v (want unchanged)", origUpdatedAt, back.UpdatedAt)
	}
	if back.Description == nil || *back.Description != desc {
		t.Errorf("description drifted on no-op patch: %v", back.Description)
	}
}

// TestGoalsUpdateMissingID pins the not-found sentinel path for
// UpdateGoal / DeleteGoal on an id that doesn't exist AT ALL (as
// opposed to belonging to another user — that's TestGoalsOwnerScoping).
// The sentinel MUST be (nil, nil) for UpdateGoal and (false, nil) for
// DeleteGoal — never a distinguishable error and never a leak.
func TestGoalsUpdateMissingID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	fx := newSender(t, d, "goals_missing")
	cleanupGoals(t, d, ctx, fx.Sender())

	// UUID that doesn't exist. UpdateGoal must return (nil, nil).
	fakeID := "00000000-0000-0000-0000-000000000000"
	nm := "ghost"
	got, err := d.UpdateGoal(ctx, fx.Sender(), fakeID, GoalPatch{Name: &nm})
	if err != nil {
		t.Errorf("UpdateGoal(missing id): unexpected err %v", err)
	}
	if got != nil {
		t.Errorf("UpdateGoal(missing id) returned non-nil: %+v", got)
	}

	// DeleteGoal must return (false, nil).
	ok, err := d.DeleteGoal(ctx, fx.Sender(), fakeID)
	if err != nil || ok {
		t.Errorf("DeleteGoal(missing id): err=%v ok=%v (want false, nil)", err, ok)
	}

	// ToggleGoal (flip and exact-set) must return (_, false, nil).
	_, found, err := d.ToggleGoal(ctx, fx.Sender(), fakeID, nil)
	if err != nil || found {
		t.Errorf("ToggleGoal(missing id, flip): err=%v found=%v (want false, nil)", err, found)
	}
	setTrue := true
	_, found, err = d.ToggleGoal(ctx, fx.Sender(), fakeID, &setTrue)
	if err != nil || found {
		t.Errorf("ToggleGoal(missing id, exact-set): err=%v found=%v (want false, nil)", err, found)
	}

	// GetGoal must return (nil, nil).
	g, err := d.GetGoal(ctx, fx.Sender(), fakeID)
	if err != nil || g != nil {
		t.Errorf("GetGoal(missing id): err=%v g=%v (want nil, nil)", err, g)
	}
}

// TestGoalsListOwnerScoping is the missing owner-scoping check on the
// LIST path (TestGoalsOwnerScoping only covered Get/Update/Delete/
// Toggle). Alice's ListGoals must return ONLY her rows even when bob
// has goals in the same DB — a WHERE-clause typo dropping `owner = $1`
// would cause a cross-owner leak, and this test asserts row counts +
// exact ids to catch that.
func TestGoalsListOwnerScoping(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	alice := newSender(t, d, "goals_list_a")
	bob := newSender(t, d, "goals_list_b")
	cleanupGoals(t, d, ctx, alice.Sender())
	cleanupGoals(t, d, ctx, bob.Sender())

	// Alice: 2 goals.
	a1, _ := d.CreateGoal(ctx, alice.Sender(), "a1", nil, json.RawMessage(plantedSpec))
	a2, _ := d.CreateGoal(ctx, alice.Sender(), "a2", nil, json.RawMessage(plantedSpec))
	// Bob: 3 goals.
	b1, _ := d.CreateGoal(ctx, bob.Sender(), "b1", nil, json.RawMessage(plantedSpec))
	b2, _ := d.CreateGoal(ctx, bob.Sender(), "b2", nil, json.RawMessage(plantedSpec))
	b3, _ := d.CreateGoal(ctx, bob.Sender(), "b3", nil, json.RawMessage(plantedSpec))
	if a1 == nil || a2 == nil || b1 == nil || b2 == nil || b3 == nil {
		t.Fatalf("seed goals failed")
	}

	// Alice's list is exactly {a1, a2}. Bob's is exactly {b1, b2, b3}.
	aList, err := d.ListGoals(ctx, alice.Sender())
	if err != nil {
		t.Fatalf("ListGoals(alice): %v", err)
	}
	if len(aList) != 2 {
		t.Errorf("alice list len = %d, want 2 (owner filter dropped?)", len(aList))
	}
	aIDs := map[string]bool{}
	for _, g := range aList {
		aIDs[g.ID] = true
		if g.Owner != alice.Sender() {
			t.Errorf("alice list contains cross-owner row: owner=%s id=%s", g.Owner, g.ID)
		}
	}
	if !aIDs[a1.ID] || !aIDs[a2.ID] {
		t.Errorf("alice's own goals missing from her list: got ids=%v", aIDs)
	}

	bList, err := d.ListGoals(ctx, bob.Sender())
	if err != nil {
		t.Fatalf("ListGoals(bob): %v", err)
	}
	if len(bList) != 3 {
		t.Errorf("bob list len = %d, want 3", len(bList))
	}
	for _, g := range bList {
		if g.Owner != bob.Sender() {
			t.Errorf("bob list contains cross-owner row: owner=%s id=%s", g.Owner, g.ID)
		}
	}
}

// TestGoalsToggleExactSetOppositeValue plugs a gap in TestGoalsToggle:
// the exact-set path was ONLY tested with the current value (idempotent
// no-flip). That path also handles a REAL flip via desired=false on a
// true row — the RETURNED newEnabled must be the desired value, and a
// subsequent GET must reflect it. A regression that returned
// !currentValue (i.e., flipped anyway) would pass the idempotent test
// but fail this one.
func TestGoalsToggleExactSetOppositeValue(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	fx := newSender(t, d, "goals_exactset")
	cleanupGoals(t, d, ctx, fx.Sender())

	g, err := d.CreateGoal(ctx, fx.Sender(), "flipme", nil, json.RawMessage(plantedSpec))
	if err != nil || g == nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if !g.Enabled {
		t.Fatalf("expected default enabled=true")
	}

	// Exact-set desired=false on a true row.
	desiredFalse := false
	newEnabled, found, err := d.ToggleGoal(ctx, fx.Sender(), g.ID, &desiredFalse)
	if err != nil || !found {
		t.Fatalf("exact-set false on true: err=%v found=%v", err, found)
	}
	if newEnabled {
		t.Errorf("exact-set desired=false returned newEnabled=true (should be false)")
	}
	// GET reflects the write.
	after, _ := d.GetGoal(ctx, fx.Sender(), g.ID)
	if after == nil || after.Enabled {
		t.Errorf("GET after exact-set false: enabled=%v want false", after.Enabled)
	}

	// Exact-set desired=true on the false row (real flip via exact-set).
	desiredTrue := true
	newEnabled, found, err = d.ToggleGoal(ctx, fx.Sender(), g.ID, &desiredTrue)
	if err != nil || !found || !newEnabled {
		t.Fatalf("exact-set true on false: err=%v found=%v newEnabled=%v", err, found, newEnabled)
	}
	after, _ = d.GetGoal(ctx, fx.Sender(), g.ID)
	if after == nil || !after.Enabled {
		t.Errorf("GET after exact-set true: enabled=%v want true", after.Enabled)
	}
}

// TestGoalsInvalidateEmptyOwner confirms InvalidateGoalsForOwner is a
// no-op for an owner with zero goals (called on every heartbeat ingest;
// must not error for a fresh user). Also proves the WHERE guard is
// scoped — planting an OTHER owner's goal + cache and asserting it
// survives an invalidate for the empty owner.
func TestGoalsInvalidateEmptyOwner(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	empty := newSender(t, d, "goals_inv_empty")
	other := newSender(t, d, "goals_inv_other")
	cleanupGoals(t, d, ctx, empty.Sender())
	cleanupGoals(t, d, ctx, other.Sender())

	// Other owner has a goal with a cache row.
	og, _ := d.CreateGoal(ctx, other.Sender(), "og", nil, json.RawMessage(plantedSpec))
	if og == nil {
		t.Fatalf("seed other goal")
	}
	planted := json.RawMessage(`{"hit":true,"progress":1,"sub_conditions":[]}`)
	if err := d.UpdateGoalProgress(ctx, other.Sender(), og.ID, planted); err != nil {
		t.Fatalf("plant other cache: %v", err)
	}

	// Empty owner: no error and no effect.
	if err := d.InvalidateGoalsForOwner(ctx, empty.Sender()); err != nil {
		t.Errorf("InvalidateGoalsForOwner(empty): %v", err)
	}
	// Other owner's cache untouched.
	after, _ := d.GetGoal(ctx, other.Sender(), og.ID)
	if after == nil || len(after.LastProgress) == 0 || after.LastEvaluatedAt == nil {
		t.Errorf("other owner's cache wiped by empty-owner invalidate: progress=%s eval=%v",
			string(after.LastProgress), after.LastEvaluatedAt)
	}
}

// TestGoalsUpdateProgressNilClears exercises the explicit-clear branch
// of UpdateGoalProgress (progress=nil path). Previously only the
// planted-then-invalidated path was tested; the DIRECT nil-arg branch
// was uncovered — a regression that ignored the nil check and stored
// the null JSON literal would pass the existing test but fail here.
func TestGoalsUpdateProgressNilClears(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	fx := newSender(t, d, "goals_progclear")
	cleanupGoals(t, d, ctx, fx.Sender())

	g, err := d.CreateGoal(ctx, fx.Sender(), "clr", nil, json.RawMessage(plantedSpec))
	if err != nil || g == nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	planted := json.RawMessage(`{"hit":false,"progress":0.1,"sub_conditions":[]}`)
	if err := d.UpdateGoalProgress(ctx, fx.Sender(), g.ID, planted); err != nil {
		t.Fatalf("plant: %v", err)
	}
	// Sanity: the plant landed.
	mid, _ := d.GetGoal(ctx, fx.Sender(), g.ID)
	if mid == nil || len(mid.LastProgress) == 0 || mid.LastEvaluatedAt == nil {
		t.Fatalf("plant didn't land: %+v", mid)
	}

	// Clear via nil.
	if err := d.UpdateGoalProgress(ctx, fx.Sender(), g.ID, nil); err != nil {
		t.Fatalf("UpdateGoalProgress(nil): %v", err)
	}
	after, _ := d.GetGoal(ctx, fx.Sender(), g.ID)
	if after == nil {
		t.Fatalf("goal vanished")
	}
	if len(after.LastProgress) != 0 {
		t.Errorf("nil-clear left last_progress: %s", string(after.LastProgress))
	}
	if after.LastEvaluatedAt != nil {
		t.Errorf("nil-clear left last_evaluated_at: %v", *after.LastEvaluatedAt)
	}
}

// semanticGoalsDiff normalizes two JSON documents through json.Marshal so
// the comparison ignores object key order (Postgres JSONB does not
// preserve it) but catches missing keys, changed values, and re-ordered
// arrays. Mirrors semanticJSONDiff in dashboard_layout_test.go — kept
// local to avoid crossing package boundaries.
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
