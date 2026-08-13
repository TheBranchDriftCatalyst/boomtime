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
	if u.AmazonASIN != "B0AUDIO789" {
		t.Fatalf("unmatched amazon_asin = %q", u.AmazonASIN)
	}
}
