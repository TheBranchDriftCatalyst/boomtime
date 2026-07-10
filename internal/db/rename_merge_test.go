package db

import (
	"context"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/model"
)

// grandTotal returns the summed attributed time across a StatRow set (used to
// prove a merge is a pure relabel: totals are conserved).
func grandTotal(rows []StatRow) int64 { return totalStatSeconds(rows) }

// axisTotals maps axis-value -> summed attributed seconds and -> count of rows.
func axisTotals(rows []StatRow, axis string) (secs map[string]int64) {
	secs = map[string]int64{}
	for _, r := range rows {
		secs[statRowAxis(r, axis)] += r.TotalSeconds
	}
	return secs
}

// seedAxisBlock seeds a break beat (gap huge, unattributed) then n beats each
// carrying `each` attributed seconds, all sharing axis=val. Returns attributed
// total (n*each) and count (n+1 rows, but only n carry time).
func seedAxisBlock(t *testing.T, d *DB, ctx context.Context, sender, axis, val string, startTS time.Time, n int, each int64) (attributed int64, rowCount int) {
	t.Helper()
	mk := func(ts time.Time, gap int64) hbSeed {
		h := hbSeed{
			project: "P", language: "Go", editor: "vim", plugin: "pl",
			machine: "m", platform: "linux", branch: "main", category: "Coding",
			ts: ts, gap: gap,
		}
		switch axis {
		case "project":
			h.project = val
		case "language":
			h.language = val
		case "editor":
			h.editor = val
		}
		return h
	}
	insertSeed(t, d, ctx, sender, mk(startTS, 999999)) // break beat
	for i := 0; i < n; i++ {
		insertSeed(t, d, ctx, sender, mk(startTS.Add(time.Duration(i+1)*time.Minute), each))
	}
	return int64(n) * each, n + 1
}

// TestRenameSingleRelabelsAndAggregates: renaming X->existing Y merges X's rows
// and time into Y across every aggregation, and the audit reflects Y not X.
func TestRenameSingleRelabelsAndAggregates(t *testing.T) {
	for _, axis := range []string{"language", "project"} {
		t.Run(axis, func(t *testing.T) {
			d := openTestDB(t)
			defer d.Close()
			ctx := context.Background()

			sender := mkSender("ren1_" + axis)
			_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
			cleanupSender(t, d, ctx, sender)

			// project axis requires FK rows for every project value used.
			if axis == "project" {
				ensureProjects(t, d, ctx, sender, "X", "Y")
			} else {
				ensureProjects(t, d, ctx, sender, "P")
			}

			day := time.Date(2025, 6, 2, 9, 0, 0, 0, time.UTC)
			// X: 3 attributed beats * 100 = 300; Y: 2 * 100 = 200.
			tX, cX := seedAxisBlock(t, d, ctx, sender, axis, "X", day, 3, 100)
			tY, cY := seedAxisBlock(t, d, ctx, sender, axis, "Y", day.Add(30*time.Minute), 2, 100)
			if err := d.RefreshRollup(ctx, sender, day.AddDate(0, 0, -1)); err != nil {
				t.Fatal(err)
			}

			start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

			before, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{})
			if err != nil {
				t.Fatal(err)
			}
			totalBefore := grandTotal(before)
			if totalBefore != tX+tY {
				t.Fatalf("pre-rename total = %d, want %d", totalBefore, tX+tY)
			}

			// Rename X -> Y (merge).
			moved, err := d.ApplyRename(ctx, sender, axis, "X", "Y")
			if err != nil {
				t.Fatal(err)
			}
			if moved != int64(cX) {
				t.Fatalf("moved = %d, want %d (all X rows)", moved, cX)
			}

			after, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{})
			if err != nil {
				t.Fatal(err)
			}
			// X gone; Y == tX+tY; total conserved.
			secs := axisTotals(after, axis)
			if _, ok := secs["X"]; ok {
				t.Fatalf("[%s] X still present after rename", axis)
			}
			if secs["Y"] != tX+tY {
				t.Fatalf("[%s] Y aggregated = %d, want %d", axis, secs["Y"], tX+tY)
			}
			if grandTotal(after) != totalBefore {
				t.Fatalf("[%s] total changed by rename: %d -> %d", axis, totalBefore, grandTotal(after))
			}

			// Rollup fast path reflects the merge (both axes are rollup axes).
			roll, err := d.GetUserActivityRollup(ctx, sender, start, end, HiddenSets{})
			if err != nil {
				t.Fatal(err)
			}
			if rs := axisTotals(roll, axis); rs["Y"] != tX+tY || rs["X"] != 0 {
				t.Fatalf("[%s rollup] merged wrong: %+v", axis, rs)
			}

			// Project axis: project list shows Y, not X.
			if axis == "project" {
				projects, err := d.GetAllProjects(ctx, sender, start, end, HiddenSets{})
				if err != nil {
					t.Fatal(err)
				}
				var haveX, haveY bool
				for _, p := range projects {
					if p == "X" {
						haveX = true
					}
					if p == "Y" {
						haveY = true
					}
				}
				if haveX || !haveY {
					t.Fatalf("[project list] haveX=%v haveY=%v, want false/true", haveX, haveY)
				}
			}

			// Audit now shows Y with the merged count, X gone.
			col, _ := ExploreColumn(axis)
			groups, _, err := d.GroupHeartbeats(ctx, sender, col, start, end, nil, 500, 15)
			if err != nil {
				t.Fatal(err)
			}
			if groupsContain(groups, "X") {
				t.Fatalf("[audit] X still present after rename")
			}
			var yCount int64
			for _, g := range groups {
				if g.Value != nil && *g.Value == "Y" {
					yCount = g.Count
				}
			}
			if yCount != int64(cX+cY) {
				t.Fatalf("[audit] Y count = %d, want %d (merged rows)", yCount, cX+cY)
			}
		})
	}
}

