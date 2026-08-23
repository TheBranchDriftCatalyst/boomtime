package db

// Tests for internal/db/health.go — boom-se2.8.
//
// Two layers, both in `package db` (follows every sibling *_test.go in this
// package; the external-test path is not viable because the harness helpers
// openTestDB / newSender / deleteSenderRows all live in-package and cannot be
// reached from `package db_test` without a circular-import).
//
//   Unit    — pure helpers (sortStringsAsc, summariseDays), no DB.
//   Integ   — SaveHealthSamples / SaveWorkouts / GetHealthActivity / GetWorkouts
//             against the isolated boomtime_test DB. Every case pins exactly
//             one invariant per t.Run, named in the sub-test string.
//
// Anti-tautology: numeric assertions bound counts+min+max+total against
// well-known input rather than re-running the aggregation. Silent drops (dedup
// bug) and silent doubles (rerun bug) both flunk without the assertion
// re-implementing SUM.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// ---- shared health-domain fixtures / cleanup helpers ----

// cleanupHealthRows wipes the health-domain tables for an owner. deleteSenderRows
// covers heartbeats + users (and workout_details cascades from heartbeats), but
// it does NOT know about health_samples / health_rollup_daily, so we shim.
func cleanupHealthRows(t *testing.T, d *DB, ctx context.Context, owner string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM health_samples WHERE owner=$1`, owner)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM health_rollup_daily WHERE owner=$1`, owner)
	})
}

// newHealthOwner mints a user, wires the standard cleanup for sender-scoped
// tables, and layers the health-scoped cleanup on top.
func newHealthOwner(t *testing.T, d *DB, prefix string) (string, context.Context) {
	t.Helper()
	ctx := context.Background()
	owner := mkSender(prefix)
	cleanupSender(t, d, ctx, owner)
	cleanupHealthRows(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)
	return owner, ctx
}

// ptrF returns a *float64 pointing at v. Used to inline optional float fields.
func ptrF(v float64) *float64 { return &v }

// mkSample builds a HealthSamplePayload with the fields most tests care about.
// Optional fields (workoutUUID, qavg for HR) are set via functional options.
func mkSample(kind, unit string, qty float64, tsStart float64) model.HealthSamplePayload {
	q := qty
	return model.HealthSamplePayload{
		Kind:    kind,
		Unit:    unit,
		Qty:     &q,
		TsStart: tsStart,
	}
}

// mkSleepSample builds a sleep_stage sample with an interval (ts_end set).
func mkSleepSample(tsStart, tsEnd float64) model.HealthSamplePayload {
	te := tsEnd
	return model.HealthSamplePayload{
		Kind:    "sleep_stage",
		Unit:    "stage",
		TsStart: tsStart,
		TsEnd:   &te,
	}
}

// mkHRSample builds a heart_rate sample using QAvg (rollup path exercises the
// COALESCE(qty, q_avg) branch).
func mkHRSample(qavg float64, tsStart float64) model.HealthSamplePayload {
	q := qavg
	return model.HealthSamplePayload{
		Kind:    "heart_rate",
		Unit:    "bpm",
		QAvg:    &q,
		TsStart: tsStart,
	}
}

// mkWorkout builds a WorkoutPayload with a distinct SourceUUID so the
// workout_details UNIQUE(source_uuid) index doesn't collide across cases.
func mkWorkout(kind string, start float64, dur int64, uuid string) model.WorkoutPayload {
	return model.WorkoutPayload{
		Kind:       kind,
		Start:      start,
		End:        start + float64(dur),
		DurationS:  dur,
		SourceUUID: uuid,
	}
}

// ---- unit tests (pure helpers, no DB) ----

