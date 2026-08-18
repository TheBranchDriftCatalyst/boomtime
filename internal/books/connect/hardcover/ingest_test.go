package hardcover

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// ingest_test.go — pins the INBOUND-ORIGIN ingest (ingestShelfOnlyBooks): a
// Hardcover shelf book with no Kindle/Audible reading_item becomes a first-class
// source='hardcover' library row (cover/author/status carried, read→finished), a
// shelf book a Kindle/Audible row already matches is NOT duplicated, and a re-run
// upserts idempotently (never a second row). Runs against the ephemeral pg harness
// (openSweepDB) with a real DB — no live Hardcover client (ingestShelfOnlyBooks
// touches only the DB).

// ingestFixtureJSON: three shelf entries exercising every ingest branch.
//   - book 700: a READ book that a Kindle purchase ALSO owns (seeded + matched
//     below) → must be SKIPPED (deduped), no source='hardcover' row created.
//   - book 701: a READ physical/library book only on the shelf → created,
//     status='read', finished=true, cover+author carried, finished_at from the read.
//   - book 702: a WANT book only on the shelf → created, status='want', finished=false.
const ingestFixtureJSON = `[
  {"id":1,"book_id":700,"edition_id":10,"status_id":3,"updated_at":"2026-07-01T00:00:00Z",
   "book":{"title":"Owned Ebook","slug":"owned-ebook","image":{"url":"https://img/owned.jpg"},
           "contributions":[{"author":{"name":"E. Author"}}]},
   "user_book_reads":[]},
  {"id":2,"book_id":701,"edition_id":20,"status_id":3,"updated_at":"2026-07-02T00:00:00Z",
   "book":{"title":"Physical Only","slug":"physical-only","image":{"url":"https://img/phys.jpg"},
           "contributions":[{"author":{"name":"P. Writer"}}]},
   "user_book_reads":[{"id":9,"started_at":"2026-06-01","finished_at":"2026-06-20","progress_pages":300}]},
  {"id":3,"book_id":702,"edition_id":30,"status_id":1,"updated_at":"2026-07-03T00:00:00Z",
   "book":{"title":"Wishlist Book","slug":"wishlist-book","image":{"url":""},
           "contributions":[{"author":{"name":"W. Author"}}]},
   "user_book_reads":[]}
]`

func TestIngestShelfOnlyBooks_CreatesDedupesIdempotent(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("ingest_%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() {
		cleanupOwner(d, ctx, owner)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM hardcover_user_shelf WHERE owner=$1`, owner)
	})

	// Seed a Kindle reading_item already MATCHED to Hardcover book 700 — the dedupe
	// target: the shelf entry for 700 must NOT spawn a second (hardcover) row.
	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "kindle", ExternalID: "B0KMATCH", Title: "Owned Ebook", Authors: "E. Author"})
	if err := d.SetReadingItemHardcoverLink(ctx, owner, "kindle", "B0KMATCH", 700, 0, "asin", "owned-ebook"); err != nil {
		t.Fatalf("link kindle row to book 700: %v", err)
	}

	books, err := unmarshalUserBooks(json.RawMessage(ingestFixtureJSON))
	if err != nil {
		t.Fatalf("unmarshalUserBooks: %v", err)
	}

	svc := NewSyncService(d, nil, nil)

	// --- first ingest: creates 701 + 702, skips 700 (kindle already covers it) ---
	created, err := svc.ingestShelfOnlyBooks(ctx, owner, books)
	if err != nil {
		t.Fatalf("ingestShelfOnlyBooks: %v", err)
	}
	if created != 2 {
		t.Fatalf("created = %d, want 2 (701 + 702; 700 deduped)", created)
	}

	hc, err := d.ListReadingItems(ctx, owner, "hardcover")
	if err != nil {
		t.Fatalf("list hardcover rows: %v", err)
	}
	if len(hc) != 2 {
		t.Fatalf("hardcover rows = %d, want 2: %+v", len(hc), hc)
	}
	byExt := map[string]db.ReadingItem{}
	for _, it := range hc {
		byExt[it.ExternalID] = it
	}

	// Book 700 must NOT have a source='hardcover' row (deduped against the kindle match).
	if _, dup := byExt["700"]; dup {
		t.Fatalf("book 700 was duplicated as a source=hardcover row despite the kindle match")
	}

	// Book 701: read physical book → created + finished, cover/author/slug/link carried.
	r701 := byExt["701"]
	if r701.Source != "hardcover" || r701.ExternalID != "701" {
		t.Fatalf("701 identity wrong: %+v", r701)
	}
	if r701.Title != "Physical Only" || r701.Authors != "P. Writer" || r701.CoverURL != "https://img/phys.jpg" {
		t.Fatalf("701 display fields wrong: %+v", r701)
	}
	if r701.Status != "read" || !r701.Finished {
		t.Fatalf("701 status/finished = %q/%v, want read/true", r701.Status, r701.Finished)
	}
	if r701.FinishedAt == nil || !r701.FinishedAt.UTC().Equal(time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("701 finished_at = %v, want 2026-06-20 (from the read)", r701.FinishedAt)
	}
	if r701.HardcoverBookID == nil || *r701.HardcoverBookID != 701 {
		t.Fatalf("701 hardcover_book_id = %v, want 701", r701.HardcoverBookID)
	}
	if r701.HardcoverSlug == nil || *r701.HardcoverSlug != "physical-only" {
		t.Fatalf("701 hardcover_slug = %v, want physical-only", r701.HardcoverSlug)
	}
	if r701.HardcoverMatchedAt == nil {
		t.Fatal("701 hardcover_matched_at was not stamped (inherently Hardcover-linked)")
	}

	// Book 702: want book → created, not finished.
	r702 := byExt["702"]
	if r702.Status != "want" || r702.Finished {
		t.Fatalf("702 status/finished = %q/%v, want want/false", r702.Status, r702.Finished)
	}
	if r702.HardcoverBookID == nil || *r702.HardcoverBookID != 702 {
		t.Fatalf("702 hardcover_book_id = %v, want 702", r702.HardcoverBookID)
	}

	// The kindle row is untouched (still exactly one, still its own source).
	if kn, _ := d.ListReadingItems(ctx, owner, "kindle"); len(kn) != 1 {
		t.Fatalf("kindle rows = %d, want 1 (ingest must not touch it)", len(kn))
	}

	// --- second ingest (re-pull): idempotent — nothing NEWLY created, no dup rows ---
	created2, err := svc.ingestShelfOnlyBooks(ctx, owner, books)
	if err != nil {
		t.Fatalf("second ingestShelfOnlyBooks: %v", err)
	}
	if created2 != 0 {
		t.Fatalf("second run created = %d, want 0 (idempotent re-upsert)", created2)
	}
	if hc2, _ := d.ListReadingItems(ctx, owner, "hardcover"); len(hc2) != 2 {
		t.Fatalf("hardcover rows after re-run = %d, want 2 (upsert, not insert)", len(hc2))
	}
}
