// reading_items_test.go — pins the ListReadingItems SELECT/Scan alignment
// (gaka-qic0). A column-list ↔ Scan-dest mismatch is a runtime pgx error, not a
// compile error, so this seeds a row (incl. the isbn/amazon_asin metadata AND
// the migration-00063 hardcover_* linkage) and asserts every field round-trips.
// Also pins the honest pre-match reality: a row with NULL hardcover columns
// reads back with nil pointers (the "not matched" state the UI renders).
package db

import (
	"context"
	"testing"
	"time"
)

func TestListReadingItems_ScansAllFields(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("reading")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	// A MATCHED row: seed base metadata via Upsert, then set the hardcover_*
	// linkage the match sync would write.
	matchedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "audible", ExternalID: "B0MATCH01",
		Title: "Dune", Authors: "Frank Herbert", Status: "read",
		ProgressPercent: 100, Finished: true,
		ISBN: "9780441172719", AmazonASIN: "B0PRINT456",
	}); err != nil {
		t.Fatalf("upsert matched: %v", err)
	}
	bookID := int64(123456)
	if _, err := d.Pool.Exec(ctx,
		`UPDATE reading_items
		    SET hardcover_book_id=$3, hardcover_status='read', hardcover_matched_at=$4
		  WHERE owner=$1 AND source='audible' AND external_id=$2`,
		owner, "B0MATCH01", bookID, matchedAt); err != nil {
		t.Fatalf("set hardcover linkage: %v", err)
	}

	// An UNMATCHED row: hardcover_* stay NULL (the pre-sync reality).
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "audible", ExternalID: "B0UNMATCH2",
		Title: "Anathem", Authors: "Neal Stephenson", Status: "reading",
		ProgressPercent: 20, ISBN: "", AmazonASIN: "B0AUDIO789",
	}); err != nil {
		t.Fatalf("upsert unmatched: %v", err)
	}

	items, err := d.ListReadingItems(ctx, owner, "")
	if err != nil {
		t.Fatalf("ListReadingItems: %v", err) // a SELECT/Scan misalignment surfaces here
	}

	byID := map[string]ReadingItem{}
	for _, it := range items {
		byID[it.ExternalID] = it
	}
	if len(byID) != 2 {
		t.Fatalf("want 2 rows, got %d", len(byID))
	}

	m := byID["B0MATCH01"]
	if m.ISBN != "9780441172719" || m.AmazonASIN != "B0PRINT456" {
		t.Fatalf("matched isbn/asin scanned wrong: %q / %q", m.ISBN, m.AmazonASIN)
	}
	if m.HardcoverBookID == nil || *m.HardcoverBookID != bookID {
		t.Fatalf("HardcoverBookID = %v, want %d", m.HardcoverBookID, bookID)
	}
	if m.HardcoverStatus == nil || *m.HardcoverStatus != "read" {
		t.Fatalf("HardcoverStatus = %v, want read", m.HardcoverStatus)
	}
	if m.HardcoverMatchedAt == nil || !m.HardcoverMatchedAt.Equal(matchedAt) {
		t.Fatalf("HardcoverMatchedAt = %v, want %v", m.HardcoverMatchedAt, matchedAt)
	}

	u := byID["B0UNMATCH2"]
	if u.HardcoverBookID != nil || u.HardcoverStatus != nil || u.HardcoverMatchedAt != nil {
		t.Fatalf("unmatched row should have nil hardcover_* pointers, got %v/%v/%v",
			u.HardcoverBookID, u.HardcoverStatus, u.HardcoverMatchedAt)
	}
	// A never-matched row reads back with a nil slug (the honest "no deep-link
	// target yet" state — the FE falls back to a search).
	if u.HardcoverSlug != nil {
		t.Fatalf("unmatched row should have nil HardcoverSlug, got %v", u.HardcoverSlug)
	}
	// A never-pushed row reads back with nil edition + nil pushed-progress.
	if u.HardcoverEditionID != nil || u.HardcoverPushedProgress != nil {
		t.Fatalf("unpushed row should have nil edition/pushed_progress, got %v/%v",
			u.HardcoverEditionID, u.HardcoverPushedProgress)
	}
	if u.AmazonASIN != "B0AUDIO789" {
		t.Fatalf("unmatched amazon_asin = %q", u.AmazonASIN)
	}
}

