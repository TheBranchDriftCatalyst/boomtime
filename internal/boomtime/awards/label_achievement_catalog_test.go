// awards_coverage_test.go — parametrized "every label in the catalog
// fires against a seeded minimum-viable dataset" test (gaka-hc6.6).
//
// Runs one subtest per label in the DB catalog. Each subtest:
//
//  1. mints a fresh user (auto-cleanup: heartbeats + hb_rollup_daily +
//     award_ledger + users row all get DELETEd via testutil.Cleanup)
//  2. reads the label's Condition, dispatches to a per-primitive
//     heartbeat synthesizer that seeds the minimum data to satisfy it
//  3. hits GET /awards over HTTP and asserts the label's id appears
//
// Skipped intentionally (documented per subtest via t.Skip):
//   - `trend` primitive (needs a specific 14-day doubling pattern —
//     synthesizable but skipped in v1)
//   - `not` and composed `all` / `any` at top level (would need
//     recursion + case analysis over each subcondition — future work)
//   - Any label whose axis is unknown to the synthesizer (defensive
//     for future primitives; loud rather than silent)
//
// Follow-up ticket to close the gap: gaka-hc6.6.1 (fires only when
// this test's skip-count changes).
//
// Isolation guarantees:
//   - Fresh username per subtest, cleaned up on t.Cleanup
//   - No shared TestMain seeding
//   - go test -run 'TestLabelCoverage/languages-python-master' -v works
//     standalone
package awards_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/labels"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// gapCapSeconds mirrors the aggregation's per-heartbeat cap (see
// CASE WHEN gap_seconds <= $4*60 across get_user_activity.sql et al).
// 900s = 15min = the FE's default gap-limit knob. Anything above zeros
// out at read time, so we use exactly this for every seeded beat.
const gapCapSeconds int64 = 900

// beatsForHours returns how many gap-capped heartbeats are needed to
// attribute `hours` on some axis, plus a small safety margin.
func beatsForHours(hours float64) int {
	if hours <= 0 {
		return 1
	}
	needed := int((hours*3600.0)/float64(gapCapSeconds)) + 2
	return needed
}

// axisFieldSetter returns a function that populates the right axis
// field on an HB struct. Case-insensitive matching in the evaluator
// means we can seed with the value verbatim.
func axisFieldSetter(axis labels.Axis, value string) func(*testutil.HB) bool {
	switch axis {
	case labels.AxisLanguages:
		return func(h *testutil.HB) bool { h.Language = value; return true }
	case labels.AxisEditors:
		return func(h *testutil.HB) bool { h.Editor = value; return true }
	case labels.AxisProjects:
		return func(h *testutil.HB) bool { h.Project = value; return true }
	case labels.AxisCategories:
		return func(h *testutil.HB) bool { h.Category = value; return true }
	case labels.AxisPlatforms:
		return func(h *testutil.HB) bool { h.Platform = value; return true }
	}
	return nil
}

// baseHB is the shape every seeded beat lands on unless a primitive
// overrides an axis. Uses coding+file so the aggregation attributes.
func baseHB() testutil.HB {
	return testutil.HB{
		Project:  "covproj",
		Editor:   "vim",
		Language: "go",
		Platform: "linux",
		Category: "coding",
		Entity:   "cov.go",
	}
}

// synthesize dispatches on the Condition kind. Returns an error string
// when the primitive isn't handled (subtest gets t.Skip'd with the
// message). Sender + Projects("covproj") are done by the caller.
type synthResult struct {
	beats []testutil.HB
	skip  string // non-empty → subtest calls t.Skip
	at    time.Time
}

