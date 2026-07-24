package db

import (
	"testing"
	"time"
)

// aggregation_invariants_test.go pins additional aggregation-arithmetic
// invariants the audit (gaka-oew) uncovered but weren't specifically pinned
// by any earlier test:
//
//   - GetCategoryDaily's per-day rows sum to the grand-total (rollup accum).
//   - GetTotalTimeBetween returns per-input ranges in ASCENDING order (the
//     comment says so; a drift would break the timeline widget's rendering
//     which depends on the returned order).
//   - ListHeartbeats page boundaries don't drop or duplicate rows.
//   - GetActiveFiles case-variant projects DISTINCT count doesn't
//     double-count when a shared file is stored under two case-variant
//     project names.

// TestCategoryDailyPerDaySumsMatchGrandTotal: two days of category rows must
// sum per row (each row is one (day, category) bucket) to the same total the
// GetTotalTimeToday-style scan would compute. Guards against a regression in
// the outer wrap that PARTITION-fills or drops days.
func TestCategoryDailyPerDaySumsMatchGrandTotal(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "catgrand")
	sender := f.Sender()
	ctx := f.Ctx()
	f.Projects("P")

	day1 := time.Date(2025, 6, 10, 10, 0, 0, 0, time.UTC)
	day2 := day1.AddDate(0, 0, 1)

	// day1: 200s Coding, 100s Debugging.
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go",
		category: "Coding", ts: day1, gap: 999999}) // break
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go",
		category: "Coding", ts: day1.Add(time.Minute), gap: 100})
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go",
		category: "Coding", ts: day1.Add(2 * time.Minute), gap: 100})
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "b.go",
		category: "Debugging", ts: day1.Add(3 * time.Minute), gap: 999999})
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "b.go",
		category: "Debugging", ts: day1.Add(4 * time.Minute), gap: 100})
	// day2: 100s Coding.
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go",
		category: "Coding", ts: day2, gap: 999999})
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go",
		category: "Coding", ts: day2.Add(time.Minute), gap: 100})

	start := day1.AddDate(0, 0, -1)
	end := day2.AddDate(0, 0, 1)

	cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15,
		HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Per-day sums.
	byDay := map[string]int64{}
	var grand int64
	for _, c := range cats {
		byDay[c.Day.Format("2006-01-02")] += c.TotalSeconds
		grand += c.TotalSeconds
	}
	// day1: 200 + 100 = 300; day2: 100.
	if got := byDay[day1.Format("2006-01-02")]; got != 300 {
		t.Fatalf("day1 sum = %d, want 300", got)
	}
	if got := byDay[day2.Format("2006-01-02")]; got != 100 {
		t.Fatalf("day2 sum = %d, want 100", got)
	}
	// Grand total = 400.
	if grand != 400 {
		t.Fatalf("grand total = %d, want 400", grand)
	}
	// Sanity cross-check against GetUserActivity for the same window (both
	// use the same gap-attributed seconds so they must agree).
	act, err := d.GetUserActivity(ctx, sender, start, end, 15,
		HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if totalStatSeconds(act) != grand {
		t.Fatalf("GetUserActivity total (%d) != GetCategoryDaily grand (%d)",
			totalStatSeconds(act), grand)
	}
}

// TestTotalTimeBetweenAscendingOrder_KnownBroken is a REGRESSION PLACEHOLDER
// for the ascending-order invariant on GetTotalTimeBetween. During the
// gaka-oew audit an ACTUAL BUG was found: the query as authored fails on
// EVERY call under this Postgres version with:
//
//	ERROR: function pg_catalog.unnest(unknown) is not unique (SQLSTATE 42725)
//
// The bug affects the /api/v1/commits/:project/report endpoint (only path
// that calls GetTotalTimeBetween). There is no handler test exercising it
// (blocked by GitHub-credential requirement in commits.go), so the bug has
// no other regression coverage. Filed in the audit report — SHOULD BE
// TRACKED VIA A NEW BEAD (unfixed here to keep the audit surface clean per
// the brief: "if you discover an ACTUAL bug, STOP and report before fixing").
//
// Once fixed (likely: cast the array params in get_time_between.sql to
// explicit types, e.g. `unnest($1::text[], $2::text[], $3::timestamp[],
// $4::timestamp[])`), re-enable this test to pin the reverse-order behavior.
func TestTotalTimeBetweenAscendingOrder_KnownBroken(t *testing.T) {
	t.Skip("gaka-oew audit finding: GetTotalTimeBetween SQL fails on all calls " +
		"(unnest ambiguity in get_time_between.sql). See test file for details.")
}

