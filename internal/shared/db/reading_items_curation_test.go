// reading_items_curation_test.go — pins the curation OVERRIDE layer (migration
// 00069): the two-layer status model (derived vs override, effective = override ??
// derived), the ingest-can't-clobber-an-override invariant, the Amazon-finish
// promotion (idempotent, transition-only), and the Hardcover-pull LWW branch
// (remote-newer adopts / local-newer keeps / echo-suppressed). Runs against the
// ephemeral pg harness.
package db

import (
	"context"
	"testing"
	"time"
)

func fptr(f float64) *float64 { return &f }
func sptr(s string) *string   { return &s }

func TestSetReadingItemCuration_WritesOverridesAndStamp(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("curwrite")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	// A derived-layer row: Amazon says 'reading', no rating, no finish.
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0CUR0001",
		Title: "Dune", Authors: "Frank Herbert", Status: "reading", ProgressPercent: 40,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	fa := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	got, err := d.SetReadingItemCuration(ctx, owner, "kindle", "B0CUR0001", ReadingItemCurationPatch{
		Status: sptr("dnf"), SetStatus: true,
		Rating: fptr(4.5), SetRating: true,
		FinishedAt: &fa, SetFinishedAt: true,
	})
	if err != nil {
		t.Fatalf("SetReadingItemCuration: %v", err)
	}

	// Override cols written; derived cols untouched; stamp set.
	if got.StatusOverride == nil || *got.StatusOverride != "dnf" {
		t.Fatalf("status_override = %v, want dnf", got.StatusOverride)
	}
	if got.Status != "reading" {
		t.Fatalf("derived status = %q, want reading (untouched)", got.Status)
	}
	if got.EffectiveStatus() != "dnf" {
		t.Fatalf("effective status = %q, want dnf", got.EffectiveStatus())
	}
	if got.RatingOverride == nil || *got.RatingOverride != 4.5 {
		t.Fatalf("rating_override = %v, want 4.5", got.RatingOverride)
	}
	if got.FinishedAtOverride == nil || !got.FinishedAtOverride.Equal(fa) {
		t.Fatalf("finished_at_override = %v, want %v", got.FinishedAtOverride, fa)
	}
	if got.CurationUpdatedAt == nil {
		t.Fatalf("curation_updated_at not stamped")
	}

	// Invalid status is rejected before any write.
	if _, err := d.SetReadingItemCuration(ctx, owner, "kindle", "B0CUR0001", ReadingItemCurationPatch{
		Status: sptr("bogus"), SetStatus: true,
	}); err == nil {
		t.Fatalf("expected an error for an invalid curation status")
	}

	// Clearing an override (Set with nil) reverts to the derived layer.
	cleared, err := d.SetReadingItemCuration(ctx, owner, "kindle", "B0CUR0001", ReadingItemCurationPatch{
		Status: nil, SetStatus: true,
	})
	if err != nil {
		t.Fatalf("clear status override: %v", err)
	}
	if cleared.StatusOverride != nil {
		t.Fatalf("status_override = %v, want nil after clear", cleared.StatusOverride)
	}
	if cleared.EffectiveStatus() != "reading" {
		t.Fatalf("effective after clear = %q, want reading (derived)", cleared.EffectiveStatus())
	}
}

// TestUpsertReadingItem_CannotClobberOverride is the structural invariant: ingest
// writes only the derived layer, so a re-sync that changes derived status leaves
// the user's override intact (effective still follows the override).
func TestUpsertReadingItem_CannotClobberOverride(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("curinv")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0INV0001",
		Title: "Book", Authors: "A", Status: "reading", ProgressPercent: 30,
	}); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}

	// User overrides to paused.
	if _, err := d.SetReadingItemCuration(ctx, owner, "kindle", "B0INV0001", ReadingItemCurationPatch{
		Status: sptr("paused"), SetStatus: true,
	}); err != nil {
		t.Fatalf("override: %v", err)
	}

	// Ingest re-syncs: Amazon still reports 'reading' with fresh progress. This
	// upsert MUST NOT touch the override columns.
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0INV0001",
		Title: "Book", Authors: "A", Status: "reading", ProgressPercent: 55,
	}); err != nil {
		t.Fatalf("re-sync upsert: %v", err)
	}

	it, err := d.GetReadingItem(ctx, owner, "kindle", "B0INV0001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if it.StatusOverride == nil || *it.StatusOverride != "paused" {
		t.Fatalf("override clobbered by ingest: status_override = %v, want paused", it.StatusOverride)
	}
	if it.Status != "reading" || it.ProgressPercent != 55 {
		t.Fatalf("derived layer should have re-synced: status=%q pct=%d", it.Status, it.ProgressPercent)
	}
	if it.EffectiveStatus() != "paused" {
		t.Fatalf("effective = %q, want paused", it.EffectiveStatus())
	}
}

