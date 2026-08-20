// reading_events_test.go — the readingEvents domain over reading_events_enriched
// (migration 00081 / books 00003). Two layers:
//   - Compile assertions (no pg harness, run everywhere): the `reads` measure
//     compiles owner-scoped count(*) SQL over the view and whitelists the event
//     axes. Non-tautological: the origin case FAILS if `origin` is dropped from
//     the reads Dims whitelist (or the dim map) — the events-only axis this adds.
//   - An integration test (isolated test DB, skips when unreachable): seeds a
//     reading_item + several reading_events (a re-read + a hardcover-matched read)
//     and proves a grouped `reads` query returns the ENRICHED rows grouped by
//     origin (the LATERAL join to reading_items resolved title/series).
package query_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/query"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

func TestReadingEvents_ReadsMeasure_Compile(t *testing.T) {
	// The reads measure must compile owner-scoped count(*) SQL over the view.
	sql, args, err := query.Compile("alice", query.Q("readingEvents").Measure("reads"))
	if err != nil {
		t.Fatalf("reads scalar compile: %v", err)
	}
	if !strings.Contains(sql, "count(*)") {
		t.Errorf("reads SQL missing count(*): %q", sql)
	}
	if !strings.Contains(sql, "FROM reading_events_enriched") {
		t.Errorf("reads SQL not over the enriched view: %q", sql)
	}
	if !strings.Contains(sql, "owner = $1") || len(args) == 0 || args[0] != "alice" {
		t.Errorf("reads SQL not owner-scoped to $1=alice: sql=%q args=%v", sql, args)
	}
}

func TestReadingEvents_GroupByDims_Compile(t *testing.T) {
	// Every event axis groups. origin is the events-ONLY axis — this case fails if
	// it isn't whitelisted on the reads measure (the gap this domain closes); the
	// rest reuse reading_items metadata the view exposes.
	for dim, wantExpr := range map[string]string{
		"origin": "origin",
		"source": "source",
		"series": "series",
		"author": "authors",                          // reading_items.authors
		"genre":  "genres->>0",                        // first jsonb element
		"status": "COALESCE(status_override, status)", // effective status
		"title":  "title",
	} {
		sql, _, err := query.Compile("bob",
			query.Q("readingEvents").Measure("reads").Group(dim))
		if err != nil {
			t.Errorf("reads group by %q: unexpected error: %v", dim, err)
			continue
		}
		if !strings.Contains(sql, "GROUP BY") {
			t.Errorf("reads group by %q: no GROUP BY in %q", dim, sql)
		}
		if !strings.Contains(sql, wantExpr) {
			t.Errorf("reads group by %q: expected expr %q in SQL, got %q", dim, wantExpr, sql)
		}
	}
}

func TestReadingEvents_UnknownAxis_Rejected(t *testing.T) {
	// A dim that is NOT on the events domain (e.g. the reading library's isMatched)
	// must be rejected by the whitelist — proves the domains are independent.
	if _, _, err := query.Compile("carol",
		query.Q("readingEvents").Measure("reads").Group("isMatched")); err == nil {
		t.Fatalf("expected unknown-dimension error for isMatched on readingEvents")
	}
}

// seedEvent inserts one reading_events row. external_read_id is the idempotency
// key (unique per owner+origin), so callers pass a distinct one per read.
func seedEvent(t *testing.T, hz *testutil.Harness, owner, origin, source, extID, extReadID string, hardcoverBookID *int64, finishedAt time.Time) {
	t.Helper()
	_, err := hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO reading_events (owner, origin, source, external_id, hardcover_book_id,
			external_read_id, started_at, finished_at, progress_pages)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		owner, origin, source, extID, hardcoverBookID, extReadID,
		finishedAt.AddDate(0, 0, -1), finishedAt, 300)
	if err != nil {
		t.Fatalf("seed event %s/%s: %v", origin, extReadID, err)
	}
	t.Cleanup(func() {
		_, _ = hz.DB.Pool.Exec(context.Background(), `DELETE FROM reading_events WHERE owner=$1`, owner)
	})
}

