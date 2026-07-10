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

// axisTotals maps axis-value -> summed attributed seconds.
func axisTotals(rows []StatRow, axis string) (secs map[string]int64) {
	secs = map[string]int64{}
	for _, r := range rows {
		secs[statRowAxis(r, axis)] += r.TotalSeconds
	}
	return secs
}

// seedAxisBlock seeds a break beat (gap huge, unattributed) then n beats each
// carrying `each` attributed seconds, all sharing axis=val. Returns attributed
// total (n*each) and row count (n+1 rows, but only n carry time).
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

// createRename stores a rename rule (action='rename') — the ONLY effect of a
// rename in the non-destructive model. It writes no raw data.
func createRename(t *testing.T, d *DB, ctx context.Context, sender, axis, match, newVal string) int {
	t.Helper()
	rule, err := d.CreateCurationRule(ctx, sender, axis, "rename", match, &newVal)
	if err != nil {
		t.Fatalf("createRename %s %s->%s: %v", axis, match, newVal, err)
	}
	return rule.ID
}

func loadRenames(t *testing.T, d *DB, ctx context.Context, sender string) RenameSets {
	t.Helper()
	rs, err := d.LoadRenameSets(ctx, sender)
	if err != nil {
		t.Fatalf("LoadRenameSets: %v", err)
	}
	return rs
}

// rawCount returns the number of raw heartbeats for sender where col=val.
func rawCount(t *testing.T, d *DB, ctx context.Context, sender, col, val string) int {
	t.Helper()
	var n int
	q := "SELECT count(*) FROM heartbeats WHERE sender=$1 AND " + col + "=$2"
	if err := d.Pool.QueryRow(ctx, q, sender, val).Scan(&n); err != nil {
		t.Fatalf("rawCount %s=%s: %v", col, val, err)
	}
	return n
}

