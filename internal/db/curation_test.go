package db

import (
	"context"
	"testing"
	"time"
)

// mkHiddenSets builds a HiddenSets from an axis->values map (test helper).
func mkHiddenSets(byAxis map[string][]string) HiddenSets {
	m := make(map[string][]string, len(byAxis))
	for k, v := range byAxis {
		if len(v) > 0 {
			m[k] = v
		}
	}
	return HiddenSets{byAxis: m}
}

func TestExclusionPredicateShape(t *testing.T) {
	hs := mkHiddenSets(map[string][]string{
		"project": {"secret"},
		"machine": {"laptop", "desktop"},
	})
	args := []any{"sender", time.Now(), time.Now(), int64(15)}
	sql, outArgs, next := exclusionPredicate(hs, rawHeartbeatCols, 5, args)

	// hiddenAxes order puts project before machine, so $5=project, $6=machine.
	want := " AND NOT (project = ANY($5)) AND NOT (machine = ANY($6))"
	if sql != want {
		t.Fatalf("exclusion SQL = %q, want %q", sql, want)
	}
	if next != 7 {
		t.Fatalf("next arg = %d, want 7", next)
	}
	// Two array args appended (project set, machine set); others empty → skipped.
	if len(outArgs) != 6 {
		t.Fatalf("args len = %d, want 6", len(outArgs))
	}
}

func TestExclusionPredicateEmpty(t *testing.T) {
	sql, args, next := exclusionPredicate(HiddenSets{}, rawHeartbeatCols, 5, []any{"x"})
	if sql != "" || next != 5 || len(args) != 1 {
		t.Fatalf("empty exclusion: sql=%q next=%d args=%d, want ''/5/1", sql, next, len(args))
	}
	if (HiddenSets{}).AnyHidden() {
		t.Fatal("empty HiddenSets.AnyHidden() should be false")
	}
}

func TestInjectAfterAnchorsExist(t *testing.T) {
	// The hide exclusion is spliced after these anchors; if the embedded .sql
	// drifts and drops them, the exclusion silently no-ops — guard against that.
	anchors := []struct {
		name  string
		query string
		anch  string
	}{
		{"activity", qGetUserActivity, activityRangeAnchor},
		{"rollup", qGetUserActivityRoll, rollupRangeAnchor},
		{"activity_by_tag", qGetUserActivityTag, userActivityTagRangeAnchor},
		{"projects_stats", qGetProjectsStats, projectStatsRangeAnchor},
		{"tag_stats", qGetTagStats, tagStatsRangeAnchor},
		{"leaderboards", qGetLeaderboards, leaderboardsRangeAnchor},
		{"time_today", qGetTimeToday, timeTodayRangeAnchor},
		{"category_daily", qGetCategoryDaily, bigBetRangeAnchor},
		{"punchcard", qGetPunchcard, bigBetRangeAnchor},
		{"sessions", qGetSessions, bigBetRangeAnchor},
		{"momentum", qGetMomentum, bigBetRangeAnchor},
	}
	for _, a := range anchors {
		if got := injectAfter(a.query, a.anch, "X"); got == a.query {
			t.Fatalf("%s: anchor %q not found — exclusion would be a silent no-op", a.name, a.anch)
		}
	}
	// Empty addition is a no-op.
	if got := injectAfter(qGetUserActivity, activityRangeAnchor, ""); got != qGetUserActivity {
		t.Fatal("empty addition should leave the query unchanged")
	}
}

// --- DB-backed tests (skip when no dev DB) ---