func synthesize(cond labels.Condition, sender string) synthResult {
	// Anchor all seeded time to a recent WEEKDAY MIDDAY. Some primitives
	// (streak, punchcard-*) override the timestamps. Landing 7 days back
	// puts us safely inside the 60-day payload window.
	base := time.Now().UTC().Add(-7 * 24 * time.Hour)
	base = time.Date(base.Year(), base.Month(), base.Day(), 12, 0, 0, 0, time.UTC)
	if base.Weekday() == time.Saturday {
		base = base.Add(48 * time.Hour)
	} else if base.Weekday() == time.Sunday {
		base = base.Add(24 * time.Hour)
	}

	switch c := cond.(type) {
	case labels.AxisTimeCond:
		if c.Op != labels.OpGE {
			return synthResult{skip: "axis-time <= not synthesizable (seed = 0 vacuously satisfies most thresholds)"}
		}
		setter := axisFieldSetter(c.Axis, c.Value)
		if setter == nil {
			return synthResult{skip: "unknown axis " + string(c.Axis)}
		}
		n := beatsForHours(c.Hours)
		beats := makeBlock(base, n, setter)
		return synthResult{beats: beats, at: base}

	case labels.AxisTimeSumCond:
		if c.Op != labels.OpGE {
			return synthResult{skip: "axis-time-sum <= not synthesizable"}
		}
		perValue := c.Hours / float64(len(c.Values))
		var beats []testutil.HB
		anchor := base
		for _, v := range c.Values {
			setter := axisFieldSetter(c.Axis, v)
			if setter == nil {
				return synthResult{skip: "unknown axis " + string(c.Axis)}
			}
			n := beatsForHours(perValue) + 1 // +1 safety per value
			beats = append(beats, makeBlock(anchor, n, setter)...)
			anchor = anchor.Add(time.Duration(n+1) * time.Minute)
		}
		return synthResult{beats: beats, at: base}

	case labels.DistinctCountCond:
		if c.Op != labels.OpGE {
			return synthResult{skip: "distinct-count <= not synthesizable"}
		}
		var beats []testutil.HB
		anchor := base
		// Generate c.N distinct values with each ≥ minHoursEach hours.
		for i := 0; i < c.N; i++ {
			val := fmt.Sprintf("distinctval%d", i)
			setter := axisFieldSetter(c.Axis, val)
			if setter == nil {
				return synthResult{skip: "unknown axis " + string(c.Axis)}
			}
			n := beatsForHours(c.MinHoursEach + 0.1) // safety cushion
			beats = append(beats, makeBlock(anchor, n, setter)...)
			anchor = anchor.Add(time.Duration(n+1) * time.Minute)
		}
		return synthResult{beats: beats, at: base}

	case labels.DailyAvgCond:
		if c.Op != labels.OpGE {
			return synthResult{skip: "daily-avg <= not synthesizable"}
		}
		// dailyAvg = totalSeconds / range-days (60d for public payload).
		// To hit an average of c.Hours per day, need c.Hours * 60 total
		// hours over the window. Spread across 60 days = c.Hours/day but
		// simplest is one big Block at base contributing the sum.
		hoursNeeded := c.Hours * 60.0
		n := beatsForHours(hoursNeeded)
		if n > 3000 {
			return synthResult{skip: "daily-avg threshold too high to seed cheaply"}
		}
		setter := func(h *testutil.HB) bool { return true } // baseHB already has language=go
		beats := makeBlock(base, n, setter)
		return synthResult{beats: beats, at: base}

	case labels.StreakCond:
		if c.Op != labels.OpGE {
			return synthResult{skip: "streak <= not synthesizable"}
		}
		// The /awards handler builds a 60-day payload window. A streak
		// condition with days > 60 is unreachable via this endpoint —
		// dailyTotal will never exceed the window length so no streak
		// >60 days can fire. Report this as a coverage-test skip; the
		// label itself might still be reached via a future
		// full-history-window backfill endpoint (gaka-hc6.6.2).
		if c.Days > 60 {
			return synthResult{skip: fmt.Sprintf("streak.days=%d exceeds /awards 60d payload window", c.Days)}
		}
		// Seed heartbeats on `c.Days` consecutive days ENDING at today
		// (so `current` streak = c.Days; `longest` also = c.Days).
		// r.at must anchor at the OLDEST seeded day so RefreshRollup
		// covers the whole window (default RefreshRollup start is
		// r.at - 24h).
		var beats []testutil.HB
		oldest := time.Now().UTC().Add(-time.Duration(c.Days-1) * 24 * time.Hour)
		oldest = time.Date(oldest.Year(), oldest.Month(), oldest.Day(), 12, 0, 0, 0, time.UTC)
		for d := c.Days - 1; d >= 0; d-- {
			day := time.Now().UTC().Add(-time.Duration(d) * 24 * time.Hour)
			day = time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, time.UTC)
			beats = append(beats, makeBlock(day, 2, func(h *testutil.HB) bool { return true })...)
		}
		return synthResult{beats: beats, at: oldest}

	case labels.PunchcardHourPctCond:
		if c.Op != labels.OpGE {
			return synthResult{skip: "punchcard-hour-pct <= not synthesizable"}
		}
		// Seed 20 beats in a target hour + 5 beats outside → ~80% in-set.
		// pct threshold up to 0.75 comfortably satisfied.
		if c.Pct > 0.75 {
			return synthResult{skip: "punchcard threshold too tight for cheap seed"}
		}
		if len(c.HoursIn) == 0 {
			return synthResult{skip: "empty hoursIn"}
		}
		anchorHour := c.HoursIn[0]
		day := base
		inSet := time.Date(day.Year(), day.Month(), day.Day(), anchorHour, 0, 0, 0, time.UTC)
		outHour := 12
		if anchorHour == 12 {
			outHour = 6
		}
		outSet := time.Date(day.Year(), day.Month(), day.Day(), outHour, 0, 0, 0, time.UTC)
		beats := append(
			makeBlock(inSet, 20, func(h *testutil.HB) bool { return true }),
			makeBlock(outSet, 5, func(h *testutil.HB) bool { return true })...)
		return synthResult{beats: beats, at: base}

	case labels.PunchcardDowPctCond:
		if c.Op != labels.OpGE {
			return synthResult{skip: "punchcard-dow-pct <= not synthesizable"}
		}
		if c.Pct > 0.75 {
			return synthResult{skip: "punchcard dow threshold too tight"}
		}
		if len(c.DowIn) == 0 {
			return synthResult{skip: "empty dowIn"}
		}
		// Anchor at a day whose weekday matches the first entry in DowIn.
		targetDow := time.Weekday(c.DowIn[0])
		day := time.Now().UTC().Add(-3 * 24 * time.Hour)
		for day.Weekday() != targetDow {
			day = day.Add(-24 * time.Hour)
		}
		day = time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, time.UTC)
		outDay := day.Add(-24 * time.Hour) // one day earlier, likely different dow
		beats := append(
			makeBlock(day, 20, func(h *testutil.HB) bool { return true }),
			makeBlock(outDay, 5, func(h *testutil.HB) bool { return true })...)
		return synthResult{beats: beats, at: base}

	case labels.AxisPctCond:
		if c.Op != labels.OpGE {
			return synthResult{skip: "axis-pct <= not synthesizable"}
		}
		setter := axisFieldSetter(c.Axis, c.Value)
		if setter == nil {
			return synthResult{skip: "unknown axis"}
		}
		if c.Pct >= 0.99 {
			return synthResult{skip: "axis-pct threshold too tight"}
		}
		// Want target/(target+distract) >= pct + margin. Solve for target
		// given a fixed small distractor: target = distract*(pct+margin)/(1-pct-margin).
		// Margin keeps us safely above the inclusive-`>=` threshold.
		distractorBeats := 5
		margin := 0.05
		targetPct := c.Pct + margin
		if targetPct >= 0.99 {
			targetPct = c.Pct + 0.005
		}
		targetBeats := int(float64(distractorBeats)*targetPct/(1-targetPct)) + 2
		anchor := base
		var beats []testutil.HB
		beats = append(beats, makeBlock(anchor, targetBeats, setter)...)
		anchor = anchor.Add(time.Duration(targetBeats+1) * time.Minute)
		// Distractor on a made-up value that won't match anyone.
		distractSetter := axisFieldSetter(c.Axis, "covdistract")
		beats = append(beats, makeBlock(anchor, distractorBeats, distractSetter)...)
		return synthResult{beats: beats, at: base}

	case labels.TopShareCond:
		if c.Op != labels.OpGE {
			return synthResult{skip: "top-share <= not synthesizable"}
		}
		if c.Pct >= 0.99 {
			return synthResult{skip: "top-share threshold too tight"}
		}
		// TopShareCond in the evaluator uses list[0] as the "top" — but
		// segmentStat returns first-seen order (not TotalSeconds-desc).
		// Legacy semantics inherited from the TS evaluator. To avoid the
		// ordering trap we seed ONLY the target value — total = target,
		// share = 100% ≥ any pct threshold. Deliberately doesn't test the
		// two-value case (that's an evaluator-behavior gap tracked in
		// gaka-hc6.6.1).
		bigSetter := axisFieldSetter(c.Axis, "covtop")
		if bigSetter == nil {
			return synthResult{skip: "unknown axis"}
		}
		beats := makeBlock(base, 20, bigSetter)
		return synthResult{beats: beats, at: base}

	case labels.TrendCond:
		return synthResult{skip: "trend primitive — needs 14d specific pattern; deferred to gaka-hc6.6.1"}

	case labels.NotCond:
		return synthResult{skip: "top-level `not` — negation seed logic deferred to gaka-hc6.6.1"}

	case labels.AllCond, labels.AnyCond:
		return synthResult{skip: "top-level composed all/any — recursion + case analysis deferred to gaka-hc6.6.1"}
	}
	return synthResult{skip: fmt.Sprintf("unhandled condition kind %T", cond)}
}

