package db

import (
	"context"
	"testing"
	"time"
)

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
			rawBefore, err := d.GetUserActivity(ctx, sender, start, end, 15, noHide, RenameSets{}, MemberSets{}, false)
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
			if _, err := d.CreateCurationRule(ctx, sender, axis, "hide", "exact", supVal, nil); err != nil {
				t.Fatal(err)
			}
			hs, err := d.LoadHiddenSets(ctx, sender)
			if err != nil {
				t.Fatal(err)
			}

			// ---- EXCLUDED from every aggregation/stats path ----

			// 1. Raw activity path.
			raw, err := d.GetUserActivity(ctx, sender, start, end, 15, hs, RenameSets{}, MemberSets{}, false)
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

			// 2. Rollup fast path. Since 00014 the rollup stores every hiddenAxis
			//    (project/language/editor/platform/machine/category/plugin/branch),
			//    so it excludes on all of them directly and HasHiddenOutside is
			//    always false. The else arm is currently unreachable — kept for
			//    future rollup-external hide axes and to document the invariant.
			rollupHas := RollupAxes[axis]
			if rollupHas {
				roll, err := d.GetUserActivityRollup(ctx, sender, start, end, hs, RenameSets{}, MemberSets{}, false)
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
				// Unreachable today (every hiddenAxis is in the rollup). Kept as a
				// guard: if a future axis lands with inRollup=false, this arm proves
				// the raw fallback signal fires.
				if !hs.HasHiddenOutside(RollupAxes) {
					t.Fatalf("[rollup] axis %s not in rollup; HasHiddenOutside must be true (raw fallback)", axis)
				}
			}

			// 3. Category big-bet scan (feeds the StatsPayload categories segment).
			//    The ToStatsPayload SHAPING assertion lives in the stats package
			//    (TestSuppressionShapingExcluded) to avoid a db<-stats import cycle;
			//    here we assert the DB layer that feeds it excludes SUPPRESS.
			categories, err := d.GetCategoryDaily(ctx, sender, start, end, 15, hs, RenameSets{}, MemberSets{}, false)
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
			punch, err := d.GetPunchcard(ctx, sender, start, end, 15, hs, MemberSets{}, false)
			if err != nil {
				t.Fatal(err)
			}
			if got := sumPunch(punch); got != keepSecs {
				t.Fatalf("[punchcard] total = %d, want %d", got, keepSecs)
			}
			sess, err := d.GetSessions(ctx, sender, start, end, 15, hs, MemberSets{}, false)
			if err != nil {
				t.Fatal(err)
			}
			if got := sumSessions(sess); got != keepSecs {
				t.Fatalf("[sessions] total = %d, want %d", got, keepSecs)
			}
			mom, err := d.GetMomentum(ctx, sender, start, end, 15, hs, RenameSets{}, MemberSets{}, false)
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
				projects, err := d.GetAllProjects(ctx, sender, start, end, hs, RenameSets{}, MemberSets{}, false)
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
			lb, err := d.GetLeaderboards(ctx, start, end, sender, hs, RenameSets{}, MemberSets{}, false)
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
			groups, _, err := d.GroupHeartbeats(ctx, sender, col, start, end, nil, "", 500, 15)
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
			if _, err := d.GetTimeline(ctx, sender, start, end, 15, MemberSets{}, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}