func seedHB(t *testing.T, d *DB, ctx context.Context, sender, project, lang string, ts time.Time) {
	t.Helper()
	_, err := d.Pool.Exec(ctx, `INSERT INTO heartbeats (sender, project, language, entity, ty, time_sent, user_agent) VALUES ($1,$2,$3,'a.go','file',$4,'ua')`, sender, project, lang, ts)
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplyRenameProjectMerges(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := "curate_" + time.Now().Format("150405.000000")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	_, _ = d.Pool.Exec(ctx, `INSERT INTO projects (owner,name) VALUES ($1,'old'),($1,'new') ON CONFLICT DO NOTHING`, sender)
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, sender)
	})

	base := time.Date(2025, 5, 1, 10, 0, 0, 0, time.UTC)
	seedHB(t, d, ctx, sender, "old", "Go", base)
	seedHB(t, d, ctx, sender, "old", "Go", base.Add(time.Minute))
	seedHB(t, d, ctx, sender, "new", "Go", base.Add(2*time.Minute))

	moved, err := d.ApplyRename(ctx, sender, "project", "old", "new")
	if err != nil {
		t.Fatal(err)
	}
	if moved != 2 {
		t.Fatalf("moved = %d, want 2", moved)
	}

	// All 3 heartbeats now on 'new'; 'old' project row removed (merged).
	var onNew, onOld int
	if err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND project='new'`, sender).Scan(&onNew); err != nil {
		t.Fatal(err)
	}
	if err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND project='old'`, sender).Scan(&onOld); err != nil {
		t.Fatal(err)
	}
	if onNew != 3 || onOld != 0 {
		t.Fatalf("after rename: new=%d old=%d, want 3/0", onNew, onOld)
	}
	var oldProjRows int
	if err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE owner=$1 AND name='old'`, sender).Scan(&oldProjRows); err != nil {
		t.Fatal(err)
	}
	if oldProjRows != 0 {
		t.Fatalf("old project row should be deleted, got %d", oldProjRows)
	}
}

func TestApplyRenameNonProject(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := "curate2_" + time.Now().Format("150405.000000")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	_, _ = d.Pool.Exec(ctx, `INSERT INTO projects (owner,name) VALUES ($1,'p') ON CONFLICT DO NOTHING`, sender)
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, sender)
	})

	base := time.Date(2025, 5, 2, 10, 0, 0, 0, time.UTC)
	seedHB(t, d, ctx, sender, "p", "golang", base)
	seedHB(t, d, ctx, sender, "p", "Go", base.Add(time.Minute))

	moved, err := d.ApplyRename(ctx, sender, "language", "golang", "Go")
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("moved = %d, want 1", moved)
	}
	var goCount int
	if err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND language='Go'`, sender).Scan(&goCount); err != nil {
		t.Fatal(err)
	}
	if goCount != 2 {
		t.Fatalf("Go count = %d, want 2 (merged)", goCount)
	}
}

func TestHideExclusionInStats(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := "curate3_" + time.Now().Format("150405.000000")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	_, _ = d.Pool.Exec(ctx, `INSERT INTO projects (owner,name) VALUES ($1,'keep'),($1,'hideme') ON CONFLICT DO NOTHING`, sender)
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM hb_rollup_daily WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, sender)
	})

	base := time.Date(2025, 5, 3, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		seedHB(t, d, ctx, sender, "keep", "Go", base.Add(time.Duration(i)*time.Minute))
	}
	for i := 0; i < 2; i++ {
		seedHB(t, d, ctx, sender, "hideme", "Go", base.Add(time.Duration(10+i)*time.Minute))
	}
	// Build gaps + rollup so the fast path has data.
	if err := d.RecomputeGaps(ctx, sender, base.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	if err := d.RefreshRollup(ctx, sender, base.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}

	start := base.AddDate(0, 0, -1)
	end := base.AddDate(0, 0, 1)

	// No hide: both projects appear on the raw path.
	rows, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasProject(rows, "hideme") {
		t.Fatal("expected 'hideme' project before hiding")
	}

	// Add a hide rule and reload the hidden sets.
	if _, err := d.CreateCurationRule(ctx, sender, "project", "hide", "hideme", nil); err != nil {
		t.Fatal(err)
	}
	hs, err := d.LoadHiddenSets(ctx, sender)
	if err != nil {
		t.Fatal(err)
	}
	if !hs.AnyHidden() {
		t.Fatal("expected AnyHidden after adding a project hide")
	}

	// Raw path excludes it.
	rows, err = d.GetUserActivity(ctx, sender, start, end, 15, hs)
	if err != nil {
		t.Fatal(err)
	}
	if hasProject(rows, "hideme") {
		t.Fatal("raw activity should exclude the hidden project")
	}
	if !hasProject(rows, "keep") {
		t.Fatal("raw activity should still include the kept project")
	}

	// Rollup fast path excludes it too.
	rrows, err := d.GetUserActivityRollup(ctx, sender, start, end, hs)
	if err != nil {
		t.Fatal(err)
	}
	if hasProject(rrows, "hideme") {
		t.Fatal("rollup should exclude the hidden project")
	}
	if !hasProject(rrows, "keep") {
		t.Fatal("rollup should still include the kept project")
	}

	// Projects list excludes it.
	projects, err := d.GetAllProjects(ctx, sender, start, end, hs)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if p == "hideme" {
			t.Fatal("project list should exclude the hidden project")
		}
	}
}

func hasProject(rows []StatRow, p string) bool {
	for _, r := range rows {
		if r.Project == p {
			return true
		}
	}
	return false
}