// TestSetReadingItemHardcoverLink_PersistsSlug pins the deep-link fix (gaka-qic0):
// the linkage writer persists hardcover_slug (round-trips through ListReadingItems),
// and a later link with an EMPTY slug is COALESCE-guarded so it never blanks a good
// slug written earlier — while a non-empty slug DOES overwrite.
func TestSetReadingItemHardcoverLink_PersistsSlug(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("hclinkslug")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0SLUG01",
		Title: "The Way of Kings", Authors: "Brandon Sanderson", Status: "reading",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	read := func() ReadingItem {
		items, err := d.ListReadingItems(ctx, owner, "kindle")
		if err != nil {
			t.Fatalf("ListReadingItems: %v", err)
		}
		for _, it := range items {
			if it.ExternalID == "B0SLUG01" {
				return it
			}
		}
		t.Fatal("row not found")
		return ReadingItem{}
	}

	// First link carries a slug — it must persist.
	if err := d.SetReadingItemHardcoverLink(ctx, owner, "kindle", "B0SLUG01", 4242, 0, "asin", "the-way-of-kings"); err != nil {
		t.Fatalf("link with slug: %v", err)
	}
	if got := read(); got.HardcoverSlug == nil || *got.HardcoverSlug != "the-way-of-kings" {
		t.Fatalf("HardcoverSlug = %v, want the-way-of-kings", got.HardcoverSlug)
	}

	// A re-link with an EMPTY slug (e.g. a fuzzy path that carried none) must NOT
	// clobber the good slug — COALESCE guard.
	if err := d.SetReadingItemHardcoverLink(ctx, owner, "kindle", "B0SLUG01", 4242, 0, "asin", ""); err != nil {
		t.Fatalf("re-link empty slug: %v", err)
	}
	if got := read(); got.HardcoverSlug == nil || *got.HardcoverSlug != "the-way-of-kings" {
		t.Fatalf("empty-slug re-link clobbered slug: got %v, want the-way-of-kings", got.HardcoverSlug)
	}

	// A non-empty slug DOES overwrite.
	if err := d.SetReadingItemHardcoverLink(ctx, owner, "kindle", "B0SLUG01", 4242, 0, "asin", "kings-v2"); err != nil {
		t.Fatalf("re-link new slug: %v", err)
	}
	if got := read(); got.HardcoverSlug == nil || *got.HardcoverSlug != "kings-v2" {
		t.Fatalf("non-empty slug should overwrite: got %v, want kings-v2", got.HardcoverSlug)
	}
}

// TestSetReadingItemPushedProgress pins the continuous-progress dedup cursor
// (migration 00065): the setter writes hardcover_pushed_progress for exactly the
// keyed row and ListReadingItems scans it back; an unrelated row stays NULL.
func TestSetReadingItemPushedProgress(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("pushprog")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "audible", ExternalID: "B0PUSH01",
		Title: "In Progress", Authors: "Author", Status: "reading", ProgressPercent: 42,
	}); err != nil {
		t.Fatalf("upsert target: %v", err)
	}
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "audible", ExternalID: "B0PUSH02",
		Title: "Other", Authors: "Author", Status: "reading", ProgressPercent: 10,
	}); err != nil {
		t.Fatalf("upsert other: %v", err)
	}

	if err := d.SetReadingItemPushedProgress(ctx, owner, "audible", "B0PUSH01", 42); err != nil {
		t.Fatalf("SetReadingItemPushedProgress: %v", err)
	}

	items, err := d.ListReadingItems(ctx, owner, "audible")
	if err != nil {
		t.Fatalf("ListReadingItems: %v", err)
	}
	byID := map[string]ReadingItem{}
	for _, it := range items {
		byID[it.ExternalID] = it
	}
	target := byID["B0PUSH01"]
	if target.HardcoverPushedProgress == nil || *target.HardcoverPushedProgress != 42 {
		t.Fatalf("target pushed_progress = %v, want 42", target.HardcoverPushedProgress)
	}
	if other := byID["B0PUSH02"]; other.HardcoverPushedProgress != nil {
		t.Fatalf("unrelated row pushed_progress = %v, want nil", other.HardcoverPushedProgress)
	}
}

