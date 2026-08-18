// query_test.go — DB-backed tests for the cross-domain query DSL (gaka-174.q).
//
// Uses the shared testutil harness (ephemeral/isolated boomtime_test pg, auto
// migrated) exactly like the goals + stats suites. Each test mints a fresh user
// (owner FK), seeds a handful of hb_rollup_daily / reading_activity /
// reading_items rows, and asserts the compiled+run result. Reading tables are
// not in the harness's default cleanup, so each test registers its own.
package query_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/query"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

func seedRollup(t *testing.T, hz *testutil.Harness, owner string, day time.Time, project, language string, seconds int64) {
	t.Helper()
	_, err := hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO hb_rollup_daily (sender, day, project, language, editor,
			platform, machine, category, plugin, branch, total_seconds)
		VALUES ($1,$2::date,$3,$4,'vim','linux','m','Coding','pl','main',$5)
		ON CONFLICT (sender, day, project, language, editor, platform, machine, category, plugin, branch)
		DO UPDATE SET total_seconds = hb_rollup_daily.total_seconds + EXCLUDED.total_seconds`,
		owner, day, project, language, seconds)
	if err != nil {
		t.Fatalf("seed rollup: %v", err)
	}
}

func seedActivity(t *testing.T, hz *testutil.Harness, owner, source string, bucket time.Time, seconds int64) {
	t.Helper()
	_, err := hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO reading_activity (owner, source, granularity, bucket_date, listening_seconds)
		VALUES ($1,$2,'day',$3::date,$4)`,
		owner, source, bucket, seconds)
	if err != nil {
		t.Fatalf("seed activity: %v", err)
	}
	t.Cleanup(func() {
		_, _ = hz.DB.Pool.Exec(context.Background(), `DELETE FROM reading_activity WHERE owner=$1`, owner)
	})
}

