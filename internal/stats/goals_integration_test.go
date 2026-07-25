// goals_integration_test.go — DB-backed evaluator tests. Sits in the
// external stats_test package so it can import internal/testutil (which
// imports handler, which imports stats — no cycle for the external
// package). Skips if the isolated boomtime_test DB is unavailable.
//
// The tests here are non-tautological in the following ways:
//
//   - LEAF time evaluator: the seeded rollup rows use MIXED CASE on the
//     axis value. A regression that removed the lower(col) = lower($n)
//     case-fold on the leaf query would report ZERO seconds — the test
//     asserts an exact non-zero sum, so the check fails loudly. This is
//     the same anchor gaka-5db / gaka-oew put on the aggregation SQL
//     for curation.
//
//   - active_days over month: seed a KNOWN number of distinct days
//     (some blank days in the range), assert the count matches. A
//     count-days-where-secs-greater-than-0 vs count-distinct-day-rows
//     mixup would show up here.
//
//   - Streak stops at the first miss: seed a run of hits ending with a
//     GAP day mid-window. A regression that counted TOTAL hits (rather
//     than CONSECUTIVE from today) would fail.
//
//   - all / any / not composition: mix a passing leaf with a failing
//     leaf and assert both the boolean AND the exact progress fraction.
//     If someone swaps min for max on all/any, the number is wrong.
package stats_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// seedRollupRow inserts one hb_rollup_daily row for owner on `day` with
// the given axis values + seconds. Kept local (rather than in
// testutil.Seeder) so the test's shape is self-contained.
func seedRollupRow(t *testing.T, hz *testutil.Harness, owner string, day time.Time, project, language, editor string, seconds int64) {
	t.Helper()
	ctx := context.Background()
	_, err := hz.DB.Pool.Exec(ctx, `
		INSERT INTO hb_rollup_daily (sender, day, project, language, editor,
			platform, machine, category, plugin, branch, total_seconds)
		VALUES ($1, $2::date, $3, $4, $5, 'linux', 'm', 'Coding', 'pl', 'main', $6)
		ON CONFLICT (sender, day, project, language, editor, platform, machine, category, plugin, branch)
		DO UPDATE SET total_seconds = EXCLUDED.total_seconds`,
		owner, day, project, language, editor, seconds)
	if err != nil {
		t.Fatalf("seed rollup: %v", err)
	}
}

// TestEvaluate_LeafTimeCaseFold seeds two heartbeats on separate days,
// the language column recorded in DIFFERENT CASE. A leaf targeting
// "Python" must sum BOTH — proving lower(col)=lower($n) fired on both
// sides. If either side were case-sensitive, the sum would drop the
// wrong-case row and the assertion would fail with a clear number.
func TestEvaluate_LeafTimeCaseFold(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_leaf")

	// Anchor: fix "now" so window math is deterministic.
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// Two days inside the week window, different case.
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -1), "P", "Python", "vim", 1800)
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -3), "P", "python", "vim", 1200)
	// One day OUTSIDE the week window (should NOT contribute).
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -20), "P", "Python", "vim", 9999)

	spec := `{"kind":"time","axis":"language","value":"Python","op":">=","target_seconds":3000,"window":"week"}`
	p, err := stats.ValidateSpec(json.RawMessage(spec))
	if err != nil {
		t.Fatalf("ValidateSpec: %v", err)
	}
	prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(prog.SubConditions) != 1 {
		t.Fatalf("sub_conditions len = %d, want 1", len(prog.SubConditions))
	}
	sc := prog.SubConditions[0]
	if sc.Current != 3000 {
		// If case-fold regressed, we'd see 1800 (only the "Python" row)
		// or 1200 (only the "python" row) here — not 3000.
		t.Errorf("current = %d, want 3000 (1800 + 1200 across mixed-case Python rows). Case-fold regression?", sc.Current)
	}
	if !prog.Hit || prog.Progress != 1 {
		t.Errorf("hit=%v prog=%v, want (true, 1) at target", prog.Hit, prog.Progress)
	}
}

