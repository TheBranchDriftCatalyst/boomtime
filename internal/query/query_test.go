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
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/query"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
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