func seedItem(t *testing.T, hz *testutil.Harness, owner, asin, title, status, series string, runtimeMin int, finishedAt *time.Time) {
	t.Helper()
	_, err := hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO reading_items (owner, source, external_id, title, status, series,
			runtime_min, finished, finished_at, genres)
		VALUES ($1,'audible',$2,$3,$4,$5,$6,$7,$8,'["Fiction","Sci-Fi"]'::jsonb)
		ON CONFLICT (owner, source, external_id) DO UPDATE SET status=EXCLUDED.status`,
		owner, asin, title, status, series, runtimeMin, finishedAt != nil, finishedAt)
	if err != nil {
		t.Fatalf("seed item: %v", err)
	}
	t.Cleanup(func() {
		_, _ = hz.DB.Pool.Exec(context.Background(), `DELETE FROM reading_items WHERE owner=$1`, owner)
	})
}

// coding: per-project seconds with no time axis → Groups.
func TestCoding_GroupProjectScalarWindow(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_coding_grp")
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	seedRollup(t, hz, owner, now.AddDate(0, 0, -1), "alpha", "Go", 1000)
	seedRollup(t, hz, owner, now.AddDate(0, 0, -2), "alpha", "Rust", 500)
	seedRollup(t, hz, owner, now.AddDate(0, 0, -1), "beta", "Go", 300)

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("coding").Measure("seconds").Group("project").Over(query.GranNone, query.Range{}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Kind != query.ResultGroups {
		t.Fatalf("kind = %v, want groups", res.Kind)
	}
	got := map[string]float64{}
	for _, g := range res.Groups {
		got[g.Key] = g.Value
	}
	if got["alpha"] != 1500 {
		t.Errorf("alpha = %v, want 1500", got["alpha"])
	}
	if got["beta"] != 300 {
		t.Errorf("beta = %v, want 300", got["beta"])
	}
	// Sorted by measure desc: alpha before beta.
	if len(res.Groups) != 2 || res.Groups[0].Key != "alpha" {
		t.Errorf("groups order = %+v, want alpha first", res.Groups)
	}
}

// coding: filtered scalar (no group, no time axis) → Scalar. Also proves the
// where-predicate whitelist + parameterized value path.
func TestCoding_ScalarWithPredicate(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_coding_scalar")
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	seedRollup(t, hz, owner, now.AddDate(0, 0, -1), "alpha", "Go", 1000)
	seedRollup(t, hz, owner, now.AddDate(0, 0, -2), "alpha", "Rust", 500)

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("coding").Measure("seconds").
			Where(query.Leaf("language", query.OpEq, "go")). // lower-folded → matches "Go"
			Over(query.GranNone, query.Range{}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Kind != query.ResultScalar {
		t.Fatalf("kind = %v, want scalar", res.Kind)
	}
	if res.Scalar != 1000 {
		t.Errorf("scalar = %v, want 1000 (case-folded Go only)", res.Scalar)
	}
}

// reading time-series: weekly buckets over last 8 weeks sum correctly.
func TestReading_WeeklySeries(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_reading_series")
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) // a Wednesday

	// Two rows in the same ISO week (should sum into one bucket) + one a week back.
	seedActivity(t, hz, owner, "audible", now.AddDate(0, 0, -1), 600) // Tue
	seedActivity(t, hz, owner, "audible", now, 400)                   // Wed (same week)
	seedActivity(t, hz, owner, "audible", now.AddDate(0, 0, -8), 900) // prior week
	// Outside the 8-week window (must be excluded).
	seedActivity(t, hz, owner, "audible", now.AddDate(0, 0, -70), 12345)

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Measure("seconds").
			Over(query.GranWeek, query.LastN(8)).At(now))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Kind != query.ResultSeries {
		t.Fatalf("kind = %v, want series", res.Kind)
	}
	var total float64
	var thisWeek float64
	_, wkStart := isoWeek(now)
	for _, p := range res.Series {
		total += p.Value
		if p.Bucket.Equal(wkStart) {
			thisWeek = p.Value
		}
	}
	if total != 1900 { // 600+400+900, the -70d row excluded
		t.Errorf("series total = %v, want 1900 (8w window excludes ancient row)", total)
	}
	if thisWeek != 1000 { // 600+400 merged into the current week bucket
		t.Errorf("current-week bucket = %v, want 1000 (two rows merged)", thisWeek)
	}
}

// isoWeek returns (year, Monday-midnight-UTC start of t's ISO week).
func isoWeek(t time.Time) (int, time.Time) {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	wd := (int(d.Weekday()) + 6) % 7
	start := d.AddDate(0, 0, -wd)
	y, _ := start.ISOWeek()
	return y, start
}

// reading groups: books count grouped by status → Groups.
func TestReading_BooksByStatus(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_reading_status")
	fin := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	seedItem(t, hz, owner, "A1", "Book A", "read", "Foundation", 600, &fin)
	seedItem(t, hz, owner, "A2", "Book B", "read", "Dune", 720, &fin)
	seedItem(t, hz, owner, "A3", "Book C", "reading", "Culture", 300, nil)

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Measure("books").Group("status"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := map[string]float64{}
	for _, g := range res.Groups {
		got[g.Key] = g.Value
	}
	if got["read"] != 2 {
		t.Errorf("read = %v, want 2", got["read"])
	}
	if got["reading"] != 1 {
		t.Errorf("reading = %v, want 1", got["reading"])
	}
}

// reading groups + bucket policy: pinned kept, rest → Other.
func TestReading_BucketPolicy(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_reading_bucket")
	fin := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// series values (books per series): big=3, mid=2, small1=1, small2=1, pinme=1
	seedItem(t, hz, owner, "B1", "b1", "read", "big", 1, &fin)
	seedItem(t, hz, owner, "B2", "b2", "read", "big", 1, &fin)
	seedItem(t, hz, owner, "B3", "b3", "read", "big", 1, &fin)
	seedItem(t, hz, owner, "M1", "m1", "read", "mid", 1, &fin)
	seedItem(t, hz, owner, "M2", "m2", "read", "mid", 1, &fin)
	seedItem(t, hz, owner, "S1", "s1", "read", "small1", 1, &fin)
	seedItem(t, hz, owner, "S2", "s2", "read", "small2", 1, &fin)
	seedItem(t, hz, owner, "P1", "p1", "read", "pinme", 1, &fin)

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Measure("books").Group("series").
			Bucket(query.BucketPolicy{TopN: 2, Pin: []string{"pinme"}, Other: true}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := map[string]float64{}
	for _, g := range res.Groups {
		got[g.Key] = g.Value
	}
	// Top-2 non-pinned by count: big(3), mid(2). Pinned: pinme(1). Rest
	// (small1=1, small2=1) → Other=2.
	if got["big"] != 3 || got["mid"] != 2 {
		t.Errorf("top-2 = big:%v mid:%v, want 3,2", got["big"], got["mid"])
	}
	if got["pinme"] != 1 {
		t.Errorf("pinme = %v, want 1 (pinned, never bucketed)", got["pinme"])
	}
	if got["Other"] != 2 {
		t.Errorf("Other = %v, want 2 (small1+small2)", got["Other"])
	}
	if _, leaked := got["small1"]; leaked {
		t.Errorf("small1 should have rolled into Other, got %+v", res.Groups)
	}
	if got["Other"] != 0 && res.Groups[len(res.Groups)-1].Key != "Other" {
		t.Errorf("Other must be the last row, got %+v", res.Groups)
	}
}

// runtime measure: sum(runtime_min) grouped by series.
func TestReading_RuntimeBySeries(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_reading_runtime")
	fin := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	seedItem(t, hz, owner, "R1", "r1", "read", "Foundation", 600, &fin)
	seedItem(t, hz, owner, "R2", "r2", "read", "Foundation", 720, &fin)

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Measure("runtime").Group("series"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := map[string]float64{}
	for _, g := range res.Groups {
		got[g.Key] = g.Value
	}
	if got["Foundation"] != 1320 {
		t.Errorf("Foundation runtime = %v, want 1320", got["Foundation"])
	}
}

// rollups: a grouped query with rollups returns per-group Stats (count + each
// rollup measure) computed in ONE round-trip, with the primary measure in Value.
func TestReading_GroupRollups(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_reading_rollups")
	fin := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// series "Foundation": 2 rows, runtime 600+720=1320, finished 1 (only R1).
	seedItem(t, hz, owner, "F1", "f1", "read", "Foundation", 600, &fin)
	seedItem(t, hz, owner, "F2", "f2", "reading", "Foundation", 720, nil)
	// series "Dune": 3 rows, runtime 300+100+50=450, finished 2 (D1,D2).
	seedItem(t, hz, owner, "D1", "d1", "read", "Dune", 300, &fin)
	seedItem(t, hz, owner, "D2", "d2", "read", "Dune", 100, &fin)
	seedItem(t, hz, owner, "D3", "d3", "reading", "Dune", 50, nil)

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Measure("books").Group("series").
			Rollups("runtime", "finished"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Kind != query.ResultGroups {
		t.Fatalf("kind = %v, want groups", res.Kind)
	}
	by := map[string]query.Group{}
	for _, g := range res.Groups {
		by[g.Key] = g
	}
	check := func(series string, wantCount, wantRuntime, wantFinished float64) {
		g, ok := by[series]
		if !ok {
			t.Fatalf("missing group %q in %+v", series, res.Groups)
		}
		if g.Stats == nil {
			t.Fatalf("group %q has nil Stats (rollups not populated)", series)
		}
		if g.Value != wantCount {
			t.Errorf("%s Value (books) = %v, want %v", series, g.Value, wantCount)
		}
		if g.Stats["count"] != wantCount {
			t.Errorf("%s stats.count = %v, want %v", series, g.Stats["count"], wantCount)
		}
		if g.Stats["runtime"] != wantRuntime {
			t.Errorf("%s stats.runtime = %v, want %v", series, g.Stats["runtime"], wantRuntime)
		}
		if g.Stats["finished"] != wantFinished {
			t.Errorf("%s stats.finished = %v, want %v", series, g.Stats["finished"], wantFinished)
		}
	}
	check("Foundation", 2, 1320, 1)
	check("Dune", 3, 450, 2)
}

// rollups safety: an unknown rollup name, and a rollup measure on a DIFFERENT
// table than the grouping measure, are both rejected at Compile — no SQL.
func TestSafety_RejectsBadRollups(t *testing.T) {
	// Unknown rollup measure.
	if sql, _, err := query.Compile("owner",
		query.Q("reading").Measure("books").Group("status").Rollups("bogus")); err == nil {
		t.Errorf("expected rejection for unknown rollup, got sql=%q", sql)
	}
	// Cross-table rollup: books lives on reading_items; seconds lives on
	// reading_activity → cannot ride as a rollup of a reading_items group.
	if sql, _, err := query.Compile("owner",
		query.Q("reading").Measure("books").Group("source").Rollups("seconds")); err == nil {
		t.Errorf("expected rejection for cross-table rollup, got sql=%q", sql)
	}
	// Positive control: same-table rollups compile.
	if _, _, err := query.Compile("owner",
		query.Q("reading").Measure("books").Group("status").Rollups("runtime", "finished")); err != nil {
		t.Fatalf("same-table rollups should compile, got %v", err)
	}
}

// back-compat: a grouped query WITHOUT rollups emits the byte-identical
// two-column SELECT (no count/rollup columns leak in).
func TestReading_GroupNoRollupsBackCompat(t *testing.T) {
	sql, _, err := query.Compile("owner", query.Q("reading").Measure("books").Group("status"))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if strings.Contains(sql, "count(*) AS count") || strings.Contains(sql, "AS r_") {
		t.Errorf("non-rollups grouped SQL leaked rollup columns:\n%s", sql)
	}
}

// leaf rows mode: owner-scoped + paginated. Another owner's rows never appear;
// the page/total math is correct; the row maps carry the FE JSON keys.
func TestReading_LeafRowsPaginationAndScope(t *testing.T) {
	hz := testutil.NewHarness(t)
	alice, _ := hz.MintUser("q_rows_alice")
	bob, _ := hz.MintUser("q_rows_bob")

	// Alice: 3 finished items, distinct finished_at so DefaultSort is deterministic.
	d1 := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	d3 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	seedItem(t, hz, alice, "A1", "Alpha", "read", "S", 10, &d1)
	seedItem(t, hz, alice, "A2", "Bravo", "read", "S", 20, &d2)
	seedItem(t, hz, alice, "A3", "Chuck", "read", "S", 30, &d3)
	// Bob: one item that must never leak into Alice's rows.
	seedItem(t, hz, bob, "B1", "BobBook", "read", "S", 99, &d1)

	// Page 1 of size 2 → 2 rows, total 3, ordered finished_at DESC (A1 then A2).
	res, err := query.Run(context.Background(), hz.DB.Pool, alice,
		query.Q("reading").Rows().Page(1, 2))
	if err != nil {
		t.Fatalf("run page1: %v", err)
	}
	if res.Kind != query.ResultRows {
		t.Fatalf("kind = %v, want rows", res.Kind)
	}
	if res.Total != 3 {
		t.Errorf("total = %d, want 3", res.Total)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("page1 rows = %d, want 2", len(res.Rows))
	}
	if got := res.Rows[0]["title"]; got != "Alpha" {
		t.Errorf("page1[0].title = %v, want Alpha (finished_at DESC)", got)
	}
	if got := res.Rows[0]["externalId"]; got != "A1" {
		t.Errorf("page1[0].externalId = %v, want A1 (JSON key mapping)", got)
	}
	// No Bob row on any page.
	for _, r := range res.Rows {
		if r["title"] == "BobBook" {
			t.Fatalf("owner scope breached: bob's row leaked into alice's rows")
		}
	}

	// Page 2 of size 2 → the remaining 1 row (Chuck), total still 3.
	res2, err := query.Run(context.Background(), hz.DB.Pool, alice,
		query.Q("reading").Rows().Page(2, 2))
	if err != nil {
		t.Fatalf("run page2: %v", err)
	}
	if res2.Total != 3 || len(res2.Rows) != 1 || res2.Rows[0]["title"] != "Chuck" {
		t.Errorf("page2 = total:%d rows:%d first:%v, want total:3 rows:1 first:Chuck",
			res2.Total, len(res2.Rows), func() any {
				if len(res2.Rows) > 0 {
					return res2.Rows[0]["title"]
				}
				return nil
			}())
	}
}

// leaf rows injection: an injection-y drill value rides as an ARG, so it matches
// nothing (scoped empty set) and never executes as SQL — the table survives.
func TestReading_LeafRowsInjectionValueIsArg(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_rows_inject")
	fin := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	seedItem(t, hz, owner, "Z1", "Zed", "read", "RealSeries", 10, &fin)

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Rows().
			Where(query.Leaf("series", query.OpEq, "'; drop table reading_items; --")).
			Page(1, 50))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Kind != query.ResultRows {
		t.Fatalf("kind = %v, want rows", res.Kind)
	}
	if res.Total != 0 || len(res.Rows) != 0 {
		t.Errorf("injection drill = total:%d rows:%d, want an empty scoped set", res.Total, len(res.Rows))
	}
	// The table must still exist (the value was an arg, not executed SQL): a
	// plain rows query still returns the seeded row.
	res2, err := query.Run(context.Background(), hz.DB.Pool, owner, query.Q("reading").Rows())
	if err != nil {
		t.Fatalf("post-injection run: %v (table dropped?!)", err)
	}
	if res2.Total != 1 {
		t.Errorf("post-injection total = %d, want 1 (table intact, row present)", res2.Total)
	}
}

// ilike substring: a case-insensitive substring predicate on title matches
// regardless of case, constrains BOTH the leaf rows and the grouped aggregate,
// and a non-matching needle yields an empty scoped set.
func TestReading_ILikeSubstring(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_reading_ilike")
	fin := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	seedItem(t, hz, owner, "H1", "The Hobbit", "read", "Middle-earth", 600, &fin)
	seedItem(t, hz, owner, "D1", "Dune", "read", "Dune", 720, &fin)
	seedItem(t, hz, owner, "D2", "Dune Messiah", "read", "Dune", 400, &fin)

	// rows mode: ilike 'hob' matches "The Hobbit" case-insensitively (lowercase
	// needle vs mixed-case title), and nothing else.
	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Rows().
			Where(query.Leaf("title", query.OpILike, "hob")).Page(1, 50))
	if err != nil {
		t.Fatalf("run ilike rows: %v", err)
	}
	if res.Kind != query.ResultRows {
		t.Fatalf("kind = %v, want rows", res.Kind)
	}
	if res.Total != 1 || len(res.Rows) != 1 || res.Rows[0]["title"] != "The Hobbit" {
		t.Errorf("ilike 'hob' = total:%d rows:%d, want the single Hobbit row", res.Total, len(res.Rows))
	}

	// ilike 'dune' matches BOTH Dune rows (substring, case-insensitive).
	res2, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Rows().
			Where(query.Leaf("title", query.OpILike, "DUNE")).Page(1, 50))
	if err != nil {
		t.Fatalf("run ilike rows 2: %v", err)
	}
	if res2.Total != 2 {
		t.Errorf("ilike 'DUNE' total = %d, want 2 (both Dune titles, case-insensitive)", res2.Total)
	}

	// A non-matching needle yields an empty scoped set (not an error).
	res3, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Rows().
			Where(query.Leaf("title", query.OpILike, "xyz")).Page(1, 50))
	if err != nil {
		t.Fatalf("run ilike rows 3: %v", err)
	}
	if res3.Total != 0 || len(res3.Rows) != 0 {
		t.Errorf("ilike 'xyz' = total:%d rows:%d, want empty", res3.Total, len(res3.Rows))
	}

	// grouped aggregate: the SAME ilike predicate constrains the group counts —
	// ilike 'dune' by series → only the Dune series with count 2 (Hobbit excluded).
	gres, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Measure("books").Group("series").
			Where(query.Leaf("title", query.OpILike, "dune")))
	if err != nil {
		t.Fatalf("run ilike group: %v", err)
	}
	got := map[string]float64{}
	for _, g := range gres.Groups {
		got[g.Key] = g.Value
	}
	if got["Dune"] != 2 {
		t.Errorf("grouped ilike 'dune' Dune count = %v, want 2 (aggregate constrained)", got["Dune"])
	}
	if _, leaked := got["Middle-earth"]; leaked {
		t.Errorf("Hobbit's series leaked past the ilike filter: %+v", gres.Groups)
	}
}

// ilike injection: a needle containing SQL metacharacters (%, quotes, a
// drop-table payload) rides as a bound ARG — it is a LITERAL substring, so it
// matches nothing here and never executes; the table survives intact.
func TestReading_ILikeInjectionValueIsArg(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_reading_ilike_inject")
	fin := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	seedItem(t, hz, owner, "Z1", "Zed", "read", "RealSeries", 10, &fin)

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Rows().
			Where(query.Leaf("title", query.OpILike, "%'; drop table reading_items; --")).
			Page(1, 50))
	if err != nil {
		t.Fatalf("run ilike injection: %v", err)
	}
	if res.Kind != query.ResultRows {
		t.Fatalf("kind = %v, want rows", res.Kind)
	}
	// The whole payload is one literal substring — no seeded title contains it.
	if res.Total != 0 || len(res.Rows) != 0 {
		t.Errorf("ilike injection = total:%d rows:%d, want empty scoped set", res.Total, len(res.Rows))
	}
	// Table intact: a plain rows query still returns the seeded row.
	res2, err := query.Run(context.Background(), hz.DB.Pool, owner, query.Q("reading").Rows())
	if err != nil {
		t.Fatalf("post-injection run: %v (table dropped?!)", err)
	}
	if res2.Total != 1 {
		t.Errorf("post-injection total = %d, want 1 (table intact)", res2.Total)
	}
}

// ilike multi-value: multiple needles OR together (contains ANY substring).
func TestReading_ILikeMultiValueOR(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_reading_ilike_multi")
	fin := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	seedItem(t, hz, owner, "H1", "The Hobbit", "read", "Middle-earth", 600, &fin)
	seedItem(t, hz, owner, "D1", "Dune", "read", "Dune", 720, &fin)
	seedItem(t, hz, owner, "C1", "Neuromancer", "read", "Sprawl", 300, &fin)

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Rows().
			Where(query.Leaf("title", query.OpILike, "hob", "dune")).Page(1, 50))
	if err != nil {
		t.Fatalf("run ilike multi: %v", err)
	}
	if res.Total != 2 {
		t.Errorf("ilike ['hob','dune'] total = %d, want 2 (OR of substrings)", res.Total)
	}
}

// safety: unknown measure/dimension names are rejected at Compile — no SQL.
func TestSafety_RejectsUnknownNames(t *testing.T) {
	cases := []struct {
		name string
		q    *query.Query
	}{
		{"unknown domain", query.Q("nope").Measure("seconds")},
		{"unknown measure", query.Q("coding").Measure("bogus")},
		{"unknown group dim", query.Q("coding").Measure("seconds").Group("bogus")},
		{"unknown filter dim", query.Q("coding").Measure("seconds").
			Where(query.Leaf("bogus", query.OpEq, "x"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sql, args, err := query.Compile("owner", c.q)
			if err == nil {
				t.Fatalf("expected error, got sql=%q args=%v", sql, args)
			}
			if sql != "" {
				t.Errorf("no SQL should be emitted on rejection, got %q", sql)
			}
		})
	}
}

// safety: group-by a dim the measure doesn't support (right domain, wrong
// table) is rejected. seconds (reading_activity) supports only "source" — NOT
// the reading_items book dimensions.
func TestSafety_RejectsCrossTableDim(t *testing.T) {
	// reading.seconds lives on reading_activity → cannot group by "status"
	// (a reading_items dimension), even though "status" is a valid dim of the
	// reading domain for the books/runtime measures.
	if _, _, err := query.Compile("owner",
		query.Q("reading").Measure("seconds").Group("status")); err == nil {
		t.Fatal("expected rejection: seconds measure must not support the status dim")
	}
	// And the same via a where-filter.
	if _, _, err := query.Compile("owner",
		query.Q("reading").Measure("seconds").Where(query.Leaf("status", query.OpEq, "read"))); err == nil {
		t.Fatal("expected rejection: seconds measure must not filter on the status dim")
	}
	// Positive control: books DOES support status.
	if _, _, err := query.Compile("owner",
		query.Q("reading").Measure("books").Group("status")); err != nil {
		t.Fatalf("books.status should compile, got %v", err)
	}
}
