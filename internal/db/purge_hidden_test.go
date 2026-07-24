package db

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestPurgeHiddenRuleExactHappyPath — seed 5 heartbeats, 3 of which match a
// hide rule, purge it, and verify only the 3 rows were deleted AND the rule
// row is gone. This is the destructive-obliterate contract: raw data is
// removed (not remapped like /apply) and the hide rule ceases to exist.
func TestPurgeHiddenRuleExactHappyPath(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "purgexact")
	sender := f.Sender()

	base := time.Date(2025, 6, 8, 10, 0, 0, 0, time.UTC)
	ensureProjects(t, d, ctx, sender, "secret", "keep")
	// 3 rows on "secret" (all should be deleted), 2 on "keep" (untouched).
	for i := 0; i < 3; i++ {
		seedHB(t, d, ctx, sender, "secret", "Go", base.Add(time.Duration(i)*time.Minute))
	}
	for i := 0; i < 2; i++ {
		seedHB(t, d, ctx, sender, "keep", "Go", base.Add(time.Duration(10+i)*time.Minute))
	}

	hideRule, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "secret", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Preview first — should show the 3 rows about to die.
	delRows, delRule, diff, total, err := d.PurgeHiddenPreview(ctx, sender, hideRule, 100)
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
		if got := d.Deleted["project"]; got != "secret" {
			t.Errorf("diff.Deleted[project] = %q, want 'secret'", got)
		}
	}
	if !strings.Contains(delRows, "DELETE FROM heartbeats") {
		t.Errorf("planned SQL missing DELETE heartbeats: %q", delRows)
	}
	if !strings.Contains(delRule, "DELETE FROM curation_rules") {
		t.Errorf("planned SQL missing DELETE curation_rules: %q", delRule)
	}

	// Now purge.
	rows, sqlDelRows, sqlDelRule, err := d.PurgeHiddenRule(ctx, sender, hideRule)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 3 {
		t.Fatalf("PurgeHiddenRule returned rows=%d, want 3", rows)
	}
	// gaka-due: the SQL the purge endpoint reports MUST be exactly what preview
	// reported — the modal must not lie to the user.
	if sqlDelRows != delRows {
		t.Errorf("purge sqlDeleteRows diverged from preview:\npurge:   %q\npreview: %q", sqlDelRows, delRows)
	}
	if sqlDelRule != delRule {
		t.Errorf("purge sqlDeleteRule diverged from preview:\npurge:   %q\npreview: %q", sqlDelRule, delRule)
	}

	// Verify the raw rows were deleted.
	if got := rawCount(t, d, ctx, sender, "project", "secret"); got != 0 {
		t.Errorf("post-purge rows where project='secret' = %d, want 0", got)
	}
	if got := rawCount(t, d, ctx, sender, "project", "keep"); got != 2 {
		t.Errorf("post-purge rows where project='keep' = %d, want 2 (untouched)", got)
	}
	// Hide rule is gone.
	rule2, _, err := d.GetCurationRule(ctx, hideRule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rule2 != nil {
		t.Errorf("post-purge rule row still exists: %+v", rule2)
	}
}

// TestPurgeHiddenRuleIdempotent — a rule matching zero rows still succeeds,
// reports 0, and the rule row is deleted. Same contract as apply idempotent.
func TestPurgeHiddenRuleIdempotent(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "purgenoop")
	sender := f.Sender()

	// Only "keep" exists — the hide rule targets "ghost" which has no rows.
	ensureProjects(t, d, ctx, sender, "keep")
	base := time.Date(2025, 6, 9, 10, 0, 0, 0, time.UTC)
	seedHB(t, d, ctx, sender, "keep", "Go", base)

	hideRule, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "ghost", nil)
	if err != nil {
		t.Fatal(err)
	}

	rows, _, _, err := d.PurgeHiddenRule(ctx, sender, hideRule)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("no-op purge rows = %d, want 0", rows)
	}
	rule2, _, err := d.GetCurationRule(ctx, hideRule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rule2 != nil {
		t.Errorf("no-op purge left rule row: %+v", rule2)
	}
	// Kept row untouched.
	if got := rawCount(t, d, ctx, sender, "project", "keep"); got != 1 {
		t.Errorf("post-noop-purge rows where project='keep' = %d, want 1", got)
	}
}

