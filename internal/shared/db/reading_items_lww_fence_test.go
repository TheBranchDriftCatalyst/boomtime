// reading_items_lww_fence_test.go — regression pins for the two cross-job defects
// the 2026-09-06 audit found in the books sync (epic boom-l827):
//
//	boom-m5kq  UpdateHardcoverLinkFromPull's echo suppression was value-based and
//	           therefore PERMANENT — once we had pushed status X, a genuine later
//	           Hardcover edit back to X could never be adopted, and the outbound
//	           diverged push then reversed it.
//	boom-o6q5  The Kindle library sync wrote its structural percentageRead=0 through
//	           the derived layer as want/finished=false, re-arming the transition-only
//	           finish promotion so every insights run re-clobbered the user's status
//	           override back to 'read'.
//
// Both are only visible when a SEQUENCE of writers is traced (push → pull → push,
// sync → insights → curate → sync → insights); the per-writer tests around them all
// pass either way, which is why they survived. Runs against the ephemeral pg harness.
package db

import (
	"context"
	"testing"
	"time"
)

// seedLinkedItem creates one owner-scoped kindle reading_item linked to a distinct
// Hardcover book id, with the given derived status.
func seedLinkedItem(t *testing.T, d *DB, ctx context.Context, owner, externalID string, bookID int64, derived string) {
	t.Helper()
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: externalID,
		Title: "Endurance", Authors: "Alfred Lansing", Status: derived, ProgressPercent: 30,
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}
	if _, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET hardcover_book_id=$3
		  WHERE owner=$1 AND source='kindle' AND external_id=$2`, owner, externalID, bookID); err != nil {
		t.Fatalf("seed link: %v", err)
	}
}

// stampPush simulates a completed outbound push at pushedAt: the override the user
// chose, the echo-suppression stamp, and the local mirror of Hardcover's shelf —
// exactly the column set SetReadingItemCuration + SetReadingItemPushed leave behind.
func stampPush(t *testing.T, d *DB, ctx context.Context, owner, externalID, status string, pushedAt time.Time) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET
		    status_override         = $3,
		    curation_updated_at     = $4,
		    hardcover_pushed_status = $3,
		    hardcover_pushed_at     = $4,
		    hardcover_status        = $3
		  WHERE owner=$1 AND source='kindle' AND external_id=$2`,
		owner, externalID, status, pushedAt); err != nil {
		t.Fatalf("stamp push: %v", err)
	}
}

