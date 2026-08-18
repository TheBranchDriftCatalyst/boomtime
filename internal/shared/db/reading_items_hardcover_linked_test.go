package db

import (
	"context"
	"testing"
)

// reading_items_hardcover_linked_test.go — pins ListOwnerHardcoverLinkedBookIDs,
// the dedupe input for the inbound Hardcover shelf-ingest: it returns the
// hardcover_book_ids linked to a NON-hardcover reading_item (a matched
// Kindle/Audible row) and DELIBERATELY excludes source='hardcover' rows (so the
// ingest keeps re-upserting its own rows) and unlinked rows.
func TestListOwnerHardcoverLinkedBookIDs(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("hclinked")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM reading_items WHERE owner=$1`, owner) })

	// A kindle row matched to book 100 and an audible row matched to book 200 — both
	// must appear in the linked set.
	if err := d.UpsertReadingItem(ctx, ReadingItem{Owner: owner, Source: "kindle", ExternalID: "B0K100", Title: "K"}); err != nil {
		t.Fatalf("upsert kindle: %v", err)
	}
	if err := d.SetReadingItemHardcoverLink(ctx, owner, "kindle", "B0K100", 100, 0, "asin", ""); err != nil {
		t.Fatalf("link kindle: %v", err)
	}
	if err := d.UpsertReadingItem(ctx, ReadingItem{Owner: owner, Source: "audible", ExternalID: "B0A200", Title: "A"}); err != nil {
		t.Fatalf("upsert audible: %v", err)
	}
	if err := d.SetReadingItemHardcoverLink(ctx, owner, "audible", "B0A200", 200, 0, "asin", ""); err != nil {
		t.Fatalf("link audible: %v", err)
	}

	// A source='hardcover' row linked to book 300 — must be EXCLUDED (its own linkage
	// must not make the ingest treat it as "already covered by an external match").
	if err := d.UpsertReadingItem(ctx, ReadingItem{Owner: owner, Source: "hardcover", ExternalID: "300", Title: "H"}); err != nil {
		t.Fatalf("upsert hardcover: %v", err)
	}
	if err := d.SetReadingItemHardcoverLink(ctx, owner, "hardcover", "300", 300, 0, "hardcover", ""); err != nil {
		t.Fatalf("link hardcover: %v", err)
	}

	// A kindle row with NO Hardcover link — must be EXCLUDED (nothing to dedupe on).
	if err := d.UpsertReadingItem(ctx, ReadingItem{Owner: owner, Source: "kindle", ExternalID: "B0KNONE", Title: "Unlinked"}); err != nil {
		t.Fatalf("upsert unlinked: %v", err)
	}

	set, err := d.ListOwnerHardcoverLinkedBookIDs(ctx, owner)
	if err != nil {
		t.Fatalf("ListOwnerHardcoverLinkedBookIDs: %v", err)
	}
	if len(set) != 2 || !set[100] || !set[200] {
		t.Fatalf("linked set = %v, want {100,200}", set)
	}
	if set[300] {
		t.Fatal("source='hardcover' book 300 must be EXCLUDED from the linked set")
	}
}
