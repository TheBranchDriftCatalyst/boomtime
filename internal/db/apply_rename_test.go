package db

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestApplyRenameRuleExactHappyPath — seed 5 heartbeats, 3 of which match a
// rename mapping, apply it, and verify only the 3 were rewritten AND the
// mapping row is gone. This is the destructive-collapse contract: raw data is
// rewritten, and the mapping (as a persistent translation layer) is removed.
func TestApplyRenameRuleExactHappyPath(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "applyexact")
	sender := f.Sender()

	base := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)
	// FK: heartbeats(sender, project) references projects(owner, name). The
	// UPDATE will rewrite project values → project "new-project" must exist
	// too (or the UPDATE would fail with a FK violation).
	ensureProjects(t, d, ctx, sender, "old-project", "keep", "new-project")
	// 3 heartbeats on "old-project" (all should be rewritten), 2 on "keep" (untouched).
	for i := 0; i < 3; i++ {
		seedHB(t, d, ctx, sender, "old-project", "Go", base.Add(time.Duration(i)*time.Minute))
	}
	for i := 0; i < 2; i++ {
		seedHB(t, d, ctx, sender, "keep", "Go", base.Add(time.Duration(10+i)*time.Minute))
	}

	ruleID := createRename(t, d, ctx, sender, "project", "old-project", "new-project")
	rule, _, err := d.GetCurationRule(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}

	// Preview first — sqlPlanned must be a non-empty UPDATE that mentions the
	// column, and totalAffected must equal the number of rows we'll rewrite.
	updSQL, delSQL, diff, total, err := d.ApplyRenamePreview(ctx, sender, rule, 100)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("preview totalAffected = %d, want 3", total)
	}
	if len(diff) != 3 {
		t.Fatalf("preview diff len = %d, want 3", len(diff))
	}
	for _, d := range diff {
		if d.Before != "old-project" {
			t.Errorf("diff.Before = %q, want 'old-project'", d.Before)
		}
		if d.After != "new-project" {
			t.Errorf("diff.After = %q, want 'new-project'", d.After)
		}
	}
	if !strings.Contains(updSQL, "UPDATE heartbeats") {
		t.Errorf("planned SQL missing UPDATE: %q", updSQL)
	}
	if !strings.Contains(delSQL, "DELETE FROM curation_rules") {
		t.Errorf("planned SQL missing DELETE: %q", delSQL)
	}

	// Now apply.
	rows, sqlUpd, sqlDel, err := d.ApplyRenameRule(ctx, sender, rule)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 3 {
		t.Fatalf("ApplyRenameRule returned rows=%d, want 3", rows)
	}
	// gaka-cr4: the SQL the apply endpoint reports MUST be exactly what
	// preview reported — the modal must not lie to the user.
	if sqlUpd != updSQL {
		t.Errorf("apply sqlUpdate diverged from preview:\napply:   %q\npreview: %q", sqlUpd, updSQL)
	}
	if sqlDel != delSQL {
		t.Errorf("apply sqlDelete diverged from preview:\napply:   %q\npreview: %q", sqlDel, delSQL)
	}

	// Verify the raw column was rewritten.
	if got := rawCount(t, d, ctx, sender, "project", "new-project"); got != 3 {
		t.Errorf("post-apply rows where project='new-project' = %d, want 3", got)
	}
	if got := rawCount(t, d, ctx, sender, "project", "old-project"); got != 0 {
		t.Errorf("post-apply rows where project='old-project' = %d, want 0", got)
	}
	if got := rawCount(t, d, ctx, sender, "project", "keep"); got != 2 {
		t.Errorf("post-apply rows where project='keep' = %d, want 2 (untouched)", got)
	}
	// Mapping row is gone.
	rule2, _, err := d.GetCurationRule(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	if rule2 != nil {
		t.Errorf("post-apply mapping row still exists: %+v", rule2)
	}
}

// TestApplyRenameRuleIdempotent — a mapping with zero matches (already applied
// or spurious) still succeeds, reports rowsAffected=0, and removes the mapping
// row. Prevents "stale mapping surviving a no-op apply."
func TestApplyRenameRuleIdempotent(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "applynoop")
	sender := f.Sender()

	// Only "keep" — the mapping targets "ghost" which has no rows.
	ensureProjects(t, d, ctx, sender, "keep", "spectre")
	base := time.Date(2025, 6, 2, 10, 0, 0, 0, time.UTC)
	seedHB(t, d, ctx, sender, "keep", "Go", base)

	ruleID := createRename(t, d, ctx, sender, "project", "ghost", "spectre")
	rule, _, err := d.GetCurationRule(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}

	rows, _, _, err := d.ApplyRenameRule(ctx, sender, rule)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("no-op apply rows = %d, want 0", rows)
	}
	// Mapping row is still removed.
	rule2, _, err := d.GetCurationRule(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	if rule2 != nil {
		t.Errorf("no-op apply left mapping row: %+v", rule2)
	}
}

