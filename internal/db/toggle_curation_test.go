package db

import (
	"context"
	"testing"
	"time"
)

// gaka-dfd: tests for the enable/disable flag on curation_rules.
//
// Contract:
//   - Rules default to enabled=true (schema default; CreateCurationRule inherits it).
//   - ToggleCurationRule flips the flag; SetCurationRuleEnabled writes an exact value.
//   - Both are owner-scoped: a wrong-sender toggle reports found=false and never
//     touches the row.
//   - Both are idempotent (a set-to-current-value still returns found=true).
//   - LoadHiddenSets / LoadRenameSets skip disabled rules (their query-time effect
//     is paused while the row survives for the UI).
//   - ListCurationRules still returns disabled rules so the UI can surface them.

func TestToggleCurationRuleHappyPath(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "toggle_hp")
	sender := f.Sender()

	ensureProjects(t, d, ctx, sender, "keep")
	id := createRename(t, d, ctx, sender, "project", "keep", "kept")

	// Newly-created rule is enabled.
	rule, _, err := d.GetCurationRule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !rule.Enabled {
		t.Fatal("newly-created rule should be enabled=true")
	}

	// Flip once — should now be false.
	got, found, err := d.ToggleCurationRule(ctx, sender, id)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("ToggleCurationRule reported not-found on an owned rule")
	}
	if got {
		t.Fatal("first toggle should have returned enabled=false")
	}

	// Flip again — back to true.
	got, found, err = d.ToggleCurationRule(ctx, sender, id)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("second toggle reported not-found")
	}
	if !got {
		t.Fatal("second toggle should have returned enabled=true")
	}
}

func TestToggleCurationRuleOwnerScoped(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Two isolated fixtures so the wrong-sender toggle can't collide with a
	// rule the wrong sender legitimately owns.
	fA := newSender(t, d, "toggle_ownerA")
	fB := newSender(t, d, "toggle_ownerB")
	senderA, senderB := fA.Sender(), fB.Sender()

	ensureProjects(t, d, ctx, senderA, "aProj")
	id := createRename(t, d, ctx, senderA, "project", "aProj", "aProjRenamed")

	// Wrong sender must not flip A's rule.
	_, found, err := d.ToggleCurationRule(ctx, senderB, id)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("cross-owner toggle must report found=false (never leak existence)")
	}

	// A's rule is still enabled — cross-owner attempt did not sneak through.
	rule, _, err := d.GetCurationRule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !rule.Enabled {
		t.Fatal("cross-owner toggle attempt mutated the rule (owner-scope violation)")
	}
}

func TestSetCurationRuleEnabledIdempotent(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "toggle_idem")
	sender := f.Sender()

	ensureProjects(t, d, ctx, sender, "proj")
	id := createRename(t, d, ctx, sender, "project", "proj", "projRenamed")

	// Set enabled=true when it is already true — should still return found=true.
	found, err := d.SetCurationRuleEnabled(ctx, sender, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("idempotent set-to-current-value must still return found=true")
	}

	// Set enabled=false; verify it stuck.
	found, err = d.SetCurationRuleEnabled(ctx, sender, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("set to a new value must return found=true")
	}
	rule, _, err := d.GetCurationRule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if rule.Enabled {
		t.Fatal("SetCurationRuleEnabled(false) did not persist")
	}

	// Set enabled=false AGAIN — no change, still found.
	found, err = d.SetCurationRuleEnabled(ctx, sender, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("second idempotent set must still return found=true")
	}
}

func TestSetCurationRuleEnabledMissing(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "toggle_missing")
	sender := f.Sender()

	// A very unlikely id — must return found=false, not an error.
	found, err := d.SetCurationRuleEnabled(ctx, sender, 999999999, true)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("missing rule must report found=false")
	}
}

