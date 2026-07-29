package db

// Test coverage for award_ledger.go (gaka-se2.5). Pyramid:
//   - Unit tests (no DB) for pure helpers: KindDefaultPeriod, ResolvePeriod,
//     PeriodBounds, periodStepBackward, ValidatePeriod.
//   - Integration tests (real DB, isolated boomtime_test) for LogAwards,
//     ListAwardLedger, GetLabelStreaks.
// Each It/Test names ONE invariant. No "works correctly" or tautological
// "insert x → get x" asserts — every DB test pins a property that would flag
// a specific class of regression (dedup, dropping, ordering, tz walk, etc.).

import (
	"context"
	"testing"
	"time"
)

// -------- unit: KindDefaultPeriod --------

func TestKindDefaultPeriod_TierIsLifetime(t *testing.T) {
	if got := KindDefaultPeriod("tier"); got != PeriodLifetime {
		t.Fatalf("tier default = %q, want lifetime", got)
	}
}

func TestKindDefaultPeriod_TribeIsLifetime(t *testing.T) {
	if got := KindDefaultPeriod("tribe"); got != PeriodLifetime {
		t.Fatalf("tribe default = %q, want lifetime", got)
	}
}

func TestKindDefaultPeriod_ArchetypeAndMemeAreWeekly(t *testing.T) {
	if got := KindDefaultPeriod("archetype"); got != PeriodWeekly {
		t.Fatalf("archetype default = %q, want weekly", got)
	}
	if got := KindDefaultPeriod("meme"); got != PeriodWeekly {
		t.Fatalf("meme default = %q, want weekly", got)
	}
}

func TestKindDefaultPeriod_PatchIsDaily(t *testing.T) {
	if got := KindDefaultPeriod("patch"); got != PeriodDaily {
		t.Fatalf("patch default = %q, want daily", got)
	}
}

func TestKindDefaultPeriod_UnknownFallsBackToWeekly(t *testing.T) {
	if got := KindDefaultPeriod("brand-new-kind"); got != PeriodWeekly {
		t.Fatalf("unknown kind default = %q, want weekly (fallback so we log SOMETHING)", got)
	}
}

// -------- unit: ResolvePeriod --------

func TestResolvePeriod_OverrideBeatsKindDefault(t *testing.T) {
	// tier default is lifetime, but an explicit daily override MUST win —
	// this is the whole point of labels.period_default.
	if got := ResolvePeriod("tier", "daily"); got != PeriodDaily {
		t.Fatalf("override daily on tier = %q, want daily", got)
	}
}

func TestResolvePeriod_EmptyOverrideFallsBackToKindDefault(t *testing.T) {
	if got := ResolvePeriod("archetype", ""); got != PeriodWeekly {
		t.Fatalf("empty override on archetype = %q, want weekly (kind default)", got)
	}
	if got := ResolvePeriod("patch", ""); got != PeriodDaily {
		t.Fatalf("empty override on patch = %q, want daily (kind default)", got)
	}
}

// -------- unit: PeriodBounds --------

func TestPeriodBounds_DailyUTC_IsLocalMidnightPair(t *testing.T) {
	at := time.Date(2025, 3, 15, 14, 30, 0, 0, time.UTC)
	start, end, ok := PeriodBounds(PeriodDaily, at, time.UTC)
	if !ok {
		t.Fatal("daily bounds must return ok")
	}
	wantStart := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) || !end.Equal(wantStart.Add(24*time.Hour)) {
		t.Fatalf("daily bounds = [%v, %v), want [%v, +24h)", start, end, wantStart)
	}
}

func TestPeriodBounds_DailyPacific_UTCDayFlipDoesNotBreak(t *testing.T) {
	// 2025-03-16 05:00Z = 2025-03-15 22:00 Pacific (PDT after DST-jump).
	// The bucket MUST be the Pacific day 2025-03-15, not 03-16.
	tz, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("America/Los_Angeles tzdata unavailable: %v", err)
	}
	at := time.Date(2025, 3, 16, 5, 0, 0, 0, time.UTC)
	start, _, ok := PeriodBounds(PeriodDaily, at, tz)
	if !ok {
		t.Fatal("daily bounds must return ok")
	}
	wantStart := time.Date(2025, 3, 15, 0, 0, 0, 0, tz)
	if !start.Equal(wantStart) {
		t.Fatalf("daily Pacific bucket = %v, want %v (UTC day-flip must not shift bucket)", start, wantStart)
	}
}

