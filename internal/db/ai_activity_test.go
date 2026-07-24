package db

import (
	"context"
	"testing"
	"time"
)

// ai_activity_test.go pins the promised invariants on GetAIActivity that no
// prior test exercised. The comment in ai_activity.go states two load-bearing
// promises to the FE Overview card:
//
//   1. Only heartbeats with at least one AI signal contribute (HasData/
//      HeartbeatsWithAI filter). Non-AI editor heartbeats must NOT drag the
//      distinct-session count or the sum totals.
//   2. Distinct-sessions is computed OVER THE WHOLE RANGE (single COUNT
//      DISTINCT), so a session that spans two days counts ONCE. The per-day
//      series's session counts are per-day; summing them WOULD double-count,
//      and the code explicitly avoids that by computing the range total via
//      a separate query.
//
// A regression that reintroduced `SUM(per_day.sessions)` on a shared session
// would be a silent user-facing overcount on every dashboard load.

// insertAIHB inserts one heartbeat carrying AI-signal columns for the given
// sender + timestamp + optional session id. All AI columns not passed default
// to NULL so the "no AI" filter behaves correctly.
func insertAIHB(t *testing.T, d *DB, ctx context.Context, sender string, ts time.Time, session *string, in, out, aiLines, humanLines int64) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx, `
		INSERT INTO heartbeats
		  (sender, entity, ty, time_sent, user_agent, gap_seconds,
		   ai_input_tokens, ai_output_tokens, ai_line_changes, human_line_changes, ai_session)
		VALUES ($1, 'a.go', 'file', $2, 'ua', 60,
		        $3, $4, $5, $6, $7)`,
		sender, ts, in, out, aiLines, humanLines, session); err != nil {
		t.Fatal(err)
	}
}

// insertPlainHB inserts a heartbeat with NO AI columns set. Must NOT bleed
// into GetAIActivity's totals (the filter fires on NULL-across-all-signals).
func insertPlainHB(t *testing.T, d *DB, ctx context.Context, sender string, ts time.Time) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx, `
		INSERT INTO heartbeats
		  (sender, entity, ty, time_sent, user_agent, gap_seconds)
		VALUES ($1, 'plain.go', 'file', $2, 'ua', 60)`,
		sender, ts); err != nil {
		t.Fatal(err)
	}
}

// TestAIActivityFiltersNonAIHeartbeats: rows with all AI columns NULL are
// excluded from every aggregate (HeartbeatsWithAI, TotalSessions, per-day
// series). If the filter breaks, our plain-vim heartbeats would fluff the
// totals.
func TestAIActivityFiltersNonAIHeartbeats(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "ai_filter")
	sender := f.Sender()
	ctx := f.Ctx()

	day := time.Date(2025, 5, 1, 10, 0, 0, 0, time.UTC)

	sess := "sess-1"
	// One AI-tagged hb, one plain hb — both same day.
	insertAIHB(t, d, ctx, sender, day, &sess, 10, 20, 5, 3)
	insertPlainHB(t, d, ctx, sender, day.Add(time.Minute))
	// A row with any single AI column non-null still qualifies (only-tokens variant).
	insertAIHB(t, d, ctx, sender, day.Add(2*time.Minute), nil, 15, 0, 0, 0)

	start := day.AddDate(0, 0, -1)
	end := day.AddDate(0, 0, 1)

	sum, err := d.GetAIActivity(ctx, sender, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if !sum.HasData {
		t.Fatal("HasData=false, want true (two AI-tagged rows exist)")
	}
	if sum.HeartbeatsWithAI != 2 {
		t.Fatalf("HeartbeatsWithAI = %d, want 2 (plain hb must be filtered)", sum.HeartbeatsWithAI)
	}
	if sum.TotalInputTokens != 25 {
		t.Fatalf("TotalInputTokens = %d, want 25 (10+15)", sum.TotalInputTokens)
	}
	if sum.TotalOutputTokens != 20 {
		t.Fatalf("TotalOutputTokens = %d, want 20", sum.TotalOutputTokens)
	}
	if sum.TotalAILineChanges != 5 {
		t.Fatalf("TotalAILineChanges = %d, want 5", sum.TotalAILineChanges)
	}
	if sum.TotalHumanLineChanges != 3 {
		t.Fatalf("TotalHumanLineChanges = %d, want 3", sum.TotalHumanLineChanges)
	}
}

