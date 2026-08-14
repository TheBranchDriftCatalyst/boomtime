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
// hits; LookupByASIN returns metadata for keys in metas; editionsByField serves
// the BATCH exact-id rung by projecting the exact-id hits in `hits` back to
// hcEditions (so one `hits` map drives both the per-row and the batch paths).
type fakeMatcher struct {
	hits      map[string]MatchResult // keyed by the ASIN/ISBN/title we expect
	metas     map[string]*BookMeta   // keyed by ASIN
	matchCall int                    // per-row Match (fuzzy tail) calls
	batchCall int                    // editionsByField (batch exact-id) calls
	batchErr  error                  // when set, editionsByField returns it (abort/backoff tests)
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

// editionsByField projects the exact-id hits in `hits` into hcEditions keyed by the
// matched value — mirroring the real batch rung. Only hits whose Method matches the
// field (asin→MatchByASIN, isbn_13→MatchByISBN13) resolve, so a fuzzy/title hit is
// never served by the batch (proving the fuzzy tail still goes through Match).
func (f *fakeMatcher) editionsByField(_ context.Context, field string, values []string) (map[string]hcEdition, error) {
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	f.batchCall++
	out := map[string]hcEdition{}
	for _, v := range values {
		r, ok := f.hits[v]
		if !ok {
			continue
		}
		if field == "asin" && r.Method != MatchByASIN {
			continue
		}
		if field == "isbn_13" && r.Method != MatchByISBN13 {
			continue
		}
		ed := hcEdition{ID: r.EditionID, BookID: r.BookID}
		ed.Book.Slug = r.Slug
		if field == "asin" {
			ed.Asin = v
		} else {
			ed.Isbn13 = v
		}
		out[v] = ed
	}
	return out, nil
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

// TestMatchWith_CacheHitSkipsAPI (gaka-wzgr) — a row whose ASIN is already in the
// GLOBAL cache is linked WITHOUT a single client.Match call. The fake's Match
// counter must stay 0 and res.CacheHits must count the row.
func TestMatchWith_CacheHitSkipsAPI(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("sweep_hit_%d", time.Now().UnixNano())
	asin := fmt.Sprintf("B0CACHEHIT%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() {
		cleanupOwner(d, ctx, owner)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM hardcover_match_cache WHERE external_id=$1`, asin)
	})

	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "audible", ExternalID: asin, AmazonASIN: asin, Title: "Some Title"})

	// Pre-seed the global cache (with a slug) so the sweep resolves without
	// touching the API — and so we can assert the slug rides the cache→link path.
	if err := d.PutHardcoverMatch(ctx, "asin", asin, 777, 7707, "asin", "cached-book-slug"); err != nil {
		t.Fatalf("pre-seed cache: %v", err)
	}

	// Fake with NO scripted hits — if the sweep called Match it would return
	// MatchNone and the row would NOT link, so a link proves the cache path.
	fake := &fakeMatcher{hits: map[string]MatchResult{}}

	svc := NewSyncService(d, NewStore(d), nil)
	res, err := svc.matchWith(ctx, owner, fake)
	if err != nil {
		t.Fatalf("matchWith: %v", err)
	}
	if res.CacheHits != 1 {
		t.Fatalf("CacheHits = %d, want 1", res.CacheHits)
	}
	if res.Matched != 1 {
		t.Fatalf("Matched = %d, want 1", res.Matched)
	}
	if fake.matchCall != 0 {
		t.Fatalf("client.Match was called %d times, want 0 (cache should have served it)", fake.matchCall)
	}

	// The row must actually be linked (cache hit still writes the per-user link).
	remaining, err := d.ListUnmatchedReadingItems(ctx, owner)
	if err != nil {
		t.Fatalf("list unmatched: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("row not linked from cache: %d still unmatched", len(remaining))
	}

	// The cached slug must have been written onto the per-user row — else a
	// cache-hit match would still 404 the deep-link.
	var gotSlug *string
	if err := d.Pool.QueryRow(ctx,
		`SELECT hardcover_slug FROM reading_items WHERE owner=$1 AND source='audible' AND external_id=$2`,
		owner, asin).Scan(&gotSlug); err != nil {
		t.Fatalf("read back slug: %v", err)
	}
	if gotSlug == nil || *gotSlug != "cached-book-slug" {
		t.Fatalf("hardcover_slug from cache-hit link = %v, want %q", gotSlug, "cached-book-slug")
	}
}

// TestMatchWith_CacheMissPopulates (gaka-wzgr) — an exact-id (asin) cache miss is
// resolved by the BATCH rung (editionsByField, not per-row Match) AND writes the
// resolved identity + slug into the global cache for the next user.
func TestMatchWith_CacheMissPopulates(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("sweep_miss_%d", time.Now().UnixNano())
	asin := fmt.Sprintf("B0CACHEMISS%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() {
		cleanupOwner(d, ctx, owner)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM hardcover_match_cache WHERE external_id=$1`, asin)
	})

	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "audible", ExternalID: asin, AmazonASIN: asin, Title: "Fresh Book"})

	fake := &fakeMatcher{
		hits: map[string]MatchResult{
			asin: {BookID: 555, EditionID: 5505, Slug: "fresh-book-slug", Method: MatchByASIN, Confidence: 1},
		},
	}

	svc := NewSyncService(d, NewStore(d), nil)
	res, err := svc.matchWith(ctx, owner, fake)
	if err != nil {
		t.Fatalf("matchWith: %v", err)
	}
	if res.Matched != 1 || res.CacheHits != 0 || res.BatchHits != 1 {
		t.Fatalf("Matched=%d CacheHits=%d BatchHits=%d, want 1/0/1", res.Matched, res.CacheHits, res.BatchHits)
	}
	if fake.matchCall != 0 {
		t.Fatalf("per-row Match called %d times, want 0 (exact-id must resolve via the batch rung)", fake.matchCall)
	}
	if fake.batchCall == 0 {
		t.Fatalf("editionsByField was never called — exact-id miss must hit the batch rung")
	}

	// The global cache must now carry the resolved identity for the next user.
	cached, ok, err := d.LookupHardcoverMatch(ctx, "asin", asin)
	if err != nil || !ok {
		t.Fatalf("cache not populated after exact-id miss: ok=%v err=%v", ok, err)
	}
	if cached.BookID != 555 || cached.EditionID != 5505 || cached.Method != "asin" || cached.Slug != "fresh-book-slug" {
		t.Fatalf("cached = %+v, want {555 5505 asin fresh-book-slug}", cached)
	}
}

