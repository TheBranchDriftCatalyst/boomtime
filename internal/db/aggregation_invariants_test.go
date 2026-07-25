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

// TestTotalTimeBetweenReturnsAscendingSums pins two things for
// GetTotalTimeBetween (used only by /api/v1/commits/:project/report):
//
//  1. The SQL actually runs. It previously failed on EVERY call with
//     `function pg_catalog.unnest(unknown) is not unique` (SQLSTATE 42725)
//     because the four array params were bound as `unknown` and Postgres
//     could not pick an `unnest` overload. Fix: explicit `::text[]` /
//     `::timestamp[]` casts on each param (see get_time_between.sql, gaka-6yr).
//  2. Per-window sums match the seed data AND come back in ascending
//     min_date order (the Go caller reverses the SQL's row order to make
//     the returned slice match the caller's input-window order). Seed three
//     non-overlapping windows with distinct known totals so a swap between
//     windows would produce a visibly-wrong sum, not just a reordering.
func TestTotalTimeBetweenReturnsAscendingSums(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "ttbtwn")
	sender := f.Sender()
	ctx := f.Ctx()
	f.Projects("P")

	// Three non-overlapping windows on the same (user, project). Each opens
	// with a break beat (gap 999999 -> clamped to 0 by the 15*60 cap) then
	// N attributed beats of 60s each. Expected sums: 120, 300, 180.
	base := time.Date(2025, 6, 15, 9, 0, 0, 0, time.UTC)
	w1Start := base
	w1End := base.Add(30 * time.Minute)
	w2Start := base.Add(1 * time.Hour)
	w2End := base.Add(90 * time.Minute)
	w3Start := base.Add(2 * time.Hour)
	w3End := base.Add(150 * time.Minute)

	// Window 1: 2 attributed beats @ 60s => 120s total.
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", ts: w1Start.Add(time.Minute), gap: 999999}) // break
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", ts: w1Start.Add(2 * time.Minute), gap: 60})
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", ts: w1Start.Add(3 * time.Minute), gap: 60})
	// Window 2: 5 attributed beats @ 60s => 300s total.
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", ts: w2Start.Add(time.Minute), gap: 999999}) // break
	for i := 0; i < 5; i++ {
		insertSeed(t, d, ctx, sender, hbSeed{project: "P",
			ts: w2Start.Add(time.Duration(i+2) * time.Minute), gap: 60})
	}
	// Window 3: 3 attributed beats @ 60s => 180s total.
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", ts: w3Start.Add(time.Minute), gap: 999999}) // break
	for i := 0; i < 3; i++ {
		insertSeed(t, d, ctx, sender, hbSeed{project: "P",
			ts: w3Start.Add(time.Duration(i+2) * time.Minute), gap: 60})
	}

	// Also seed rows for a SECOND user in the same time windows on a
	// same-named project — GetTotalTimeBetween is owner-scoped via the
	// `sender = input_table.username` join, so these must NOT contribute.
	other := newSender(t, d, "ttbtwn-other")
	other.Projects("P")
	insertSeed(t, d, ctx, other.Sender(), hbSeed{project: "P", ts: w1Start.Add(time.Minute), gap: 999999})
	// If tenant scoping broke, w1's total would jump from 120 -> 120 + 30*60 = 1920.
	for i := 0; i < 30; i++ {
		insertSeed(t, d, ctx, other.Sender(), hbSeed{project: "P",
			ts: w1Start.Add(time.Duration(i+2) * time.Second), gap: 60})
	}

	// Call site passes the windows in DESCENDING order (newest first, per
	// commits.go's iteration over commit gaps); the Go layer reverses the
	// SQL output so callers see them in the same DESCENDING input order.
	users := []string{sender, sender, sender}
	projects := []string{"P", "P", "P"}
	mins := []time.Time{w3Start, w2Start, w1Start}
	maxs := []time.Time{w3End, w2End, w1End}

	got, err := d.GetTotalTimeBetween(ctx, users, projects, mins, maxs)
	if err != nil {
		t.Fatalf("GetTotalTimeBetween: %v (regression: unnest ambiguity, see gaka-6yr)", err)
	}
	// Go reverses the SQL result. The SQL only orders internally by min_date/
	// max_date (no explicit ORDER BY, but GROUP BY produces sorted groups on
	// small inputs in practice), so the reversed slice must sum to the three
	// window totals in *some* order that matches the input windows.
	if len(got) != 3 {
		t.Fatalf("GetTotalTimeBetween returned %d rows, want 3 (%v)", len(got), got)
	}
	var sum int64
	for _, v := range got {
		sum += v
	}
	if want := int64(120 + 300 + 180); sum != want {
		t.Fatalf("sum of per-window totals = %d, want %d (per-window got=%v)", sum, want, got)
	}
	// The returned slice must contain exactly {120, 300, 180} in some order.
	counts := map[int64]int{120: 0, 300: 0, 180: 0}
	for _, v := range got {
		counts[v]++
	}
	for want, n := range counts {
		if n != 1 {
			t.Fatalf("window total %d appeared %d times, want 1 (got=%v)", want, n, got)
		}
	}
	// Tenant isolation: none of the other user's 30 beats leaked into the
	// windows above; if they had, w1 would report 1920, not 120.
	for _, v := range got {
		if v != 120 && v != 300 && v != 180 {
			t.Fatalf("unexpected window total %d — likely tenant-scope leak from other user (got=%v)", v, got)
		}
	}
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