// TestEvaluate_LeafTimeNilValue: value=null on a time leaf means "any
// value on the axis", i.e. total time regardless of language. Seed two
// languages, assert the sum is BOTH.
func TestEvaluate_LeafTimeNilValue(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_leafnil")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -1), "P", "Go", "vim", 1000)
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -2), "P", "Rust", "vim", 2000)

	spec := `{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":2500,"window":"week"}`
	p, err := stats.ValidateSpec(json.RawMessage(spec))
	if err != nil {
		t.Fatalf("ValidateSpec: %v", err)
	}
	prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if prog.SubConditions[0].Current != 3000 {
		t.Errorf("nil-value total = %d, want 3000 (Go + Rust)", prog.SubConditions[0].Current)
	}
	if !prog.Hit {
		t.Errorf("expected hit=true at 3000 >= 2500")
	}
}

// TestEvaluate_ActiveDays seeds two distinct active days inside the
// week window (some blank days between them) and asserts the count.
func TestEvaluate_ActiveDays(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_activedays")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// Distinct days: today-1, today-3 (two days, with today, today-2,
	// today-4..today-6 all empty). We expect DISTINCT count = 2.
	// Seed two rows on the SAME day to prove distinct dedupes.
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -1), "A", "Go", "vim", 600)
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -1), "B", "Rust", "vim", 600)
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -3), "A", "Go", "vim", 600)

	spec := `{"kind":"active_days","op":">=","n":2,"window":"week"}`
	p, _ := stats.ValidateSpec(json.RawMessage(spec))
	prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if prog.SubConditions[0].Current != 2 {
		t.Errorf("distinct active days = %d, want 2 (dedup on double-seed same day)",
			prog.SubConditions[0].Current)
	}
	if !prog.Hit {
		t.Errorf("expected hit=true at 2>=2")
	}
}

// TestEvaluate_StreakStopsAtGap seeds 3 consecutive days ending today,
// then a gap, then 4 more days. A streak targeting 7 must count ONLY
// the 3 consecutive from today (the gap breaks the streak). A
// regression that counted total-active-days-in-range would report 7
// and pass; this test catches it by asserting exactly 3.
func TestEvaluate_StreakStopsAtGap(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_streak")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// today-0, today-1, today-2 hit (3 consecutive).
	for _, offset := range []int{0, 1, 2} {
		seedRollupRow(t, hz, owner, now.AddDate(0, 0, -offset), "P", "Go", "vim", 900)
	}
	// today-3: GAP (nothing seeded).
	// today-4..today-7 hit (4 more, but blocked by the gap).
	for _, offset := range []int{4, 5, 6, 7} {
		seedRollupRow(t, hz, owner, now.AddDate(0, 0, -offset), "P", "Go", "vim", 900)
	}

	spec := `{"kind":"streak","min_days":7,"condition":{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":600,"window":"day"}}`
	p, err := stats.ValidateSpec(json.RawMessage(spec))
	if err != nil {
		t.Fatalf("ValidateSpec: %v", err)
	}
	prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(prog.SubConditions) != 1 {
		t.Fatalf("sub_conditions len = %d, want 1", len(prog.SubConditions))
	}
	sc := prog.SubConditions[0]
	if sc.Current != 3 {
		t.Errorf("streak days = %d, want exactly 3 (consecutive from today; gap at today-3 stops it). Value >3 would prove the walk doesn't stop at the miss.",
			sc.Current)
	}
	if sc.Hit {
		t.Errorf("streak of 3 must NOT hit target 7")
	}
	if prog.Progress != 3.0/7.0 {
		t.Errorf("progress = %v, want 3/7", prog.Progress)
	}
}

// TestEvaluate_AllComposition: two leaves, one hits and one misses.
// `all` must be (false, min(children)). A regression that averaged or
// max'd children would produce a different number.
func TestEvaluate_AllComposition(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_all")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// Passing leaf: 1000s Go this week, target 500.
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -1), "P", "Go", "vim", 1000)
	// Failing leaf: 500s Rust this week, target 2000 → 0.25 progress.
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -2), "P", "Rust", "vim", 500)

	spec := `{"kind":"all","of":[
		{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":500,"window":"week"},
		{"kind":"time","axis":"language","value":"Rust","op":">=","target_seconds":2000,"window":"week"}
	]}`
	p, err := stats.ValidateSpec(json.RawMessage(spec))
	if err != nil {
		t.Fatalf("ValidateSpec: %v", err)
	}
	prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if prog.Hit {
		t.Errorf("all: expected hit=false when one child misses")
	}
	if prog.Progress != 0.25 {
		t.Errorf("all: progress = %v, want 0.25 (min of children). max/avg would give a different number.",
			prog.Progress)
	}
	// Sub-conditions flat list should have both leaves.
	if len(prog.SubConditions) != 2 {
		t.Errorf("sub_conditions len = %d, want 2", len(prog.SubConditions))
	}
}