func TestSortStringsAsc(t *testing.T) {
	// invariant: input order becomes lex-ascending; identity for degenerate sizes.
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"unsorted becomes ascending", []string{"c", "a", "b"}, []string{"a", "b", "c"}},
		{"already-sorted is unchanged", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"empty stays empty", []string{}, []string{}},
		{"single element unchanged", []string{"only"}, []string{"only"}},
		{"YYYY-MM-DD dates ascend", []string{"2025-04-03", "2025-04-01", "2025-04-02"},
			[]string{"2025-04-01", "2025-04-02", "2025-04-03"}},
		{"duplicates preserved and clustered", []string{"b", "a", "b", "a"},
			[]string{"a", "a", "b", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := append([]string(nil), tc.in...)
			sortStringsAsc(got)
			if len(got) != len(tc.want) {
				t.Fatalf("len=%d, want %d (got %v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("index %d = %q, want %q (got %v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestSummariseDays(t *testing.T) {
	t.Run("empty range yields zero totals with Day=range", func(t *testing.T) {
		got := summariseDays(nil)
		if got.Day != "range" {
			t.Fatalf("Day=%q, want %q", got.Day, "range")
		}
		if got.Workouts != 0 || got.WorkoutMinutes != 0 || got.ActiveKcal != 0 ||
			got.Steps != 0 || got.SleepMinutes != 0 || got.MindfulMinutes != 0 ||
			got.AvgHR != 0 || got.RestingHR != 0 || got.HRVMs != 0 {
			t.Fatalf("empty totals must be zero, got %+v", got)
		}
	})

	t.Run("single day with AvgHR=0 excluded from average (no div-by-zero)", func(t *testing.T) {
		days := []model.HealthActivityDay{{Day: "2025-04-01", AvgHR: 0, RestingHR: 0, HRVMs: 0}}
		got := summariseDays(days)
		if got.AvgHR != 0 {
			t.Fatalf("AvgHR=%v, want 0 (no /0)", got.AvgHR)
		}
		if got.RestingHR != 0 || got.HRVMs != 0 {
			t.Fatalf("all HR-family stays 0 when input is 0, got %+v", got)
		}
	})

	t.Run("three days [70,80,90] average exactly to 80 (no float drift)", func(t *testing.T) {
		days := []model.HealthActivityDay{
			{Day: "d1", AvgHR: 70},
			{Day: "d2", AvgHR: 80},
			{Day: "d3", AvgHR: 90},
		}
		got := summariseDays(days)
		if got.AvgHR != 80 {
			t.Fatalf("AvgHR=%v, want exactly 80", got.AvgHR)
		}
	})

	t.Run("mixed AvgHR averaged over non-zero days only", func(t *testing.T) {
		// [60, 0, 100, 0, 80] — only the three non-zero contribute: (60+100+80)/3 = 80.
		days := []model.HealthActivityDay{
			{AvgHR: 60}, {AvgHR: 0}, {AvgHR: 100}, {AvgHR: 0}, {AvgHR: 80},
		}
		got := summariseDays(days)
		if got.AvgHR != 80 {
			t.Fatalf("AvgHR=%v, want 80 (mean of non-zero: {60,100,80})", got.AvgHR)
		}
	})

	t.Run("integer counters sum exactly across days", func(t *testing.T) {
		days := []model.HealthActivityDay{
			{Workouts: 1, Steps: 1000, WorkoutMinutes: 30, ActiveKcal: 100, SleepMinutes: 400, MindfulMinutes: 10},
			{Workouts: 2, Steps: 2500, WorkoutMinutes: 15.5, ActiveKcal: 50, SleepMinutes: 380, MindfulMinutes: 5},
		}
		got := summariseDays(days)
		if got.Workouts != 3 {
			t.Fatalf("Workouts=%d, want 3", got.Workouts)
		}
		if got.Steps != 3500 {
			t.Fatalf("Steps=%d, want 3500", got.Steps)
		}
		if got.WorkoutMinutes < 45.4 || got.WorkoutMinutes > 45.6 {
			t.Fatalf("WorkoutMinutes=%v, want ~45.5", got.WorkoutMinutes)
		}
		if got.ActiveKcal != 150 {
			t.Fatalf("ActiveKcal=%v, want 150", got.ActiveKcal)
		}
		if got.SleepMinutes != 780 {
			t.Fatalf("SleepMinutes=%v, want 780", got.SleepMinutes)
		}
		if got.MindfulMinutes != 15 {
			t.Fatalf("MindfulMinutes=%v, want 15", got.MindfulMinutes)
		}
	})

	t.Run("RestingHR and HRV independently averaged over their own non-zero days", func(t *testing.T) {
		// RestingHR non-zero on 2 days: (50+60)/2=55. HRV non-zero on 3 days: (30+40+50)/3=40.
		days := []model.HealthActivityDay{
			{RestingHR: 50, HRVMs: 30},
			{RestingHR: 0, HRVMs: 40},
			{RestingHR: 60, HRVMs: 50},
		}
		got := summariseDays(days)
		if got.RestingHR != 55 {
			t.Fatalf("RestingHR=%v, want 55", got.RestingHR)
		}
		if got.HRVMs != 40 {
			t.Fatalf("HRVMs=%v, want 40", got.HRVMs)
		}
	})
}

// ---- integration tests (real DB) ----

// day returns a UTC time.Time at midnight on the given Y/M/D.
func day(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

// unix returns the unix-seconds float for a time.Time.
func unix(t time.Time) float64 { return float64(t.Unix()) }

func TestSaveHealthSamples(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	t.Run("roundtrip: 10 samples land in health_samples for the owner", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "hs_round")
		base := day(2025, 5, 1).Add(10 * time.Hour)
		samples := make([]model.HealthSamplePayload, 10)
		for i := range samples {
			// distinct ts_start so the dedupe index doesn't collapse them.
			samples[i] = mkSample("steps", "count", 100+float64(i),
				unix(base.Add(time.Duration(i)*time.Minute)))
		}
		n, err := d.SaveHealthSamples(ctx, owner, samples)
		if err != nil {
			t.Fatal(err)
		}
		if n != 10 {
			t.Fatalf("SaveHealthSamples returned n=%d, want 10", n)
		}
		var count int
		if err := d.Pool.QueryRow(ctx,
			`SELECT count(*) FROM health_samples WHERE owner=$1`, owner).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 10 {
			t.Fatalf("row count = %d, want 10 (samples: dropped or duped?)", count)
		}
		// Anti-tautology: also bound distinct-qty and min/max to catch a silent
		// truncation where fewer rows survive but the count somehow matches.
		var distinctQty int
		var minQty, maxQty float64
		if err := d.Pool.QueryRow(ctx,
			`SELECT count(distinct qty), min(qty), max(qty) FROM health_samples WHERE owner=$1`,
			owner).Scan(&distinctQty, &minQty, &maxQty); err != nil {
			t.Fatal(err)
		}
		if distinctQty != 10 {
			t.Fatalf("distinct qty = %d, want 10", distinctQty)
		}
		if minQty < 99.5 || minQty > 100.5 {
			t.Fatalf("min qty = %v, want ~100", minQty)
		}
		if maxQty < 108.5 || maxQty > 109.5 {
			t.Fatalf("max qty = %v, want ~109", maxQty)
		}
	})

	t.Run("empty payload short-circuits to n=0 no writes", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "hs_empty")
		n, err := d.SaveHealthSamples(ctx, owner, nil)
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("empty n=%d, want 0", n)
		}
		var count int
		_ = d.Pool.QueryRow(ctx,
			`SELECT count(*) FROM health_samples WHERE owner=$1`, owner).Scan(&count)
		if count != 0 {
			t.Fatalf("row count = %d, want 0", count)
		}
	})

	t.Run("idempotency: same 5 samples inserted 3x still = 5 rows", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "hs_idem")
		base := day(2025, 5, 2).Add(9 * time.Hour)
		samples := []model.HealthSamplePayload{
			mkSample("steps", "count", 100, unix(base)),
			mkSample("steps", "count", 200, unix(base.Add(1*time.Minute))),
			mkSample("steps", "count", 300, unix(base.Add(2*time.Minute))),
			mkSample("steps", "count", 400, unix(base.Add(3*time.Minute))),
			mkSample("steps", "count", 500, unix(base.Add(4*time.Minute))),
		}
		for i := 0; i < 3; i++ {
			if _, err := d.SaveHealthSamples(ctx, owner, samples); err != nil {
				t.Fatalf("save round %d: %v", i, err)
			}
		}
		var count int
		if err := d.Pool.QueryRow(ctx,
			`SELECT count(*) FROM health_samples WHERE owner=$1`, owner).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 5 {
			t.Fatalf("row count = %d, want 5 (idx_health_samples_dedupe missed a dup?)", count)
		}
	})

	t.Run("cross-owner isolation: A rows do not leak into B's owner scope", func(t *testing.T) {
		ownerA, ctx := newHealthOwner(t, d, "hs_isoA")
		ownerB, _ := newHealthOwner(t, d, "hs_isoB")
		base := day(2025, 5, 3).Add(8 * time.Hour)
		aSamples := []model.HealthSamplePayload{
			mkSample("steps", "count", 111, unix(base)),
			mkSample("steps", "count", 222, unix(base.Add(1*time.Minute))),
			mkSample("steps", "count", 333, unix(base.Add(2*time.Minute))),
		}
		bSamples := []model.HealthSamplePayload{
			mkSample("steps", "count", 999, unix(base)),
			mkSample("steps", "count", 888, unix(base.Add(1*time.Minute))),
		}
		if _, err := d.SaveHealthSamples(ctx, ownerA, aSamples); err != nil {
			t.Fatal(err)
		}
		if _, err := d.SaveHealthSamples(ctx, ownerB, bSamples); err != nil {
			t.Fatal(err)
		}
		var aCount, bCount int
		if err := d.Pool.QueryRow(ctx,
			`SELECT count(*) FROM health_samples WHERE owner=$1`, ownerA).Scan(&aCount); err != nil {
			t.Fatal(err)
		}
		if err := d.Pool.QueryRow(ctx,
			`SELECT count(*) FROM health_samples WHERE owner=$1`, ownerB).Scan(&bCount); err != nil {
			t.Fatal(err)
		}
		if aCount != 3 {
			t.Fatalf("ownerA count = %d, want 3", aCount)
		}
		if bCount != 2 {
			t.Fatalf("ownerB count = %d, want 2", bCount)
		}
		// And explicitly: no A qty leaked to B (999/888 are B-exclusive; 111/222/333 A-exclusive).
		var leaked int
		if err := d.Pool.QueryRow(ctx,
			`SELECT count(*) FROM health_samples WHERE owner=$1 AND qty IN (999,888)`, ownerA).Scan(&leaked); err != nil {
			t.Fatal(err)
		}
		if leaked != 0 {
			t.Fatalf("A saw B's qty values: %d rows", leaked)
		}
	})

	t.Run("rollup: sleep+HR one-day batch populates health_rollup_daily per kind", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "hs_roll")
		// All samples must land on the SAME UTC day so refreshHealthRollup
		// groups them into one health_rollup_daily row per kind.
		base := day(2025, 5, 4).Add(9 * time.Hour)
		samples := []model.HealthSamplePayload{
			// Two sleep intervals totaling 60min+30min = 5400s, both on the same day.
			mkSleepSample(unix(base), unix(base.Add(60*time.Minute))),
			mkSleepSample(unix(base.Add(2*time.Hour)), unix(base.Add(150*time.Minute))),
			// HR samples: 60, 90, 120 → avg = 90.
			mkHRSample(60, unix(base.Add(15*time.Minute))),
			mkHRSample(90, unix(base.Add(30*time.Minute))),
			mkHRSample(120, unix(base.Add(45*time.Minute))),
		}
		if _, err := d.SaveHealthSamples(ctx, owner, samples); err != nil {
			t.Fatal(err)
		}
		type rollupRow struct {
			kind        string
			totalQty    float64
			avgQty      float64
			sampleCount int
		}
		rows, err := d.Pool.Query(ctx,
			`SELECT kind, coalesce(total_qty,0), coalesce(avg_qty,0), sample_count
			   FROM health_rollup_daily WHERE owner=$1 ORDER BY kind`, owner)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		byKind := map[string]rollupRow{}
		for rows.Next() {
			var r rollupRow
			if err := rows.Scan(&r.kind, &r.totalQty, &r.avgQty, &r.sampleCount); err != nil {
				t.Fatal(err)
			}
			byKind[r.kind] = r
		}
		if len(byKind) != 2 {
			t.Fatalf("rollup kinds = %d (%v), want 2 (sleep_stage, heart_rate)", len(byKind), byKind)
		}
		sleep, ok := byKind["sleep_stage"]
		if !ok {
			t.Fatalf("no sleep_stage rollup row: %v", byKind)
		}
		if sleep.sampleCount != 2 {
			t.Fatalf("sleep sample_count = %d, want 2", sleep.sampleCount)
		}
		// 60min + 30min = 5400s. Bound to [5390,5410] to tolerate float32 storage.
		if sleep.totalQty < 5390 || sleep.totalQty > 5410 {
			t.Fatalf("sleep total_qty = %v seconds, want ~5400", sleep.totalQty)
		}
		hr, ok := byKind["heart_rate"]
		if !ok {
			t.Fatalf("no heart_rate rollup row: %v", byKind)
		}
		if hr.sampleCount != 3 {
			t.Fatalf("HR sample_count = %d, want 3", hr.sampleCount)
		}
		// Average of {60,90,120} = 90. Bound wide to tolerate real4 storage.
		if hr.avgQty < 89 || hr.avgQty > 91 {
			t.Fatalf("HR avg_qty = %v, want ~90", hr.avgQty)
		}
	})

	t.Run("rollup determinism: two consecutive refreshes yield identical rows", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "hs_det")
		base := day(2025, 5, 5).Add(10 * time.Hour)
		samples := []model.HealthSamplePayload{
			mkSample("steps", "count", 100, unix(base)),
			mkSample("steps", "count", 200, unix(base.Add(1*time.Minute))),
			mkSample("steps", "count", 300, unix(base.Add(2*time.Minute))),
		}
		if _, err := d.SaveHealthSamples(ctx, owner, samples); err != nil {
			t.Fatal(err)
		}
		var t1, a1 float64
		var c1 int
		if err := d.Pool.QueryRow(ctx,
			`SELECT total_qty, avg_qty, sample_count FROM health_rollup_daily
			  WHERE owner=$1 AND kind='steps'`, owner).Scan(&t1, &a1, &c1); err != nil {
			t.Fatal(err)
		}
		// Second call — should DELETE+INSERT to bit-identical state.
		if _, err := d.SaveHealthSamples(ctx, owner, samples); err != nil {
			t.Fatal(err)
		}
		var t2, a2 float64
		var c2 int
		if err := d.Pool.QueryRow(ctx,
			`SELECT total_qty, avg_qty, sample_count FROM health_rollup_daily
			  WHERE owner=$1 AND kind='steps'`, owner).Scan(&t2, &a2, &c2); err != nil {
			t.Fatal(err)
		}
		if t1 != t2 || a1 != a2 || c1 != c2 {
			t.Fatalf("rollup drift across refreshes: {t=%v a=%v c=%d} vs {t=%v a=%v c=%d}",
				t1, a1, c1, t2, a2, c2)
		}
	})

	t.Run("workout FK: valid uuid resolves to workout_id; bogus uuid → NULL not error", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "hs_fk")
		base := day(2025, 5, 6).Add(9 * time.Hour)

		// First, insert one workout so we have a real source_uuid → heartbeat_id.
		validUUID := "wk-fk-valid-" + owner
		ids, err := d.SaveWorkouts(ctx, owner, []model.WorkoutPayload{
			mkWorkout("running", unix(base), 900, validUUID),
		})
		if err != nil {
			t.Fatalf("seed workout: %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 workout id, got %d", len(ids))
		}
		wantHBID := ids[0]

		// Now try samples: one references the real uuid, one references garbage.
		valid := validUUID
		bogus := "wk-fk-nonexistent-" + owner
		validSample := mkHRSample(80, unix(base.Add(5*time.Minute)))
		validSample.WorkoutUUID = &valid
		bogusSample := mkHRSample(85, unix(base.Add(6*time.Minute)))
		bogusSample.WorkoutUUID = &bogus

		if _, err := d.SaveHealthSamples(ctx, owner, []model.HealthSamplePayload{validSample, bogusSample}); err != nil {
			t.Fatalf("SaveHealthSamples with mixed FK: %v", err)
		}

		// The valid uuid sample must point at the workout's heartbeat id.
		var gotHBID int64
		if err := d.Pool.QueryRow(ctx,
			`SELECT workout_id FROM health_samples WHERE owner=$1 AND q_avg=80`, owner).Scan(&gotHBID); err != nil {
			t.Fatal(err)
		}
		if gotHBID != wantHBID {
			t.Fatalf("valid uuid resolved to workout_id=%d, want %d", gotHBID, wantHBID)
		}
		// The bogus uuid sample must have NULL workout_id (batch didn't fail either).
		var nullCount int
		if err := d.Pool.QueryRow(ctx,
			`SELECT count(*) FROM health_samples WHERE owner=$1 AND q_avg=85 AND workout_id IS NULL`,
			owner).Scan(&nullCount); err != nil {
			t.Fatal(err)
		}
		if nullCount != 1 {
			t.Fatalf("bogus-uuid sample: expected 1 row with NULL workout_id, got %d", nullCount)
		}
	})

	t.Run("meta jsonb roundtrip: nil meta stays NULL, populated meta persists", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "hs_meta")
		base := day(2025, 5, 7).Add(9 * time.Hour)
		withMeta := mkSample("hrv", "ms", 42, unix(base))
		withMeta.Meta = map[string]any{"device": "watch"}
		noMeta := mkSample("hrv", "ms", 43, unix(base.Add(1*time.Minute)))
		if _, err := d.SaveHealthSamples(ctx, owner, []model.HealthSamplePayload{withMeta, noMeta}); err != nil {
			t.Fatal(err)
		}
		var populated, isNull int
		_ = d.Pool.QueryRow(ctx,
			`SELECT count(*) FROM health_samples WHERE owner=$1 AND meta->>'device'='watch'`,
			owner).Scan(&populated)
		if populated != 1 {
			t.Fatalf("meta jsonb populated rows = %d, want 1", populated)
		}
		_ = d.Pool.QueryRow(ctx,
			`SELECT count(*) FROM health_samples WHERE owner=$1 AND meta IS NULL`, owner).Scan(&isNull)
		if isNull != 1 {
			t.Fatalf("meta NULL rows = %d, want 1 (empty map must become NULL, not '{}')", isNull)
		}
	})
}