// TestHardcoverPull_ReEditBackToPushedValueIsAdopted is the boom-m5kq scenario, end
// to end on the real SQL:
//
//	t0  user sets 'read' locally; the push stamps hardcover_pushed_status='read'
//	t1  user changes the book to 'dnf' ON Hardcover        → must adopt
//	t3  user changes it BACK to 'read' ON Hardcover        → must ALSO adopt
//
// The old value-based gate ("remote status ≠ hardcover_pushed_status") refused t3
// forever, and because hardcover_status was still COALESCE'd to 'read' the row then
// looked diverged ('dnf' local vs 'read' remote) and PushDivergedToHardcover wrote
// the user's real edit back out as 'dnf'. This test fails on that gate.
func TestHardcoverPull_ReEditBackToPushedValueIsAdopted(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("lwwfence")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	const externalID = "B0FENCE001"
	const bookID = int64(90001)
	seedLinkedItem(t, d, ctx, owner, externalID, bookID, "reading")

	// t0 — we pushed 'read'. Well outside the echo-skew window by t1/t3.
	t0 := time.Now().Add(-3 * time.Hour)
	stampPush(t, d, ctx, owner, externalID, "read", t0)

	// t1 — a genuine Hardcover edit to 'dnf' (a value we never pushed).
	t1 := t0.Add(1 * time.Hour)
	if _, err := d.UpdateHardcoverLinkFromPull(ctx, owner, HardcoverUserBookLink{
		BookID: bookID, Status: "dnf", RemoteUpdatedAt: t1,
	}); err != nil {
		t.Fatalf("pull t1: %v", err)
	}
	it, err := d.GetReadingItem(ctx, owner, "kindle", externalID)
	if err != nil {
		t.Fatalf("get after t1: %v", err)
	}
	if it.EffectiveStatus() != "dnf" {
		t.Fatalf("t1: remote 'dnf' not adopted, effective = %q", it.EffectiveStatus())
	}

	// t3 — the user changes it BACK to 'read' on Hardcover. 'read' equals the status
	// we last pushed at t0, but this is two hours LATER than that push: it is a real
	// remote edit, not our echo.
	t3 := t0.Add(2 * time.Hour)
	if _, err := d.UpdateHardcoverLinkFromPull(ctx, owner, HardcoverUserBookLink{
		BookID: bookID, Status: "read", RemoteUpdatedAt: t3,
	}); err != nil {
		t.Fatalf("pull t3: %v", err)
	}
	it, err = d.GetReadingItem(ctx, owner, "kindle", externalID)
	if err != nil {
		t.Fatalf("get after t3: %v", err)
	}
	if it.EffectiveStatus() != "read" {
		t.Fatalf("t3: a genuine later remote edit back to a previously-pushed value was "+
			"suppressed as our own echo — effective = %q, want read", it.EffectiveStatus())
	}
	if it.CurationUpdatedAt == nil || !it.CurationUpdatedAt.UTC().Equal(t3.UTC()) {
		t.Fatalf("t3: curation_updated_at = %v, want the remote time %v", it.CurationUpdatedAt, t3.UTC())
	}

	// …and because the adopt landed, the outbound half sees NO divergence, so the
	// user's edit is not reversed by the push that runs in the same sync.
	diverged, err := d.ListDivergedHardcoverItems(ctx, owner)
	if err != nil {
		t.Fatalf("ListDivergedHardcoverItems: %v", err)
	}
	for _, dv := range diverged {
		if dv.ExternalID == externalID {
			t.Fatalf("row is still 'diverged' after adopting the remote edit — the outbound "+
				"push would overwrite Hardcover with %q", dv.EffectiveStatus)
		}
	}
}

