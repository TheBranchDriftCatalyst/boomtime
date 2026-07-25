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