func TestSaveWorkouts(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	t.Run("empty payload short-circuits: no rows, no error, empty ids slice", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "wk_empty")
		ids, err := d.SaveWorkouts(ctx, owner, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 0 {
			t.Fatalf("ids len = %d, want 0", len(ids))
		}
	})

	t.Run("atomic write: 3 workouts → 3 heartbeats(ty=workout) + 3 workout_details", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "wk_atomic")
		base := day(2025, 6, 1).Add(7 * time.Hour)
		workouts := []model.WorkoutPayload{
			mkWorkout("running", unix(base), 1800, "wk_atom_A_"+owner),
			mkWorkout("cycling", unix(base.Add(3*time.Hour)), 2400, "wk_atom_B_"+owner),
			mkWorkout("hiking", unix(base.Add(6*time.Hour)), 3600, "wk_atom_C_"+owner),
		}
		ids, err := d.SaveWorkouts(ctx, owner, workouts)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 3 {
			t.Fatalf("ids len = %d, want 3", len(ids))
		}
		var hbCount, wdCount int
		_ = d.Pool.QueryRow(ctx,
			`SELECT count(*) FROM heartbeats WHERE sender=$1 AND ty=$2`,
			owner, WorkoutType).Scan(&hbCount)
		_ = d.Pool.QueryRow(ctx,
			`SELECT count(*) FROM workout_details wd JOIN heartbeats h ON h.id = wd.heartbeat_id
			  WHERE h.sender=$1`, owner).Scan(&wdCount)
		if hbCount != 3 {
			t.Fatalf("heartbeats(ty=workout) count = %d, want 3", hbCount)
		}
		if wdCount != 3 {
			t.Fatalf("workout_details rows for owner = %d, want 3", wdCount)
		}
	})

	t.Run("ids returned: 5 workouts → 5 unique non-zero ids in input order", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "wk_ids")
		base := day(2025, 6, 2).Add(6 * time.Hour)
		workouts := make([]model.WorkoutPayload, 5)
		for i := range workouts {
			workouts[i] = mkWorkout("running",
				unix(base.Add(time.Duration(i)*time.Hour)),
				int64(600+i*60),
				"wk_ids_"+owner+"_"+string(rune('A'+i)))
		}
		ids, err := d.SaveWorkouts(ctx, owner, workouts)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 5 {
			t.Fatalf("ids len = %d, want 5", len(ids))
		}
		seen := map[int64]bool{}
		for i, id := range ids {
			if id <= 0 {
				t.Fatalf("ids[%d] = %d, want >0", i, id)
			}
			if seen[id] {
				t.Fatalf("duplicate id %d at position %d", id, i)
			}
			seen[id] = true
		}
		// Input order: ids should be strictly increasing (serial sequence,
		// same-tx insert order via pgx.Batch).
		for i := 1; i < len(ids); i++ {
			if ids[i] <= ids[i-1] {
				t.Fatalf("ids not in input order: ids[%d]=%d <= ids[%d]=%d",
					i, ids[i], i-1, ids[i-1])
			}
		}
		// Retrievable by id.
		for _, id := range ids {
			var got int64
			if err := d.Pool.QueryRow(ctx, `SELECT id FROM heartbeats WHERE id=$1 AND sender=$2`,
				id, owner).Scan(&got); err != nil {
				t.Fatalf("id %d not retrievable: %v", id, err)
			}
		}
	})

	t.Run("HR series jsonb: populated → JSON array; empty → NULL not '[]'", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "wk_hr")
		base := day(2025, 6, 3).Add(6 * time.Hour)
		withHR := mkWorkout("running", unix(base), 1200, "wk_hr_with_"+owner)
		withHR.HRSeries = []model.HRSeriesPoint{
			{T: unix(base), BPM: 120},
			{T: unix(base.Add(30 * time.Second)), BPM: 135},
			{T: unix(base.Add(60 * time.Second)), BPM: 150},
		}
		noHR := mkWorkout("cycling", unix(base.Add(2*time.Hour)), 1800, "wk_hr_none_"+owner)
		if _, err := d.SaveWorkouts(ctx, owner, []model.WorkoutPayload{withHR, noHR}); err != nil {
			t.Fatal(err)
		}
		// Row for the withHR workout: hr_series must be a non-null JSON array of length 3.
		var seriesRaw []byte
		if err := d.Pool.QueryRow(ctx,
			`SELECT wd.hr_series FROM workout_details wd
			   JOIN heartbeats h ON h.id = wd.heartbeat_id
			  WHERE h.sender=$1 AND wd.source_uuid=$2`,
			owner, withHR.SourceUUID).Scan(&seriesRaw); err != nil {
			t.Fatal(err)
		}
		var series []model.HRSeriesPoint
		if err := json.Unmarshal(seriesRaw, &series); err != nil {
			t.Fatalf("hr_series not valid JSON: %v (raw=%s)", err, seriesRaw)
		}
		if len(series) != 3 {
			t.Fatalf("hr_series len = %d, want 3", len(series))
		}
		if series[0].BPM != 120 || series[2].BPM != 150 {
			t.Fatalf("hr_series contents wrong: %+v", series)
		}
		// Row for the noHR workout: hr_series must be SQL NULL, not the literal "[]".
		var isNull bool
		if err := d.Pool.QueryRow(ctx,
			`SELECT wd.hr_series IS NULL FROM workout_details wd
			   JOIN heartbeats h ON h.id = wd.heartbeat_id
			  WHERE h.sender=$1 AND wd.source_uuid=$2`,
			owner, noHR.SourceUUID).Scan(&isNull); err != nil {
			t.Fatal(err)
		}
		if !isNull {
			t.Fatalf("hr_series must be NULL for workout with no HR series, was JSON literal")
		}
	})

	t.Run("rollup refresh: workouts on 2 days → hb_rollup_daily rows for both days", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "wk_roll")
		d1 := day(2025, 6, 4).Add(10 * time.Hour)
		d2 := day(2025, 6, 5).Add(10 * time.Hour)
		workouts := []model.WorkoutPayload{
			mkWorkout("running", unix(d1), 1800, "wk_roll_A_"+owner),
			mkWorkout("cycling", unix(d2), 3600, "wk_roll_B_"+owner),
		}
		if _, err := d.SaveWorkouts(ctx, owner, workouts); err != nil {
			t.Fatal(err)
		}
		var distinctDays int
		if err := d.Pool.QueryRow(ctx,
			`SELECT count(distinct day) FROM hb_rollup_daily WHERE sender=$1`, owner).Scan(&distinctDays); err != nil {
			t.Fatal(err)
		}
		if distinctDays != 2 {
			t.Fatalf("rollup distinct days = %d, want 2 (SaveWorkouts must refresh rollup for touched days)", distinctDays)
		}
		// Sum across both days = 1800 + 3600 = 5400. Bound tightly.
		var totalSec int64
		_ = d.Pool.QueryRow(ctx,
			`SELECT coalesce(sum(total_seconds),0) FROM hb_rollup_daily WHERE sender=$1`,
			owner).Scan(&totalSec)
		if totalSec < 5390 || totalSec > 5410 {
			t.Fatalf("rollup total_seconds = %d, want ~5400", totalSec)
		}
	})

	t.Run("out-of-order workouts: earliest-first rollup anchor picks true minimum", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "wk_reorder")
		// Deliberately not in chronological order. If SaveWorkouts anchors
		// rollup at workouts[0].Start (2025-06-08) instead of true min
		// (2025-06-07), the earlier-day rollup would be missing → we assert
		// hb_rollup_daily has BOTH dates. Exercises the "t.Before(earliest)"
		// branch.
		later := day(2025, 6, 8).Add(10 * time.Hour)
		earlier := day(2025, 6, 7).Add(10 * time.Hour)
		workouts := []model.WorkoutPayload{
			mkWorkout("running", unix(later), 600, "wk_reorder_late_"+owner),
			mkWorkout("cycling", unix(earlier), 900, "wk_reorder_early_"+owner),
		}
		if _, err := d.SaveWorkouts(ctx, owner, workouts); err != nil {
			t.Fatal(err)
		}
		var distinctDays int
		if err := d.Pool.QueryRow(ctx,
			`SELECT count(distinct day) FROM hb_rollup_daily WHERE sender=$1`, owner).Scan(&distinctDays); err != nil {
			t.Fatal(err)
		}
		if distinctDays != 2 {
			t.Fatalf("rollup distinct days = %d, want 2 (earliest-anchor branch missed the earlier workout)", distinctDays)
		}
	})

	t.Run("route jsonb: populated route persists; empty route stays NULL", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "wk_route")
		base := day(2025, 6, 10).Add(6 * time.Hour)
		withRoute := mkWorkout("running", unix(base), 900, "wk_route_A_"+owner)
		alt := 12.5
		withRoute.Route = []model.RoutePoint{
			{T: unix(base), Lat: 37.77, Lon: -122.41, Alt: &alt},
			{T: unix(base.Add(30 * time.Second)), Lat: 37.78, Lon: -122.42},
		}
		noRoute := mkWorkout("cycling", unix(base.Add(2*time.Hour)), 900, "wk_route_B_"+owner)
		if _, err := d.SaveWorkouts(ctx, owner, []model.WorkoutPayload{withRoute, noRoute}); err != nil {
			t.Fatal(err)
		}
		var routeRaw []byte
		if err := d.Pool.QueryRow(ctx,
			`SELECT wd.route FROM workout_details wd JOIN heartbeats h ON h.id = wd.heartbeat_id
			  WHERE h.sender=$1 AND wd.source_uuid=$2`,
			owner, withRoute.SourceUUID).Scan(&routeRaw); err != nil {
			t.Fatal(err)
		}
		var pts []model.RoutePoint
		if err := json.Unmarshal(routeRaw, &pts); err != nil {
			t.Fatalf("route not valid JSON: %v (%s)", err, routeRaw)
		}
		if len(pts) != 2 {
			t.Fatalf("route len = %d, want 2", len(pts))
		}
		var isNull bool
		_ = d.Pool.QueryRow(ctx,
			`SELECT wd.route IS NULL FROM workout_details wd JOIN heartbeats h ON h.id = wd.heartbeat_id
			  WHERE h.sender=$1 AND wd.source_uuid=$2`,
			owner, noRoute.SourceUUID).Scan(&isNull)
		if !isNull {
			t.Fatalf("route must be NULL for workout with no route, not JSON literal")
		}
	})

	t.Run("label routes to project bucket; empty label falls back to kind", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "wk_label")
		base := day(2025, 6, 6).Add(6 * time.Hour)
		label := "Morning Run"
		labeled := mkWorkout("running", unix(base), 1200, "wk_lbl_A_"+owner)
		labeled.Label = &label
		unlabeled := mkWorkout("cycling", unix(base.Add(2*time.Hour)), 1200, "wk_lbl_B_"+owner)
		if _, err := d.SaveWorkouts(ctx, owner, []model.WorkoutPayload{labeled, unlabeled}); err != nil {
			t.Fatal(err)
		}
		var projLabeled, projUnlabeled string
		_ = d.Pool.QueryRow(ctx,
			`SELECT project FROM heartbeats WHERE sender=$1 AND workout_kind='running'`,
			owner).Scan(&projLabeled)
		_ = d.Pool.QueryRow(ctx,
			`SELECT project FROM heartbeats WHERE sender=$1 AND workout_kind='cycling'`,
			owner).Scan(&projUnlabeled)
		if projLabeled != "Morning Run" {
			t.Fatalf("labeled project = %q, want %q", projLabeled, label)
		}
		if projUnlabeled != "cycling" {
			t.Fatalf("unlabeled project = %q, want fallback to kind %q", projUnlabeled, "cycling")
		}
	})
}