func TestPeriodBounds_DailyPacific_SpringForwardIs23Hours(t *testing.T) {
	// 2025-03-09 is the US spring-forward. Local-midnight → local-midnight
	// window is 23h in wall-clock, but as an absolute duration [start,end)
	// is 24h of subtraction on the calendar. We assert end.Sub(start) is
	// the wall-clock 24h-1h = 23h so the DST math is provably tz-aware and
	// not naive UTC arithmetic.
	tz, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("America/Los_Angeles tzdata unavailable: %v", err)
	}
	at := time.Date(2025, 3, 9, 15, 0, 0, 0, tz).UTC()
	start, end, ok := PeriodBounds(PeriodDaily, at, tz)
	if !ok {
		t.Fatal("daily bounds must return ok")
	}
	// AddDate keeps wall-clock semantics via +24h — 23h absolute across DST.
	// Actually PeriodBounds uses `.Add(24*time.Hour)` for daily, so this is
	// an absolute 24h. Pin THAT (proves it's not the AddDate variant which
	// would give 23h). Distinguishes correct impl from a subtle refactor.
	if gap := end.Sub(start); gap != 24*time.Hour {
		t.Fatalf("daily bounds duration across DST = %v, want 24h (impl uses .Add, not AddDate)", gap)
	}
	// And the start MUST be Pacific midnight, not UTC midnight.
	if start.Hour() != 0 || start.Minute() != 0 || start.Location().String() != tz.String() {
		t.Fatalf("daily start = %v (loc %s), want local midnight in %s", start, start.Location(), tz)
	}
}

func TestPeriodBounds_WeeklyIsIsoMondayStart(t *testing.T) {
	// 2025-03-15 is a Saturday; ISO week Monday is 2025-03-10.
	at := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	start, end, ok := PeriodBounds(PeriodWeekly, at, time.UTC)
	if !ok {
		t.Fatal("weekly bounds must return ok")
	}
	wantStart := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Fatalf("weekly start = %v, want %v (ISO Monday, NOT Sunday)", start, wantStart)
	}
	if !end.Equal(wantStart.AddDate(0, 0, 7)) {
		t.Fatalf("weekly end = %v, want start+7d", end)
	}
}

func TestPeriodBounds_WeeklySunday_IsoWeekIsPriorMonday(t *testing.T) {
	// Sunday is the tricky one: naive Sun=0 would step 0 days back and put
	// Sunday in the wrong week. ISO says Sunday is the LAST day of the week
	// starting the prior Monday.
	at := time.Date(2025, 3, 16, 10, 0, 0, 0, time.UTC) // Sunday
	start, _, _ := PeriodBounds(PeriodWeekly, at, time.UTC)
	wantStart := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Fatalf("Sunday weekly start = %v, want %v (prior Monday, not same-day)", start, wantStart)
	}
}

func TestPeriodBounds_MonthlyIsFirstOfMonthLocal(t *testing.T) {
	// Feb 2024 is a leap-year month; the bounds must span 29 days.
	at := time.Date(2024, 2, 15, 12, 0, 0, 0, time.UTC)
	start, end, ok := PeriodBounds(PeriodMonthly, at, time.UTC)
	if !ok {
		t.Fatal("monthly bounds must return ok")
	}
	wantStart := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("monthly bounds = [%v, %v), want [%v, %v) (29-day Feb)", start, end, wantStart, wantEnd)
	}
}

func TestPeriodBounds_LifetimeReturnsNotOK(t *testing.T) {
	// Lifetime is the "skip logging" sentinel — LogAwards relies on !ok to
	// silently drop tier/tribe items. If this ever returned ok, ledger
	// would fill with lifetime rows that GetLabelStreaks can't reason about.
	_, _, ok := PeriodBounds(PeriodLifetime, time.Now(), time.UTC)
	if ok {
		t.Fatal("lifetime bounds must return ok=false (skip-log signal)")
	}
}