func scalarCount(t *testing.T, d *DB, ctx context.Context, q, sender string) int {
	t.Helper()
	var n int
	if err := d.Pool.QueryRow(ctx, q, sender).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRenameRawPreservation: creating a rename rule mutates NO raw data — the
// heartbeats/projects/badges/project_tags/rollup all keep the original values.
func TestRenameRawPreservation(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("renraw")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, "A", "B")

	day := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
	seedAxisBlock(t, d, ctx, sender, "project", "A", day, 2, 100)
	seedAxisBlock(t, d, ctx, sender, "project", "B", day.Add(30*time.Minute), 1, 100)
	if _, err := d.SetTags(ctx, sender, "A", []string{"t1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateBadgeLink(ctx, sender, "A"); err != nil {
		t.Fatal(err)
	}
	if err := d.RefreshRollup(ctx, sender, day.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}

	rawABefore := rawCount(t, d, ctx, sender, "project", "A")
	createRename(t, d, ctx, sender, "project", "A", "M")

	if got := rawCount(t, d, ctx, sender, "project", "A"); got != rawABefore || got == 0 {
		t.Fatalf("raw 'A' rows changed by rename: %d -> %d", rawABefore, got)
	}
	if got := rawCount(t, d, ctx, sender, "project", "M"); got != 0 {
		t.Fatalf("rename created raw 'M' rows: %d (should be 0)", got)
	}
	if got := scalarCount(t, d, ctx, `SELECT count(*) FROM projects WHERE owner=$1 AND name='A'`, sender); got != 1 {
		t.Fatalf("projects.A = %d, want 1 (untouched)", got)
	}
	if got := scalarCount(t, d, ctx, `SELECT count(*) FROM projects WHERE owner=$1 AND name='M'`, sender); got != 0 {
		t.Fatalf("projects.M = %d, want 0 (rename creates no project row)", got)
	}
	if got := scalarCount(t, d, ctx, `SELECT count(*) FROM project_tags WHERE project_owner=$1 AND project_name='A'`, sender); got != 1 {
		t.Fatalf("project_tags.A = %d, want 1 (untouched)", got)
	}
	if got := scalarCount(t, d, ctx, `SELECT count(*) FROM badges WHERE username=$1 AND project='A'`, sender); got != 1 {
		t.Fatalf("badges.A = %d, want 1 (untouched)", got)
	}
	if got := scalarCount(t, d, ctx, `SELECT count(*) FROM hb_rollup_daily WHERE sender=$1 AND project='A'`, sender); got == 0 {
		t.Fatal("rollup should still be keyed by raw 'A'")
	}
}

// TestRenameMergeAggregates: A,B,C → M merges in the aggregations (time summed,
// A/B/C absent), grand total conserved, pct recomputed. Table-driven over axes.
func TestRenameMergeAggregates(t *testing.T) {
	for _, axis := range []string{"project", "language", "editor"} {
		t.Run(axis, func(t *testing.T) {
			d := openTestDB(t)
			defer d.Close()
			ctx := context.Background()

			sender := mkSender("renmrg_" + axis)
			_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
			cleanupSender(t, d, ctx, sender)
			if axis == "project" {
				ensureProjects(t, d, ctx, sender, "A", "B", "C")
			} else {
				ensureProjects(t, d, ctx, sender, "P")
			}

			day := time.Date(2025, 6, 2, 9, 0, 0, 0, time.UTC)
			tA, _ := seedAxisBlock(t, d, ctx, sender, axis, "A", day, 2, 100)                     // 200
			tB, _ := seedAxisBlock(t, d, ctx, sender, axis, "B", day.Add(20*time.Minute), 3, 100) // 300
			tC, _ := seedAxisBlock(t, d, ctx, sender, axis, "C", day.Add(40*time.Minute), 1, 100) // 100
			total := tA + tB + tC

			start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

			before, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{}, RenameSets{})
			if err != nil {
				t.Fatal(err)
			}
			if grandTotal(before) != total {
				t.Fatalf("pre total = %d, want %d", grandTotal(before), total)
			}

			for _, v := range []string{"A", "B", "C"} {
				createRename(t, d, ctx, sender, axis, v, "M")
			}
			rs := loadRenames(t, d, ctx, sender)

			after, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{}, rs)
			if err != nil {
				t.Fatal(err)
			}
			secs := axisTotals(after, axis)
			for _, v := range []string{"A", "B", "C"} {
				if _, ok := secs[v]; ok {
					t.Fatalf("[%s] %s still shown after merge", axis, v)
				}
			}
			if secs["M"] != total {
				t.Fatalf("[%s] M = %d, want %d", axis, secs["M"], total)
			}
			if grandTotal(after) != total {
				t.Fatalf("[%s] merge changed total: %d -> %d", axis, total, grandTotal(after))
			}

			// pct is recomputed at the outer layer; it should sum to ~1.
			var pctSum float64
			for _, r := range after {
				pctSum += r.Pct
			}
			if pctSum < 0.999 || pctSum > 1.001 {
				t.Fatalf("[%s] recomputed pct sums to %f, want ~1.0", axis, pctSum)
			}
		})
	}
}

// TestRenameRollupMerge: the rollup fast path also merges A,B -> M.
func TestRenameRollupMerge(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("renroll")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, "A", "B")

	day := time.Date(2025, 6, 3, 9, 0, 0, 0, time.UTC)
	tA, _ := seedAxisBlock(t, d, ctx, sender, "project", "A", day, 2, 100)
	tB, _ := seedAxisBlock(t, d, ctx, sender, "project", "B", day.Add(30*time.Minute), 1, 100)
	if err := d.RefreshRollup(ctx, sender, day.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

	createRename(t, d, ctx, sender, "project", "A", "M")
	createRename(t, d, ctx, sender, "project", "B", "M")
	rs := loadRenames(t, d, ctx, sender)

	roll, err := d.GetUserActivityRollup(ctx, sender, start, end, HiddenSets{}, rs)
	if err != nil {
		t.Fatal(err)
	}
	rt := axisTotals(roll, "project")
	if rt["A"] != 0 || rt["B"] != 0 {
		t.Fatalf("rollup still shows A/B after merge: %+v", rt)
	}
	if rt["M"] != tA+tB {
		t.Fatalf("rollup M = %d, want %d", rt["M"], tA+tB)
	}
}