// seedEventItem inserts the reading_items row a reading_event enriches against
// (Amazon edition source+external_id, plus a hardcover_book_id for the Work match).
func seedEventItem(t *testing.T, hz *testutil.Harness, owner, source, extID, title, series string, hardcoverBookID int64) {
	t.Helper()
	_, err := hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO reading_items (owner, source, external_id, title, authors, series,
			status, genres, hardcover_book_id)
		VALUES ($1,$2,$3,$4,'Brandon Sanderson',$5,'read','["Fantasy","Epic"]'::jsonb,$6)
		ON CONFLICT (owner, source, external_id) DO UPDATE SET title=EXCLUDED.title`,
		owner, source, extID, title, series, hardcoverBookID)
	if err != nil {
		t.Fatalf("seed event item %s: %v", extID, err)
	}
	t.Cleanup(func() {
		_, _ = hz.DB.Pool.Exec(context.Background(), `DELETE FROM reading_items WHERE owner=$1`, owner)
	})
}

func TestReadingEvents_GroupByOrigin_EnrichesAndCounts(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_reading_events")
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	const hcBook int64 = 909909

	// One book, matched to a Hardcover Work id.
	seedEventItem(t, hz, owner, "audible", "B0AUDIBLE1", "The Way of Kings", "Stormlight Archive", hcBook)

	// Three discrete reads of that one book:
	//   - two audible reads (a re-read) matched via source+external_id
	//   - one hardcover read matched via hardcover_book_id (source empty)
	seedEvent(t, hz, owner, "audible", "audible", "B0AUDIBLE1", "aud-read-1", nil, now)
	seedEvent(t, hz, owner, "audible", "audible", "B0AUDIBLE1", "aud-read-2", nil, now.AddDate(0, 0, -30))
	hb := hcBook
	seedEvent(t, hz, owner, "hardcover", "", "", "hc-read-1", &hb, now.AddDate(0, 0, -60))

	// Grouped reads by origin: audible=2, hardcover=1.
	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("readingEvents").Measure("reads").Group("origin"))
	if err != nil {
		t.Fatalf("group reads by origin: %v", err)
	}
	byOrigin := groupMap(res)
	if byOrigin["audible"] != 2 {
		t.Errorf("audible reads = %v, want 2 (the re-read counts twice)", byOrigin["audible"])
	}
	if byOrigin["hardcover"] != 1 {
		t.Errorf("hardcover reads = %v, want 1", byOrigin["hardcover"])
	}

	// Grouping by SERIES proves the LATERAL join to reading_items resolved — a bare
	// reading_events row carries no series, so a non-null "Stormlight Archive" group
	// with all 3 reads can only come from the enriched view (incl. the hardcover
	// read matched via hardcover_book_id).
	res, err = query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("readingEvents").Measure("reads").Group("series"))
	if err != nil {
		t.Fatalf("group reads by series: %v", err)
	}
	if got := groupMap(res)["Stormlight Archive"]; got != 3 {
		t.Fatalf("reads for series 'Stormlight Archive' = %v, want 3 (join across all origins)", got)
	}

	// Leaf rows: one row per READ, each enriched with the book title (the join) and
	// its origin. Non-tautological — a broken view projection leaves title NULL.
	rres, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("readingEvents").Rows().Page(1, 50))
	if err != nil {
		t.Fatalf("event rows: %v", err)
	}
	if rres.Kind != query.ResultRows {
		t.Fatalf("kind = %v, want rows", rres.Kind)
	}
	if len(rres.Rows) != 3 {
		t.Fatalf("event rows = %d, want 3 (one per read)", len(rres.Rows))
	}
	origins := map[string]int{}
	for _, r := range rres.Rows {
		if r["title"] != "The Way of Kings" {
			t.Errorf("event row title = %v, want enriched 'The Way of Kings'", r["title"])
		}
		if o, ok := r["origin"].(string); ok {
			origins[o]++
		}
	}
	if origins["audible"] != 2 || origins["hardcover"] != 1 {
		t.Fatalf("event-row origins = %v, want audible:2 hardcover:1", origins)
	}
}