// TestMarkReadingItemFinished_PromotesOverrideIdempotent pins the Amazon-finish
// promotion: a genuine finish supersedes a stale non-read override once, on the
// transition, and a re-run does not re-promote (so a later user override sticks).
func TestMarkReadingItemFinished_PromotesOverrideIdempotent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("curfin")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	// A book the user marked DNF while it was still 'reading' on Amazon.
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0FIN0001",
		Title: "Book", Authors: "A", Status: "reading", ProgressPercent: 80,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := d.SetReadingItemCuration(ctx, owner, "kindle", "B0FIN0001", ReadingItemCurationPatch{
		Status: sptr("dnf"), SetStatus: true,
	}); err != nil {
		t.Fatalf("override dnf: %v", err)
	}

	// Amazon genuinely finishes it → promotion to read.
	finAt := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	transitioned, _, found, err := d.MarkReadingItemFinished(ctx, owner, "kindle", "B0FIN0001", finAt)
	if err != nil || !found {
		t.Fatalf("MarkReadingItemFinished: err=%v found=%v", err, found)
	}
	if !transitioned {
		t.Fatalf("expected a finish transition")
	}
	it, _ := d.GetReadingItem(ctx, owner, "kindle", "B0FIN0001")
	if it.StatusOverride == nil || *it.StatusOverride != "read" {
		t.Fatalf("promotion failed: status_override = %v, want read", it.StatusOverride)
	}
	if it.CurationUpdatedAt == nil {
		t.Fatalf("promotion did not stamp curation_updated_at")
	}

	// The user changes their mind AFTER the finish: back to dnf.
	if _, err := d.SetReadingItemCuration(ctx, owner, "kindle", "B0FIN0001", ReadingItemCurationPatch{
		Status: sptr("dnf"), SetStatus: true,
	}); err != nil {
		t.Fatalf("re-override: %v", err)
	}
	// A re-run of the finish sweep must NOT re-promote (prev.finished already true).
	if _, _, _, err := d.MarkReadingItemFinished(ctx, owner, "kindle", "B0FIN0001", finAt); err != nil {
		t.Fatalf("re-run finish: %v", err)
	}
	it, _ = d.GetReadingItem(ctx, owner, "kindle", "B0FIN0001")
	if it.StatusOverride == nil || *it.StatusOverride != "dnf" {
		t.Fatalf("idempotency violated: re-run re-promoted to %v, want the user's dnf", it.StatusOverride)
	}
}

// TestSetReadingItemFinishedFromInsights_PromotesOverride mirrors the promotion on
// the Kindle-insights finish path.
func TestSetReadingItemFinishedFromInsights_PromotesOverride(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("curinsight")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0INS0001",
		Title: "Book", Authors: "A", Status: "reading", ProgressPercent: 90,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := d.SetReadingItemCuration(ctx, owner, "kindle", "B0INS0001", ReadingItemCurationPatch{
		Status: sptr("paused"), SetStatus: true,
	}); err != nil {
		t.Fatalf("override paused: %v", err)
	}

	newlyDated, found, err := d.SetReadingItemFinishedFromInsights(ctx, owner, "B0INS0001",
		time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC))
	if err != nil || !found || !newlyDated {
		t.Fatalf("SetReadingItemFinishedFromInsights: err=%v found=%v newlyDated=%v", err, found, newlyDated)
	}
	it, _ := d.GetReadingItem(ctx, owner, "kindle", "B0INS0001")
	if it.StatusOverride == nil || *it.StatusOverride != "read" {
		t.Fatalf("insights promotion failed: status_override = %v, want read", it.StatusOverride)
	}
}

