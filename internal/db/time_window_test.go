package db

import (
	"testing"
	"time"
)

// time_window_test.go pins the [start, end] boundary invariant: every
// aggregation query claims a semantic window; drift in the inclusive/exclusive
// edge would cause silent double-counting or gap losses at day boundaries.
//
// Every /stats/ handler passes user-selected `start` and `end` timestamps
// straight into these queries — a user who picks "yesterday only" and gets
// today's rows silently added, or vice versa, is a data-quality bug the same
// class as gaka-5db. There was NO test seeding a heartbeat exactly at the edge
// and asserting membership.
//
// Boundary semantics per the .sql files (audited 2026-07-24):
//   - get_user_activity, get_category_daily, get_punchcard, get_sessions,
//     get_momentum, get_projects_stats: `time_sent >= $2 AND time_sent <= $3`
//     -> [start, end] INCLUSIVE both sides.
//   - get_timeline: `time_sent < $3` -> [start, end) EXCLUSIVE end.
//   - GetTotalTimeToday: `time_sent < (current_date + interval '1' day)`.

// TestTimeWindowInclusiveBothEdges pins that a heartbeat at exactly the start
// AND a heartbeat at exactly the end both COUNT for inclusive-both aggregations.
// This is the shared invariant across GetUserActivity/GetCategoryDaily/GetPunchcard/
// GetSessions/GetMomentum: `time_sent >= $2 AND time_sent <= $3`.
func TestTimeWindowInclusiveBothEdges(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "twin_incl")
	sender := f.Sender()
	ctx := f.Ctx()
	f.Projects("P")

	// Range spanning exactly [start, end]. Seed a break + 2 attributed:
	// - break at start (0 attributed, but it IS a row inside the window)
	// - attributed at start+1min (100s)
	// - attributed at end                (100s)
	// If the query used `time_sent < $3` (exclusive end), the end row would
	// disappear and the total would collapse to 100 instead of 200.
	start := time.Date(2025, 4, 10, 8, 0, 0, 0, time.UTC)
	end := time.Date(2025, 4, 10, 12, 0, 0, 0, time.UTC)

	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go",
		category: "Coding", ts: start, gap: 999999}) // break at start edge
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go",
		category: "Coding", ts: start.Add(time.Minute), gap: 100})
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go",
		category: "Coding", ts: end, gap: 100}) // exactly at end edge

	rows, err := d.GetUserActivity(ctx, sender, start, end, 15,
		HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := totalStatSeconds(rows); got != 200 {
		t.Fatalf("[GetUserActivity] total = %d, want 200 (both edges must be inclusive)", got)
	}

	cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15,
		HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	var catTot int64
	for _, c := range cats {
		catTot += c.TotalSeconds
	}
	if catTot != 200 {
		t.Fatalf("[GetCategoryDaily] total = %d, want 200 (both edges inclusive)", catTot)
	}

	punch, err := d.GetPunchcard(ctx, sender, start, end, 15,
		HiddenSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := sumPunch(punch); got != 200 {
		t.Fatalf("[GetPunchcard] total = %d, want 200 (both edges inclusive)", got)
	}

	sess, err := d.GetSessions(ctx, sender, start, end, 15,
		HiddenSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := sumSessions(sess); got != 200 {
		t.Fatalf("[GetSessions] total = %d, want 200 (both edges inclusive)", got)
	}

	mom, err := d.GetMomentum(ctx, sender, start, end, 15,
		HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := sumMomentum(mom); got != 200 {
		t.Fatalf("[GetMomentum] total = %d, want 200 (both edges inclusive)", got)
	}
}

// TestTimeWindowExcludesOutside pins that heartbeats one second OUTSIDE the
// [start, end] window are excluded. Complements the inclusive-edge test: guards
// against the reverse drift (widening the predicate to `<= end + 1s`).
func TestTimeWindowExcludesOutside(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "twin_excl")
	sender := f.Sender()
	ctx := f.Ctx()
	f.Projects("P")

	start := time.Date(2025, 4, 11, 8, 0, 0, 0, time.UTC)
	end := time.Date(2025, 4, 11, 12, 0, 0, 0, time.UTC)

	// Attributed row BEFORE start, and AFTER end — must NOT count.
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go",
		category: "Coding", ts: start.Add(-time.Second), gap: 100})
	// Attributed inside window (single row, 100s).
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go",
		category: "Coding", ts: start, gap: 999999}) // break
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go",
		category: "Coding", ts: start.Add(time.Hour), gap: 100})
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go",
		category: "Coding", ts: end.Add(time.Second), gap: 100})

	rows, err := d.GetUserActivity(ctx, sender, start, end, 15,
		HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := totalStatSeconds(rows); got != 100 {
		t.Fatalf("[GetUserActivity] total = %d, want 100 (out-of-window rows must be dropped)", got)
	}
}

// TestTimeWindowTimelineExclusiveEnd pins GetTimeline's half-open [start, end)
// window — the SQL uses `time_sent < $3`. A row at exactly `end` must NOT
// appear. This differs from every other aggregation; drift toward `<= end`
// would silently double-count the boundary tick against the next window.
func TestTimeWindowTimelineExclusiveEnd(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "twin_tl")
	sender := f.Sender()
	ctx := f.Ctx()
	f.Projects("P")

	start := time.Date(2025, 4, 12, 8, 0, 0, 0, time.UTC)
	end := time.Date(2025, 4, 12, 12, 0, 0, 0, time.UTC)

	// A row AT start, inside window, and AT end (the exclusive edge).
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", language: "Go", entity: "a.go",
		ts: start, gap: 100})
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", language: "Go", entity: "a.go",
		ts: start.Add(time.Hour), gap: 100})
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", language: "Go", entity: "a.go",
		ts: end, gap: 100})

	tl, err := d.GetTimeline(ctx, sender, start, end, 15, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}

	// Query the same window with end shifted forward by 1s — the end-boundary
	// row must now appear. If both counts match, the boundary is inclusive
	// (behavior drift) — this test fails.
	tl2, err := d.GetTimeline(ctx, sender, start, end.Add(time.Second), 15, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}

	if len(tl) == len(tl2) {
		t.Fatalf("timeline end edge is inclusive (rows same for [start,end)=%d and [start,end+1s)=%d) — must be exclusive per get_timeline.sql (`time_sent < $3`)",
			len(tl), len(tl2))
	}
}