func TestGetHealthActivity(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	t.Run("empty range → HasData=false and zeroed totals", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "ga_empty")
		t0 := day(2025, 7, 1)
		got, err := d.GetHealthActivity(ctx, owner, t0, t0.AddDate(0, 0, 7))
		if err != nil {
			t.Fatal(err)
		}
		if got.HasData {
			t.Fatalf("HasData=true on empty owner, want false")
		}
		if len(got.Days) != 0 {
			t.Fatalf("Days len = %d, want 0", len(got.Days))
		}
		if got.Totals.Workouts != 0 || got.Totals.Steps != 0 || got.Totals.AvgHR != 0 {
			t.Fatalf("Totals not zeroed: %+v", got.Totals)
		}
	})

	t.Run("day merge: workouts d1 + samples d1+d2 → 2 HealthActivityDay entries", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "ga_merge")
		d1 := day(2025, 7, 2).Add(9 * time.Hour)
		d2 := day(2025, 7, 3).Add(9 * time.Hour)
		// 2 workouts on d1.
		workouts := []model.WorkoutPayload{
			mkWorkout("running", unix(d1), 1200, "ga_merge_A_"+owner),
			mkWorkout("cycling", unix(d1.Add(2*time.Hour)), 1800, "ga_merge_B_"+owner),
		}
		if _, err := d.SaveWorkouts(ctx, owner, workouts); err != nil {
			t.Fatal(err)
		}
		// 5 samples across d1 and d2: 3 on d1 (steps), 2 on d2 (steps).
		samples := []model.HealthSamplePayload{
			mkSample("steps", "count", 500, unix(d1.Add(30*time.Minute))),
			mkSample("steps", "count", 600, unix(d1.Add(60*time.Minute))),
			mkSample("steps", "count", 700, unix(d1.Add(90*time.Minute))),
			mkSample("steps", "count", 800, unix(d2)),
			mkSample("steps", "count", 900, unix(d2.Add(15*time.Minute))),
		}
		if _, err := d.SaveHealthSamples(ctx, owner, samples); err != nil {
			t.Fatal(err)
		}

		got, err := d.GetHealthActivity(ctx, owner, day(2025, 7, 1), day(2025, 7, 10))
		if err != nil {
			t.Fatal(err)
		}
		if !got.HasData {
			t.Fatalf("HasData=false, want true")
		}
		if len(got.Days) != 2 {
			t.Fatalf("Days len = %d, want 2 (%v)", len(got.Days), got.Days)
		}
		if got.Days[0].Day != "2025-07-02" || got.Days[1].Day != "2025-07-03" {
			t.Fatalf("day sort broke: %+v", got.Days)
		}
	})

	t.Run("workout aggregate same-day: 3 workouts → count=3 minutes~4.5 kcal=450", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "ga_wksum")
		d1 := day(2025, 7, 4).Add(7 * time.Hour)
		kcalA, kcalB, kcalC := 100.0, 200.0, 150.0
		workouts := []model.WorkoutPayload{
			// durations 60s, 90s, 120s → total 270s = 4.5min
			{Kind: "running", Start: unix(d1), End: unix(d1.Add(60 * time.Second)),
				DurationS: 60, Kcal: &kcalA, SourceUUID: "ga_wksum_A_" + owner},
			{Kind: "cycling", Start: unix(d1.Add(1 * time.Hour)),
				End:       unix(d1.Add(1*time.Hour + 90*time.Second)),
				DurationS: 90, Kcal: &kcalB, SourceUUID: "ga_wksum_B_" + owner},
			{Kind: "hiking", Start: unix(d1.Add(2 * time.Hour)),
				End:       unix(d1.Add(2*time.Hour + 120*time.Second)),
				DurationS: 120, Kcal: &kcalC, SourceUUID: "ga_wksum_C_" + owner},
		}
		if _, err := d.SaveWorkouts(ctx, owner, workouts); err != nil {
			t.Fatal(err)
		}
		got, err := d.GetHealthActivity(ctx, owner, day(2025, 7, 4), day(2025, 7, 5))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Days) != 1 {
			t.Fatalf("Days len = %d, want 1", len(got.Days))
		}
		d1row := got.Days[0]
		if d1row.Workouts != 3 {
			t.Fatalf("Workouts = %d, want 3", d1row.Workouts)
		}
		if d1row.WorkoutMinutes < 4.49 || d1row.WorkoutMinutes > 4.51 {
			t.Fatalf("WorkoutMinutes = %v, want ~4.5", d1row.WorkoutMinutes)
		}
		if d1row.ActiveKcal < 449.5 || d1row.ActiveKcal > 450.5 {
			t.Fatalf("ActiveKcal = %v, want ~450", d1row.ActiveKcal)
		}
	})

	t.Run("HR sample aggregate: [70,75,80] → day.AvgHR=75 (within tolerance)", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "ga_hr")
		d1 := day(2025, 7, 5).Add(9 * time.Hour)
		samples := []model.HealthSamplePayload{
			mkHRSample(70, unix(d1)),
			mkHRSample(75, unix(d1.Add(10*time.Minute))),
			mkHRSample(80, unix(d1.Add(20*time.Minute))),
		}
		if _, err := d.SaveHealthSamples(ctx, owner, samples); err != nil {
			t.Fatal(err)
		}
		got, err := d.GetHealthActivity(ctx, owner, day(2025, 7, 5), day(2025, 7, 6))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Days) != 1 {
			t.Fatalf("Days len = %d, want 1 (%+v)", len(got.Days), got.Days)
		}
		hr := got.Days[0].AvgHR
		if hr < 74.5 || hr > 75.5 {
			t.Fatalf("AvgHR = %v, want ~75", hr)
		}
	})

	t.Run("half-open window: workout at t1-1s included, at t1 excluded", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "ga_bound")
		// t0 exclusive-of-nothing, t1 exclusive-of-t1.
		t0 := day(2025, 7, 10)
		t1 := day(2025, 7, 11)
		// Workout right before t1 (included) and right at t1 (excluded).
		insideStart := t1.Add(-1 * time.Second)
		onBoundary := t1
		workouts := []model.WorkoutPayload{
			mkWorkout("running", unix(insideStart), 1, "ga_bound_in_"+owner),
			mkWorkout("running", unix(onBoundary), 1, "ga_bound_edge_"+owner),
		}
		if _, err := d.SaveWorkouts(ctx, owner, workouts); err != nil {
			t.Fatal(err)
		}
		got, err := d.GetHealthActivity(ctx, owner, t0, t1)
		if err != nil {
			t.Fatal(err)
		}
		var totalWorkouts int64
		for _, dd := range got.Days {
			totalWorkouts += dd.Workouts
		}
		if totalWorkouts != 1 {
			t.Fatalf("workouts in [t0,t1) = %d, want 1 (half-open must exclude the row AT t1)", totalWorkouts)
		}
	})

	t.Run("all sample kinds fan out: active_energy/steps/hrv/mindful populate distinct day fields", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "ga_kinds")
		d1 := day(2025, 7, 15).Add(6 * time.Hour)
		samples := []model.HealthSamplePayload{
			// active_energy: two kcal deltas — 40 + 60 = 100.
			mkSample("active_energy", "kcal", 40, unix(d1)),
			mkSample("active_energy", "kcal", 60, unix(d1.Add(30*time.Minute))),
			// steps: 3000.
			mkSample("steps", "count", 3000, unix(d1.Add(1*time.Hour))),
			// hrv (avg-of-avg): 30, 50 → 40.
			{Kind: "hrv", Unit: "ms", QAvg: ptrF(30), TsStart: unix(d1.Add(2 * time.Hour))},
			{Kind: "hrv", Unit: "ms", QAvg: ptrF(50), TsStart: unix(d1.Add(3 * time.Hour))},
			// mindful intervals: 5min + 10min = 900s → 15 minutes.
			{Kind: "mindful", Unit: "minutes", TsStart: unix(d1.Add(4 * time.Hour)),
				TsEnd: ptrF(unix(d1.Add(4*time.Hour + 5*time.Minute)))},
			{Kind: "mindful", Unit: "minutes", TsStart: unix(d1.Add(5 * time.Hour)),
				TsEnd: ptrF(unix(d1.Add(5*time.Hour + 10*time.Minute)))},
		}
		if _, err := d.SaveHealthSamples(ctx, owner, samples); err != nil {
			t.Fatal(err)
		}
		got, err := d.GetHealthActivity(ctx, owner, day(2025, 7, 15), day(2025, 7, 16))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Days) == 0 {
			t.Fatalf("no days returned")
		}
		row := got.Days[0]
		if row.ActiveKcal < 99 || row.ActiveKcal > 101 {
			t.Fatalf("ActiveKcal=%v, want ~100", row.ActiveKcal)
		}
		if row.Steps < 2990 || row.Steps > 3010 {
			t.Fatalf("Steps=%d, want ~3000", row.Steps)
		}
		if row.HRVMs < 39 || row.HRVMs > 41 {
			t.Fatalf("HRVMs=%v, want ~40", row.HRVMs)
		}
		if row.MindfulMinutes < 14 || row.MindfulMinutes > 16 {
			t.Fatalf("MindfulMinutes=%v, want ~15", row.MindfulMinutes)
		}
	})

	t.Run("resting_heart_rate and sleep_stage flow into their own day fields", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "ga_ext")
		d1 := day(2025, 7, 12).Add(6 * time.Hour)
		// resting HR: 55, 65 → avg = 60. Sleep interval: 1h.
		rest1 := mkSample("resting_heart_rate", "bpm", 55, unix(d1))
		rest2 := mkSample("resting_heart_rate", "bpm", 65, unix(d1.Add(1*time.Hour)))
		sleep := mkSleepSample(unix(d1.Add(-2*time.Hour)), unix(d1.Add(-1*time.Hour)))
		if _, err := d.SaveHealthSamples(ctx, owner, []model.HealthSamplePayload{rest1, rest2, sleep}); err != nil {
			t.Fatal(err)
		}
		got, err := d.GetHealthActivity(ctx, owner, day(2025, 7, 12), day(2025, 7, 13))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Days) == 0 {
			t.Fatalf("no days returned")
		}
		row := got.Days[0]
		if row.RestingHR < 59 || row.RestingHR > 61 {
			t.Fatalf("RestingHR = %v, want ~60", row.RestingHR)
		}
		// 3600s / 60 = 60 minutes. Wide tolerance for real4 storage.
		if row.SleepMinutes < 59 || row.SleepMinutes > 61 {
			t.Fatalf("SleepMinutes = %v, want ~60", row.SleepMinutes)
		}
	})
}