// TestUpdateHardcoverLinkFromPull_LWW pins the bidirectional last-writer-wins
// branch: remote-newer adopts into the override layer; local-newer keeps; and our
// own push echo is suppressed (never re-adopted as a remote edit).
func TestUpdateHardcoverLinkFromPull_LWW(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("curlww")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	// Each row links to a DISTINCT Hardcover book_id (the match is 1:1 per owner),
	// so a per-book pull touches exactly one reading_item.
	seed := func(externalID string, bookID int64) {
		if err := d.UpsertReadingItem(ctx, ReadingItem{
			Owner: owner, Source: "kindle", ExternalID: externalID,
			Title: "Book", Authors: "A", Status: "reading", ProgressPercent: 20,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if _, err := d.Pool.Exec(ctx,
			`UPDATE reading_items SET hardcover_book_id=$3 WHERE owner=$1 AND source='kindle' AND external_id=$2`,
			owner, externalID, bookID); err != nil {
			t.Fatalf("link: %v", err)
		}
	}

	// --- (a) remote-newer adopts ---------------------------------------------
	const bookID = int64(7788)
	seed("B0LWW0001", bookID)
	// User sets dnf at T1.
	t1 := time.Now().Add(-2 * time.Hour)
	if _, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET status_override='dnf', curation_updated_at=$3
		   WHERE owner=$1 AND source='kindle' AND external_id=$2`, owner, "B0LWW0001", t1); err != nil {
		t.Fatalf("seed override: %v", err)
	}
	// Hardcover genuinely changes to reading at T2 > T1 (not our echo).
	t2 := time.Now().Add(-1 * time.Hour)
	n, err := d.UpdateHardcoverLinkFromPull(ctx, owner, HardcoverUserBookLink{
		BookID: bookID, Status: "reading", RemoteUpdatedAt: t2, Rating: fptr(3),
	})
	if err != nil || n != 1 {
		t.Fatalf("pull adopt: err=%v n=%d", err, n)
	}
	it, _ := d.GetReadingItem(ctx, owner, "kindle", "B0LWW0001")
	if it.StatusOverride == nil || *it.StatusOverride != "reading" {
		t.Fatalf("remote-newer should adopt: status_override = %v, want reading", it.StatusOverride)
	}
	if it.RatingOverride == nil || *it.RatingOverride != 3 {
		t.Fatalf("remote-newer should adopt rating: %v, want 3", it.RatingOverride)
	}
	if it.CurationUpdatedAt == nil || !it.CurationUpdatedAt.UTC().Equal(t2.UTC()) {
		t.Fatalf("curation_updated_at = %v, want the remote time %v", it.CurationUpdatedAt, t2.UTC())
	}

	// --- (b) local-newer keeps ------------------------------------------------
	seed("B0LWW0002", 7789)
	tNew := time.Now()
	if _, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET status_override='dnf', curation_updated_at=$3
		   WHERE owner=$1 AND source='kindle' AND external_id=$2`, owner, "B0LWW0002", tNew); err != nil {
		t.Fatalf("seed override: %v", err)
	}
	// A stale remote change (older than our override) must NOT be adopted.
	tOld := time.Now().Add(-3 * time.Hour)
	if _, err := d.UpdateHardcoverLinkFromPull(ctx, owner, HardcoverUserBookLink{
		BookID: 7789, Status: "reading", RemoteUpdatedAt: tOld,
	}); err != nil {
		t.Fatalf("pull stale: %v", err)
	}
	it, _ = d.GetReadingItem(ctx, owner, "kindle", "B0LWW0002")
	if it.StatusOverride == nil || *it.StatusOverride != "dnf" {
		t.Fatalf("local-newer should keep: status_override = %v, want dnf", it.StatusOverride)
	}

	// --- (c) echo-suppressed --------------------------------------------------
	seed("B0LWW0003", 7790)
	tUser := time.Now().Add(-2 * time.Hour)
	if _, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET status_override='dnf', curation_updated_at=$3,
		        hardcover_pushed_status='dnf', hardcover_pushed_at=now()
		   WHERE owner=$1 AND source='kindle' AND external_id=$2`, owner, "B0LWW0003", tUser); err != nil {
		t.Fatalf("seed pushed: %v", err)
	}
	// Hardcover echoes our own push back (status equals hardcover_pushed_status)
	// with a newer updated_at — it must NOT be adopted as a remote edit.
	tEcho := time.Now()
	if _, err := d.UpdateHardcoverLinkFromPull(ctx, owner, HardcoverUserBookLink{
		BookID: 7790, Status: "dnf", RemoteUpdatedAt: tEcho,
	}); err != nil {
		t.Fatalf("pull echo: %v", err)
	}
	it, _ = d.GetReadingItem(ctx, owner, "kindle", "B0LWW0003")
	if it.CurationUpdatedAt == nil || !it.CurationUpdatedAt.UTC().Equal(tUser.UTC()) {
		t.Fatalf("echo should be suppressed: curation_updated_at = %v, want unchanged %v", it.CurationUpdatedAt, tUser.UTC())
	}
}
