package db

import (
	"context"
	"testing"
	"time"
)

// TestSourceHealthShape verifies the per-(plugin, machine) MAX(time_sent)+count
// rollup, exclusion of plugin-less heartbeats, the 'unknown' machine fallback,
// and stalest-first ordering against the isolated test DB.
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
	insert := func(entity string, ts time.Time, plugin, machine *string) {
		_, err := d.Pool.Exec(ctx,
			`INSERT INTO heartbeats (sender, project, entity, ty, time_sent, user_agent, plugin, machine)
			 VALUES ($1,'proj',$2,'file',$3,'ua',$4,$5)`,
			sender, entity, ts, plugin, machine)
		if err != nil {
			t.Fatal(err)
		}
	}

	recent := time.Now().UTC().Add(-2 * time.Hour)
	old := time.Now().UTC().Add(-40 * 24 * time.Hour)
	vscodePlugin := "vscode-wakatime"
	vimPlugin := "vim-wakatime"
	laptop := "laptop"
	desktop := "desktop"

	// Same plugin on two machines => two distinct sources (compound key).
	insert("a.go", recent, &vscodePlugin, &laptop)
	insert("b.go", recent.Add(-time.Hour), &vscodePlugin, &laptop) // second beat (count=2)
	insert("c.go", old, &vscodePlugin, &desktop)                   // vscode on desktop: stale (oldest)
	insert("d.go", recent, &vimPlugin, &laptop)                    // different plugin, same machine
	insert("e.go", recent, nil, &laptop)                           // NULL plugin -> excluded
	insert("f.go", recent, &vimPlugin, nil)                        // NULL machine -> 'unknown'

	got, err := d.SourceHealth(ctx, sender)
	if err != nil {
		t.Fatal(err)
	}

	byKey := map[string]SourceHealth{}
	for _, s := range got {
		if s.Plugin == "" {
			t.Fatalf("plugin-less heartbeat leaked into results: %+v", s)
		}
		byKey[s.Plugin+"@"+s.Machine] = s
	}

	// vscode-wakatime @ laptop: two beats, most recent.
	v, ok := byKey["vscode-wakatime@laptop"]
	if !ok || v.Count != 2 {
		t.Fatalf("vscode-wakatime@laptop = %+v (ok=%v), want count 2", v, ok)
	}
	if !v.LastSeen.Equal(recent) {
		t.Fatalf("vscode-wakatime@laptop lastSeen = %v, want %v", v.LastSeen, recent)
	}
	// Same plugin on a different machine is a separate source.
	if _, ok := byKey["vscode-wakatime@desktop"]; !ok {
		t.Fatalf("missing vscode-wakatime@desktop source; got %+v", got)
	}
	// Different plugin on the same machine is a separate source.
	if _, ok := byKey["vim-wakatime@laptop"]; !ok {
		t.Fatalf("missing vim-wakatime@laptop source; got %+v", got)
	}
	// Missing machine collapses to 'unknown'.
	if _, ok := byKey["vim-wakatime@unknown"]; !ok {
		t.Fatalf("missing vim-wakatime@unknown source; got %+v", got)
	}

	// Stalest-first: results are ordered by lastSeen ASC.
	for i := 1; i < len(got); i++ {
		if got[i].LastSeen.Before(got[i-1].LastSeen) {
			t.Fatalf("results not ordered stalest-first: [%d]=%v before [%d]=%v",
				i, got[i].LastSeen, i-1, got[i-1].LastSeen)
		}
	}
}