// TestRenameThreeWayMerge: A,B,C -> M collapses into one group; time & counts
// summed; totals conserved; A/B/C absent from aggregations AND audit.
func TestRenameThreeWayMerge(t *testing.T) {
	for _, axis := range []string{"language", "project"} {
		t.Run(axis, func(t *testing.T) {
			d := openTestDB(t)
			defer d.Close()
			ctx := context.Background()

			sender := mkSender("ren3_" + axis)
			_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
			cleanupSender(t, d, ctx, sender)

			if axis == "project" {
				ensureProjects(t, d, ctx, sender, "A", "B", "C", "M")
			} else {
				ensureProjects(t, d, ctx, sender, "P")
			}

			day := time.Date(2025, 6, 3, 9, 0, 0, 0, time.UTC)
			tA, cA := seedAxisBlock(t, d, ctx, sender, axis, "A", day, 2, 100)                     // 200
			tB, cB := seedAxisBlock(t, d, ctx, sender, axis, "B", day.Add(20*time.Minute), 3, 100) // 300
			tC, cC := seedAxisBlock(t, d, ctx, sender, axis, "C", day.Add(40*time.Minute), 1, 100) // 100
			if err := d.RefreshRollup(ctx, sender, day.AddDate(0, 0, -1)); err != nil {
				t.Fatal(err)
			}
			start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

			before, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{})
			if err != nil {
				t.Fatal(err)
			}
			totalBefore := grandTotal(before)
			if totalBefore != tA+tB+tC {
				t.Fatalf("pre total = %d, want %d", totalBefore, tA+tB+tC)
			}

			for _, v := range []string{"A", "B", "C"} {
				if _, err := d.ApplyRename(ctx, sender, axis, v, "M"); err != nil {
					t.Fatalf("rename %s->M: %v", v, err)
				}
			}

			after, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{})
			if err != nil {
				t.Fatal(err)
			}
			secs := axisTotals(after, axis)
			for _, v := range []string{"A", "B", "C"} {
				if _, ok := secs[v]; ok {
					t.Fatalf("[%s] %s still present after 3-way merge", axis, v)
				}
			}
			if secs["M"] != tA+tB+tC {
				t.Fatalf("[%s] M = %d, want %d", axis, secs["M"], tA+tB+tC)
			}
			// Exactly one M group.
			var mGroups int
			for k := range secs {
				if k == "M" {
					mGroups++
				}
			}
			if mGroups != 1 {
				t.Fatalf("[%s] expected exactly one M group, got %d", axis, mGroups)
			}
			// Conservation: grand total unchanged.
			if grandTotal(after) != totalBefore {
				t.Fatalf("[%s] merge changed total: %d -> %d", axis, totalBefore, grandTotal(after))
			}

			// Audit: A,B,C gone, M has merged count.
			col, _ := ExploreColumn(axis)
			groups, _, err := d.GroupHeartbeats(ctx, sender, col, start, end, nil, 500, 15)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range []string{"A", "B", "C"} {
				if groupsContain(groups, v) {
					t.Fatalf("[audit] %s still present after merge", v)
				}
			}
			var mCount int64
			for _, g := range groups {
				if g.Value != nil && *g.Value == "M" {
					mCount = g.Count
				}
			}
			if mCount != int64(cA+cB+cC) {
				t.Fatalf("[audit] M count = %d, want %d", mCount, cA+cB+cC)
			}
		})
	}
}