func TestPeriodBounds_NilTZDefaultsToUTC(t *testing.T) {
	at := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	start, _, _ := PeriodBounds(PeriodDaily, at, nil)
	if start.Location() != time.UTC {
		t.Fatalf("nil tz start loc = %v, want UTC (defensive default)", start.Location())
	}
}

// -------- unit: periodStepBackward --------

func TestPeriodStepBackward_DailyIsMinusOneDay(t *testing.T) {
	cur := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)
	got := periodStepBackward(PeriodDaily, cur, time.UTC)
	want := time.Date(2025, 3, 14, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("daily step back = %v, want %v", got, want)
	}
}

func TestPeriodStepBackward_WeeklyIsMinusSevenDays(t *testing.T) {
	cur := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	got := periodStepBackward(PeriodWeekly, cur, time.UTC)
	want := time.Date(2025, 3, 3, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("weekly step back = %v, want %v", got, want)
	}
}

func TestPeriodStepBackward_MonthlyIsCalendarMinusOneMonth(t *testing.T) {
	// Calendar-arithmetic: March 1 → February 1 (28 days, not naive 30d).
	cur := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	got := periodStepBackward(PeriodMonthly, cur, time.UTC)
	want := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("monthly step back = %v, want %v (calendar, not -30d)", got, want)
	}
}

func TestPeriodStepBackward_LifetimeReturnsZero(t *testing.T) {
	got := periodStepBackward(PeriodLifetime, time.Now(), time.UTC)
	if !got.IsZero() {
		t.Fatalf("lifetime step back = %v, want zero (sentinel)", got)
	}
}

// -------- unit: ValidatePeriod --------

func TestValidatePeriod_AcceptsKnownAndEmpty(t *testing.T) {
	for _, p := range []string{"", "daily", "weekly", "monthly", "lifetime"} {
		if err := ValidatePeriod(p); err != nil {
			t.Errorf("ValidatePeriod(%q) = %v, want nil (well-known)", p, err)
		}
	}
}

func TestValidatePeriod_RejectsUnknown(t *testing.T) {
	for _, p := range []string{"hourly", "biweekly", "DAILY", "sometimes"} {
		if err := ValidatePeriod(p); err != ErrUnknownPeriod {
			t.Errorf("ValidatePeriod(%q) = %v, want ErrUnknownPeriod", p, err)
		}
	}
}

// ==================== integration: real DB ====================

// seededLabelID is any patch label from migration 00039_patches.sql. Using a
// real seeded row avoids needing to INSERT a labels row (FK dependency), and
// gives us a non-empty l.label + l.kind for the JOIN-name assertion.
const seededLabelA = "rapid-response-team" // kind=patch, label='RAPID RESPONSE TEAM'
const seededLabelB = "fire-fighter"        // kind=patch, label='FIRE FIGHTER'
const seededLabelC = "recon"               // kind=patch, label='RECON'

