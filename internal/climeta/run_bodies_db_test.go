package climeta_test

// run_bodies_db_test.go — DB-backed happy paths for the extracted run bodies
// exactly as the admin CLI-runner invokes them: through the registry's
// Invoke closures, output captured to a bytes.Buffer. Uses the shared
// isolated test DB (skips when Postgres is unreachable, like every other
// DB-backed suite).

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/climeta"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

func TestRunUserListViaRegistryInvoke(t *testing.T) {
	hz := testutil.NewHarnessWithDB(t, testutil.OpenDB(t))
	username, _ := hz.MintUser("climeta_list")

	entry := climeta.Registry()["user list"]
	var buf bytes.Buffer
	if err := entry.Invoke(context.Background(), hz.DB, climeta.RunArgs{}, &buf); err != nil {
		t.Fatalf("user list invoke: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, username) {
		t.Errorf("user list output missing seeded user %q:\n%s", username, out)
	}
	if !strings.Contains(out, "USERNAME") || !strings.Contains(out, "ROLE") {
		t.Errorf("user list output missing table header:\n%s", out)
	}
}

func TestRunUserShowViaRegistryInvoke(t *testing.T) {
	hz := testutil.NewHarnessWithDB(t, testutil.OpenDB(t))
	username, _ := hz.MintUser("climeta_show")

	entry := climeta.Registry()["user show"]
	var buf bytes.Buffer
	args := climeta.RunArgs{Positional: []string{username}}
	if err := entry.Invoke(context.Background(), hz.DB, args, &buf); err != nil {
		t.Fatalf("user show invoke: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"username:", username, "role:", "status:", "capabilities:"} {
		if !strings.Contains(out, want) {
			t.Errorf("user show output missing %q:\n%s", want, out)
		}
	}

	// Unknown user surfaces as a command error, not a panic.
	buf.Reset()
	err := entry.Invoke(context.Background(), hz.DB, climeta.RunArgs{Positional: []string{"nope_does_not_exist"}}, &buf)
	if err == nil || !strings.Contains(err.Error(), "no such user") {
		t.Errorf("want no-such-user error, got %v", err)
	}
}

func TestRunBackfillLastContextDryRunViaRegistryInvoke(t *testing.T) {
	hz := testutil.NewHarnessWithDB(t, testutil.OpenDB(t))

	entry := climeta.Registry()["backfill last-context"]
	var buf bytes.Buffer
	args := climeta.RunArgs{Flags: map[string]any{"dry-run": true}}
	if err := entry.Invoke(context.Background(), hz.DB, args, &buf); err != nil {
		t.Fatalf("backfill last-context --dry-run invoke: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "would resolve (dry-run)") {
		t.Errorf("dry-run output missing preview label:\n%s", out)
	}
	if !strings.Contains(out, "dry-run: no rows written") {
		t.Errorf("dry-run output missing no-write confirmation:\n%s", out)
	}
}