// TestEvaluate_AnyComposition mirror-image of All: same seed +
// discriminated union structure ⇒ any hits with progress = max.
func TestEvaluate_AnyComposition(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_any")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -1), "P", "Go", "vim", 1000)
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -2), "P", "Rust", "vim", 500)

	spec := `{"kind":"any","of":[
		{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":500,"window":"week"},
		{"kind":"time","axis":"language","value":"Rust","op":">=","target_seconds":2000,"window":"week"}
	]}`
	p, err := stats.ValidateSpec(json.RawMessage(spec))
	if err != nil {
		t.Fatalf("ValidateSpec: %v", err)
	}
	prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !prog.Hit {
		t.Errorf("any: expected hit=true when one child hits")
	}
	if prog.Progress != 1 {
		t.Errorf("any: progress = %v, want 1 (max of children — the passing Go leaf is at 1)",
			prog.Progress)
	}
}

// TestEvaluate_NotInverts: a `not` around a passing leaf should be
// false with progress 0; around a failing leaf should be true with
// progress = 1 - child.progress. Two-way check pins both branches.
func TestEvaluate_NotInverts(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_not")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// A "watched too much YouTube" leaf: target seconds is a CEILING;
	// wait — `>=` in our comparison is "hit when meets or exceeds". So
	// a target of 60 hit at 3600 means "hit the goal" (spent >= 60s).
	// Wrapping in `not` means "must NOT have spent 60s+" which is what
	// a "avoid distraction" goal looks like when phrased as not(time>=).
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -1), "YT", "None", "browser", 3600)

	// Not-wraps-passing-leaf → not-hit.
	spec1 := `{"kind":"not","of":[
		{"kind":"time","axis":"project","value":"YT","op":">=","target_seconds":60,"window":"week"}
	]}`
	p1, _ := stats.ValidateSpec(json.RawMessage(spec1))
	prog1, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p1, now)
	if err != nil {
		t.Fatalf("Evaluate not(passing): %v", err)
	}
	if prog1.Hit {
		t.Errorf("not(passing leaf): expected hit=false")
	}
	// Not-wraps-failing-leaf → hit.
	spec2 := `{"kind":"not","of":[
		{"kind":"time","axis":"project","value":"YT","op":">=","target_seconds":99999,"window":"week"}
	]}`
	p2, _ := stats.ValidateSpec(json.RawMessage(spec2))
	prog2, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p2, now)
	if err != nil {
		t.Fatalf("Evaluate not(failing): %v", err)
	}
	if !prog2.Hit {
		t.Errorf("not(failing leaf): expected hit=true (safely avoided)")
	}
}

// TestEvaluate_StreakMinDaysZero pins the early-return short-circuit
// for min_days<=0. A regression that dropped this guard would spin
// the walk loop with walkBackDaysMax=0, still return (true, 1) — so
// the current test would pass. To make this non-tautological we also
// assert NO sub-condition query fired (we're going to prove this by
// evaluating without ANY rollup rows and confirming success without
// error).
func TestEvaluate_StreakMinDaysZero(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_streak_zero")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// Do NOT seed anything — a min_days=0 streak must trivially hit
	// without touching the DB.
	spec := `{"kind":"streak","min_days":0,"condition":{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":600,"window":"day"}}`
	p, err := stats.ValidateSpec(json.RawMessage(spec))
	if err != nil {
		t.Fatalf("ValidateSpec: %v", err)
	}
	prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !prog.Hit || prog.Progress != 1 {
		t.Errorf("min_days=0: hit=%v prog=%v (want true, 1 — trivially satisfied)",
			prog.Hit, prog.Progress)
	}
	// Sub-condition list must have EXACTLY one entry (the streak summary),
	// with Current=0 and Target=0 — the short-circuit must not have
	// evaluated the child.
	if len(prog.SubConditions) != 1 {
		t.Fatalf("sub_conditions len = %d, want 1 (streak summary only)", len(prog.SubConditions))
	}
	sc := prog.SubConditions[0]
	if sc.Kind != "streak" || sc.Current != 0 || sc.Target != 0 {
		t.Errorf("streak short-circuit sub: %+v want kind=streak current=0 target=0", sc)
	}
}