// TestSetReadingItemPushed_AdvancesHardcoverMirror pins the out-of-sync fix: after
// a successful push, SetReadingItemPushed advances BOTH the echo-suppression stamp
// (hardcover_pushed_status) AND the local mirror (hardcover_status) to the pushed
// value — so a book that was 'want' on Hardcover but pushed as 'read' immediately
// reads as synced (hardcover_status == effective), clearing the "want → finished"
// divergence badge without waiting for a pull.
func TestSetReadingItemPushed_AdvancesHardcoverMirror(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("pushmirror")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "audible", ExternalID: "B0MIRROR1",
		Title: "Project Hail Mary", Authors: "Andy Weir", Status: "read",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Simulate the pre-push state: matched, last-pulled Hardcover shelf = 'want'
	// (diverged from the effective 'read').
	if _, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET hardcover_book_id=54321, hardcover_status='want'
		  WHERE owner=$1 AND source='audible' AND external_id='B0MIRROR1'`, owner); err != nil {
		t.Fatalf("seed diverged state: %v", err)
	}

	if err := d.SetReadingItemPushed(ctx, owner, "audible", "B0MIRROR1", "read"); err != nil {
		t.Fatalf("SetReadingItemPushed: %v", err)
	}

	items, err := d.ListReadingItems(ctx, owner, "audible")
	if err != nil {
		t.Fatalf("ListReadingItems: %v", err)
	}
	var it *ReadingItem
	for i := range items {
		if items[i].ExternalID == "B0MIRROR1" {
			it = &items[i]
		}
	}
	if it == nil {
		t.Fatalf("row not found")
	}
	if it.HardcoverStatus == nil || *it.HardcoverStatus != "read" {
		t.Errorf("hardcover_status (mirror) = %v, want 'read' — must advance so the divergence badge clears", it.HardcoverStatus)
	}
	// hardcover_pushed_status isn't in the List projection — read it directly.
	var pushed *string
	if err := d.Pool.QueryRow(ctx,
		`SELECT hardcover_pushed_status FROM reading_items
		  WHERE owner=$1 AND source='audible' AND external_id='B0MIRROR1'`, owner).Scan(&pushed); err != nil {
		t.Fatalf("read pushed_status: %v", err)
	}
	if pushed == nil || *pushed != "read" {
		t.Errorf("hardcover_pushed_status (echo stamp) = %v, want 'read'", pushed)
	}
}

// TestListDivergedHardcoverItems returns ONLY matched rows whose effective status
// differs from the last-pulled Hardcover shelf — the bulk outbound work-list.
func TestListDivergedHardcoverItems(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("diverged")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	// seed helper: a matched row with a given hardcover_status + effective status.
	seed := func(ext, effStatus, hcStatus string, matched bool) {
		if err := d.UpsertReadingItem(ctx, ReadingItem{
			Owner: owner, Source: "kindle", ExternalID: ext,
			Title: ext, Authors: "A", Status: effStatus,
		}); err != nil {
			t.Fatalf("upsert %s: %v", ext, err)
		}
		bookSQL := "NULL"
		if matched {
			bookSQL = "42"
		}
		if _, err := d.Pool.Exec(ctx,
			"UPDATE reading_items SET hardcover_book_id="+bookSQL+", hardcover_status=$2, hardcover_edition_id=99 WHERE owner=$1 AND source='kindle' AND external_id=$3",
			owner, hcStatus, ext); err != nil {
			t.Fatalf("seed %s: %v", ext, err)
		}
	}
	seed("DIV1", "read", "want", true)  // matched + diverged  → included
	seed("SYNC1", "read", "read", true) // matched + in sync   → excluded
	seed("UNM1", "read", "want", false) // diverged but UNMATCHED → excluded
	// matched but hardcover_status NULL (never pulled) → excluded (no known remote).
	if err := d.UpsertReadingItem(ctx, ReadingItem{Owner: owner, Source: "kindle", ExternalID: "NULLHC", Title: "n", Authors: "A", Status: "read"}); err != nil {
		t.Fatalf("upsert NULLHC: %v", err)
	}
	if _, err := d.Pool.Exec(ctx, "UPDATE reading_items SET hardcover_book_id=7 WHERE owner=$1 AND source='kindle' AND external_id='NULLHC'", owner); err != nil {
		t.Fatalf("seed NULLHC: %v", err)
	}

	got, err := d.ListDivergedHardcoverItems(ctx, owner)
	if err != nil {
		t.Fatalf("ListDivergedHardcoverItems: %v", err)
	}
	if len(got) != 1 || got[0].ExternalID != "DIV1" {
		t.Fatalf("want exactly [DIV1], got %+v", got)
	}
	if got[0].EffectiveStatus != "read" || got[0].HardcoverBookID != 42 || got[0].HardcoverEditID != 99 {
		t.Errorf("DIV1 payload = %+v, want status=read book=42 edition=99", got[0])
	}
}