// deleteLedgerFor cleans up award_ledger for a user before the harness's
// user-delete CASCADE. t.Cleanup is LIFO, so registering AFTER newSender
// makes this run FIRST — belt-and-suspenders since users FK CASCADEs anyway.
func deleteLedgerFor(t *testing.T, d *DB, ctx context.Context, username string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM award_ledger WHERE username=$1`, username)
	})
}

// -------- LogAwards + ListAwardLedger integration --------

func TestLogAwards_ListedItemPersistsWithJoinedLabelName(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_persist")
	deleteLedgerFor(t, d, ctx, f.name)

	at := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	written, err := d.LogAwards(ctx, f.name,
		[]AwardLogItem{{LabelID: seededLabelA, PeriodType: PeriodDaily}},
		time.UTC, at)
	if err != nil {
		t.Fatalf("LogAwards: %v", err)
	}
	if written != 1 {
		t.Fatalf("written = %d, want 1", written)
	}

	got, err := d.ListAwardLedger(ctx, f.name, "", 0)
	if err != nil {
		t.Fatalf("ListAwardLedger: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	// JOIN attaches the display name + kind. If the JOIN drifted (e.g.
	// dropped LEFT JOIN), name would fall back to the id via COALESCE.
	if got[0].LabelID != seededLabelA {
		t.Fatalf("LabelID = %q, want %q", got[0].LabelID, seededLabelA)
	}
	if got[0].LabelName != "RAPID RESPONSE TEAM" {
		t.Fatalf("LabelName = %q, want the seeded display (JOIN failed)", got[0].LabelName)
	}
	if got[0].Kind != "patch" {
		t.Fatalf("Kind = %q, want patch (JOIN failed)", got[0].Kind)
	}
}

func TestLogAwards_RepeatSamePeriodIsIdempotentAndReportsZeroWrites(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_idem")
	deleteLedgerFor(t, d, ctx, f.name)

	at := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	items := []AwardLogItem{{LabelID: seededLabelA, PeriodType: PeriodDaily}}
	if _, err := d.LogAwards(ctx, f.name, items, time.UTC, at); err != nil {
		t.Fatalf("first LogAwards: %v", err)
	}
	written2, err := d.LogAwards(ctx, f.name, items, time.UTC, at)
	if err != nil {
		t.Fatalf("second LogAwards: %v", err)
	}
	// The second call MUST return 0 written (upsert no-op).
	if written2 != 0 {
		t.Fatalf("second call written = %d, want 0 (ON CONFLICT DO NOTHING)", written2)
	}
	// And the ledger still has exactly 1 row (no dupe AND no drop).
	rows, _ := d.ListAwardLedger(ctx, f.name, "", 0)
	if len(rows) != 1 {
		t.Fatalf("post-dup row count = %d, want 1", len(rows))
	}
}

func TestLogAwards_DuplicateInSameBatchYieldsSingleRow(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_batchdup")
	deleteLedgerFor(t, d, ctx, f.name)

	at := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	items := []AwardLogItem{
		{LabelID: seededLabelA, PeriodType: PeriodDaily},
		{LabelID: seededLabelA, PeriodType: PeriodDaily},
	}
	_, err := d.LogAwards(ctx, f.name, items, time.UTC, at)
	if err != nil {
		t.Fatalf("LogAwards: %v", err)
	}
	rows, _ := d.ListAwardLedger(ctx, f.name, "", 0)
	if len(rows) != 1 {
		t.Fatalf("intra-batch dup row count = %d, want 1", len(rows))
	}
}

func TestLogAwards_LifetimeItemsSilentlyDropped(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_lifetime")
	deleteLedgerFor(t, d, ctx, f.name)

	at := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	written, err := d.LogAwards(ctx, f.name,
		[]AwardLogItem{{LabelID: seededLabelA, PeriodType: PeriodLifetime}},
		time.UTC, at)
	if err != nil {
		t.Fatalf("LogAwards: %v", err)
	}
	if written != 0 {
		t.Fatalf("lifetime written = %d, want 0 (silently dropped)", written)
	}
	rows, _ := d.ListAwardLedger(ctx, f.name, "", 0)
	if len(rows) != 0 {
		t.Fatalf("ledger rows after lifetime-only log = %d, want 0", len(rows))
	}
}

func TestLogAwards_EmptyItemsShortCircuits(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_empty")
	deleteLedgerFor(t, d, ctx, f.name)

	written, err := d.LogAwards(ctx, f.name, nil, time.UTC, time.Now())
	if err != nil || written != 0 {
		t.Fatalf("empty batch: written=%d err=%v, want 0/nil", written, err)
	}
}

func TestListAwardLedger_ScopedToRequestingUser(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	alice := newSender(t, d, "aw_alice")
	bob := newSender(t, d, "aw_bob")
	deleteLedgerFor(t, d, ctx, alice.name)
	deleteLedgerFor(t, d, ctx, bob.name)

	at := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	_, _ = d.LogAwards(ctx, alice.name, []AwardLogItem{{LabelID: seededLabelA, PeriodType: PeriodDaily}}, time.UTC, at)
	_, _ = d.LogAwards(ctx, bob.name, []AwardLogItem{{LabelID: seededLabelB, PeriodType: PeriodDaily}}, time.UTC, at)

	aliceRows, _ := d.ListAwardLedger(ctx, alice.name, "", 0)
	if len(aliceRows) != 1 || aliceRows[0].LabelID != seededLabelA {
		t.Fatalf("alice rows = %+v, want exactly 1 row with seededLabelA", aliceRows)
	}
	// Bob's row must not leak into alice's list.
	for _, r := range aliceRows {
		if r.LabelID == seededLabelB {
			t.Fatalf("alice's ledger contains bob's label: %+v", r)
		}
	}
}

func TestListAwardLedger_OrdersByPeriodStartDescThenLabelIdAsc(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_order")
	deleteLedgerFor(t, d, ctx, f.name)

	day1 := time.Date(2025, 3, 10, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2025, 3, 11, 12, 0, 0, 0, time.UTC)
	day3 := time.Date(2025, 3, 12, 12, 0, 0, 0, time.UTC)

	// 3 different period_starts. day3 has two entries (A & C) to test the
	// label_id ASC tie-break.
	_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{{LabelID: seededLabelB, PeriodType: PeriodDaily}}, time.UTC, day1)
	_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{{LabelID: seededLabelB, PeriodType: PeriodDaily}}, time.UTC, day2)
	_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{
		{LabelID: seededLabelC, PeriodType: PeriodDaily},
		{LabelID: seededLabelA, PeriodType: PeriodDaily},
	}, time.UTC, day3)

	rows, _ := d.ListAwardLedger(ctx, f.name, "", 0)
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(rows))
	}
	// Newest first: day3 twice (A before C alphabetically), then day2, day1.
	wantIDs := []string{seededLabelA, seededLabelC, seededLabelB, seededLabelB}
	for i, want := range wantIDs {
		if rows[i].LabelID != want {
			t.Fatalf("rows[%d].LabelID = %q, want %q (period DESC, label ASC)", i, rows[i].LabelID, want)
		}
	}
	// And period_start monotonically non-increasing.
	for i := 1; i < len(rows); i++ {
		if rows[i-1].PeriodStart.Before(rows[i].PeriodStart) {
			t.Fatalf("row[%d] older than row[%d] — ordering broken", i-1, i)
		}
	}
}

func TestListAwardLedger_LabelIDFilterExcludesOtherLabels(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_filter")
	deleteLedgerFor(t, d, ctx, f.name)

	at := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{
		{LabelID: seededLabelA, PeriodType: PeriodDaily},
		{LabelID: seededLabelB, PeriodType: PeriodDaily},
	}, time.UTC, at)

	rows, err := d.ListAwardLedger(ctx, f.name, seededLabelB, 0)
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(rows) != 1 || rows[0].LabelID != seededLabelB {
		t.Fatalf("filter=%q rows = %+v, want exactly [seededLabelB]", seededLabelB, rows)
	}
}

func TestListAwardLedger_ZeroLimitDefaultsTo500(t *testing.T) {
	// Sanity: `limit <= 0` MUST NOT return an unbounded SELECT (would blow
	// through prod on a chatty user). The impl swaps in 500. We can't
	// reasonably insert 501 rows here, so pin the smaller invariant: the
	// call MUST succeed with limit=0 and not error out on a bad LIMIT.
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_deflim")
	deleteLedgerFor(t, d, ctx, f.name)

	at := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{{LabelID: seededLabelA, PeriodType: PeriodDaily}}, time.UTC, at)

	rows, err := d.ListAwardLedger(ctx, f.name, "", 0)
	if err != nil {
		t.Fatalf("limit=0 query errored: %v (should silently default to 500)", err)
	}
	if len(rows) != 1 {
		t.Fatalf("limit=0 got %d rows, want the 1 we inserted", len(rows))
	}
	// Negative limits map to 500 as well.
	rows2, err := d.ListAwardLedger(ctx, f.name, "", -7)
	if err != nil {
		t.Fatalf("limit=-7 query errored: %v (should silently default to 500)", err)
	}
	if len(rows2) != 1 {
		t.Fatalf("limit=-7 got %d rows, want 1", len(rows2))
	}
}

func TestListAwardLedger_TwoDistinctLogsProduceExactlyTwoRows(t *testing.T) {
	// Anti-tautology: not just "insert x → get x". Log two distinct items
	// separately and prove BOTH landed (no drop) AND that no ghost row
	// appeared (no dupe / no leak from prior test).
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_two")
	deleteLedgerFor(t, d, ctx, f.name)

	at := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{{LabelID: seededLabelA, PeriodType: PeriodDaily}}, time.UTC, at)
	_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{{LabelID: seededLabelB, PeriodType: PeriodDaily}}, time.UTC, at)

	rows, _ := d.ListAwardLedger(ctx, f.name, "", 0)
	if len(rows) != 2 {
		t.Fatalf("two distinct logs → %d rows, want EXACTLY 2 (no drop, no dupe)", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.LabelID] = true
	}
	if !seen[seededLabelA] || !seen[seededLabelB] {
		t.Fatalf("expected both labels in rows, got %+v", seen)
	}
}

// -------- GetLabelStreaks integration --------

func TestGetLabelStreaks_EmptyLedgerReturnsEmpty(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_streak_empty")
	deleteLedgerFor(t, d, ctx, f.name)

	streaks, err := d.GetLabelStreaks(ctx, f.name, time.UTC, time.Now())
	if err != nil {
		t.Fatalf("GetLabelStreaks: %v", err)
	}
	if len(streaks) != 0 {
		t.Fatalf("empty ledger streaks = %+v, want []", streaks)
	}
}

func TestGetLabelStreaks_SingleCurrentPeriodEntryReturnsCountOne(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_streak_one")
	deleteLedgerFor(t, d, ctx, f.name)

	// Log for "today" and query for later-same-day.
	today := time.Date(2025, 3, 15, 9, 0, 0, 0, time.UTC)
	queryAt := time.Date(2025, 3, 15, 22, 0, 0, 0, time.UTC)
	_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{{LabelID: seededLabelA, PeriodType: PeriodDaily}}, time.UTC, today)

	streaks, _ := d.GetLabelStreaks(ctx, f.name, time.UTC, queryAt)
	if len(streaks) != 1 || streaks[0].LabelID != seededLabelA || streaks[0].StreakCount != 1 {
		t.Fatalf("single-day streak = %+v, want 1 entry with count=1", streaks)
	}
}

func TestGetLabelStreaks_GapBeforeCurrentPeriodBreaksStreakToZero(t *testing.T) {
	// GOOD (per pyramid): entries on day1 + day2, query at day4. Streak is
	// broken by day3's miss AND the current-period requirement, so this
	// label MUST NOT appear at all.
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_streak_gap")
	deleteLedgerFor(t, d, ctx, f.name)

	day1 := time.Date(2025, 3, 10, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2025, 3, 11, 12, 0, 0, 0, time.UTC)
	day4 := time.Date(2025, 3, 13, 22, 0, 0, 0, time.UTC)
	_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{{LabelID: seededLabelA, PeriodType: PeriodDaily}}, time.UTC, day1)
	_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{{LabelID: seededLabelA, PeriodType: PeriodDaily}}, time.UTC, day2)

	streaks, _ := d.GetLabelStreaks(ctx, f.name, time.UTC, day4)
	for _, s := range streaks {
		if s.LabelID == seededLabelA {
			t.Fatalf("gapped label appears with count=%d, want ABSENT (Wed-query for Mon-fired label)", s.StreakCount)
		}
	}
}

func TestGetLabelStreaks_NConsecutivePeriodsCountExactlyN(t *testing.T) {
	// Anti-tautology: prove the walk visits ALL consecutive rows AND that
	// the count is exactly N (not off-by-one). N=4 is enough to catch both
	// under- and over-counting.
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_streak_n")
	deleteLedgerFor(t, d, ctx, f.name)

	N := 4
	// Seed N consecutive days ending today (today = day-N-1 offset from base).
	base := time.Date(2025, 3, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < N; i++ {
		at := base.AddDate(0, 0, i)
		_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{{LabelID: seededLabelA, PeriodType: PeriodDaily}}, time.UTC, at)
	}
	queryAt := base.AddDate(0, 0, N-1).Add(6 * time.Hour) // same day as last entry

	streaks, _ := d.GetLabelStreaks(ctx, f.name, time.UTC, queryAt)
	var got int
	for _, s := range streaks {
		if s.LabelID == seededLabelA {
			got = s.StreakCount
		}
	}
	if got != N {
		t.Fatalf("consecutive-days streak count = %d, want %d (walker must visit every row)", got, N)
	}
}

func TestGetLabelStreaks_GapMidHistoryStopsWalkAtGap(t *testing.T) {
	// Seed days: T-4, T-3, T-1, T (with T-2 MISSING). Walker starts at T
	// and MUST stop when it reaches the T-2 gap, yielding count=2 (T + T-1)
	// — NOT 4, which would prove it silently ignored the gap.
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_streak_midgap")
	deleteLedgerFor(t, d, ctx, f.name)

	base := time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC) // T
	seed := func(offset int) {
		at := base.AddDate(0, 0, offset)
		_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{{LabelID: seededLabelA, PeriodType: PeriodDaily}}, time.UTC, at)
	}
	seed(-4)
	seed(-3)
	// skip -2 (the gap)
	seed(-1)
	seed(0)

	queryAt := base.Add(6 * time.Hour)
	streaks, _ := d.GetLabelStreaks(ctx, f.name, time.UTC, queryAt)
	var got int
	for _, s := range streaks {
		if s.LabelID == seededLabelA {
			got = s.StreakCount
		}
	}
	// Walker: T (count=1) → T-1 (count=2) → expects T-2 → sees T-3 → STOP.
	if got != 2 {
		t.Fatalf("mid-gap streak count = %d, want 2 (walker must halt at first gap, not skip it)", got)
	}
}

func TestGetLabelStreaks_TimezoneAwareCurrentPeriodBoundary(t *testing.T) {
	// Row logged at 04:00 UTC on 2025-03-16 = 21:00 Pacific on 2025-03-15.
	// For a Pacific-tz user, that ledger row's period_start is Pacific
	// midnight 2025-03-15. Query at 06:00 UTC 2025-03-16 = 23:00 Pacific
	// 2025-03-15 (still same Pacific day). Streak MUST count as 1.
	tz, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("America/Los_Angeles tzdata unavailable: %v", err)
	}
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "aw_streak_tz")
	deleteLedgerFor(t, d, ctx, f.name)

	logAt := time.Date(2025, 3, 16, 4, 0, 0, 0, time.UTC)
	queryAt := time.Date(2025, 3, 16, 6, 0, 0, 0, time.UTC)
	_, _ = d.LogAwards(ctx, f.name, []AwardLogItem{{LabelID: seededLabelA, PeriodType: PeriodDaily}}, tz, logAt)

	streaks, _ := d.GetLabelStreaks(ctx, f.name, tz, queryAt)
	var got int
	for _, s := range streaks {
		if s.LabelID == seededLabelA {
			got = s.StreakCount
		}
	}
	if got != 1 {
		t.Fatalf("Pacific-tz same-day streak = %d, want 1 (tz param must gate current-period comparison)", got)
	}

	// Sanity-check: if we mis-passed UTC as the query tz, the row's Pacific
	// period_start would NOT equal the UTC-derived current period, and the
	// walk would drop the row. Assert THAT to prove the tz arg is
	// load-bearing (not vestigial).
	utcStreaks, _ := d.GetLabelStreaks(ctx, f.name, time.UTC, queryAt)
	for _, s := range utcStreaks {
		if s.LabelID == seededLabelA && s.StreakCount > 0 {
			t.Fatalf("UTC-queried streak for Pacific row = %d, want 0 (tz mismatch must drop the row)", s.StreakCount)
		}
	}
}
