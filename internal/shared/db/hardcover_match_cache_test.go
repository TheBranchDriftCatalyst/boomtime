// hardcover_match_cache_test.go — pins the GLOBAL match cache seams (gaka-wzgr):
// Put→Lookup round-trips for asin + isbn13 rows independently, a later Put upserts
// (Lookup reflects the new ids), an absent key returns ok=false with a nil error,
// and an editionID of 0 is stored as NULL and read back as 0. Runs against the
// ephemeral pg harness. The cache is process-global (no owner column), so each
// test uses a unique external_id to stay isolated on the shared DB.
package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func mkCacheID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func TestHardcoverMatchCache_RoundTripAsinAndIsbn(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	asinID := mkCacheID("B0ASIN")
	isbnID := mkCacheID("978ISBN")
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM hardcover_match_cache WHERE external_id IN ($1,$2)`, asinID, isbnID)
	})

	// asin row (carries a slug — the deep-link segment must round-trip).
	if err := d.PutHardcoverMatch(ctx, "asin", asinID, 111, 1101, "asin", "the-asin-book"); err != nil {
		t.Fatalf("put asin: %v", err)
	}
	// isbn13 row (independent key space — same-ish id, different id_type).
	if err := d.PutHardcoverMatch(ctx, "isbn13", isbnID, 222, 2202, "isbn13", "the-isbn-book"); err != nil {
		t.Fatalf("put isbn13: %v", err)
	}

	got, ok, err := d.LookupHardcoverMatch(ctx, "asin", asinID)
	if err != nil || !ok {
		t.Fatalf("lookup asin: ok=%v err=%v", ok, err)
	}
	if got.BookID != 111 || got.EditionID != 1101 || got.Method != "asin" || got.Slug != "the-asin-book" {
		t.Fatalf("asin lookup = %+v, want {111 1101 asin the-asin-book}", got)
	}

	got, ok, err = d.LookupHardcoverMatch(ctx, "isbn13", isbnID)
	if err != nil || !ok {
		t.Fatalf("lookup isbn13: ok=%v err=%v", ok, err)
	}
	if got.BookID != 222 || got.EditionID != 2202 || got.Method != "isbn13" || got.Slug != "the-isbn-book" {
		t.Fatalf("isbn13 lookup = %+v, want {222 2202 isbn13 the-isbn-book}", got)
	}

	// The two id_types are independent PKs: an asin lookup of the isbn key misses.
	if _, ok, err := d.LookupHardcoverMatch(ctx, "asin", isbnID); err != nil || ok {
		t.Fatalf("cross-idtype lookup should miss: ok=%v err=%v", ok, err)
	}
}

func TestHardcoverMatchCache_PutUpserts(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	id := mkCacheID("B0UPSERT")
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM hardcover_match_cache WHERE external_id=$1`, id) })

	if err := d.PutHardcoverMatch(ctx, "asin", id, 111, 1101, "asin", "orig-slug"); err != nil {
		t.Fatalf("put initial: %v", err)
	}
	// Re-resolve to new ids with an EMPTY slug — must overwrite the ids but the
	// COALESCE guard must PRESERVE the earlier good slug (an empty slug never
	// blanks a cached one).
	if err := d.PutHardcoverMatch(ctx, "asin", id, 999, 9909, "asin", ""); err != nil {
		t.Fatalf("put upsert: %v", err)
	}

	got, ok, err := d.LookupHardcoverMatch(ctx, "asin", id)
	if err != nil || !ok {
		t.Fatalf("lookup after upsert: ok=%v err=%v", ok, err)
	}
	if got.BookID != 999 || got.EditionID != 9909 {
		t.Fatalf("upsert lookup = %+v, want {999 9909 ...}", got)
	}
	if got.Slug != "orig-slug" {
		t.Fatalf("upsert with empty slug clobbered the cached slug: got %q, want %q", got.Slug, "orig-slug")
	}

	// A non-empty slug on a later Put DOES overwrite.
	if err := d.PutHardcoverMatch(ctx, "asin", id, 999, 9909, "asin", "new-slug"); err != nil {
		t.Fatalf("put upsert with new slug: %v", err)
	}
	got, _, err = d.LookupHardcoverMatch(ctx, "asin", id)
	if err != nil {
		t.Fatalf("lookup after slug overwrite: %v", err)
	}
	if got.Slug != "new-slug" {
		t.Fatalf("non-empty slug should overwrite: got %q, want %q", got.Slug, "new-slug")
	}
}

func TestHardcoverMatchCache_AbsentKeyMisses(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	got, ok, err := d.LookupHardcoverMatch(ctx, "asin", mkCacheID("B0ABSENT"))
	if err != nil {
		t.Fatalf("lookup absent: unexpected err %v", err)
	}
	if ok {
		t.Fatalf("lookup absent: ok=true, want false (got %+v)", got)
	}
}

func TestHardcoverMatchCache_ZeroEditionStoredNull(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	id := mkCacheID("B0NOEDITION")
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM hardcover_match_cache WHERE external_id=$1`, id) })

	// editionID 0 → stored as NULL.
	if err := d.PutHardcoverMatch(ctx, "asin", id, 333, 0, "asin", "zero-edition-book"); err != nil {
		t.Fatalf("put with zero edition: %v", err)
	}

	// The column must actually be NULL (not 0) in the row.
	var isNull bool
	if err := d.Pool.QueryRow(ctx,
		`SELECT hardcover_edition_id IS NULL FROM hardcover_match_cache WHERE id_type='asin' AND external_id=$1`, id).
		Scan(&isNull); err != nil {
		t.Fatalf("check null: %v", err)
	}
	if !isNull {
		t.Fatal("hardcover_edition_id should be NULL when editionID<=0")
	}

	// And Lookup COALESCEs it back to 0.
	got, ok, err := d.LookupHardcoverMatch(ctx, "asin", id)
	if err != nil || !ok {
		t.Fatalf("lookup zero-edition: ok=%v err=%v", ok, err)
	}
	if got.BookID != 333 || got.EditionID != 0 {
		t.Fatalf("zero-edition lookup = %+v, want {333 0 asin}", got)
	}
}
