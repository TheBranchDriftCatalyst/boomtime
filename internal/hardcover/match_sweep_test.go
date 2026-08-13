package hardcover

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// match_sweep_test.go — exercises the explicit `hardcover-match` sweep end-to-end
// against the ephemeral pg harness with a FAKE matcher (no network): an unmatched
// reading_item gets linked, a bare-ASIN Kindle row gets its title/author/cover
// enriched via LookupByASIN, a no-match row is left alone, and last_match_at
// (migration 00064) is stamped. Non-tautological — it asserts the actual DB
// side-effects, so a broken link write / cursor write / enrich path fails here.
//
// It connects directly (db.New + db.MigrateURL) rather than through internal/testutil,
// because testutil transitively imports this package (via handler→identity) and an
// in-package test needs the unexported matchWith seam + fake matcher.

const sweepTestDefaultDSN = "postgres://test:test@localhost:5432/boomtime_test?sslmode=disable"

var (
	sweepDBOnce sync.Once
	sweepDB     *db.DB
	sweepDBErr  error
)

func sweepTestDSN() string {
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return sweepTestDefaultDSN
}

// openSweepDB connects + migrates the isolated test DB once per binary, skipping
// the test when Postgres is unreachable (unless BOOM_REQUIRE_DB=1).
func openSweepDB(t *testing.T) *db.DB {
	t.Helper()
	sweepDBOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := db.MigrateURL(ctx, sweepTestDSN()); err != nil {
			sweepDBErr = fmt.Errorf("migrate: %w", err)
			return
		}
		sweepDB, sweepDBErr = db.New(ctx, sweepTestDSN())
	})
	if sweepDBErr != nil {
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			t.Fatalf("test DB required but unavailable: %v", sweepDBErr)
		}
		t.Skipf("skipping: isolated test DB unavailable: %v", sweepDBErr)
	}
	return sweepDB
}

// fakeMatcher is a scripted matcher: Match returns a hit for asin/title keys in
// hits; LookupByASIN returns metadata for keys in metas.
type fakeMatcher struct {
	hits      map[string]MatchResult // keyed by the ASIN/ISBN/title we expect
	metas     map[string]*BookMeta   // keyed by ASIN
	matchCall int
	lookupErr error
}

func (f *fakeMatcher) Match(_ context.Context, in MatchInput) (MatchResult, error) {
	f.matchCall++
	if r, ok := f.hits[in.ASIN]; ok {
		return r, nil
	}
	if r, ok := f.hits[in.ISBN13]; ok {
		return r, nil
	}
	if r, ok := f.hits[in.Title]; ok {
		return r, nil
	}
	return MatchResult{Method: MatchNone}, nil
}

func (f *fakeMatcher) LookupByASIN(_ context.Context, asin string) (*BookMeta, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	return f.metas[asin], nil
}

func TestMatchWith_LinksEnrichesAndStampsCursor(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("sweep_%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() { cleanupOwner(d, ctx, owner) })

	// Seed three reading_items:
	//   (1) bare-ASIN Kindle row (blank title)  → matches by ASIN + gets enriched
	//   (2) audible row with title/isbn         → matches by ISBN, NOT enriched (has title)
	//   (3) an obscure Kindle row               → no match, left unmatched
	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "kindle", ExternalID: "B0KINDLEAAA", AmazonASIN: "B0KINDLEAAA"})
	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "audible", ExternalID: "B0AUDIBLEBBB", Title: "Anathem", Authors: "Neal Stephenson", ISBN: "9780061474095"})
	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "kindle", ExternalID: "B0NOMATCHCCC", Title: "Obscure Zine"})

	fake := &fakeMatcher{
		hits: map[string]MatchResult{
			"B0KINDLEAAA":   {BookID: 111, EditionID: 1101, Method: MatchByASIN, Confidence: 1},
			"9780061474095": {BookID: 222, EditionID: 2202, Method: MatchByISBN13, Confidence: 1},
		},
		metas: map[string]*BookMeta{
			"B0KINDLEAAA": {BookID: 111, EditionID: 1101, Title: "Project Hail Mary", Authors: "Andy Weir", CoverURL: "https://img/phm.jpg"},
		},
	}

	svc := NewSyncService(d, NewStore(d), nil)
	res, err := svc.matchWith(ctx, owner, fake)
	if err != nil {
		t.Fatalf("matchWith: %v", err)
	}
	if res.Scanned != 3 {
		t.Fatalf("Scanned = %d, want 3", res.Scanned)
	}
	if res.Matched != 2 {
		t.Fatalf("Matched = %d, want 2", res.Matched)
	}
	if res.NoMatch != 1 {
		t.Fatalf("NoMatch = %d, want 1", res.NoMatch)
	}
	if res.Enriched != 1 {
		t.Fatalf("Enriched = %d, want 1 (only the bare kindle row)", res.Enriched)
	}

	// The two matched rows must now carry their hardcover_book_id + drop out of the
	// unmatched worklist, leaving only the no-match row.
	remaining, err := d.ListUnmatchedReadingItems(ctx, owner)
	if err != nil {
		t.Fatalf("list unmatched: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ExternalID != "B0NOMATCHCCC" {
		t.Fatalf("remaining unmatched = %+v, want only B0NOMATCHCCC", remaining)
	}

	// The bare Kindle row's display metadata must have been backfilled.
	items, _ := d.ListReadingItems(ctx, owner, "kindle")
	var kindle db.ReadingItem
	for _, it := range items {
		if it.ExternalID == "B0KINDLEAAA" {
			kindle = it
		}
	}
	if kindle.Title != "Project Hail Mary" || kindle.Authors != "Andy Weir" || kindle.CoverURL != "https://img/phm.jpg" {
		t.Fatalf("kindle row not enriched: %+v", kindle)
	}
	if kindle.HardcoverBookID == nil || *kindle.HardcoverBookID != 111 {
		t.Fatalf("kindle hardcover_book_id = %v, want 111", kindle.HardcoverBookID)
	}

	// last_match_at (migration 00064) must be stamped on the 'hardcover' state row.
	st, err := d.GetBookSyncState(ctx, owner, matchStateSource)
	if err != nil {
		t.Fatalf("get sync state: %v", err)
	}
	if st.LastMatchAt == nil {
		t.Fatal("last_match_at was not stamped after the sweep")
	}
}

// --- tiny DB helpers (this package has no shared harness) --------------------

func seedUser(t *testing.T, d *db.DB, ctx context.Context, owner string) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`,
		owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func cleanupOwner(d *db.DB, ctx context.Context, owner string) {
	_, _ = d.Pool.Exec(ctx, `DELETE FROM reading_items WHERE owner=$1`, owner)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM book_sync_state WHERE owner=$1`, owner)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, owner)
}

func mustUpsert(t *testing.T, d *db.DB, ctx context.Context, it db.ReadingItem) {
	t.Helper()
	if err := d.UpsertReadingItem(ctx, it); err != nil {
		t.Fatalf("upsert %s: %v", it.ExternalID, err)
	}
}
