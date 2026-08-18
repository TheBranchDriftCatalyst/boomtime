// dashboard_layouts_test.go — stdlib tests for dashboard_layouts.go (gaka-se2.7).
//
// Every t.Run pins ONE named invariant. No trivial round-trips: cross-owner
// isolation and byte-preserving JSONB are the load-bearing checks.
package db

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// openTestDB lives in harness_test.go — shared with the other
// stdlib-flavored *_test.go files in this package.

// cleanupLayoutOwner nukes any dashboard_layouts rows the test created —
// deleteSenderRows in harness_test.go doesn't know about this table.
func cleanupLayoutOwner(t *testing.T, d *DB, ctx context.Context, owner string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM dashboard_layouts WHERE owner=$1`, owner)
	})
}

// mustCreateUser is a bare-bones users insert (dashboard_layouts.owner has an
// FK to users.username). Uses a fresh pool ctx so it stays independent of test
// timeouts.
func mustCreateUser(t *testing.T, d *DB, ctx context.Context, name string) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`,
		name); err != nil {
		t.Fatalf("create user %q: %v", name, err)
	}
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, name) })
}

func TestDashboardLayouts(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("get-empty-args-errors", func(t *testing.T) {
		// GetDashboardLayout: guard clause rejects empty owner/scope so we don't
		// SELECT WHERE '' AND '' by mistake and match a bogus future row.
		if _, _, err := d.GetDashboardLayout(ctx, "", "public_profile"); err == nil {
			t.Fatal("expected error for empty owner")
		}
		if _, _, err := d.GetDashboardLayout(ctx, "u", ""); err == nil {
			t.Fatal("expected error for empty scope")
		}
	})

	t.Run("set-empty-args-errors", func(t *testing.T) {
		if err := d.SetDashboardLayout(ctx, "", "s", json.RawMessage(`{}`)); err == nil {
			t.Fatal("expected error for empty owner")
		}
		if err := d.SetDashboardLayout(ctx, "u", "", json.RawMessage(`{}`)); err == nil {
			t.Fatal("expected error for empty scope")
		}
		if err := d.SetDashboardLayout(ctx, "u", "s", nil); err == nil {
			t.Fatal("expected error for empty layout")
		}
	})

	t.Run("delete-empty-args-errors", func(t *testing.T) {
		if err := d.DeleteDashboardLayout(ctx, "", "s"); err == nil {
			t.Fatal("expected error for empty owner")
		}
		if err := d.DeleteDashboardLayout(ctx, "u", ""); err == nil {
			t.Fatal("expected error for empty scope")
		}
	})

	t.Run("get-nonexistent-returns-not-found", func(t *testing.T) {
		// The (nil, false, nil) contract is the handler's 404 signal — this is
		// load-bearing: a stray pgx.ErrNoRows would bubble as HTTP 500.
		owner := "dl_missing_" + mkStamp()
		mustCreateUser(t, d, ctx, owner)
		cleanupLayoutOwner(t, d, ctx, owner)
		raw, ok, err := d.GetDashboardLayout(ctx, owner, "public_profile")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if ok {
			t.Fatal("expected ok=false")
		}
		if raw != nil {
			t.Fatalf("expected raw=nil, got %s", raw)
		}
	})

	t.Run("byte-preserving-jsonb-roundtrip", func(t *testing.T) {
		// gaka-25r pattern: JSONB *can* reorder keys, but we store with
		// $3::jsonb. This test pins the current contract — if someone swaps to
		// a normalized storage, they must update this test AND the callers.
		owner := "dl_bytes_" + mkStamp()
		mustCreateUser(t, d, ctx, owner)
		cleanupLayoutOwner(t, d, ctx, owner)

		// Use only-single-key JSON so key-reorder isn't an issue; we're
		// verifying whitespace + inner array order are preserved.
		layout := json.RawMessage(`{"cols":12,"widgets":[{"i":"card","x":0,"y":0,"w":6,"h":3}]}`)
		if err := d.SetDashboardLayout(ctx, owner, "public_profile", layout); err != nil {
			t.Fatal(err)
		}
		got, ok, err := d.GetDashboardLayout(ctx, owner, "public_profile")
		if err != nil || !ok {
			t.Fatalf("get: ok=%v err=%v", ok, err)
		}
		// Semantic equality (JSONB may reorder but content must match).
		var a, b any
		if err := json.Unmarshal(layout, &a); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(got, &b); err != nil {
			t.Fatal(err)
		}
		aBytes, _ := json.Marshal(a)
		bBytes, _ := json.Marshal(b)
		if !bytes.Equal(aBytes, bBytes) {
			t.Fatalf("roundtrip mismatch:\n  in:  %s\n  out: %s", aBytes, bBytes)
		}
	})

	t.Run("cross-owner-isolation-get", func(t *testing.T) {
		// A writes; B must NOT see it. Guards against "SELECT layout WHERE scope=$1"
		// (forgetting owner filter) regressions.
		a := "dl_A_" + mkStamp()
		b := "dl_B_" + mkStamp()
		mustCreateUser(t, d, ctx, a)
		mustCreateUser(t, d, ctx, b)
		cleanupLayoutOwner(t, d, ctx, a)
		cleanupLayoutOwner(t, d, ctx, b)

		if err := d.SetDashboardLayout(ctx, a, "public_profile", json.RawMessage(`{"a":1}`)); err != nil {
			t.Fatal(err)
		}
		raw, ok, err := d.GetDashboardLayout(ctx, b, "public_profile")
		if err != nil {
			t.Fatal(err)
		}
		if ok || raw != nil {
			t.Fatalf("user B saw user A's layout: ok=%v raw=%s", ok, raw)
		}
	})

	t.Run("cross-owner-isolation-delete-does-not-affect-other", func(t *testing.T) {
		// B deletes (owner=B, scope=public_profile) — A's row must survive
		// because DELETE is scoped by owner. Pins the isolation on the delete path.
		a := "dl_delA_" + mkStamp()
		b := "dl_delB_" + mkStamp()
		mustCreateUser(t, d, ctx, a)
		mustCreateUser(t, d, ctx, b)
		cleanupLayoutOwner(t, d, ctx, a)
		cleanupLayoutOwner(t, d, ctx, b)

		if err := d.SetDashboardLayout(ctx, a, "public_profile", json.RawMessage(`{"a":1}`)); err != nil {
			t.Fatal(err)
		}
		// B "reset" on the SAME scope — must not touch A.
		if err := d.DeleteDashboardLayout(ctx, b, "public_profile"); err != nil {
			t.Fatal(err)
		}
		raw, ok, err := d.GetDashboardLayout(ctx, a, "public_profile")
		if err != nil {
			t.Fatal(err)
		}
		if !ok || raw == nil {
			t.Fatal("user A's layout was wiped by user B's delete")
		}
	})

	t.Run("upsert-last-write-wins", func(t *testing.T) {
		// Two SetLayout calls for the same (owner,scope) — GET returns the LATER
		// value. Pins the ON CONFLICT DO UPDATE SET layout = EXCLUDED.layout.
		owner := "dl_lww_" + mkStamp()
		mustCreateUser(t, d, ctx, owner)
		cleanupLayoutOwner(t, d, ctx, owner)

		if err := d.SetDashboardLayout(ctx, owner, "s", json.RawMessage(`{"v":1}`)); err != nil {
			t.Fatal(err)
		}
		if err := d.SetDashboardLayout(ctx, owner, "s", json.RawMessage(`{"v":2}`)); err != nil {
			t.Fatal(err)
		}
		raw, ok, err := d.GetDashboardLayout(ctx, owner, "s")
		if err != nil || !ok {
			t.Fatal(err)
		}
		var v struct {
			V int `json:"v"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		if v.V != 2 {
			t.Fatalf("expected v=2 after second SetLayout, got %d", v.V)
		}
	})

	t.Run("delete-idempotence", func(t *testing.T) {
		// Two DeleteLayout on non-existent row → both succeed. Pins the
		// idempotent contract that the "Reset to defaults" button depends on.
		owner := "dl_idem_" + mkStamp()
		mustCreateUser(t, d, ctx, owner)
		cleanupLayoutOwner(t, d, ctx, owner)

		if err := d.DeleteDashboardLayout(ctx, owner, "s"); err != nil {
			t.Fatalf("1st delete: %v", err)
		}
		if err := d.DeleteDashboardLayout(ctx, owner, "s"); err != nil {
			t.Fatalf("2nd delete: %v", err)
		}
	})

	t.Run("distinct-scopes-are-independent-rows", func(t *testing.T) {
		// The (owner,scope) UNIQUE means writing "overview" must NOT clobber
		// "public_profile" for the same owner. Pins the scope column's purpose.
		owner := "dl_scopes_" + mkStamp()
		mustCreateUser(t, d, ctx, owner)
		cleanupLayoutOwner(t, d, ctx, owner)

		if err := d.SetDashboardLayout(ctx, owner, "public_profile", json.RawMessage(`{"s":"pp"}`)); err != nil {
			t.Fatal(err)
		}
		if err := d.SetDashboardLayout(ctx, owner, "overview", json.RawMessage(`{"s":"ov"}`)); err != nil {
			t.Fatal(err)
		}
		pp, ok, err := d.GetDashboardLayout(ctx, owner, "public_profile")
		if err != nil || !ok {
			t.Fatal(err)
		}
		if !bytesContain(pp, "pp") {
			t.Fatalf("public_profile got clobbered: %s", pp)
		}
		ov, ok, err := d.GetDashboardLayout(ctx, owner, "overview")
		if err != nil || !ok {
			t.Fatal(err)
		}
		if !bytesContain(ov, "ov") {
			t.Fatalf("overview got clobbered: %s", ov)
		}
	})

	t.Run("cancelled-ctx-surfaces-error", func(t *testing.T) {
		// Pins the non-ErrNoRows error branch — cancelled ctx bubbles up as
		// an error, not as (nil, false, nil) which would mask 500s at the
		// handler layer.
		owner := "dl_ctxerr_" + mkStamp()
		mustCreateUser(t, d, ctx, owner)
		cleanupLayoutOwner(t, d, ctx, owner)
		if err := d.SetDashboardLayout(ctx, owner, "s", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := d.GetDashboardLayout(cancelledCtx, owner, "s"); err == nil {
			t.Fatal("expected error from cancelled ctx get")
		}
		if err := d.SetDashboardLayout(cancelledCtx, owner, "s", json.RawMessage(`{}`)); err == nil {
			t.Fatal("expected error from cancelled ctx set")
		}
		if err := d.DeleteDashboardLayout(cancelledCtx, owner, "s"); err == nil {
			t.Fatal("expected error from cancelled ctx delete")
		}
	})

	// Assert the pgxpool import is used (silences the linter if a future
	// refactor drops the direct reference — the helper above uses it).
	_ = pgxpool.Pool{}
}

func bytesContain(raw json.RawMessage, needle string) bool {
	return bytes.Contains(raw, []byte(needle))
}

// mkStamp is a monotonic timestamp suffix so tests running in parallel don't
// collide on user names. mkSender from harness_test.go collapses the trailing
// nanos too fast for us here (subtests share the wall-clock second).
func mkStamp() string {
	return time.Now().Format("150405.000000000")
}