func TestLoadHiddenSetsSkipsDisabled(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "toggle_hide_skip")
	sender := f.Sender()

	// Two hide rules on `project`; disable one; only the enabled one should
	// show up in LoadHiddenSets. Direct SQL to insert the hide rule (we don't
	// have a shared helper).
	ensureProjects(t, d, ctx, sender, "hideA", "hideB")
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO curation_rules (sender, axis, action, match_type, match_value, enabled)
		 VALUES ($1, 'project', 'hide', 'exact', 'hideA', true),
		        ($1, 'project', 'hide', 'exact', 'hideB', false)`, sender); err != nil {
		t.Fatal(err)
	}

	hs, err := d.LoadHiddenSets(ctx, sender)
	if err != nil {
		t.Fatal(err)
	}
	if !hs.AnyHidden() {
		t.Fatal("expected at least one hidden value from the enabled rule")
	}
	// Collect the loaded values (case-lowered in memory).
	// We can't peek the private field directly, so probe via exclusionPredicate
	// shape via the byAxis reader helper: use the ability to enumerate through
	// the raw axis map by relying on LoadHiddenSets writing to hs.byAxis. Use
	// the direct field access (test lives in package db so it's fine).
	got := hs.byAxis["project"]
	if len(got) != 1 {
		t.Fatalf("LoadHiddenSets returned %d project hides, want 1 (disabled rule must be skipped): %v", len(got), got)
	}
	if got[0] != "hidea" {
		t.Fatalf("LoadHiddenSets returned %q, want 'hidea' (values are pre-lowered)", got[0])
	}
}

func TestLoadRenameSetsSkipsDisabled(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "toggle_rename_skip")
	sender := f.Sender()

	ensureProjects(t, d, ctx, sender, "src1", "src2", "dst1", "dst2")
	idOn := createRename(t, d, ctx, sender, "project", "src1", "dst1")
	idOff := createRename(t, d, ctx, sender, "project", "src2", "dst2")

	// Disable the second one.
	if _, err := d.SetCurationRuleEnabled(ctx, sender, idOff, false); err != nil {
		t.Fatal(err)
	}

	rs, err := d.LoadRenameSets(ctx, sender)
	if err != nil {
		t.Fatal(err)
	}
	a := rs.byAxis["project"]
	if len(a.exact) != 1 {
		t.Fatalf("LoadRenameSets returned %d exact project renames, want 1 (disabled rule must be skipped): %v",
			len(a.exact), a.exact)
	}
	if _, ok := a.exact["src1"]; !ok {
		t.Fatalf("expected the ENABLED rename 'src1' to survive; got %v", a.exact)
	}
	if _, ok := a.exact["src2"]; ok {
		t.Fatalf("DISABLED rename 'src2' leaked into RenameSets: %v", a.exact)
	}

	// The rule is still in ListCurationRules — the UI needs it.
	rules, err := d.ListCurationRules(ctx, sender)
	if err != nil {
		t.Fatal(err)
	}
	var seenDisabled bool
	for _, r := range rules {
		if r.ID == idOff {
			seenDisabled = true
			if r.Enabled {
				t.Fatal("disabled rule was returned with enabled=true — struct/scan drift")
			}
		}
		if r.ID == idOn && !r.Enabled {
			t.Fatal("enabled rule reported as disabled — scan drift")
		}
	}
	if !seenDisabled {
		t.Fatal("ListCurationRules must still return disabled rules (UI depends on it)")
	}
}

func TestCreateCurationRuleReEnablesOnUpsert(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "toggle_upsert")
	sender := f.Sender()

	ensureProjects(t, d, ctx, sender, "px", "pxNew", "pxNewer")
	id := createRename(t, d, ctx, sender, "project", "px", "pxNew")

	// Pause it.
	if _, err := d.SetCurationRuleEnabled(ctx, sender, id, false); err != nil {
		t.Fatal(err)
	}

	// Re-add the SAME rule with a new target — the ON CONFLICT upsert must
	// re-enable it (documented behavior: re-adding a rule you paused
	// clearly means "turn it back on").
	newTarget := "pxNewer"
	if _, err := d.CreateCurationRule(ctx, sender, "project", "rename", "exact", "px", &newTarget); err != nil {
		t.Fatal(err)
	}
	rule, _, err := d.GetCurationRule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !rule.Enabled {
		t.Fatal("upsert of a matching key must re-enable a paused rule")
	}
	if rule.NewValue == nil || *rule.NewValue != "pxNewer" {
		t.Fatalf("upsert did not update new_value: got %v", rule.NewValue)
	}
	_ = time.Now() // keep the time import in case timing helpers get added
}