// TestMatchWith_FuzzyNotCached (gaka-wzgr) — a fuzzy (MatchBySearch) resolution
// links the per-user row but must NEVER poison the global cache: a wrong edition
// picked by fuzzy would then be served to every user.
func TestMatchWith_FuzzyNotCached(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("sweep_fuzzy_%d", time.Now().UnixNano())
	asin := fmt.Sprintf("B0FUZZY%d", time.Now().UnixNano())
	isbn := fmt.Sprintf("978%013d", time.Now().UnixNano()%1e13)
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() {
		cleanupOwner(d, ctx, owner)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM hardcover_match_cache WHERE external_id IN ($1,$2)`, asin, isbn)
	})

	// Row carries both an asin and an isbn so we can prove NEITHER key gets cached.
	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "kindle", ExternalID: asin, AmazonASIN: asin, ISBN: isbn, Title: "Fuzzy Matched Book", Authors: "Some Author"})

	fake := &fakeMatcher{
		hits: map[string]MatchResult{
			// Keyed on title so it resolves via the search rung shape.
			"Fuzzy Matched Book": {BookID: 888, EditionID: 8808, Method: MatchBySearch, Confidence: 0.9},
		},
	}

	svc := NewSyncService(d, NewStore(d), nil)
	res, err := svc.matchWith(ctx, owner, fake)
	if err != nil {
		t.Fatalf("matchWith: %v", err)
	}
	if res.Matched != 1 {
		t.Fatalf("Matched = %d, want 1 (fuzzy still links the row)", res.Matched)
	}

	// The row IS linked...
	remaining, err := d.ListUnmatchedReadingItems(ctx, owner)
	if err != nil {
		t.Fatalf("list unmatched: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("fuzzy match did not link the row: %d still unmatched", len(remaining))
	}

	// ...but the global cache must have NO row under either key.
	if _, ok, err := d.LookupHardcoverMatch(ctx, "asin", asin); err != nil || ok {
		t.Fatalf("fuzzy poisoned the cache under asin: ok=%v err=%v", ok, err)
	}
	if _, ok, err := d.LookupHardcoverMatch(ctx, "isbn13", isbn); err != nil || ok {
		t.Fatalf("fuzzy poisoned the cache under isbn13: ok=%v err=%v", ok, err)
	}
}

// TestMatchWith_BatchResolvesManyInOneRequest pins the batch rung end-to-end:
// three ASIN rows resolve in a SINGLE editionsByField call (batchCall==1), each
// gets linked + slug-carried + cached, and per-row Match is never touched.
func TestMatchWith_BatchResolvesManyInOneRequest(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("sweep_batch_%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	a1 := fmt.Sprintf("B0BATCH1%d", time.Now().UnixNano())
	a2 := fmt.Sprintf("B0BATCH2%d", time.Now().UnixNano())
	a3 := fmt.Sprintf("B0BATCH3%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupOwner(d, ctx, owner)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM hardcover_match_cache WHERE external_id IN ($1,$2,$3)`, a1, a2, a3)
	})

	for i, a := range []string{a1, a2, a3} {
		mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "audible", ExternalID: a, AmazonASIN: a, Title: fmt.Sprintf("Book %d", i)})
	}

	fake := &fakeMatcher{hits: map[string]MatchResult{
		a1: {BookID: 11, EditionID: 110, Slug: "slug-1", Method: MatchByASIN, Confidence: 1},
		a2: {BookID: 22, EditionID: 220, Slug: "slug-2", Method: MatchByASIN, Confidence: 1},
		a3: {BookID: 33, EditionID: 330, Slug: "slug-3", Method: MatchByASIN, Confidence: 1},
	}}

	svc := NewSyncService(d, NewStore(d), nil)
	res, err := svc.matchWith(ctx, owner, fake)
	if err != nil {
		t.Fatalf("matchWith: %v", err)
	}
	if res.Matched != 3 || res.BatchHits != 3 {
		t.Fatalf("Matched=%d BatchHits=%d, want 3/3", res.Matched, res.BatchHits)
	}
	if fake.batchCall != 1 {
		t.Fatalf("editionsByField called %d times, want 1 (all three ASINs in one batch)", fake.batchCall)
	}
	if fake.matchCall != 0 {
		t.Fatalf("per-row Match called %d times, want 0 (batch resolved everything)", fake.matchCall)
	}

	// All three linked + slug carried, cache populated for the next user.
	if remaining, _ := d.ListUnmatchedReadingItems(ctx, owner); len(remaining) != 0 {
		t.Fatalf("%d rows still unmatched after batch", len(remaining))
	}
	cached, ok, err := d.LookupHardcoverMatch(ctx, "asin", a2)
	if err != nil || !ok || cached.BookID != 22 || cached.Slug != "slug-2" {
		t.Fatalf("cache for a2 = %+v ok=%v err=%v, want book 22 slug-2", cached, ok, err)
	}
}