func TestGetWorkouts(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	t.Run("empty range → HasData=false and empty lists", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "gw_empty")
		got, err := d.GetWorkouts(ctx, owner, day(2025, 8, 1), day(2025, 8, 2))
		if err != nil {
			t.Fatal(err)
		}
		if got.HasData {
			t.Fatalf("HasData=true on empty, want false")
		}
		if len(got.Events) != 0 || len(got.ByLabel) != 0 {
			t.Fatalf("Events=%d ByLabel=%d, want both empty", len(got.Events), len(got.ByLabel))
		}
	})

	t.Run("list sort: 5 workouts returned time_sent DESC", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "gw_sort")
		base := day(2025, 8, 2).Add(6 * time.Hour)
		workouts := make([]model.WorkoutPayload, 5)
		for i := range workouts {
			workouts[i] = mkWorkout("running",
				unix(base.Add(time.Duration(i)*time.Hour)),
				600, "gw_sort_"+owner+"_"+string(rune('A'+i)))
		}
		if _, err := d.SaveWorkouts(ctx, owner, workouts); err != nil {
			t.Fatal(err)
		}
		got, err := d.GetWorkouts(ctx, owner, day(2025, 8, 2), day(2025, 8, 3))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Events) != 5 {
			t.Fatalf("Events len = %d, want 5", len(got.Events))
		}
		// DESC by start: successive StartUnix values must strictly decrease.
		for i := 1; i < len(got.Events); i++ {
			if got.Events[i].StartUnix >= got.Events[i-1].StartUnix {
				t.Fatalf("not sorted DESC at %d: %v then %v",
					i, got.Events[i-1].StartUnix, got.Events[i].StartUnix)
			}
		}
	})

	t.Run("per-label grouping: A(2)+B(1) → totals correct, sorted by totalMin DESC", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "gw_grp")
		base := day(2025, 8, 3).Add(6 * time.Hour)
		labelA, labelB := "LabelA", "LabelB"
		kcalA1, kcalA2, kcalB := 50.0, 80.0, 200.0
		a1 := mkWorkout("running", unix(base), 60, "gw_grp_A1_"+owner)
		a1.Label = &labelA
		a1.Kcal = &kcalA1
		a2 := mkWorkout("running", unix(base.Add(2*time.Hour)), 90, "gw_grp_A2_"+owner)
		a2.Label = &labelA
		a2.Kcal = &kcalA2
		b1 := mkWorkout("cycling", unix(base.Add(4*time.Hour)), 120, "gw_grp_B1_"+owner)
		b1.Label = &labelB
		b1.Kcal = &kcalB
		if _, err := d.SaveWorkouts(ctx, owner, []model.WorkoutPayload{a1, a2, b1}); err != nil {
			t.Fatal(err)
		}
		got, err := d.GetWorkouts(ctx, owner, day(2025, 8, 3), day(2025, 8, 4))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.ByLabel) != 2 {
			t.Fatalf("ByLabel len = %d, want 2 (%+v)", len(got.ByLabel), got.ByLabel)
		}
		// Sort invariant: totalMin DESC. A total = (60+90)/60=2.5; B = 120/60=2.0.
		// So ByLabel[0]=LabelA, ByLabel[1]=LabelB.
		if got.ByLabel[0].Label != labelA {
			t.Fatalf("ByLabel[0].Label=%q, want %q (totalMin sort broke)", got.ByLabel[0].Label, labelA)
		}
		if got.ByLabel[0].Count != 2 {
			t.Fatalf("LabelA.Count=%d, want 2", got.ByLabel[0].Count)
		}
		if got.ByLabel[0].TotalMin < 2.49 || got.ByLabel[0].TotalMin > 2.51 {
			t.Fatalf("LabelA.TotalMin=%v, want ~2.5", got.ByLabel[0].TotalMin)
		}
		if got.ByLabel[1].Label != labelB {
			t.Fatalf("ByLabel[1].Label=%q, want %q", got.ByLabel[1].Label, labelB)
		}
		if got.ByLabel[1].Count != 1 {
			t.Fatalf("LabelB.Count=%d, want 1", got.ByLabel[1].Count)
		}
		if got.ByLabel[1].TotalMin < 1.99 || got.ByLabel[1].TotalMin > 2.01 {
			t.Fatalf("LabelB.TotalMin=%v, want ~2.0", got.ByLabel[1].TotalMin)
		}
		// Kcal accumulation: LabelA = 50+80 = 130; LabelB = 200.
		if got.ByLabel[0].TotalKcal < 129 || got.ByLabel[0].TotalKcal > 131 {
			t.Fatalf("LabelA.TotalKcal=%v, want ~130", got.ByLabel[0].TotalKcal)
		}
		if got.ByLabel[1].TotalKcal < 199 || got.ByLabel[1].TotalKcal > 201 {
			t.Fatalf("LabelB.TotalKcal=%v, want ~200", got.ByLabel[1].TotalKcal)
		}
	})

	t.Run("cross-owner isolation: owner B sees 0 rows from owner A", func(t *testing.T) {
		ownerA, ctx := newHealthOwner(t, d, "gw_isoA")
		ownerB, _ := newHealthOwner(t, d, "gw_isoB")
		base := day(2025, 8, 4).Add(6 * time.Hour)
		if _, err := d.SaveWorkouts(ctx, ownerA, []model.WorkoutPayload{
			mkWorkout("running", unix(base), 600, "gw_iso_A_"+ownerA),
			mkWorkout("cycling", unix(base.Add(2*time.Hour)), 600, "gw_iso_B_"+ownerA),
		}); err != nil {
			t.Fatal(err)
		}
		got, err := d.GetWorkouts(ctx, ownerB, day(2025, 8, 4), day(2025, 8, 5))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Events) != 0 || got.HasData {
			t.Fatalf("cross-owner leak: ownerB got %d events HasData=%v", len(got.Events), got.HasData)
		}
	})

	t.Run("label HR averaging: mean of non-zero AvgHR; NULL-only label → 0", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "gw_hr")
		base := day(2025, 8, 5).Add(6 * time.Hour)
		hrA1 := int64(140)
		hrA2 := int64(160)
		labelWithHR, labelNoHR := "WithHR", "NoHR"
		// Two "WithHR" workouts with AvgHR=140 and 160 → mean 150.
		wA1 := mkWorkout("running", unix(base), 600, "gw_hr_A1_"+owner)
		wA1.Label = &labelWithHR
		wA1.AvgHR = &hrA1
		wA2 := mkWorkout("running", unix(base.Add(1*time.Hour)), 600, "gw_hr_A2_"+owner)
		wA2.Label = &labelWithHR
		wA2.AvgHR = &hrA2
		// One "NoHR" workout with NULL AvgHR → summary AvgHR must be 0 (no /0).
		wB := mkWorkout("cycling", unix(base.Add(2*time.Hour)), 600, "gw_hr_B_"+owner)
		wB.Label = &labelNoHR
		// AvgHR left nil.
		if _, err := d.SaveWorkouts(ctx, owner, []model.WorkoutPayload{wA1, wA2, wB}); err != nil {
			t.Fatal(err)
		}
		got, err := d.GetWorkouts(ctx, owner, day(2025, 8, 5), day(2025, 8, 6))
		if err != nil {
			t.Fatal(err)
		}
		byLabel := map[string]model.WorkoutLabelSummary{}
		for _, s := range got.ByLabel {
			byLabel[s.Label] = s
		}
		withHR, ok := byLabel[labelWithHR]
		if !ok {
			t.Fatalf("no ByLabel entry for %q: %+v", labelWithHR, got.ByLabel)
		}
		if withHR.AvgHR < 149.5 || withHR.AvgHR > 150.5 {
			t.Fatalf("%s AvgHR=%v, want ~150", labelWithHR, withHR.AvgHR)
		}
		noHR, ok := byLabel[labelNoHR]
		if !ok {
			t.Fatalf("no ByLabel entry for %q: %+v", labelNoHR, got.ByLabel)
		}
		if noHR.AvgHR != 0 {
			t.Fatalf("%s AvgHR=%v, want 0 (all-NULL label must NOT /0)", labelNoHR, noHR.AvgHR)
		}
	})

	t.Run("zero-AvgHR sample skipped from label mean (avgHR>0 gate)", func(t *testing.T) {
		owner, ctx := newHealthOwner(t, d, "gw_zerohr")
		base := day(2025, 8, 6).Add(6 * time.Hour)
		label := "MixHR"
		hr0 := int64(0)
		hr100 := int64(100)
		w1 := mkWorkout("running", unix(base), 600, "gw_zerohr_A_"+owner)
		w1.Label = &label
		w1.AvgHR = &hr0
		w2 := mkWorkout("running", unix(base.Add(1*time.Hour)), 600, "gw_zerohr_B_"+owner)
		w2.Label = &label
		w2.AvgHR = &hr100
		if _, err := d.SaveWorkouts(ctx, owner, []model.WorkoutPayload{w1, w2}); err != nil {
			t.Fatal(err)
		}
		got, err := d.GetWorkouts(ctx, owner, day(2025, 8, 6), day(2025, 8, 7))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.ByLabel) != 1 {
			t.Fatalf("ByLabel len = %d, want 1", len(got.ByLabel))
		}
		if got.ByLabel[0].AvgHR < 99.5 || got.ByLabel[0].AvgHR > 100.5 {
			t.Fatalf("AvgHR=%v, want ~100 (zero-HR workout must not pull the mean down to 50)", got.ByLabel[0].AvgHR)
		}
	})
}