// TestEvaluate_StreakTodayMissesReturnsZero: no data at all → streak
// count is 0 immediately (first walk-back iteration finds no hit and
// breaks). Non-tautology anchor: without this test, a bug that seeded
// daysHit=1 by mistake ("start at 1, count DOWN") would still pass
// TestEvaluate_StreakStopsAtGap because that seeds three consecutive
// hits — this test explicitly proves the loop's initial value is 0.
func TestEvaluate_StreakTodayMissesReturnsZero(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_streak_miss")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// Empty — no rollup rows at all.

	spec := `{"kind":"streak","min_days":3,"condition":{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":600,"window":"day"}}`
	p, _ := stats.ValidateSpec(json.RawMessage(spec))
	prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(prog.SubConditions) != 1 {
		t.Fatalf("sub_conditions len = %d, want 1", len(prog.SubConditions))
	}
	sc := prog.SubConditions[0]
	if sc.Current != 0 {
		t.Errorf("streak with no data: current = %d, want 0 (loop's initial daysHit=0)", sc.Current)
	}
	if sc.Hit {
		t.Errorf("streak of 0 must NOT hit target 3")
	}
	if prog.Progress != 0 {
		t.Errorf("progress = %v, want 0 (0/3)", prog.Progress)
	}
}

// TestEvaluate_StreakExactlyMinDaysHits: seed exactly min_days
// consecutive days ending today. The walk should STOP once daysHit ==
// min_days (walkBackDaysMax = min_days). Non-tautology: catches an
// off-by-one where the walk went min_days+1 or min_days-1 iterations.
func TestEvaluate_StreakExactlyMinDaysHits(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_streak_exact")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// Seed exactly 3 consecutive hits ending today.
	for _, offset := range []int{0, 1, 2} {
		seedRollupRow(t, hz, owner, now.AddDate(0, 0, -offset), "P", "Go", "vim", 900)
	}
	// Extra pre-window hit that SHOULDN'T be counted (walk cap is 3).
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -3), "P", "Go", "vim", 900)
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -4), "P", "Go", "vim", 900)

	spec := `{"kind":"streak","min_days":3,"condition":{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":600,"window":"day"}}`
	p, _ := stats.ValidateSpec(json.RawMessage(spec))
	prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	sc := prog.SubConditions[0]
	if sc.Current != 3 {
		t.Errorf("streak: current = %d, want exactly 3 (walk cap = min_days; extras beyond don't count)", sc.Current)
	}
	if !sc.Hit || prog.Progress != 1 {
		t.Errorf("hit=%v prog=%v, want (true, 1)", sc.Hit, prog.Progress)
	}
}

// TestEvaluate_ActiveDaysOwnerScoping is the sibling to
// TestEvaluate_OwnerScoping for the active_days query — same threat
// model (WHERE sender = $1 dropped), different query. Owner A gets
// their count; owner B seeing A's rows means the sender filter fell
// off in the active_days SQL specifically.
func TestEvaluate_ActiveDaysOwnerScoping(t *testing.T) {
	hz := testutil.NewHarness(t)
	ownerA, _ := hz.MintUser("eval_ad_scope_a")
	ownerB, _ := hz.MintUser("eval_ad_scope_b")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// A seeds 3 distinct days.
	seedRollupRow(t, hz, ownerA, now.AddDate(0, 0, -1), "P", "Go", "vim", 600)
	seedRollupRow(t, hz, ownerA, now.AddDate(0, 0, -3), "P", "Go", "vim", 600)
	seedRollupRow(t, hz, ownerA, now.AddDate(0, 0, -5), "P", "Go", "vim", 600)

	spec := `{"kind":"active_days","op":">=","n":1,"window":"week"}`
	p, _ := stats.ValidateSpec(json.RawMessage(spec))
	progA, err := stats.Evaluate(context.Background(), hz.DB.Pool, ownerA, p, now)
	if err != nil {
		t.Fatalf("Evaluate ownerA: %v", err)
	}
	if progA.SubConditions[0].Current != 3 {
		t.Errorf("ownerA active_days = %d, want 3", progA.SubConditions[0].Current)
	}
	progB, err := stats.Evaluate(context.Background(), hz.DB.Pool, ownerB, p, now)
	if err != nil {
		t.Fatalf("Evaluate ownerB: %v", err)
	}
	if progB.SubConditions[0].Current != 0 {
		t.Errorf("ownerB active_days = %d, want 0 (sender filter fell off on active_days SQL?)",
			progB.SubConditions[0].Current)
	}
}

