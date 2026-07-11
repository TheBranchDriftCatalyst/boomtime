package db

import (
	"context"
	"testing"
	"time"
)

// mkMembers builds a MemberSets from an axis -> {exact,regex} map (test helper),
// mirroring the shape LoadMemberSets produces.
func mkMembers(exact map[string][]string, regex map[string][]string) MemberSets {
	ms := MemberSets{byAxis: map[string]axisMembers{}}
	for axis, vals := range exact {
		a := ms.byAxis[axis]
		a.exact = append(a.exact, vals...)
		ms.byAxis[axis] = a
	}
	for axis, vals := range regex {
		a := ms.byAxis[axis]
		a.regex = append(a.regex, vals...)
		ms.byAxis[axis] = a
	}
	return ms
}

// TestInclusionPredicateShape asserts the SQL/arg shape of inclusionPredicate:
// one OR-group across axes (deterministic order), exact -> ANY, each regex -> ~.
func TestInclusionPredicateShape(t *testing.T) {
	ms := mkMembers(
		map[string][]string{"project": {"a", "b"}},
		map[string][]string{"editor": {"^vim", "code"}},
	)
	args := []any{"sender", time.Now(), time.Now(), int64(15)}
	sql, outArgs, next := inclusionPredicate(ms, rawHeartbeatCols, "", 5, args)

	// hiddenAxes order: project before editor. project exact -> $5; editor regex
	// -> $6, $7 (in load order).
	want := " AND (project = ANY($5) OR editor ~ $6 OR editor ~ $7)"
	if sql != want {
		t.Fatalf("inclusion SQL = %q, want %q", sql, want)
	}
	if next != 8 {
		t.Fatalf("next arg = %d, want 8", next)
	}
	if len(outArgs) != 7 {
		t.Fatalf("args len = %d, want 7", len(outArgs))
	}
}

// TestSpaceScopePredicateEmpty: an empty (rule-less) space that IS requested must
// match NOTHING (` AND FALSE`); an unrequested scope adds no predicate.
func TestSpaceScopePredicateEmpty(t *testing.T) {
	// Requested but no members -> AND FALSE.
	sql, _, next := spaceScopePredicate(MemberSets{}, rawHeartbeatCols, "", 5, []any{"x"}, true)
	if sql != " AND FALSE" || next != 5 {
		t.Fatalf("empty requested scope: sql=%q next=%d, want ' AND FALSE'/5", sql, next)
	}
	// Not requested -> no predicate.
	sql2, _, _ := spaceScopePredicate(MemberSets{}, rawHeartbeatCols, "", 5, []any{"x"}, false)
	if sql2 != "" {
		t.Fatalf("unrequested scope: sql=%q, want ''", sql2)
	}
	if (MemberSets{}).AnyMember() {
		t.Fatal("empty MemberSets.AnyMember() should be false")
	}
}

// TestHasMemberOutside: a rule on a non-rollup axis (project is a rollup axis;
// entity is not — it's the only rollup-external axis after 00014 widened the
// rollup to include category/plugin/branch) forces the raw path.
func TestHasMemberOutside(t *testing.T) {
	inRollup := mkMembers(map[string][]string{"project": {"p"}}, nil)
	if inRollup.HasMemberOutside(RollupAxes) {
		t.Fatal("project is a rollup axis; HasMemberOutside should be false")
	}
	// entity is intentionally not in the rollup (per-file cardinality would blow
	// it up), so a Space rule on entity must force the raw path.
	outside := mkMembers(map[string][]string{"entity": {"main.go"}}, nil)
	if !outside.HasMemberOutside(RollupAxes) {
		t.Fatal("entity is NOT a rollup axis; HasMemberOutside should be true")
	}
}

// spaceTestSender seeds a sender + user and returns a fixture-less helper context.
func newSpaceSender(t *testing.T, d *DB, prefix string) (context.Context, string) {
	t.Helper()
	ctx := context.Background()
	sender := mkSender(prefix)
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	return ctx, sender
}

// TestSpaceInclusionUnionAcrossAxes: a Space with an exact project rule + a regex
// editor rule includes rows matching EITHER (union), excludes the rest.
func TestSpaceInclusionUnionAcrossAxes(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx, sender := newSpaceSender(t, d, "spc_union")
	ensureProjects(t, d, ctx, sender, "catalyst-web", "catalyst-api", "personal")

	day := time.Date(2025, 9, 1, 9, 0, 0, 0, time.UTC)
	// catalyst-web edited in vim (matches project rule).
	web, _ := seedAxisBlock2(t, d, ctx, sender, hbSeed{project: "catalyst-web", editor: "vim", language: "Go"}, day, 2, 100)
	// personal edited in code (matches editor regex rule ^code, not project).
	code, _ := seedAxisBlock2(t, d, ctx, sender, hbSeed{project: "personal", editor: "code", language: "Go"}, day.Add(20*time.Minute), 3, 100)
	// personal edited in emacs (matches NEITHER rule) -> excluded.
	_, _ = seedAxisBlock2(t, d, ctx, sender, hbSeed{project: "personal", editor: "emacs", language: "Go"}, day.Add(40*time.Minute), 1, 100)

	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)
	ms := mkMembers(
		map[string][]string{"project": {"catalyst-web"}},
		map[string][]string{"editor": {"^code"}},
	)

	rows, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{}, RenameSets{}, ms, true)
	if err != nil {
		t.Fatal(err)
	}
	total := totalStatSeconds(rows)
	if total != web+code {
		t.Fatalf("scoped total = %d, want %d (catalyst-web + code-edited personal)", total, web+code)
	}
	secs := axisTotals(rows, "editor")
	if _, ok := secs["emacs"]; ok {
		t.Fatal("emacs rows should be excluded (match no rule)")
	}
}