// TestRenameProjectFKMerge: after merging A,B,C -> M on the project axis, the
// projects table has only M (A/B/C rows removed), and project_tags/badges are
// re-pointed to M with no duplicate-key violation.
func TestRenameProjectFKMerge(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("renfk")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, "A", "B", "C", "M")

	day := time.Date(2025, 6, 4, 9, 0, 0, 0, time.UTC)
	seedAxisBlock(t, d, ctx, sender, "project", "A", day, 1, 100)
	seedAxisBlock(t, d, ctx, sender, "project", "B", day.Add(20*time.Minute), 1, 100)
	seedAxisBlock(t, d, ctx, sender, "project", "C", day.Add(40*time.Minute), 1, 100)

	// Tag both A and M with the SAME tag so the merge must dedupe on collision.
	if _, err := d.SetTags(ctx, sender, "A", []string{"shared", "onlyA"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetTags(ctx, sender, "M", []string{"shared"}); err != nil {
		t.Fatal(err)
	}
	// Badge on A only.
	if _, err := d.CreateBadgeLink(ctx, sender, "A"); err != nil {
		t.Fatal(err)
	}

	for _, v := range []string{"A", "B", "C"} {
		if _, err := d.ApplyRename(ctx, sender, "project", v, "M"); err != nil {
			t.Fatalf("rename %s->M: %v", v, err)
		}
	}

	// projects table: only M remains for this sender.
	var names []string
	rows, err := d.Pool.Query(ctx, `SELECT name FROM projects WHERE owner=$1 ORDER BY name`, sender)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	if len(names) != 1 || names[0] != "M" {
		t.Fatalf("projects after merge = %v, want [M]", names)
	}

	// project_tags: M carries the union {shared, onlyA} with no duplicate row.
	tags, err := d.GetTags(ctx, sender, "M")
	if err != nil {
		t.Fatal(err)
	}
	tagSet := map[string]int{}
	for _, tg := range tags {
		tagSet[tg]++
	}
	if tagSet["shared"] != 1 {
		t.Fatalf("'shared' tag count on M = %d, want exactly 1 (deduped)", tagSet["shared"])
	}
	if tagSet["onlyA"] != 1 {
		t.Fatalf("'onlyA' tag missing on M after merge: %+v", tagSet)
	}

	// badges: exactly one badge row for M, none for A/B/C.
	var badgeCount int
	if err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM badges WHERE username=$1`, sender).Scan(&badgeCount); err != nil {
		t.Fatal(err)
	}
	if badgeCount != 1 {
		t.Fatalf("badge rows = %d, want 1 (moved to M, no dup)", badgeCount)
	}
	var badgeProject string
	if err := d.Pool.QueryRow(ctx, `SELECT project FROM badges WHERE username=$1`, sender).Scan(&badgeProject); err != nil {
		t.Fatal(err)
	}
	if badgeProject != "M" {
		t.Fatalf("badge project = %q, want M", badgeProject)
	}
}

// TestRenameIngestCanonicalization: with a rename rule A->M active, a NEW
// heartbeat ingested with raw value A is stored/aggregated under M (persistence
// for future data), and gap_seconds/rollup reflect it under M.
func TestRenameIngestCanonicalization(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("renING")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, "P")

	// Seed one existing M language beat so M already exists.
	day := time.Date(2025, 6, 5, 9, 0, 0, 0, time.UTC)
	seedAxisBlock(t, d, ctx, sender, "language", "M", day, 1, 100)

	// Create the rename rule A -> M (also relabels history, of which there is none).
	mVal := "M"
	if _, err := d.CreateCurationRule(ctx, sender, "language", "rename", "A", &mVal); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ApplyRename(ctx, sender, "language", "A", "M"); err != nil {
		t.Fatal(err)
	}

	// Ingest NEW heartbeats whose raw language is A, via the same path as live
	// ingest. SaveHeartbeats canonicalizes A -> M before insert.
	ptr := func(s string) *string { return &s }
	proj := "P"
	beats := []model.HeartbeatPayload{
		{Sender: ptr(sender), Project: &proj, Language: ptr("A"), Entity: "n.go", Type: model.FileType, TimeSent: float64(day.Add(2 * time.Hour).Unix()), UserAgent: "ua"},
		{Sender: ptr(sender), Project: &proj, Language: ptr("A"), Entity: "n.go", Type: model.FileType, TimeSent: float64(day.Add(2*time.Hour + time.Minute).Unix()), UserAgent: "ua"},
	}
	if _, err := d.SaveHeartbeats(ctx, beats); err != nil {
		t.Fatal(err)
	}

	// Raw storage: no row has language 'A'; the ingested beats are stored as 'M'.
	var aCount, mCount int
	if err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND language='A'`, sender).Scan(&aCount); err != nil {
		t.Fatal(err)
	}
	if err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND language='M'`, sender).Scan(&mCount); err != nil {
		t.Fatal(err)
	}
	if aCount != 0 {
		t.Fatalf("ingested 'A' should have been canonicalized to 'M', found %d 'A' rows", aCount)
	}
	// seedAxisBlock('M',n=1) inserts 1 break + 1 attributed = 2 rows; ingest adds 2.
	if mCount != 4 {
		t.Fatalf("M rows = %d, want 4 (2 seeded + 2 ingested-as-M)", mCount)
	}

	// Aggregation attributes the ingested time under M (rollup rebuilt by ingest).
	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)
	rows, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{})
	if err != nil {
		t.Fatal(err)
	}
	secs := axisTotals(rows, "language")
	if _, ok := secs["A"]; ok {
		t.Fatal("aggregation shows 'A' after canonicalization")
	}
	if secs["M"] == 0 {
		t.Fatal("aggregation has no time under M after ingesting A-as-M")
	}
	// The second ingested beat has a ~60s gap to the first; that attributed time
	// lands under M. Assert M's total exceeds the 100 seeded (i.e. ingest added
	// attributed time under M, not A).
	if secs["M"] <= 100 {
		t.Fatalf("M total = %d, want > 100 (ingested attributed time merged into M)", secs["M"])
	}
}
