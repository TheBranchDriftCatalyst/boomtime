// reading_items_lists_test.go — pins SetReadingItemListsForBook: a book's
// Hardcover list memberships are written onto ALL editions of the Work (every
// reading_item sharing the hardcover_book_id) and round-trip through the
// projection into ReadingItem.HardcoverLists.
package db

import (
	"context"
	"testing"
)

func TestSetReadingItemListsForBook(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("lists")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	// Two editions of one Work + an unrelated book.
	for _, ext := range []string{"LB0AUD01", "LB0KIN01", "LB0OTHER9"} {
		if err := d.UpsertReadingItem(ctx, ReadingItem{
			Owner: owner, Source: "audible", ExternalID: ext,
			Title: "T", Authors: "A", Status: "read",
		}); err != nil {
			t.Fatalf("seed %s: %v", ext, err)
		}
	}
	work := int64(888002)
	if _, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET hardcover_book_id=$2
		  WHERE owner=$1 AND external_id IN ('LB0AUD01','LB0KIN01')`, owner, work); err != nil {
		t.Fatalf("link work: %v", err)
	}

	if err := d.SetReadingItemListsForBook(ctx, owner, work, []byte(`["Owned","Hard Sci Fi"]`)); err != nil {
		t.Fatalf("set lists: %v", err)
	}

	// Both editions of the Work carry the lists; the unrelated book has none.
	got, err := d.ListReadingItemsForWork(ctx, owner, &work, "")
	if err != nil {
		t.Fatalf("load work: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 editions, got %d", len(got))
	}
	for _, it := range got {
		if string(it.HardcoverLists) != `["Owned", "Hard Sci Fi"]` &&
			string(it.HardcoverLists) != `["Owned","Hard Sci Fi"]` {
			t.Errorf("edition %s lists = %s, want the two", it.ExternalID, it.HardcoverLists)
		}
	}

	// no-op on a zero book id.
	if err := d.SetReadingItemListsForBook(ctx, owner, 0, []byte(`["x"]`)); err != nil {
		t.Errorf("zero bookID should be a no-op, got %v", err)
	}
}
