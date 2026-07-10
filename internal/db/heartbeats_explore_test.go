package db

import (
	"context"
	"testing"
	"time"
)

func TestExploreColumnWhitelist(t *testing.T) {
	// Whitelisted axes map to trusted columns.
	want := map[string]string{
		"day":       "time_sent::date",
		"project":   "project",
		"language":  "language",
		"editor":    "editor",
		"plugin":    "plugin",
		"platform":  "platform",
		"machine":   "machine",
		"branch":    "branch",
		"category":  "category",
		"type":      "ty",
		"entity":    "entity",
		"isWrite":   "is_write",
		"userAgent": "user_agent",
	}
	for name, col := range want {
		got, ok := ExploreColumn(name)
		if !ok || got != col {
			t.Fatalf("ExploreColumn(%q) = %q,%v want %q,true", name, got, ok, col)
		}
	}
	// Non-whitelisted axes are rejected — including a raw column name and an
	// obvious injection attempt.
	for _, bad := range []string{"sender", "id", "ty", "time_sent", "is_write", "user_agent", "1; DROP TABLE heartbeats", ""} {
		if _, ok := ExploreColumn(bad); ok {
			t.Fatalf("ExploreColumn(%q) should be rejected", bad)
		}
	}
}

func TestBuildFilterClause(t *testing.T) {
	col, _ := ExploreColumn("language")
	nullCol, _ := ExploreColumn("project")
	v := "Go"
	filters := []ExploreFilter{
		{Column: col, Value: &v},
		{Column: nullCol, Value: nil}, // IS NULL branch
	}
	sql, args, next := buildFilterClause(filters, 4, []any{"sender", time.Now(), time.Now()})
	if sql != " AND language::text = $4 AND project IS NULL" {
		t.Fatalf("filter SQL = %q", sql)
	}
	if next != 5 {
		t.Fatalf("nextArg = %d, want 5", next)
	}
	if len(args) != 4 || args[3] != "Go" {
		t.Fatalf("args = %v, want [...,\"Go\"]", args)
	}
}

// --- DB-backed shape tests (skip when no dev DB) ---

func TestGroupHeartbeatsDayShape(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := "explore_user_" + time.Now().Format("150405.000000")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	_, _ = d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,'proj') ON CONFLICT DO NOTHING`, sender)
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, sender)
	})

	base := time.Date(2025, 4, 1, 10, 0, 0, 0, time.UTC)
	// 2 heartbeats on day 1 (Go), 1 on day 2 (Go), 1 with NULL language.
	insert := func(ts time.Time, lang *string) {
		_, err := d.Pool.Exec(ctx, `INSERT INTO heartbeats (sender, project, language, entity, ty, time_sent, user_agent) VALUES ($1,'proj',$2,'a.go','file',$3,'ua')`, sender, lang, ts)
		if err != nil {
			t.Fatal(err)
		}
	}
	goLang := "Go"
	insert(base, &goLang)
	insert(base.Add(time.Minute), &goLang)
	insert(base.AddDate(0, 0, 1), &goLang)
	insert(base.Add(2*time.Minute), nil)

	start := base.AddDate(0, 0, -1)
	end := base.AddDate(0, 0, 2)

	// group by day: expect two YYYY-MM-DD buckets.
	dayCol, _ := ExploreColumn("day")
	groups, trunc, err := d.GroupHeartbeats(ctx, sender, dayCol, start, end, nil, 500)
	if err != nil {
		t.Fatal(err)
	}
	if trunc {
		t.Fatal("did not expect truncation")
	}
	if len(groups) != 2 {
		t.Fatalf("day groups = %d, want 2 (%+v)", len(groups), groups)
	}
	// Top bucket (count desc) is day 1 with 3 rows; value is a YYYY-MM-DD string.
	if groups[0].Value == nil || *groups[0].Value != "2025-04-01" || groups[0].Count != 3 {
		t.Fatalf("top day group = %+v, want value=2025-04-01 count=3", groups[0])
	}

	// group by language: Go bucket + a NULL bucket.
	langCol, _ := ExploreColumn("language")
	lgroups, _, err := d.GroupHeartbeats(ctx, sender, langCol, start, end, nil, 500)
	if err != nil {
		t.Fatal(err)
	}
	var haveNull, haveGo bool
	for _, g := range lgroups {
		if g.Value == nil {
			haveNull = true
		} else if *g.Value == "Go" {
			haveGo = true
		}
	}
	if !haveGo || !haveNull {
		t.Fatalf("language groups = %+v, want a Go bucket and a null bucket", lgroups)
	}

	// Filter by language=Go, group by day: NULL-language row excluded.
	filters := []ExploreFilter{{Column: langCol, Value: &goLang}}
	fg, _, err := d.GroupHeartbeats(ctx, sender, dayCol, start, end, filters, 500)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, g := range fg {
		total += g.Count
	}
	if total != 3 {
		t.Fatalf("filtered day total = %d, want 3", total)
	}

	// Rows endpoint: entity substring + language filter.
	items, cnt, err := d.ListHeartbeats(ctx, sender, start, end, filters, "a.g", 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 3 || len(items) != 3 {
		t.Fatalf("rows total=%d len=%d, want 3/3", cnt, len(items))
	}
	if items[0].Type != "file" || items[0].Entity != "a.go" {
		t.Fatalf("row[0] = %+v", items[0])
	}
}