// makeBlock produces `n` HB entries spaced 1 minute apart starting at `start`,
// each with gap_seconds=gapCapSeconds and the given axis setter applied to
// a fresh baseHB copy. Caller assigns Sender via the Seeder.
func makeBlock(start time.Time, n int, apply func(*testutil.HB) bool) []testutil.HB {
	beats := make([]testutil.HB, 0, n+1)
	// Leading break beat with a huge gap (unattributed) — matches testutil's
	// Block convention so the aggregation doesn't attribute a spurious gap
	// on the FIRST beat's own timestamp.
	brk := baseHB()
	apply(&brk)
	brk.TS = start
	brk.Gap = 999999
	beats = append(beats, brk)
	for i := 0; i < n; i++ {
		h := baseHB()
		apply(&h)
		h.TS = start.Add(time.Duration(i+1) * time.Minute)
		h.Gap = gapCapSeconds
		beats = append(beats, h)
	}
	return beats
}

// TestLabelCoverage is the epic-defining test — every label in the DB
// catalog must fire against its own minimum-viable seeded fixture.
// Skipped labels file into gaka-hc6.6.1 (recorded via t.Skip on each).
func TestLabelCoverage(t *testing.T) {
	if testing.Short() {
		t.Skip("coverage sweep is expensive; -short skips it")
	}
	hz := testutil.NewHarnessWithDB(t, testutil.OpenIsolatedDB(t, "labelcov"))
	e := hz.Router()

	dbRows, err := hz.DB.ListLabels(t.Context())
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	if len(dbRows) == 0 {
		t.Fatal("catalog is empty — check that migrations 00036/00039/00040/00043 seeded")
	}

	skipped, ran, fired := 0, 0, 0
	for _, row := range dbRows {
		row := row // capture per-subtest
		t.Run(row.ID, func(t *testing.T) {
			spec, err := labels.SpecFromDBRow(labels.DBRow{
				ID:            row.ID,
				Kind:          row.Kind,
				Label:         row.Label,
				Glyph:         row.Glyph,
				Description:   row.Description,
				Rank:          row.Rank,
				Tier:          row.Tier,
				PeriodDefault: row.PeriodDefault,
				Condition:     row.Condition,
			})
			if err != nil {
				t.Fatalf("decode spec: %v", err)
			}

			r := synthesize(spec.Condition, "")
			if r.skip != "" {
				skipped++
				t.Skip(r.skip)
				return
			}
			ran++

			username, token := hz.MintUser("cov" + strings.ReplaceAll(row.ID, "-", ""))
			sd := hz.Seeder(username).Projects("covproj")
			for _, hb := range r.beats {
				sd.Seed(hb)
			}
			if err := hz.DB.RefreshRollup(t.Context(), username, r.at.Add(-24*time.Hour)); err != nil {
				t.Fatalf("RefreshRollup: %v", err)
			}

			// Hit the endpoint the same way a client would — proves the
			// entire aggregation → evaluator → wire path fires the label.
			rec := getJSON(t, e, "/api/v1/users/current/awards", token)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /awards: status %d, body=%s", rec.Code, rec.Body.String())
			}
			var awards []map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &awards); err != nil {
				t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
			}
			if !containsAwardID(awards, row.ID) {
				// Diagnostic: dump every axis's payload so a failure reads
				// as either "synth seeded wrong data" or "evaluator missed
				// its own condition" without needing to re-run with a
				// debugger attached.
				statsRec := getJSON(t, e, "/api/v1/users/current/stats", token)
				var stats map[string]any
				_ = json.Unmarshal(statsRec.Body.Bytes(), &stats)
				var diagAxes []string
				for _, ax := range []string{"projects", "languages", "editors", "platforms", "categories"} {
					if arr, ok := stats[ax].([]any); ok && len(arr) > 0 {
						var entries []string
						for _, p := range arr {
							if m, ok := p.(map[string]any); ok {
								entries = append(entries, fmt.Sprintf("%v=%v", m["name"], m["totalSeconds"]))
							}
						}
						diagAxes = append(diagAxes, ax+": ["+strings.Join(entries, ", ")+"]")
					}
				}
				t.Errorf("label %q did NOT fire.\n  condition: %+v\n  stats: %s\n  awards fired: %v",
					row.ID, spec.Condition, strings.Join(diagAxes, "\n         "), awardIDs(awards))
				return
			}
			fired++
		})
	}
	t.Logf("TestLabelCoverage summary: %d labels ran, %d fired, %d skipped (of %d total)",
		ran, fired, skipped, len(dbRows))
}