// TestSpaceMultiRuleOR: multiple exact project rules OR together (either project in).
func TestSpaceMultiRuleOR(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx, sender := newSpaceSender(t, d, "spc_or")
	ensureProjects(t, d, ctx, sender, "alpha", "beta", "gamma")

	day := time.Date(2025, 9, 2, 9, 0, 0, 0, time.UTC)
	a, _ := seedAxisBlock2(t, d, ctx, sender, hbSeed{project: "alpha", language: "Go"}, day, 2, 100)
	b, _ := seedAxisBlock2(t, d, ctx, sender, hbSeed{project: "beta", language: "Go"}, day.Add(20*time.Minute), 3, 100)
	_, _ = seedAxisBlock2(t, d, ctx, sender, hbSeed{project: "gamma", language: "Go"}, day.Add(40*time.Minute), 1, 100)
	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

	ms := mkMembers(map[string][]string{"project": {"alpha", "beta"}}, nil)
	rows, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{}, RenameSets{}, ms, true)
	if err != nil {
		t.Fatal(err)
	}
	secs := axisTotals(rows, "project")
	if secs["alpha"] != a || secs["beta"] != b {
		t.Fatalf("scoped alpha/beta = %d/%d, want %d/%d", secs["alpha"], secs["beta"], a, b)
	}
	if _, ok := secs["gamma"]; ok {
		t.Fatal("gamma should be excluded (no rule)")
	}
}

// TestSpaceEmptyMatchesNothing: an empty space requested -> zero rows (not the full
// unscoped dashboard).
func TestSpaceEmptyMatchesNothing(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx, sender := newSpaceSender(t, d, "spc_empty")
	ensureProjects(t, d, ctx, sender, "alpha")

	day := time.Date(2025, 9, 3, 9, 0, 0, 0, time.UTC)
	seedAxisBlock2(t, d, ctx, sender, hbSeed{project: "alpha", language: "Go"}, day, 2, 100)
	start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

	rows, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{}, RenameSets{}, MemberSets{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("empty space should match nothing, got %d rows", len(rows))
	}
	// Unrequested -> full dashboard is back.
	unscoped, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if totalStatSeconds(unscoped) != 200 {
		t.Fatalf("unscoped total = %d, want 200", totalStatSeconds(unscoped))
	}
}