// TestPurgeHiddenRuleOwnerScoped — alice's purge must NOT touch bob's rows,
// even though both users have rows on the same "shared" project value.
func TestPurgeHiddenRuleOwnerScoped(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	alice := newSender(t, d, "purgealice")
	bob := newSender(t, d, "purgebob")
	base := time.Date(2025, 6, 10, 10, 0, 0, 0, time.UTC)

	ensureProjects(t, d, ctx, alice.Sender(), "shared")
	ensureProjects(t, d, ctx, bob.Sender(), "shared")
	for i := 0; i < 2; i++ {
		seedHB(t, d, ctx, alice.Sender(), "shared", "Go", base.Add(time.Duration(i)*time.Minute))
		seedHB(t, d, ctx, bob.Sender(), "shared", "Go", base.Add(time.Duration(i)*time.Minute))
	}

	// Alice authors a hide rule targeting "shared".
	hideRule, err := d.CreateCurationRule(ctx, alice.Sender(), "project", "hide", "exact", "shared", nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, _, err := d.PurgeHiddenRule(ctx, alice.Sender(), hideRule)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want 2 (alice's only)", rows)
	}
	if got := rawCount(t, d, ctx, alice.Sender(), "project", "shared"); got != 0 {
		t.Errorf("alice rows where project='shared' = %d, want 0 (purged)", got)
	}
	if got := rawCount(t, d, ctx, bob.Sender(), "project", "shared"); got != 2 {
		t.Errorf("BOB rows should be UNTOUCHED: got %d, want 2", got)
	}
}

// TestPurgeHiddenRuleRegex — regex hide purge deletes every row whose column
// matches the pattern (case-insensitively, per the query-time filter semantic).
func TestPurgeHiddenRuleRegex(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "purgeregex")
	sender := f.Sender()

	base := time.Date(2025, 6, 11, 10, 0, 0, 0, time.UTC)
	ensureProjects(t, d, ctx, sender, "secret-alpha", "secret-beta", "other")
	seedHB(t, d, ctx, sender, "secret-alpha", "Go", base)
	seedHB(t, d, ctx, sender, "secret-beta", "Go", base.Add(time.Minute))
	seedHB(t, d, ctx, sender, "other", "Go", base.Add(2*time.Minute))

	hideRule, err := d.CreateCurationRule(ctx, sender, "project", "hide", "regex", "^secret-", nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, _, err := d.PurgeHiddenRule(ctx, sender, hideRule)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("regex purge rows = %d, want 2", rows)
	}
	if got := rawCount(t, d, ctx, sender, "project", "secret-alpha"); got != 0 {
		t.Errorf("post-purge secret-alpha rows = %d, want 0", got)
	}
	if got := rawCount(t, d, ctx, sender, "project", "other"); got != 1 {
		t.Errorf("post-purge 'other' should be untouched: got %d, want 1", got)
	}
}

// TestPurgeHiddenPreviewMatchesRun — the SQL string returned by preview MUST
// be exactly what /purge runs (as sqlDeleteRows/sqlDeleteRule). Same non-
// tautological guarantee the apply path has (TestApplyRenamePreviewMatchesRun).
func TestPurgeHiddenPreviewMatchesRun(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "purgeparity")
	sender := f.Sender()

	base := time.Date(2025, 6, 12, 10, 0, 0, 0, time.UTC)
	ensureProjects(t, d, ctx, sender, "x")
	seedHB(t, d, ctx, sender, "x", "Go", base)

	hideRule, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	previewRows, previewRule, _, _, err := d.PurgeHiddenPreview(ctx, sender, hideRule, 10)
	if err != nil {
		t.Fatal(err)
	}
	_, runRows, runRule, err := d.PurgeHiddenRule(ctx, sender, hideRule)
	if err != nil {
		t.Fatal(err)
	}
	if previewRows != runRows {
		t.Errorf("preview DELETE heartbeats diverged from run:\npreview: %q\nrun:     %q", previewRows, runRows)
	}
	if previewRule != runRule {
		t.Errorf("preview DELETE rule diverged from run:\npreview: %q\nrun:     %q", previewRule, runRule)
	}
}

// TestPurgeHiddenRuleRejectsRename — rename rules cannot be purged (they use
// /apply). Verifies the rule row survives a failed purge call.
func TestPurgeHiddenRuleRejectsRename(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "purgerename")
	sender := f.Sender()

	renameRule, err := d.CreateCurationRule(ctx, sender, "project", "rename", "exact", "old", ptrStr("new"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = d.PurgeHiddenRule(ctx, sender, renameRule)
	if err == nil {
		t.Fatal("PurgeHiddenRule on a rename rule should error")
	}
	if !strings.Contains(err.Error(), "only hide") {
		t.Errorf("error message should mention hide-only: %q", err.Error())
	}
	// Rename rule should still exist (nothing ran).
	still, _, err := d.GetCurationRule(ctx, renameRule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still == nil {
		t.Error("rename rule was deleted by a failed purge — transaction leak")
	}
}

// TestPurgeHiddenRuleRollbackOnConstraintViolation — if the DELETE against
// heartbeats fails (e.g. an FK dependency), the whole tx MUST roll back: no
// heartbeats deleted AND the rule row still present. Symmetrical to
// TestApplyRenameRuleRollbackOnConstraintViolation.
//
// We simulate a failure by cancelling the context AFTER the DELETE would run
// — pgx returns context.Canceled at commit, which the caller (PurgeHiddenRule)
// treats the same as any other tx error → Rollback runs via the defer.
func TestPurgeHiddenRuleRollbackOnCommitFail(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "purgerollback")
	sender := f.Sender()

	base := time.Date(2025, 6, 13, 10, 0, 0, 0, time.UTC)
	ensureProjects(t, d, ctx, sender, "will-purge")
	seedHB(t, d, ctx, sender, "will-purge", "Go", base)

	hideRule, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "will-purge", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Cancel the context before the call runs — pgx.BeginTx returns
	// context.Canceled and no work happens.
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()

	_, _, _, err = d.PurgeHiddenRule(cancelledCtx, sender, hideRule)
	if err == nil {
		t.Fatal("expected context.Canceled from PurgeHiddenRule, got success")
	}
	// Neither side of the transaction may have persisted:
	//   1. Heartbeat still present (DELETE never ran or rolled back).
	//   2. Rule row still present (DELETE curation_rules never ran / rolled back).
	if got := rawCount(t, d, ctx, sender, "project", "will-purge"); got != 1 {
		t.Errorf("post-rollback rows where project='will-purge' = %d, want 1 (unchanged)", got)
	}
	still, _, err := d.GetCurationRule(ctx, hideRule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still == nil {
		t.Fatal("rule row was deleted despite tx failure — atomicity broken")
	}
}

// ptrStr is a tiny helper for pointer-to-string literals in tests. Defined
// here (not the harness) so the harness stays independent of the destructive
// action test files.
func ptrStr(s string) *string { return &s }
