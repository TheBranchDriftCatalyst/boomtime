// reading_items_work_test.go — pins ListReadingItemsForWork: the Book detail
// panel loads every edition of one canonical Work, keyed by hardcover_book_id
// when matched and falling back to amazon_asin for unmatched siblings.
package db

import (
	"context"
	"testing"
)

func TestListReadingItemsForWork(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("work")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	seed := func(source, ext, asin string) {
		if err := d.UpsertReadingItem(ctx, ReadingItem{
			Owner: owner, Source: source, ExternalID: ext,
			Title: "Work Title", Authors: "A. Author", Status: "read",
			AmazonASIN: asin,
		}); err != nil {
			t.Fatalf("seed %s/%s: %v", source, ext, err)
		}
	}
	// Two editions of ONE Work (share hardcover_book_id AND an amazon_asin) + an
	// unrelated third book.
	seed("audible", "WB0AUD01", "SHARED_ASIN")
	seed("kindle", "WB0KIN01", "SHARED_ASIN")
	seed("audible", "WB0OTHER9", "OTHER_ASIN")

	work := int64(777001)
	if _, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET hardcover_book_id=$2
		  WHERE owner=$1 AND external_id IN ('WB0AUD01','WB0KIN01')`, owner, work); err != nil {
		t.Fatalf("link work: %v", err)
	}

	// Matched: collapse by hardcover_book_id (the unrelated book excluded).
	got, err := d.ListReadingItemsForWork(ctx, owner, &work, "")
	if err != nil {
		t.Fatalf("by book_id: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("by book_id: want 2 editions, got %d", len(got))
	}

	// amazon_asin fallback also collapses the siblings.
	gotAsin, err := d.ListReadingItemsForWork(ctx, owner, nil, "SHARED_ASIN")
	if err != nil {
		t.Fatalf("by asin: %v", err)
	}
	if len(gotAsin) != 2 {
		t.Fatalf("by asin: want 2 editions sharing the asin, got %d", len(gotAsin))
	}

	// Neither identity → nothing.
	none, err := d.ListReadingItemsForWork(ctx, owner, nil, "")
	if err != nil || none != nil {
		t.Fatalf("no identity: want (nil,nil), got (%v,%v)", none, err)
	}
}