// TestRenameReversibility: deleting the rule instantly reverts dashboards to raw.
func TestRenameReversibility(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("renrev")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, "A", "B")

	day := time.Date(2025, 6, 4, 9, 0, 0, 0, time.UTC)
	tA, _ := seedAxisBlock(t, d, ctx, sender, "project", "A", day, 2, 100)
	tB, _ := seedAxisBlock(t, d, ctx, sender, "project", "B", day.Add(30*time.Minute), 1, 100)
	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

	ruleID := createRename(t, d, ctx, sender, "project", "A", "B")
	rs := loadRenames(t, d, ctx, sender)
	merged, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{}, rs)
	if err != nil {
		t.Fatal(err)
	}
	if ms := axisTotals(merged, "project"); ms["B"] != tA+tB || ms["A"] != 0 {
		t.Fatalf("merged view wrong: %+v", ms)
	}

	if _, err := d.DeleteCurationRule(ctx, sender, ruleID); err != nil {
		t.Fatal(err)
	}
	rs = loadRenames(t, d, ctx, sender)
	if rs.Any() {
		t.Fatal("rename set should be empty after delete")
	}
	reverted, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{}, rs)
	if err != nil {
		t.Fatal(err)
	}
	rv := axisTotals(reverted, "project")
	if rv["A"] != tA || rv["B"] != tB {
		t.Fatalf("after delete, expected raw A=%d B=%d, got %+v", tA, tB, rv)
	}
}

// TestRenameIngestStoresRaw: with A->M active, a NEW ingested heartbeat is stored
// RAW as A (not canonicalized) but aggregates under M at read time.
func TestRenameIngestStoresRaw(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("rening")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, "P")

	day := time.Date(2025, 6, 5, 9, 0, 0, 0, time.UTC)
	seedAxisBlock(t, d, ctx, sender, "language", "M", day, 1, 100)
	createRename(t, d, ctx, sender, "language", "A", "M")

	ptr := func(s string) *string { return &s }
	proj := "P"
	beats := []model.HeartbeatPayload{
		{Sender: ptr(sender), Project: &proj, Language: ptr("A"), Entity: "n.go", Type: model.FileType, TimeSent: float64(day.Add(2 * time.Hour).Unix()), UserAgent: "ua"},
		{Sender: ptr(sender), Project: &proj, Language: ptr("A"), Entity: "n.go", Type: model.FileType, TimeSent: float64(day.Add(2*time.Hour + time.Minute).Unix()), UserAgent: "ua"},
	}
	if _, err := d.SaveHeartbeats(ctx, beats); err != nil {
		t.Fatal(err)
	}

	if got := rawCount(t, d, ctx, sender, "language", "A"); got != 2 {
		t.Fatalf("ingested beats should be stored RAW as 'A', found %d 'A' rows", got)
	}

	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)
	rs := loadRenames(t, d, ctx, sender)
	rows, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{}, rs)
	if err != nil {
		t.Fatal(err)
	}
	secs := axisTotals(rows, "language")
	if _, ok := secs["A"]; ok {
		t.Fatal("aggregation shows raw 'A' despite active rename")
	}
	if secs["M"] <= 100 {
		t.Fatalf("M total = %d, want > 100 (ingested time merged into M)", secs["M"])
	}
}

// TestRenameProjectDetailByDisplayName: GetProjectStats keyed by the DISPLAY name
// aggregates all source projects (A,B -> M); identity still works.
func TestRenameProjectDetailByDisplayName(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("rendetail")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, "A", "B", "keep")

	day := time.Date(2025, 6, 6, 9, 0, 0, 0, time.UTC)
	seedAxisBlock(t, d, ctx, sender, "project", "A", day, 2, 100)                     // 200
	seedAxisBlock(t, d, ctx, sender, "project", "B", day.Add(20*time.Minute), 1, 100) // 100
	seedAxisBlock(t, d, ctx, sender, "project", "keep", day.Add(40*time.Minute), 1, 100)
	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

	createRename(t, d, ctx, sender, "project", "A", "M")
	createRename(t, d, ctx, sender, "project", "B", "M")
	rs := loadRenames(t, d, ctx, sender)

	sumRows := func(rows []ProjectStatRow) int64 {
		var s int64
		for _, r := range rows {
			s += r.TotalSeconds
		}
		return s
	}

	rowsM, err := d.GetProjectStats(ctx, sender, "M", start, end, 15, HiddenSets{}, rs)
	if err != nil {
		t.Fatal(err)
	}
	if got := sumRows(rowsM); got != 300 {
		t.Fatalf("project detail 'M' total = %d, want 300 (A+B merged)", got)
	}

	rowsKeep, err := d.GetProjectStats(ctx, sender, "keep", start, end, 15, HiddenSets{}, rs)
	if err != nil {
		t.Fatal(err)
	}
	if got := sumRows(rowsKeep); got != 100 {
		t.Fatalf("project detail 'keep' total = %d, want 100", got)
	}

	rowsA, err := d.GetProjectStats(ctx, sender, "A", start, end, 15, HiddenSets{}, rs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsA) != 0 {
		t.Fatalf("querying raw source 'A' should be empty under rename (keyed by display name), got %d rows", len(rowsA))
	}
}

