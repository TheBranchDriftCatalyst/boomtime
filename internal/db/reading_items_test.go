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
	// A never-pushed row reads back with nil edition + nil pushed-progress.
	if u.HardcoverEditionID != nil || u.HardcoverPushedProgress != nil {
		t.Fatalf("unpushed row should have nil edition/pushed_progress, got %v/%v",
			u.HardcoverEditionID, u.HardcoverPushedProgress)
	}
	if u.AmazonASIN != "B0AUDIO789" {
		t.Fatalf("unmatched amazon_asin = %q", u.AmazonASIN)
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