// TestMatchWith_NoMatchStampsAndNextSweepSkips pins the negative/attempt cache
// (migration 00071): a fuzzy no-match stamps match_attempted_at, and the FOLLOWING
// sweep excludes that row from its candidates (within the retry window) — so the
// expensive fuzzy tail runs at most once per window.
func TestMatchWith_NoMatchStampsAndNextSweepSkips(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("sweep_neg_%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() { cleanupOwner(d, ctx, owner) })

	// A title-only obscure row (no exact id) → falls to the fuzzy tail → no-match.
	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "kindle", ExternalID: "B0OBSCURE1", Title: "Nonexistent Zine Vol 9"})

	fake := &fakeMatcher{hits: map[string]MatchResult{}} // nothing resolves

	svc := NewSyncService(d, NewStore(d), nil)
	res, err := svc.matchWith(ctx, owner, fake)
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if res.Scanned != 1 || res.NoMatch != 1 {
		t.Fatalf("first sweep Scanned=%d NoMatch=%d, want 1/1", res.Scanned, res.NoMatch)
	}
	if fake.matchCall != 1 {
		t.Fatalf("first sweep matchCall=%d, want 1 (the fuzzy attempt)", fake.matchCall)
	}

	// match_attempted_at must now be stamped.
	var stamped *time.Time
	if err := d.Pool.QueryRow(ctx,
		`SELECT match_attempted_at FROM reading_items WHERE owner=$1 AND source='kindle' AND external_id=$2`,
		owner, "B0OBSCURE1").Scan(&stamped); err != nil {
		t.Fatalf("read attempted stamp: %v", err)
	}
	if stamped == nil {
		t.Fatal("match_attempted_at was not stamped after a no-match")
	}

	// The SECOND sweep must EXCLUDE the recently-attempted row: nothing scanned, and
	// the fuzzy path is not spent again.
	fake.matchCall = 0
	res2, err := svc.matchWith(ctx, owner, fake)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if res2.Scanned != 0 {
		t.Fatalf("second sweep Scanned=%d, want 0 (row is within the retry window)", res2.Scanned)
	}
	if fake.matchCall != 0 {
		t.Fatalf("second sweep matchCall=%d, want 0 (negative cache must skip the fuzzy retry)", fake.matchCall)
	}
}

// TestMatchWith_RateLimitedAborts pins that a rate-limit from the batch rung aborts
// the whole sweep with ErrRateLimited (mirroring the pull) rather than churning
// through the backlog and burning the budget.
func TestMatchWith_RateLimitedAborts(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("sweep_rl_%d", time.Now().UnixNano())
	asin := fmt.Sprintf("B0RL%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() { cleanupOwner(d, ctx, owner) })

	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "audible", ExternalID: asin, AmazonASIN: asin, Title: "Rate Limited"})

	fake := &fakeMatcher{hits: map[string]MatchResult{}, batchErr: ErrRateLimited}

	svc := NewSyncService(d, NewStore(d), nil)
	_, err := svc.matchWith(ctx, owner, fake)
	if err != ErrRateLimited {
		t.Fatalf("matchWith err = %v, want ErrRateLimited", err)
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