// TestAIActivitySessionCountDeduplicatesAcrossDays: one session id used on
// two days must count as ONE session in TotalSessions. This is the invariant
// the standalone COUNT(DISTINCT ai_session) query exists to enforce; a
// regression that summed per-day session counts would over-report by N days.
func TestAIActivitySessionCountDeduplicatesAcrossDays(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "ai_dedup")
	sender := f.Sender()
	ctx := f.Ctx()

	day1 := time.Date(2025, 5, 2, 10, 0, 0, 0, time.UTC)
	day2 := day1.AddDate(0, 0, 1)

	// One long-running session spanning day1 + day2.
	shared := "sess-multi-day"
	insertAIHB(t, d, ctx, sender, day1, &shared, 5, 5, 0, 0)
	insertAIHB(t, d, ctx, sender, day2, &shared, 5, 5, 0, 0)
	// A second distinct session, single day.
	only1 := "sess-one-day"
	insertAIHB(t, d, ctx, sender, day1.Add(time.Hour), &only1, 5, 5, 0, 0)

	start := day1.AddDate(0, 0, -1)
	end := day2.AddDate(0, 0, 1)

	sum, err := d.GetAIActivity(ctx, sender, start, end)
	if err != nil {
		t.Fatal(err)
	}
	// Per-day sums each show 1+ sessions on the days that hosted them;
	// summing them naively would be 2 (day1: shared+only1) + 1 (day2: shared) = 3.
	// The correct DISTINCT-over-range count is 2.
	if sum.TotalSessions != 2 {
		t.Fatalf("TotalSessions = %d, want 2 (a multi-day session must not double-count)",
			sum.TotalSessions)
	}
}

// TestAIActivityEmptyRangeHasFalseData: no AI-tagged rows in the range returns
// HasData=false with a stable empty payload (no nil-deref for FE early-return).
func TestAIActivityEmptyRangeHasFalseData(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "ai_empty")
	sender := f.Sender()
	ctx := f.Ctx()

	// Only a plain heartbeat, no AI signals.
	day := time.Date(2025, 5, 3, 10, 0, 0, 0, time.UTC)
	insertPlainHB(t, d, ctx, sender, day)

	sum, err := d.GetAIActivity(ctx, sender, day.AddDate(0, 0, -1), day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if sum.HasData {
		t.Fatalf("HasData=true, want false (no AI signal in range)")
	}
	if sum.HeartbeatsWithAI != 0 {
		t.Fatalf("HeartbeatsWithAI = %d, want 0", sum.HeartbeatsWithAI)
	}
	if sum.Days == nil {
		t.Fatalf("Days is nil; must be an empty slice for stable JSON marshaling")
	}
}

// TestAIActivityOwnerScoping: another user's AI heartbeats must never
// contribute to this user's summary. Complements owner_scoping_test.go with
// AI-specific rows (owner_scoping_test.go doesn't touch AI columns).
func TestAIActivityOwnerScoping(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	a := newSender(t, d, "ai_ownA")
	b := newSender(t, d, "ai_ownB")

	day := time.Date(2025, 5, 4, 10, 0, 0, 0, time.UTC)
	sa, sb := "a-sess", "b-sess"
	insertAIHB(t, d, a.Ctx(), a.Sender(), day, &sa, 100, 200, 5, 3)
	insertAIHB(t, d, b.Ctx(), b.Sender(), day, &sb, 999, 999, 99, 99)

	sum, err := d.GetAIActivity(a.Ctx(), a.Sender(), day.AddDate(0, 0, -1), day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalInputTokens != 100 {
		t.Fatalf("A TotalInputTokens = %d, want 100 (B's 999 must not leak)", sum.TotalInputTokens)
	}
	if sum.TotalSessions != 1 {
		t.Fatalf("A TotalSessions = %d, want 1 (B's session must not leak)", sum.TotalSessions)
	}
	if sum.HeartbeatsWithAI != 1 {
		t.Fatalf("A HeartbeatsWithAI = %d, want 1", sum.HeartbeatsWithAI)
	}
}