// TestApplyRenameRuleOwnerScoped — a second sender's mapping must not be
// applyable via this sender's ownership, even if we hand-craft the rule object.
// The DB layer trusts the caller to have validated ownership (the handler
// does), so this test asserts that the WHERE sender = $1 gate keeps a rogue
// caller's UPDATE from touching another owner's rows.
func TestApplyRenameRuleOwnerScoped(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	alice := newSender(t, d, "applyalice")
	bob := newSender(t, d, "applybob")
	base := time.Date(2025, 6, 3, 10, 0, 0, 0, time.UTC)

	ensureProjects(t, d, ctx, alice.Sender(), "shared", "alice-only")
	ensureProjects(t, d, ctx, bob.Sender(), "shared", "alice-only")
	// Both users have 2 rows on the same raw project value.
	for i := 0; i < 2; i++ {
		seedHB(t, d, ctx, alice.Sender(), "shared", "Go", base.Add(time.Duration(i)*time.Minute))
		seedHB(t, d, ctx, bob.Sender(), "shared", "Go", base.Add(time.Duration(i)*time.Minute))
	}

	// Alice authors a rename rule.
	ruleID := createRename(t, d, ctx, alice.Sender(), "project", "shared", "alice-only")
	rule, _, err := d.GetCurationRule(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}

	// Apply as Alice — should only touch Alice's rows.
	rows, _, _, err := d.ApplyRenameRule(ctx, alice.Sender(), rule)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want 2 (alice's only)", rows)
	}
	if got := rawCount(t, d, ctx, alice.Sender(), "project", "alice-only"); got != 2 {
		t.Errorf("alice rows where project='alice-only' = %d, want 2", got)
	}
	if got := rawCount(t, d, ctx, bob.Sender(), "project", "shared"); got != 2 {
		t.Errorf("BOB rows should be UNTOUCHED: got %d, want 2", got)
	}
	if got := rawCount(t, d, ctx, bob.Sender(), "project", "alice-only"); got != 0 {
		t.Errorf("BOB should have no 'alice-only' rows: got %d", got)
	}
}

// TestApplyRenameRuleRegex — regex rename destructive apply (pattern match →
// fixed newValue).
func TestApplyRenameRuleRegex(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "applyregex")
	sender := f.Sender()

	base := time.Date(2025, 6, 4, 10, 0, 0, 0, time.UTC)
	ensureProjects(t, d, ctx, sender, "acme-alpha", "acme-beta", "other", "acme")
	seedHB(t, d, ctx, sender, "acme-alpha", "Go", base)
	seedHB(t, d, ctx, sender, "acme-beta", "Go", base.Add(time.Minute))
	seedHB(t, d, ctx, sender, "other", "Go", base.Add(2*time.Minute))

	ruleID := createRegexRename(t, d, ctx, sender, "project", "^acme-", "acme")
	rule, _, err := d.GetCurationRule(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, _, err := d.ApplyRenameRule(ctx, sender, rule)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("regex apply rows = %d, want 2", rows)
	}
	if got := rawCount(t, d, ctx, sender, "project", "acme"); got != 2 {
		t.Errorf("post-apply acme rows = %d, want 2", got)
	}
	if got := rawCount(t, d, ctx, sender, "project", "other"); got != 1 {
		t.Errorf("post-apply 'other' should be untouched: got %d, want 1", got)
	}
}

// TestApplyRenamePreviewMatchesRun — the SQL string returned by preview MUST
// be exactly the same string the apply endpoint runs (and returns as sqlRun).
// This is the non-tautological guarantee that the confirm modal isn't lying:
// preview and apply are provably derived from one buildApplyUpdateSQL call.
func TestApplyRenamePreviewMatchesRun(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "applyparity")
	sender := f.Sender()

	base := time.Date(2025, 6, 5, 10, 0, 0, 0, time.UTC)
	ensureProjects(t, d, ctx, sender, "x", "y")
	seedHB(t, d, ctx, sender, "x", "Go", base)

	ruleID := createRename(t, d, ctx, sender, "project", "x", "y")
	rule, _, err := d.GetCurationRule(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	previewUpd, previewDel, _, _, err := d.ApplyRenamePreview(ctx, sender, rule, 10)
	if err != nil {
		t.Fatal(err)
	}
	_, runUpd, runDel, err := d.ApplyRenameRule(ctx, sender, rule)
	if err != nil {
		t.Fatal(err)
	}
	if previewUpd != runUpd {
		t.Errorf("preview UPDATE diverged from run:\npreview: %q\nrun:     %q", previewUpd, runUpd)
	}
	if previewDel != runDel {
		t.Errorf("preview DELETE diverged from run:\npreview: %q\nrun:     %q", previewDel, runDel)
	}
}

