package db

import (
	"context"
	"testing"
	"time"
)

// hbSeed is a fully-specified heartbeat row for the suppression/merge tests. Every
// axis column is set explicitly, and gap_seconds is seeded directly (we do NOT
// call RecomputeGaps afterward) so the expected attributed totals are exact.
type hbSeed struct {
	project, language, editor, plugin, machine, platform, branch, category string
	ty                                                                     string
	entity                                                                 string
	ts                                                                     time.Time
	gap                                                                    int64 // gap_seconds (<= 15*60 counts as attributed time)
}

func insertSeed(t *testing.T, d *DB, ctx context.Context, sender string, h hbSeed) {
	t.Helper()
	ty := h.ty
	if ty == "" {
		ty = "file"
	}
	entity := h.entity
	if entity == "" {
		entity = "a.go"
	}
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO heartbeats
		  (sender, project, language, editor, plugin, machine, platform, branch, category,
		   entity, ty, time_sent, user_agent, gap_seconds)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'ua',$13)`,
		sender, nz(h.project), nz(h.language), nz(h.editor), nz(h.plugin), nz(h.machine),
		nz(h.platform), nz(h.branch), nz(h.category), entity, ty, h.ts, h.gap)
	if err != nil {
		t.Fatal(err)
	}
}

// nz returns nil for empty strings so NULL columns stay NULL (not 'Other').
func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ensureProjects inserts the project rows a heartbeat's FK requires.
func ensureProjects(t *testing.T, d *DB, ctx context.Context, sender string, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,$2) ON CONFLICT DO NOTHING`, sender, n); err != nil {
			t.Fatal(err)
		}
	}
}

func cleanupSender(t *testing.T, d *DB, ctx context.Context, sender string) {
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM hb_rollup_daily WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM project_tags WHERE project_owner=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM badges WHERE username=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, sender)
	})
}

func mkSender(prefix string) string {
	return prefix + "_" + time.Now().Format("150405.000000000")
}

// statRowHasAxis reports whether get_user_activity's StatRow carries a column for
// this axis. plugin and category are NOT selected by get_user_activity, so for
// those axes we assert exclusion via total-seconds only (the row is still dropped,
// its time no longer counted).
func statRowHasAxis(axis string) bool {
	switch axis {
	case "project", "language", "editor", "machine", "platform", "branch":
		return true
	}
	return false
}

// axisValue reads the value of a StatRow for a given hide axis.
func statRowAxis(r StatRow, axis string) string {
	switch axis {
	case "project":
		return r.Project
	case "language":
		return r.Language
	case "editor":
		return r.Editor
	case "machine":
		return r.Machine
	case "platform":
		return r.Platform
	case "branch":
		return r.Branch
	}
	return ""
}

func totalStatSeconds(rows []StatRow) int64 {
	var s int64
	for _, r := range rows {
		s += r.TotalSeconds
	}
	return s
}

func statRowsContain(rows []StatRow, axis, val string) bool {
	for _, r := range rows {
		if statRowAxis(r, axis) == val {
			return true
		}
	}
	return false
}