// TestRenameProjectListMerge: the projects list shows the merged name once.
func TestRenameProjectListMerge(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("renlist")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, "A", "B")

	day := time.Date(2025, 6, 7, 9, 0, 0, 0, time.UTC)
	seedAxisBlock(t, d, ctx, sender, "project", "A", day, 2, 100)
	seedAxisBlock(t, d, ctx, sender, "project", "B", day.Add(20*time.Minute), 1, 100)
	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

	createRename(t, d, ctx, sender, "project", "A", "M")
	createRename(t, d, ctx, sender, "project", "B", "M")
	rs := loadRenames(t, d, ctx, sender)

	projects, err := d.GetAllProjects(ctx, sender, start, end, HiddenSets{}, rs)
	if err != nil {
		t.Fatal(err)
	}
	var mCount, abCount int
	for _, p := range projects {
		switch p {
		case "M":
			mCount++
		case "A", "B":
			abCount++
		}
	}
	if mCount != 1 || abCount != 0 {
		t.Fatalf("project list = %v, want a single 'M' and no A/B", projects)
	}
}

// TestRenameAuditUnaffected: audit surfaces show RAW values even with a rule.
func TestRenameAuditUnaffected(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("renaudit")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, "A", "B")

	day := time.Date(2025, 6, 8, 9, 0, 0, 0, time.UTC)
	seedAxisBlock(t, d, ctx, sender, "project", "A", day, 2, 100)
	seedAxisBlock(t, d, ctx, sender, "project", "B", day.Add(20*time.Minute), 1, 100)
	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

	createRename(t, d, ctx, sender, "project", "A", "M")
	createRename(t, d, ctx, sender, "project", "B", "M")

	col, _ := ExploreColumn("project")
	groups, _, err := d.GroupHeartbeats(ctx, sender, col, start, end, nil, 500, 15)
	if err != nil {
		t.Fatal(err)
	}
	hasVal := func(v string) bool {
		for _, g := range groups {
			if g.Value != nil && *g.Value == v {
				return true
			}
		}
		return false
	}
	if !hasVal("A") || !hasVal("B") {
		t.Fatalf("audit group must show raw A and B, got %+v", groups)
	}
	if hasVal("M") {
		t.Fatal("audit group must NOT show the remapped 'M'")
	}

	items, _, err := d.ListHeartbeats(ctx, sender, start, end, nil, "", 1, 500)
	if err != nil {
		t.Fatal(err)
	}
	var sawA bool
	for _, r := range items {
		if r.Project != nil && *r.Project == "A" {
			sawA = true
		}
		if r.Project != nil && *r.Project == "M" {
			t.Fatal("audit list must not show remapped 'M'")
		}
	}
	if !sawA {
		t.Fatal("audit list should still contain raw 'A'")
	}
}

// TestRenameHidePrecedence: hide 'A' + rename A,B -> M. Hide filters raw values
// first, so A never reaches M; B still merges into M.
func TestRenameHidePrecedence(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("renhide")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, "A", "B")

	day := time.Date(2025, 6, 9, 9, 0, 0, 0, time.UTC)
	seedAxisBlock(t, d, ctx, sender, "project", "A", day, 2, 100)                              // 200 (hidden)
	tB, _ := seedAxisBlock(t, d, ctx, sender, "project", "B", day.Add(20*time.Minute), 3, 100) // 300
	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

	createRename(t, d, ctx, sender, "project", "A", "M")
	createRename(t, d, ctx, sender, "project", "B", "M")
	if _, err := d.CreateCurationRule(ctx, sender, "project", "hide", "A", nil); err != nil {
		t.Fatal(err)
	}
	hs, err := d.LoadHiddenSets(ctx, sender)
	if err != nil {
		t.Fatal(err)
	}
	rs := loadRenames(t, d, ctx, sender)

	rows, err := d.GetUserActivity(ctx, sender, start, end, 15, hs, rs)
	if err != nil {
		t.Fatal(err)
	}
	secs := axisTotals(rows, "project")
	if secs["M"] != tB {
		t.Fatalf("M = %d, want %d (A hidden, only B merges)", secs["M"], tB)
	}
	if _, ok := secs["A"]; ok {
		t.Fatal("A should be hidden, not shown")
	}
}