// TestHardcoverPull_EchoStillSuppressed guards the OTHER direction: the fix must not
// have simply deleted echo suppression. A same-valued remote update landing right
// after our own push (inside hardcoverEchoSkew) is our write coming back and must
// not be adopted into the sticky override layer.
func TestHardcoverPull_EchoStillSuppressed(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("lwwecho")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	const externalID = "B0FENCE002"
	const bookID = int64(90002)
	seedLinkedItem(t, d, ctx, owner, externalID, bookID, "reading")

	// We pushed 'read' 10 seconds ago; the user's curation stamp is 10 seconds old.
	pushedAt := time.Now().Add(-10 * time.Second)
	stampPush(t, d, ctx, owner, externalID, "read", pushedAt)

	// Hardcover reports the same status with its own (slightly later) updated_at.
	echo := pushedAt.Add(2 * time.Second)
	if _, err := d.UpdateHardcoverLinkFromPull(ctx, owner, HardcoverUserBookLink{
		BookID: bookID, Status: "read", RemoteUpdatedAt: echo, Rating: fptr(1),
	}); err != nil {
		t.Fatalf("pull echo: %v", err)
	}
	it, err := d.GetReadingItem(ctx, owner, "kindle", externalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if it.CurationUpdatedAt == nil || !it.CurationUpdatedAt.UTC().Equal(pushedAt.UTC()) {
		t.Fatalf("our own echo re-stamped the LWW clock: curation_updated_at = %v, want %v",
			it.CurationUpdatedAt, pushedAt.UTC())
	}
	if it.RatingOverride != nil {
		t.Fatalf("our own echo wrote a rating override: %v", *it.RatingOverride)
	}
}

// TestHardcoverPull_RatingEditWithUnchangedStatusIsAdopted pins the second half of
// boom-m5kq: the rating / finished_at adopt CASEs reused the status-equality echo
// test, so after any push of status X every later Hardcover rating or finish-date
// edit made while the status stayed X was suppressed permanently.
func TestHardcoverPull_RatingEditWithUnchangedStatusIsAdopted(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("lwwrating")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	const externalID = "B0FENCE003"
	const bookID = int64(90003)
	seedLinkedItem(t, d, ctx, owner, externalID, bookID, "read")

	t0 := time.Now().Add(-2 * time.Hour)
	stampPush(t, d, ctx, owner, externalID, "read", t0)

	// An hour later the user rates the book 4.5 and records a finish date on
	// Hardcover. The shelf status never leaves 'read'.
	later := t0.Add(1 * time.Hour)
	fin := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	if _, err := d.UpdateHardcoverLinkFromPull(ctx, owner, HardcoverUserBookLink{
		BookID: bookID, Status: "read", RemoteUpdatedAt: later,
		Rating: fptr(4.5), FinishedAt: &fin,
	}); err != nil {
		t.Fatalf("pull rating edit: %v", err)
	}
	it, err := d.GetReadingItem(ctx, owner, "kindle", externalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if it.RatingOverride == nil || *it.RatingOverride != 4.5 {
		t.Fatalf("a remote rating edit made while the status stayed at the pushed value "+
			"was never adopted: rating_override = %v", it.RatingOverride)
	}
	if it.FinishedAtOverride == nil || !it.FinishedAtOverride.UTC().Equal(fin) {
		t.Fatalf("remote finish date not adopted: %v", it.FinishedAtOverride)
	}
}

// TestHardcoverPull_DifferentStatusInsideSkewWindowIsAdopted pins the OR half of the
// gate: a remote status we never pushed is obviously not our echo, so it is adopted
// immediately — the skew window must not become a blind spot for real edits made
// moments after a push.
func TestHardcoverPull_DifferentStatusInsideSkewWindowIsAdopted(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("lwwskew")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	const externalID = "B0FENCE004"
	const bookID = int64(90004)
	seedLinkedItem(t, d, ctx, owner, externalID, bookID, "reading")

	pushedAt := time.Now().Add(-30 * time.Second)
	stampPush(t, d, ctx, owner, externalID, "read", pushedAt)

	inWindow := pushedAt.Add(5 * time.Second) // well inside hardcoverEchoSkew
	if _, err := d.UpdateHardcoverLinkFromPull(ctx, owner, HardcoverUserBookLink{
		BookID: bookID, Status: "dnf", RemoteUpdatedAt: inWindow,
	}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	it, err := d.GetReadingItem(ctx, owner, "kindle", externalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if it.EffectiveStatus() != "dnf" {
		t.Fatalf("a remote status we never pushed was suppressed inside the skew window: %q",
			it.EffectiveStatus())
	}
}

// TestUpsertReadingItemNoProgress_DoesNotRearmFinishPromotion is the boom-o6q5
// scenario, replayed as the real pipeline runs it (kindle-sync → kindle-insights →
// user curates → kindle-sync → kindle-insights):
//
//  1. the library sync creates the row (want, finished=false)
//  2. the insights run marks it finished — the transition promotes status_override='read'
//  3. the user curates it to 'dnf'
//  4. the NEXT library sync re-reports percentageRead=0 for it (the feed does this for
//     every book, always)
//  5. the next insights run sees the same finish date again
//
// Before the fix step 4 wrote status='want', finished=false straight through the
// derived layer, which made step 5 look like a fresh finish TRANSITION (prev.finished
// = false) whose pre-update effective status ('dnf') was ≠ 'read' — so the promotion
// fired again and rewrote the user's override back to 'read', on every single cycle.
func TestUpsertReadingItemNoProgress_DoesNotRearmFinishPromotion(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("zeroprog")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	const asin = "B0ZERO0001"
	// The zero-progress shape the Cloud Reader feed produces for EVERY book.
	libraryRow := ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: asin,
		Title: "Endurance", Authors: "Alfred Lansing",
		Status: "want", ProgressPercent: 0, Finished: false,
	}

	// (1) first sync — the row does not exist yet, so it is created as want/0/false.
	if err := d.UpsertReadingItemNoProgress(ctx, libraryRow); err != nil {
		t.Fatalf("sync 1: %v", err)
	}

	// (2) insights marks it finished; the transition promotes the override to 'read'.
	finishedAt := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	if _, found, err := d.SetReadingItemFinishedFromInsights(ctx, owner, asin, finishedAt); err != nil || !found {
		t.Fatalf("insights 1: err=%v found=%v", err, found)
	}

	// (3) the user disagrees and curates it to 'dnf'.
	if _, err := d.SetReadingItemCuration(ctx, owner, "kindle", asin, ReadingItemCurationPatch{
		Status: sptr("dnf"), SetStatus: true,
	}); err != nil {
		t.Fatalf("curate: %v", err)
	}

	// (4) the next library sync re-reports the same structural zero.
	if err := d.UpsertReadingItemNoProgress(ctx, libraryRow); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	mid, err := d.GetReadingItem(ctx, owner, "kindle", asin)
	if err != nil {
		t.Fatalf("get after sync 2: %v", err)
	}
	if !mid.Finished || mid.Status != "read" {
		t.Fatalf("the zero-progress sync downgraded the derived layer: status=%q finished=%v "+
			"(this is what re-arms the transition-only finish promotion)", mid.Status, mid.Finished)
	}

	// (5) insights runs again over the same finish.
	if _, _, err := d.SetReadingItemFinishedFromInsights(ctx, owner, asin, finishedAt); err != nil {
		t.Fatalf("insights 2: %v", err)
	}
	got, err := d.GetReadingItem(ctx, owner, "kindle", asin)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.StatusOverride == nil || *got.StatusOverride != "dnf" {
		t.Fatalf("the pipeline re-clobbered the user's curation: status_override = %v, want dnf",
			got.StatusOverride)
	}
	if got.CurationUpdatedAt == nil || !got.CurationUpdatedAt.Equal(*mid.CurationUpdatedAt) {
		t.Fatalf("the LWW clock was re-stamped by ingest (it makes local look newer than any "+
			"later Hardcover edit): %v -> %v", mid.CurationUpdatedAt, got.CurationUpdatedAt)
	}
}

// TestUpsertReadingItemNoProgress_PreservesDerivedLayer pins the narrower contract
// the pipeline test above depends on, including the reconcile case (boom-o6q5's
// sibling): a 'reading' row promoted by the Kindle status-reconcile — with NO
// override protecting it — must survive the next zero-progress library sync, and the
// ordinary UpsertReadingItem must still overwrite (the guard is opt-in, not global).
func TestUpsertReadingItemNoProgress_PreservesDerivedLayer(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("zerokeep")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	const asin = "B0ZERO0002"
	base := ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: asin,
		Title: "Shackleton", Authors: "Anon", Status: "want", ProgressPercent: 0,
	}
	if err := d.UpsertReadingItemNoProgress(ctx, base); err != nil {
		t.Fatalf("create: %v", err)
	}
	// The status-reconcile saw a last-page-read record and promoted the DERIVED
	// status to 'reading'. Nothing in the override layer protects it.
	if flipped, err := d.SetReadingItemReading(ctx, owner, "kindle", asin); err != nil || !flipped {
		t.Fatalf("reconcile: err=%v flipped=%v", err, flipped)
	}

	// A zero-progress sync must not regress it to 'want' — doing so hid the book from
	// the reading-time poller and could push 'want' over the user's Currently Reading
	// shelf entry on Hardcover.
	if err := d.UpsertReadingItemNoProgress(ctx, base); err != nil {
		t.Fatalf("zero sync: %v", err)
	}
	got, err := d.GetReadingItem(ctx, owner, "kindle", asin)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "reading" {
		t.Fatalf("zero-progress sync regressed a reconcile-set row: status = %q, want reading", got.Status)
	}

	// The normal writer still owns the derived layer: a source that DOES know the
	// progress overwrites it as before.
	real := base
	real.Status, real.ProgressPercent, real.Finished = "read", 100, true
	if err := d.UpsertReadingItem(ctx, real); err != nil {
		t.Fatalf("informed sync: %v", err)
	}
	got, err = d.GetReadingItem(ctx, owner, "kindle", asin)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "read" || !got.Finished || got.ProgressPercent != 100 {
		t.Fatalf("UpsertReadingItem stopped writing the derived layer: %+v", got)
	}
}

// TestClearReadingItemListsExcept is the audit's stale-list scenario: book 42 is on
// the "Owned" Hardcover list, so a pull writes hardcover_lists=["Owned"]. The user
// then removes it from EVERY list. listMembershipByBook no longer contains key 42, so
// the pull's write loop never touches the row again — before the clear sweep the chip
// stayed on the book indefinitely.
func TestClearReadingItemListsExcept(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("listclear")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	seedLinkedItem(t, d, ctx, owner, "B0LIST0042", 42, "read")
	seedLinkedItem(t, d, ctx, owner, "B0LIST0043", 43, "read")
	// An UNLINKED row must never be touched by the sweep (it was never listed).
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0LISTNONE", Title: "Unlinked", Status: "want",
	}); err != nil {
		t.Fatalf("seed unlinked: %v", err)
	}

	if err := d.SetReadingItemListsForBook(ctx, owner, 42, []byte(`["Owned"]`)); err != nil {
		t.Fatalf("set lists 42: %v", err)
	}
	if err := d.SetReadingItemListsForBook(ctx, owner, 43, []byte(`["Owned","Art"]`)); err != nil {
		t.Fatalf("set lists 43: %v", err)
	}

	// The next pull's membership map covers 43 only — the user de-listed 42 entirely.
	cleared, err := d.ClearReadingItemListsExcept(ctx, owner, []int64{43})
	if err != nil {
		t.Fatalf("ClearReadingItemListsExcept: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("cleared %d rows, want exactly 1 (only the de-listed book)", cleared)
	}

	rows := map[string]ReadingItem{}
	items, err := d.ListReadingItems(ctx, owner, "kindle")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, it := range items {
		rows[it.ExternalID] = it
	}
	if got := string(rows["B0LIST0042"].HardcoverLists); got != "[]" {
		t.Fatalf("de-listed book still carries stale list names: %s", got)
	}
	if got := string(rows["B0LIST0043"].HardcoverLists); got != `["Owned", "Art"]` && got != `["Owned","Art"]` {
		t.Fatalf("a still-listed book lost its lists: %s", got)
	}
	if rows["B0LISTNONE"].HardcoverLists != nil {
		t.Fatalf("an unlinked row was written: %s", rows["B0LISTNONE"].HardcoverLists)
	}

	// Idempotent: a steady-state pull writes nothing (already-'[]' rows are excluded).
	again, err := d.ClearReadingItemListsExcept(ctx, owner, []int64{43})
	if err != nil {
		t.Fatalf("second clear: %v", err)
	}
	if again != 0 {
		t.Fatalf("steady-state clear rewrote %d rows, want 0", again)
	}

	// An EMPTY keep set (the user deleted all their lists) clears everything listed.
	if n, err := d.ClearReadingItemListsExcept(ctx, owner, nil); err != nil || n != 1 {
		t.Fatalf("empty keep set: n=%d err=%v, want 1", n, err)
	}
}