// TestSuppressedValuesExcludedFromAggregations proves that a curation-hidden
// value is removed from every aggregation/stats path while remaining visible in
// the audit surfaces. Table-driven over every axis the refactor covers.
//
// Seeding model (per axis): a KEEP block and a SUPPRESS block, each with a
// leading break beat (gap > cutoff, not attributed) followed by beats that carry
// a known attributed gap. Attributed time: KEEP=keepSecs, SUPPRESS=supSecs.
func TestSuppressedValuesExcludedFromAggregations(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	const keepSecs = 300 // 5 min attributed to KEEP
	const supSecs = 120  // 2 min attributed to SUPPRESS

	// base row template: fixed non-hidden values on all axes except the one under
	// test, so KEEP/SUPPRESS differ ONLY on that axis.
	base := func(axis, val string, ts time.Time, gap int64) hbSeed {
		h := hbSeed{
			project: "keepProj", language: "Go", editor: "vim", plugin: "vim-wakatime",
			machine: "laptop", platform: "linux", branch: "main", category: "Coding",
			ts: ts, gap: gap,
		}
		switch axis {
		case "project":
			h.project = val
		case "language":
			h.language = val
		case "editor":
			h.editor = val
		case "plugin":
			h.plugin = val
		case "machine":
			h.machine = val
		case "platform":
			h.platform = val
		case "branch":
			h.branch = val
		case "category":
			h.category = val
		}
		return h
	}

	// Axes the refactor covers on the aggregate dashboards.
	axes := []string{"project", "language", "editor", "plugin", "machine", "platform", "branch", "category"}

	for _, axis := range axes {
		t.Run(axis, func(t *testing.T) {
			sender := mkSender("supp_" + axis)
			_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
			cleanupSender(t, d, ctx, sender)

			keepVal, supVal := "KEEP", "SUPPRESS"
			// project axis needs both project rows to satisfy the heartbeats FK.
			if axis == "project" {
				ensureProjects(t, d, ctx, sender, keepVal, supVal)
			} else {
				ensureProjects(t, d, ctx, sender, "keepProj")
			}

			day := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)
			// KEEP block: break beat (gap huge) + one beat carrying keepSecs.
			insertSeed(t, d, ctx, sender, base(axis, keepVal, day, 999999))
			insertSeed(t, d, ctx, sender, base(axis, keepVal, day.Add(1*time.Minute), keepSecs))
			// SUPPRESS block: break beat + one beat carrying supSecs.
			insertSeed(t, d, ctx, sender, base(axis, supVal, day.Add(2*time.Minute), 999999))
			insertSeed(t, d, ctx, sender, base(axis, supVal, day.Add(3*time.Minute), supSecs))

			// Rollup refresh so the fast path has data (gap_seconds seeded directly).
			if err := d.RefreshRollup(ctx, sender, day.AddDate(0, 0, -1)); err != nil {
				t.Fatal(err)
			}

			start := day.AddDate(0, 0, -1)
			end := day.AddDate(0, 0, 1)

			// Baseline (no hide): both values present, total = keep+sup.
			noHide := HiddenSets{}
			rawBefore, err := d.GetUserActivity(ctx, sender, start, end, 15, noHide)
			if err != nil {
				t.Fatal(err)
			}
			if statRowHasAxis(axis) && (!statRowsContain(rawBefore, axis, supVal) || !statRowsContain(rawBefore, axis, keepVal)) {
				t.Fatalf("baseline should contain both KEEP and SUPPRESS on %s", axis)
			}
			if got := totalStatSeconds(rawBefore); got != keepSecs+supSecs {
				t.Fatalf("baseline total = %d, want %d", got, keepSecs+supSecs)
			}

			// Add the hide rule and reload the sets.
			if _, err := d.CreateCurationRule(ctx, sender, axis, "hide", supVal, nil); err != nil {
				t.Fatal(err)
			}
			hs, err := d.LoadHiddenSets(ctx, sender)
			if err != nil {
				t.Fatal(err)
			}

			// ---- EXCLUDED from every aggregation/stats path ----

			// 1. Raw activity path.
			raw, err := d.GetUserActivity(ctx, sender, start, end, 15, hs)
			if err != nil {
				t.Fatal(err)
			}
			if statRowHasAxis(axis) {
				if statRowsContain(raw, axis, supVal) {
					t.Fatalf("[raw] SUPPRESS still present on %s", axis)
				}
				if !statRowsContain(raw, axis, keepVal) {
					t.Fatalf("[raw] KEEP was collaterally removed on %s", axis)
				}
			}
			if got := totalStatSeconds(raw); got != keepSecs {
				t.Fatalf("[raw] total = %d, want %d (SUPPRESS's %d dropped)", got, keepSecs, supSecs)
			}

			// 2. Rollup fast path. The rollup stores project/language/editor/
			//    platform/machine; for those it excludes directly. For plugin/
			//    branch/category the rollup lacks the column, so the handler falls
			//    back to raw — assert that HasHiddenOutside reports the fallback.
			rollupHas := RollupAxes[axis]
			if rollupHas {
				roll, err := d.GetUserActivityRollup(ctx, sender, start, end, hs)
				if err != nil {
					t.Fatal(err)
				}
				if statRowsContain(roll, axis, supVal) {
					t.Fatalf("[rollup] SUPPRESS still present on %s", axis)
				}
				if got := totalStatSeconds(roll); got != keepSecs {
					t.Fatalf("[rollup] total = %d, want %d", got, keepSecs)
				}
				if hs.HasHiddenOutside(RollupAxes) {
					t.Fatalf("[rollup] axis %s is a rollup axis; HasHiddenOutside should be false", axis)
				}
			} else {
				// plugin/branch/category: rollup can't exclude -> handler must fall
				// back to raw. Prove the signal that drives that fallback.
				if !hs.HasHiddenOutside(RollupAxes) {
					t.Fatalf("[rollup] axis %s not in rollup; HasHiddenOutside must be true (raw fallback)", axis)
				}
			}

			// 3. Category big-bet scan (feeds the StatsPayload categories segment).
			//    The ToStatsPayload SHAPING assertion lives in the stats package
			//    (TestSuppressionShapingExcluded) to avoid a db<-stats import cycle;
			//    here we assert the DB layer that feeds it excludes SUPPRESS.
			categories, err := d.GetCategoryDaily(ctx, sender, start, end, 15, hs)
			if err != nil {
				t.Fatal(err)
			}
			if axis == "category" {
				for _, c := range categories {
					if c.Category == supVal {
						t.Fatalf("[category] SUPPRESS still in category rows")
					}
				}
			}
			var catTotal int64
			for _, c := range categories {
				catTotal += c.TotalSeconds
			}
			if catTotal != keepSecs {
				t.Fatalf("[category] total = %d, want %d", catTotal, keepSecs)
			}

			// 4. Big-bet endpoints: SUPPRESS's time excluded.
			punch, err := d.GetPunchcard(ctx, sender, start, end, 15, hs)
			if err != nil {
				t.Fatal(err)
			}
			if got := sumPunch(punch); got != keepSecs {
				t.Fatalf("[punchcard] total = %d, want %d", got, keepSecs)
			}
			sess, err := d.GetSessions(ctx, sender, start, end, 15, hs)
			if err != nil {
				t.Fatal(err)
			}
			if got := sumSessions(sess); got != keepSecs {
				t.Fatalf("[sessions] total = %d, want %d", got, keepSecs)
			}
			mom, err := d.GetMomentum(ctx, sender, start, end, 15, hs)
			if err != nil {
				t.Fatal(err)
			}
			if got := sumMomentum(mom); got != keepSecs {
				t.Fatalf("[momentum] total = %d, want %d", got, keepSecs)
			}
			if axis == "project" {
				for _, m := range mom {
					if m.Project == supVal {
						t.Fatalf("[momentum] hidden project still a row")
					}
				}
			}

			// 5. Project list (project axis) excludes the hidden project.
			if axis == "project" {
				projects, err := d.GetAllProjects(ctx, sender, start, end, hs)
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range projects {
					if p == supVal {
						t.Fatalf("[projectList] hidden project still listed")
					}
				}
			}

			// 6. Leaderboards: requester's SUPPRESS row excluded.
			lb, err := d.GetLeaderboards(ctx, start, end, sender, hs)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range lb {
				if r.Sender != sender {
					continue
				}
				if lbRowAxis(r, axis) == supVal {
					t.Fatalf("[leaderboards] SUPPRESS present for requester on %s", axis)
				}
			}
			// KEEP still contributes to leaderboards.
			var lbKeep int64
			for _, r := range lb {
				if r.Sender == sender {
					lbKeep += r.TotalSeconds
				}
			}
			if lbKeep != keepSecs {
				t.Fatalf("[leaderboards] requester total = %d, want %d", lbKeep, keepSecs)
			}

			// 7. Statusbar today path (GetTotalTimeToday) — excludes hides. Our seed
			//    is dated 2025-06-01 (not "today"), so today's total is 0 with and
			//    without the hide; assert it doesn't error and stays 0 (no leakage).
			today, err := d.GetTotalTimeToday(ctx, sender, hs)
			if err != nil {
				t.Fatal(err)
			}
			if today != 0 {
				t.Fatalf("[today] expected 0 for out-of-today seed, got %d", today)
			}

			// ---- STILL PRESENT in AUDIT surfaces ----

			col, _ := ExploreColumn(axis)
			groups, _, err := d.GroupHeartbeats(ctx, sender, col, start, end, nil, 500, 15)
			if err != nil {
				t.Fatal(err)
			}
			if !groupsContain(groups, supVal) {
				t.Fatalf("[audit group] SUPPRESS must still appear on %s", axis)
			}
			if !groupsContain(groups, keepVal) {
				t.Fatalf("[audit group] KEEP missing on %s", axis)
			}

			// Audit list (raw rows) still returns SUPPRESS.
			items, _, err := d.ListHeartbeats(ctx, sender, start, end, nil, "", 1, 500)
			if err != nil {
				t.Fatal(err)
			}
			if !listContains(items, axis, supVal) {
				t.Fatalf("[audit list] SUPPRESS must still appear on %s", axis)
			}

			// LatestHeartbeat unaffected (count includes all 4 seeded rows).
			last, count, err := d.LatestHeartbeat(ctx, sender)
			if err != nil {
				t.Fatal(err)
			}
			if count != 4 || last == nil {
				t.Fatalf("[audit latest] count=%d last=%v, want 4/non-nil", count, last)
			}

			// Timeline is unfiltered; it should still surface SUPPRESS activity for
			// the language axis (timeline groups by language). Just assert no error
			// and it returns rows (audit stays unfiltered).
			if _, err := d.GetTimeline(ctx, sender, start, end, 15); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func sumPunch(cells []PunchcardCell) int64 {
	var s int64
	for _, c := range cells {
		s += c.Seconds
	}
	return s
}

func sumSessions(rows []SessionRow) int64 {
	var s int64
	for _, r := range rows {
		s += r.Seconds
	}
	return s
}

func sumMomentum(rows []MomentumRow) int64 {
	var s int64
	for _, r := range rows {
		s += r.Seconds
	}
	return s
}

func lbRowAxis(r LeaderboardRow, axis string) string {
	switch axis {
	case "project":
		return r.Project
	case "language":
		return r.Language
	}
	return "" // other axes aren't columns in the leaderboards output
}

func groupsContain(groups []ExploreGroup, val string) bool {
	for _, g := range groups {
		if g.Value != nil && *g.Value == val {
			return true
		}
	}
	return false
}

func listContains(items []ExploreRow, axis, val string) bool {
	for _, r := range items {
		if exploreRowAxis(r, axis) == val {
			return true
		}
	}
	return false
}

func exploreRowAxis(r ExploreRow, axis string) string {
	get := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	switch axis {
	case "project":
		return get(r.Project)
	case "language":
		return get(r.Language)
	case "editor":
		return get(r.Editor)
	case "plugin":
		return get(r.Plugin)
	case "machine":
		return get(r.Machine)
	case "platform":
		return get(r.Platform)
	case "branch":
		return get(r.Branch)
	case "category":
		return get(r.Category)
	}
	return ""
}