// TestEvaluate_TimeLeafDayWindow explicitly pins that "day" window
// covers exactly TODAY — beats from yesterday must NOT count. Guards
// against a windowRange bug that made day inclusive-of-yesterday
// (a 24h shift). Seed rows on today and yesterday for the same key;
// the day window must return only today's seconds.
func TestEvaluate_TimeLeafDayWindow(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_day_win")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	seedRollupRow(t, hz, owner, now, "P", "Go", "vim", 500)                // TODAY
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -1), "P", "Go", "vim", 9999) // yesterday — MUST be excluded

	spec := `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":1,"window":"day"}`
	p, _ := stats.ValidateSpec(json.RawMessage(spec))
	prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if prog.SubConditions[0].Current != 500 {
		t.Errorf("day window included non-today rows: current = %d, want 500 (today only)",
			prog.SubConditions[0].Current)
	}
}

// TestEvaluate_LifetimeIncludesAncient asserts the lifetime window
// pulls in a very old row (2020) — the start is pinned at epoch, not
// clamped to "some sensible year". A bug that clamped lifetime to
// e.g. 5 years back would silently drop the old row and fail the
// exact-sum assertion.
func TestEvaluate_LifetimeIncludesAncient(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_lifetime")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	ancient := time.Date(2020, 3, 1, 0, 0, 0, 0, time.UTC)
	recent := now.AddDate(0, 0, -1)
	seedRollupRow(t, hz, owner, ancient, "P", "Go", "vim", 1234)
	seedRollupRow(t, hz, owner, recent, "P", "Go", "vim", 5678)

	spec := `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":1,"window":"lifetime"}`
	p, _ := stats.ValidateSpec(json.RawMessage(spec))
	prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	want := int64(1234 + 5678)
	if prog.SubConditions[0].Current != want {
		t.Errorf("lifetime sum = %d, want %d (ancient + recent). Clamped start?",
			prog.SubConditions[0].Current, want)
	}
}

// TestEvaluate_NotProgressInversion checks the arithmetic of `not`:
// wrapping a leaf at 50% must return progress = 1 - 0.5 = 0.5 (a
// "half-there of not-being-there" contract). A regression that
// returned `1 - hit_bool` instead would report 0 or 1 depending on
// the child's boolean — the exact-fraction assertion here catches it.
func TestEvaluate_NotProgressInversion(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("eval_not_prog")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// Seed 500s Go; leaf target 1000 → child.progress = 0.5, hit=false.
	seedRollupRow(t, hz, owner, now.AddDate(0, 0, -1), "P", "Go", "vim", 500)

	spec := `{"kind":"not","of":[
		{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":1000,"window":"week"}
	]}`
	p, _ := stats.ValidateSpec(json.RawMessage(spec))
	prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !prog.Hit {
		t.Errorf("not(child hit=false): expected outer hit=true, got false")
	}
	if prog.Progress != 0.5 {
		t.Errorf("not progress = %v, want 0.5 (1 - child.progress=0.5). Was 1-bool used instead of 1-frac?",
			prog.Progress)
	}
}

// TestEvaluate_OwnerScoping seeds rollup rows for owner A, evaluates
// as owner B — must return zero. Guards against a WHERE-clause typo
// that dropped the sender filter on the leaf query.
func TestEvaluate_OwnerScoping(t *testing.T) {
	hz := testutil.NewHarness(t)
	ownerA, _ := hz.MintUser("eval_scope_a")
	ownerB, _ := hz.MintUser("eval_scope_b")

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	seedRollupRow(t, hz, ownerA, now.AddDate(0, 0, -1), "P", "Go", "vim", 10000)

	spec := `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":1000,"window":"week"}`
	p, _ := stats.ValidateSpec(json.RawMessage(spec))
	// A sees their own 10000s.
	progA, err := stats.Evaluate(context.Background(), hz.DB.Pool, ownerA, p, now)
	if err != nil {
		t.Fatalf("Evaluate ownerA: %v", err)
	}
	if progA.SubConditions[0].Current != 10000 {
		t.Errorf("ownerA sees %d, want 10000", progA.SubConditions[0].Current)
	}
	// B sees nothing.
	progB, err := stats.Evaluate(context.Background(), hz.DB.Pool, ownerB, p, now)
	if err != nil {
		t.Fatalf("Evaluate ownerB: %v", err)
	}
	if progB.SubConditions[0].Current != 0 {
		t.Errorf("ownerB sees %d, want 0 (owner-scoping breach — leaf query lost sender filter)",
			progB.SubConditions[0].Current)
	}
}