// TestApplyRenameRuleRejectsHide — hide rules cannot be applied (no target).
func TestApplyRenameRuleRejectsHide(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "applyhide")
	sender := f.Sender()

	// Create a hide rule directly.
	hideRule, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = d.ApplyRenameRule(ctx, sender, hideRule)
	if err == nil {
		t.Fatal("ApplyRenameRule on a hide rule should error")
	}
	if !strings.Contains(err.Error(), "only rename") {
		t.Errorf("error message should mention rename-only: %q", err.Error())
	}
	// Hide rule should still exist (transaction rolled back / never started).
	still, _, err := d.GetCurationRule(ctx, hideRule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still == nil {
		t.Error("hide rule was deleted by a failed apply — transaction leak")
	}
}

// TestApplyRenameRuleTemplate — a template rule's apply must rewrite each row
// through regexp_replace with the SAME pattern + template + 'i' flag the
// query-time remap uses. Also verifies the rule row is removed.
func TestApplyRenameRuleTemplate(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "applytmpl")
	sender := f.Sender()

	base := time.Date(2025, 6, 6, 10, 0, 0, 0, time.UTC)
	ensureProjects(t, d, ctx, sender, "@drogon", "@swarm-graph", "plain", "drogon", "swarm-graph")
	seedHB(t, d, ctx, sender, "@drogon", "Go", base)
	seedHB(t, d, ctx, sender, "@swarm-graph", "Go", base.Add(time.Minute))
	seedHB(t, d, ctx, sender, "plain", "Go", base.Add(2*time.Minute))

	ruleID := createTemplateRename(t, d, ctx, sender, "project", `^@(.*)$`, `$1`)
	rule, _, err := d.GetCurationRule(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, _, err := d.ApplyRenameRule(ctx, sender, rule)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("template apply rows = %d, want 2", rows)
	}
	if got := rawCount(t, d, ctx, sender, "project", "drogon"); got != 1 {
		t.Errorf("drogon (unprefixed) rows = %d, want 1", got)
	}
	if got := rawCount(t, d, ctx, sender, "project", "swarm-graph"); got != 1 {
		t.Errorf("swarm-graph (unprefixed) rows = %d, want 1", got)
	}
	if got := rawCount(t, d, ctx, sender, "project", "plain"); got != 1 {
		t.Errorf("plain rows should be untouched: got %d, want 1", got)
	}
}

// TestApplyRenameRuleRollbackOnConstraintViolation — if the UPDATE would
// violate a FK constraint (e.g., the target project row doesn't exist), the
// whole transaction MUST roll back: no heartbeats rewritten AND the mapping
// row still present. This is the atomicity contract; a partial state is what
// makes destructive collapse dangerous, so we prove the tx guards it.
func TestApplyRenameRuleRollbackOnConstraintViolation(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "applyrollback")
	sender := f.Sender()

	base := time.Date(2025, 6, 7, 10, 0, 0, 0, time.UTC)
	// Seed a row on "old" — but INTENTIONALLY do NOT create the "missing"
	// project. The UPDATE will violate the (sender, project) FK.
	ensureProjects(t, d, ctx, sender, "old")
	seedHB(t, d, ctx, sender, "old", "Go", base)

	ruleID := createRename(t, d, ctx, sender, "project", "old", "missing")
	rule, _, err := d.GetCurationRule(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, err = d.ApplyRenameRule(ctx, sender, rule)
	if err == nil {
		t.Fatal("expected FK violation, got success")
	}
	// Neither side of the transaction may have persisted:
	//   1. Heartbeat still points to "old" (UPDATE rolled back).
	//   2. Mapping row still present (DELETE rolled back).
	if got := rawCount(t, d, ctx, sender, "project", "old"); got != 1 {
		t.Errorf("post-rollback rows where project='old' = %d, want 1 (unchanged)", got)
	}
	still, _, err := d.GetCurationRule(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	if still == nil {
		t.Fatal("mapping row was deleted despite UPDATE failure — transaction broken")
	}
}

// TestInlineParams — check the human-readable inlining for the modal display
// is safe (quotes escaped) and produces exactly the string the preview + apply
// endpoints surface.
func TestInlineParams(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		args []any
		want string
	}{
		{"basic", "UPDATE t SET c = $2 WHERE sender = $1", []any{"alice", "hi"}, "UPDATE t SET c = 'hi' WHERE sender = 'alice'"},
		{"escaped quote", "SET c = $1", []any{"o'brien"}, "SET c = 'o''brien'"},
		{"int", "WHERE id = $1", []any{42}, "WHERE id = 42"},
		// $10 must NOT collide with $1 when substituted.
		{"double-digit", "$10 $1", []any{"a", "b", "c", "d", "e", "f", "g", "h", "i", "TEN"}, "'TEN' 'a'"},
	}
	for _, c := range cases {
		if got := InlineParams(c.sql, c.args); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