// TestSpaceCRUDAndLoadMemberSets exercises the full CRUD + owner isolation and the
// LoadMemberSets round-trip.
func TestSpaceCRUDAndLoadMemberSets(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx, owner := newSpaceSender(t, d, "spc_crud")
	_, other := newSpaceSender(t, d, "spc_other")

	sp, err := d.CreateSpace(ctx, owner, "Work")
	if err != nil {
		t.Fatal(err)
	}
	if sp.ID == 0 || sp.Name != "Work" {
		t.Fatalf("created space = %+v", sp)
	}

	// Add an exact + regex rule.
	if _, err := d.AddSpaceRule(ctx, owner, sp.ID, "project", "catalyst-web", "exact"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddSpaceRule(ctx, owner, sp.ID, "project", "^svc-", "regex"); err != nil {
		t.Fatal(err)
	}
	// Unknown axis + bad matchType are rejected.
	if _, err := d.AddSpaceRule(ctx, owner, sp.ID, "bogus", "x", "exact"); err == nil {
		t.Fatal("unknown axis should be rejected")
	}
	if _, err := d.AddSpaceRule(ctx, owner, sp.ID, "project", "x", "template"); err == nil {
		t.Fatal("template matchType should be rejected")
	}
	// Bad regex rejected.
	if _, err := d.AddSpaceRule(ctx, owner, sp.ID, "project", "(unterminated", "regex"); err == nil {
		t.Fatal("invalid regex should be rejected")
	}

	// LoadMemberSets round-trips both rules.
	ms, err := d.LoadMemberSets(ctx, sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ms.AnyMember() {
		t.Fatal("expected members after adding rules")
	}
	a := ms.byAxis["project"]
	if len(a.exact) != 1 || a.exact[0] != "catalyst-web" || len(a.regex) != 1 || a.regex[0] != "^svc-" {
		t.Fatalf("loaded project members = %+v", a)
	}

	// GetSpace returns the space + rules; ListSpaces reports the rule count.
	gotSp, rules, err := d.GetSpace(ctx, owner, sp.ID)
	if err != nil || gotSp == nil {
		t.Fatalf("GetSpace = %+v err=%v", gotSp, err)
	}
	if len(rules) != 2 {
		t.Fatalf("GetSpace rules = %d, want 2", len(rules))
	}
	list, err := d.ListSpaces(ctx, owner)
	if err != nil || len(list) != 1 || list[0].RuleCount != 2 {
		t.Fatalf("ListSpaces = %+v err=%v", list, err)
	}

	// Owner isolation: `other` cannot see, rule, or delete owner's space.
	if sp2, _, _ := d.GetSpace(ctx, other, sp.ID); sp2 != nil {
		t.Fatal("other owner should not GET this space")
	}
	if rule, err := d.AddSpaceRule(ctx, other, sp.ID, "project", "x", "exact"); err != nil || rule != nil {
		t.Fatalf("other owner AddSpaceRule = rule %+v err %v, want nil/nil", rule, err)
	}
	if n, _ := d.DeleteSpace(ctx, other, sp.ID); n != 0 {
		t.Fatal("other owner should not delete this space")
	}
	if list2, _ := d.ListSpaces(ctx, other); len(list2) != 0 {
		t.Fatalf("other owner ListSpaces = %d, want 0", len(list2))
	}

	// Delete a rule (owner-scoped).
	if n, err := d.DeleteSpaceRule(ctx, owner, sp.ID, rules[0].ID); err != nil || n != 1 {
		t.Fatalf("DeleteSpaceRule = %d err=%v, want 1", n, err)
	}
	// Rename + reorder.
	newName := "Job"
	pos := 5
	if n, err := d.RenameSpace(ctx, owner, sp.ID, &newName, &pos); err != nil || n != 1 {
		t.Fatalf("RenameSpace = %d err=%v", n, err)
	}
	gotSp, _, _ = d.GetSpace(ctx, owner, sp.ID)
	if gotSp.Name != "Job" || gotSp.Position != 5 {
		t.Fatalf("renamed space = %+v", gotSp)
	}
	// Delete cascades its remaining rule.
	if n, err := d.DeleteSpace(ctx, owner, sp.ID); err != nil || n != 1 {
		t.Fatalf("DeleteSpace = %d err=%v", n, err)
	}
	var ruleCount int
	_ = d.Pool.QueryRow(ctx, `SELECT count(*) FROM space_rules WHERE space_id=$1`, sp.ID).Scan(&ruleCount)
	if ruleCount != 0 {
		t.Fatalf("rules should cascade on space delete, got %d", ruleCount)
	}
}

// TestSpacePreviewValues: SpacePreviewValues returns matching RAW values + counts.
func TestSpacePreviewValues(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx, owner := newSpaceSender(t, d, "spc_prev")
	ensureProjects(t, d, ctx, owner, "svc-auth", "svc-billing", "web")

	day := time.Date(2025, 9, 4, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		insertSeed(t, d, ctx, owner, hbSeed{project: "svc-auth", entity: "a.go", ts: day.Add(time.Duration(i) * time.Minute), gap: 60})
	}
	for i := 0; i < 2; i++ {
		insertSeed(t, d, ctx, owner, hbSeed{project: "svc-billing", entity: "b.go", ts: day.Add(time.Duration(10+i) * time.Minute), gap: 60})
	}
	insertSeed(t, d, ctx, owner, hbSeed{project: "web", entity: "c.go", ts: day.Add(30 * time.Minute), gap: 60})

	vals, trunc, err := d.SpacePreviewValues(ctx, owner, "project", "^svc-", "regex", 200)
	if err != nil {
		t.Fatal(err)
	}
	if trunc {
		t.Fatal("did not expect truncation")
	}
	byVal := map[string]int64{}
	for _, v := range vals {
		byVal[v.Value] = v.Count
	}
	if byVal["svc-auth"] != 3 || byVal["svc-billing"] != 2 {
		t.Fatalf("preview = %+v, want svc-auth=3 svc-billing=2", vals)
	}
	if _, ok := byVal["web"]; ok {
		t.Fatal("web must not match ^svc-")
	}
}

// seedAxisBlock2 is like Block: seeds a break beat + n attributed beats sharing the
// given template's fields, returning (attributed, rows). Reuses the fixture builder.
func seedAxisBlock2(t *testing.T, d *DB, ctx context.Context, sender string, tmpl hbSeed, startTS time.Time, n int, each int64) (int64, int) {
	t.Helper()
	if tmpl.project != "" {
		ensureProjects(t, d, ctx, sender, tmpl.project)
	}
	f := &SenderFixture{t: t, db: d, ctx: ctx, name: sender}
	return f.Block(tmpl, startTS, n, each)
}
