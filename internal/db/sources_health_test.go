package db

import (
	"context"
	"testing"
	"time"
)

// TestSourceHealthKinds pins the code-defined kind→column mapping (the injection
// guard for the UNION query). It must never grow to accept arbitrary columns.
func TestSourceHealthKinds(t *testing.T) {
	want := map[string]string{"editor": "editor", "plugin": "plugin", "machine": "machine"}
	if len(sourceHealthKinds) != len(want) {
		t.Fatalf("sourceHealthKinds len = %d, want %d", len(sourceHealthKinds), len(want))
	}
	for _, k := range sourceHealthKinds {
		if want[k.kind] != k.col {
			t.Fatalf("kind %q maps to %q, want %q", k.kind, k.col, want[k.kind])
		}
	}
}

// TestSourceHealthShape verifies the per-source MAX(time_sent)+count rollup,
// NULL/empty exclusion, and stalest-first ordering against the isolated test DB.
func TestSourceHealthShape(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := "srchealth_user_" + time.Now().Format("150405.000000")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	_, _ = d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,'proj') ON CONFLICT DO NOTHING`, sender)
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, sender)
	})

	// The heartbeats unique constraint is (entity, sender, time_sent); give each
	// row a distinct entity so no two collide on the same timestamp.
	insert := func(entity string, ts time.Time, editor, machine *string) {
		_, err := d.Pool.Exec(ctx,
			`INSERT INTO heartbeats (sender, project, entity, ty, time_sent, user_agent, editor, machine)
			 VALUES ($1,'proj',$2,'file',$3,'ua',$4,$5)`,
			sender, entity, ts, editor, machine)
		if err != nil {
			t.Fatal(err)
		}
	}

	recent := time.Now().UTC().Add(-2 * time.Hour) // vim: most recent
	old := time.Now().UTC().Add(-40 * 24 * time.Hour)
	vim := "vim"
	vscode := "vscode"
	box := "laptop"
	insert("a.go", recent, &vim, &box)
	insert("b.go", recent.Add(-time.Hour), &vim, &box) // vim second beat (count=2)
	insert("c.go", old, &vscode, &box)                 // vscode: stale (oldest editor)
	insert("d.go", recent, nil, &box)                  // NULL editor -> excluded as editor source

	got, err := d.SourceHealth(ctx, sender)
	if err != nil {
		t.Fatal(err)
	}

	// Expect editor sources vim+vscode, machine source laptop. No NULL/empty rows.
	byKindSource := map[string]SourceHealth{}
	for _, s := range got {
		if s.Source == "" {
			t.Fatalf("empty source leaked into results: %+v", s)
		}
		byKindSource[s.Kind+"/"+s.Source] = s
	}

	vimHealth, ok := byKindSource["editor/vim"]
	if !ok || vimHealth.Count != 2 {
		t.Fatalf("editor/vim = %+v (ok=%v), want count 2", vimHealth, ok)
	}
	if !vimHealth.LastSeen.Equal(recent) {
		t.Fatalf("editor/vim lastSeen = %v, want %v", vimHealth.LastSeen, recent)
	}
	if _, ok := byKindSource["editor/vscode"]; !ok {
		t.Fatalf("missing editor/vscode source; got %+v", got)
	}
	laptop, ok := byKindSource["machine/laptop"]
	if !ok || laptop.Count != 4 {
		t.Fatalf("machine/laptop = %+v (ok=%v), want count 4", laptop, ok)
	}

	// Stalest-first: results are ordered by lastSeen ASC.
	for i := 1; i < len(got); i++ {
		if got[i].LastSeen.Before(got[i-1].LastSeen) {
			t.Fatalf("results not ordered stalest-first: [%d]=%v before [%d]=%v",
				i, got[i].LastSeen, i-1, got[i-1].LastSeen)
		}
	}
}