// TestListHeartbeatsPagesArePartitioned: with 5 rows and page size 2, three
// page fetches must yield each row EXACTLY ONCE with total=5. Guards against
// an off-by-one in OFFSET arithmetic or a reordering under ORDER BY. The
// query orders by time_sent DESC, so newest rows land on page 1 and oldest
// on page 3.
func TestListHeartbeatsPagesArePartitioned(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "lhpg")
	sender := f.Sender()
	ctx := f.Ctx()
	f.Projects("P")

	day := time.Date(2025, 6, 25, 10, 0, 0, 0, time.UTC)
	// 5 rows at strictly increasing timestamps — all distinct so we can
	// track dedup by (sender, entity, time_sent) uniqueness.
	entities := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	for i, ent := range entities {
		insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: ent,
			ts: day.Add(time.Duration(i) * time.Minute), gap: 60})
	}

	start := day.AddDate(0, 0, -1)
	end := day.AddDate(0, 0, 1)

	seen := map[int64]int{}
	var totalSeen int
	for page := 1; page <= 3; page++ {
		items, total, err := d.ListHeartbeats(ctx, sender, start, end, nil, "", page, 2)
		if err != nil {
			t.Fatal(err)
		}
		if total != 5 {
			t.Fatalf("page %d total = %d, want 5", page, total)
		}
		for _, r := range items {
			seen[r.ID]++
			totalSeen++
		}
	}
	if totalSeen != 5 {
		t.Fatalf("total rows seen across pages = %d, want 5 (drops or dupes at boundary)", totalSeen)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("row id %d appeared %d times across pages (want 1)", id, n)
		}
	}
	if len(seen) != 5 {
		t.Fatalf("distinct rows seen = %d, want 5", len(seen))
	}
}

// TestActiveFilesCaseVariantProjectDistinctCount: a file touched by two
// projects whose names differ ONLY in casing must count as ONE distinct
// project (not two) in the ActiveFile.Projects field. The DISTINCT is on
// lower(project), and this test guards against a regression that used the
// raw project column (which would return 2).
func TestActiveFilesCaseVariantProjectDistinctCount(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "afcvp")
	sender := f.Sender()
	ctx := f.Ctx()
	f.Projects("MyProject", "myproject")

	day := time.Date(2025, 6, 28, 10, 0, 0, 0, time.UTC)
	// The SAME file touched by two case-variant project names.
	insertSeed(t, d, ctx, sender, hbSeed{project: "MyProject", entity: "shared.go",
		ts: day, gap: 60})
	insertSeed(t, d, ctx, sender, hbSeed{project: "myproject", entity: "shared.go",
		ts: day.Add(time.Minute), gap: 60})

	files, _, err := d.GetActiveFiles(ctx, sender, day.AddDate(0, 0, -1), day.AddDate(0, 0, 1),
		15, 20, HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, af := range files {
		if af.Entity == "shared.go" || af.Entity == "SHARED.GO" ||
			af.Entity == "Shared.go" {
			found = true
			if af.Projects != 1 {
				t.Fatalf("shared.go Projects = %d, want 1 (case-variant project names must fold to one)",
					af.Projects)
			}
			if af.Seconds != 120 {
				t.Fatalf("shared.go Seconds = %d, want 120", af.Seconds)
			}
		}
	}
	if !found {
		t.Fatal("shared.go not in active-files output")
	}
}