// TestRenameLeaderboardRequesterOnly: a requester's rename applies to THEIR rows
// only; other users' project labels are untouched.
func TestRenameLeaderboardRequesterOnly(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	me := mkSender("renlbme")
	other := mkSender("renlboth")
	for _, s := range []string{me, other} {
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, s)
		ensureProjects(t, d, ctx, s, "A")
	}
	t.Cleanup(func() {
		for _, s := range []string{me, other} {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, s)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE sender=$1`, s)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM hb_rollup_daily WHERE sender=$1`, s)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, s)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, s)
		}
	})

	day := time.Date(2025, 6, 10, 9, 0, 0, 0, time.UTC)
	seedAxisBlock(t, d, ctx, me, "project", "A", day, 2, 100)
	seedAxisBlock(t, d, ctx, other, "project", "A", day, 2, 100)
	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

	createRename(t, d, ctx, me, "project", "A", "M")
	rs := loadRenames(t, d, ctx, me)

	lb, err := d.GetLeaderboards(ctx, start, end, me, HiddenSets{}, rs)
	if err != nil {
		t.Fatal(err)
	}
	var meProj, otherProj string
	for _, r := range lb {
		switch r.Sender {
		case me:
			meProj = r.Project
		case other:
			otherProj = r.Project
		}
	}
	if meProj != "M" {
		t.Fatalf("requester's project = %q, want M", meProj)
	}
	if otherProj != "A" {
		t.Fatalf("other user's project = %q, want A (untouched by requester's rename)", otherProj)
	}
}

// TestRenameMomentumAndCategory: momentum (project) and category-daily merge; the
// punchcard/sessions signatures take NO rename param (compile-time skip proof).
func TestRenameMomentumAndCategory(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("renmomcat")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, "A", "B")

	day := time.Date(2025, 6, 11, 9, 0, 0, 0, time.UTC)
	insertSeed(t, d, ctx, sender, hbSeed{project: "A", language: "Go", category: "X", entity: "a.go", ts: day, gap: 999999})
	insertSeed(t, d, ctx, sender, hbSeed{project: "A", language: "Go", category: "X", entity: "a.go", ts: day.Add(time.Minute), gap: 120})
	insertSeed(t, d, ctx, sender, hbSeed{project: "B", language: "Go", category: "Y", entity: "b.go", ts: day.Add(2 * time.Minute), gap: 999999})
	insertSeed(t, d, ctx, sender, hbSeed{project: "B", language: "Go", category: "Y", entity: "b.go", ts: day.Add(3 * time.Minute), gap: 120})
	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

	createRename(t, d, ctx, sender, "project", "A", "M")
	createRename(t, d, ctx, sender, "project", "B", "M")
	createRename(t, d, ctx, sender, "category", "X", "Z")
	createRename(t, d, ctx, sender, "category", "Y", "Z")
	rs := loadRenames(t, d, ctx, sender)

	mom, err := d.GetMomentum(ctx, sender, start, end, 15, HiddenSets{}, rs)
	if err != nil {
		t.Fatal(err)
	}
	var momM int64
	for _, m := range mom {
		if m.Project == "A" || m.Project == "B" {
			t.Fatalf("momentum still shows raw project %q", m.Project)
		}
		if m.Project == "M" {
			momM += m.Seconds
		}
	}
	if momM != 240 {
		t.Fatalf("momentum M seconds = %d, want 240 (A+B merged)", momM)
	}

	cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15, HiddenSets{}, rs)
	if err != nil {
		t.Fatal(err)
	}
	var catZ int64
	for _, c := range cats {
		if c.Category == "X" || c.Category == "Y" {
			t.Fatalf("category still shows raw %q", c.Category)
		}
		if c.Category == "Z" {
			catZ += c.TotalSeconds
		}
	}
	if catZ != 240 {
		t.Fatalf("category Z seconds = %d, want 240 (X+Y merged)", catZ)
	}
}
