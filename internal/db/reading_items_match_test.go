// reading_items_match_test.go — pins the DB seams the explicit `hardcover-match`
// pipeline stage relies on (gaka-books): ListUnmatchedReadingItems returns ONLY
// rows with a NULL hardcover_book_id and at least one matchable identity;
// UpdateReadingItemDisplayMeta backfills a bare row's title/author/cover WITHOUT
// clobbering an already-populated field; and book_sync_state.last_match_at
// (migration 00064) round-trips. Runs against the ephemeral pg harness.
package db

import (
	"context"
	"testing"
	"time"
)

func TestListUnmatchedReadingItems_OnlyNullLinked(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("hcmatch")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	// (1) Unmatched with an ASIN identity — SHOULD appear.
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0KINDLE01",
		AmazonASIN: "B0KINDLE01", Title: "", Authors: "",
	}); err != nil {
		t.Fatalf("upsert bare kindle: %v", err)
	}
	// (2) Unmatched with only a title/isbn (no external_id would be odd, but the
	// title alone makes it matchable) — SHOULD appear.
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "audible", ExternalID: "B0AUDIBLE2",
		Title: "Anathem", Authors: "Neal Stephenson", ISBN: "9780061474095",
	}); err != nil {
		t.Fatalf("upsert audible: %v", err)
	}
	// (3) ALREADY matched (hardcover_book_id set) — must NOT appear.
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0MATCHED3",
		Title: "Dune", Authors: "Frank Herbert",
	}); err != nil {
		t.Fatalf("upsert matched: %v", err)
	}
	if err := d.SetReadingItemHardcoverLink(ctx, owner, "kindle", "B0MATCHED3", 999, 0, "asin", "dune"); err != nil {
		t.Fatalf("link matched: %v", err)
	}

	// A DIFFERENT owner's unmatched row — must NOT leak across the owner filter.
	other := mkSender("hcother")
	cleanupSender(t, d, ctx, other)
	ensureUser(t, d, ctx, other)
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: other, Source: "kindle", ExternalID: "B0OTHER999", Title: "Foreign",
	}); err != nil {
		t.Fatalf("upsert other owner: %v", err)
	}

	got, err := d.ListUnmatchedReadingItems(ctx, owner)
	if err != nil {
		t.Fatalf("ListUnmatchedReadingItems: %v", err)
	}
	ids := map[string]ReadingItem{}
	for _, it := range got {
		ids[it.ExternalID] = it
	}
	if len(ids) != 2 {
		t.Fatalf("want exactly 2 unmatched rows, got %d: %v", len(ids), ids)
	}
	if _, ok := ids["B0MATCHED3"]; ok {
		t.Fatal("already-matched row leaked into the unmatched worklist")
	}
	if _, ok := ids["B0OTHER999"]; ok {
		t.Fatal("another owner's row leaked into the worklist")
	}
	// The projected fields the ladder needs must round-trip.
	a := ids["B0AUDIBLE2"]
	if a.Source != "audible" || a.ISBN != "9780061474095" || a.Title != "Anathem" || a.Authors != "Neal Stephenson" {
		t.Fatalf("projected fields wrong: %+v", a)
	}
	if ids["B0KINDLE01"].AmazonASIN != "B0KINDLE01" {
		t.Fatalf("kindle amazon_asin projection wrong: %+v", ids["B0KINDLE01"])
	}
}

func TestUpdateReadingItemDisplayMeta_BackfillsOnlyEmpty(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("hcmeta")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	// A bare Kindle row (title/authors/cover blank).
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0BARE0001",
	}); err != nil {
		t.Fatalf("upsert bare: %v", err)
	}
	n, err := d.UpdateReadingItemDisplayMeta(ctx, owner, "kindle", "B0BARE0001",
		"Piranesi", "Susanna Clarke", "https://img/piranesi.jpg")
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 1 {
		t.Fatalf("rows updated = %d, want 1", n)
	}
	items, _ := d.ListReadingItems(ctx, owner, "kindle")
	if len(items) != 1 || items[0].Title != "Piranesi" || items[0].Authors != "Susanna Clarke" || items[0].CoverURL != "https://img/piranesi.jpg" {
		t.Fatalf("backfill did not land: %+v", items)
	}

	// A SECOND backfill with different values must NOT clobber the now-populated
	// fields (only-when-empty guard).
	if _, err := d.UpdateReadingItemDisplayMeta(ctx, owner, "kindle", "B0BARE0001",
		"WRONG TITLE", "WRONG AUTHOR", "https://wrong.jpg"); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	items, _ = d.ListReadingItems(ctx, owner, "kindle")
	if items[0].Title != "Piranesi" || items[0].Authors != "Susanna Clarke" || items[0].CoverURL != "https://img/piranesi.jpg" {
		t.Fatalf("second backfill clobbered populated fields: %+v", items[0])
	}
}

func TestBookSyncState_LastMatchAtRoundTrips(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("hcstate")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	// Fresh state → nil cursor.
	st, err := d.GetBookSyncState(ctx, owner, "hardcover")
	if err != nil {
		t.Fatalf("get fresh: %v", err)
	}
	if st.LastMatchAt != nil {
		t.Fatalf("fresh last_match_at should be nil, got %v", st.LastMatchAt)
	}

	// Set it via the migration-00064 column.
	matchAt := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	if err := d.SetBookSyncState(ctx, BookSyncState{
		Owner: owner, Source: "hardcover", LastMatchAt: &matchAt,
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	st, err = d.GetBookSyncState(ctx, owner, "hardcover")
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if st.LastMatchAt == nil || !st.LastMatchAt.UTC().Equal(matchAt) {
		t.Fatalf("last_match_at = %v, want %v", st.LastMatchAt, matchAt)
	}

	// A subsequent upsert with a nil LastMatchAt must PRESERVE it (COALESCE guard).
	if err := d.SetBookSyncState(ctx, BookSyncState{Owner: owner, Source: "hardcover"}); err != nil {
		t.Fatalf("preserve upsert: %v", err)
	}
	st, _ = d.GetBookSyncState(ctx, owner, "hardcover")
	if st.LastMatchAt == nil || !st.LastMatchAt.UTC().Equal(matchAt) {
		t.Fatalf("nil upsert blanked last_match_at, got %v", st.LastMatchAt)
	}
}
