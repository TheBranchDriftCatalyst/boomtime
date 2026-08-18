// widget_defs_test.go — stdlib tests for widget_defs.go (gaka-se2.7).
// Every t.Run pins ONE named invariant. The load-bearing checks are
// cross-owner isolation, the UNIQUE(username,name) constraint, and the
// REPLACE-ALL semantics of UpdateWidgetDef.
package db

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// mkDefSender mirrors mkWidgetSender but only cleans up widget_defs + users.
// Kept separate so a widget_defs test doesn't accidentally rely on
// widget_links cleanup ordering.
func mkDefSender(t *testing.T, d *DB, ctx context.Context, prefix string) string {
	t.Helper()
	name := prefix + "_" + mkStamp()
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`,
		name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM widget_defs WHERE username=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, name)
	})
	return name
}

func TestCreateWidgetDef(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("unique-username-name-second-create-errors", func(t *testing.T) {
		// PK is (username, name) — second CreateWidgetDef with the same name
		// must fail with a PG duplicate error. Pins the "iterate via Update"
		// contract; a silent overwrite would drop the old spec by surprise.
		user := mkDefSender(t, d, ctx, "cwd_dup")
		if _, err := d.CreateWidgetDef(ctx, user, "MyCard", json.RawMessage(`{"v":1}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := d.CreateWidgetDef(ctx, user, "MyCard", json.RawMessage(`{"v":2}`)); err == nil {
			t.Fatal("expected duplicate key error on 2nd create with same (user,name)")
		}
	})

	t.Run("different-users-can-reuse-same-name", func(t *testing.T) {
		// A "MyCard" and B "MyCard" are DIFFERENT rows — pins that the unique
		// key includes username, not just name.
		a := mkDefSender(t, d, ctx, "cwd_multiA")
		b := mkDefSender(t, d, ctx, "cwd_multiB")
		if _, err := d.CreateWidgetDef(ctx, a, "MyCard", json.RawMessage(`{"o":"a"}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := d.CreateWidgetDef(ctx, b, "MyCard", json.RawMessage(`{"o":"b"}`)); err != nil {
			t.Fatalf("B should be able to reuse name across owners, got %v", err)
		}
	})

	t.Run("returns-nonzero-uuid", func(t *testing.T) {
		user := mkDefSender(t, d, ctx, "cwd_uuid")
		id, err := d.CreateWidgetDef(ctx, user, "X", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if id == uuid.Nil {
			t.Fatal("uuid_generate_v4 returned nil uuid — schema regression")
		}
	})
}

func TestGetWidgetDef(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("missing-id-returns-ok-false-no-error", func(t *testing.T) {
		_, _, ok, err := d.GetWidgetDef(ctx, uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatal("expected ok=false for random uuid")
		}
	})

	t.Run("returns-owner-for-public-renderer-curation", func(t *testing.T) {
		// The public renderer NEEDS the owner back — that's how it applies the
		// right curation. If GetWidgetDef ever stopped returning owner, public
		// widgets would render with someone else's rename/hide rules.
		user := mkDefSender(t, d, ctx, "gwd_owner")
		id, err := d.CreateWidgetDef(ctx, user, "N", json.RawMessage(`{"k":true}`))
		if err != nil {
			t.Fatal(err)
		}
		owner, def, ok, err := d.GetWidgetDef(ctx, id)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if owner != user {
			t.Fatalf("owner mismatch: got %q want %q", owner, user)
		}
		if def.Name != "N" || def.DefID != id {
			t.Fatalf("def fields mismatch: %+v", def)
		}
	})

	t.Run("cross-owner-B-fetching-A-def-still-returns-A-as-owner", func(t *testing.T) {
		// GetWidgetDef is by id, so B *can* fetch it — but the returned owner
		// MUST be A (so B's handler layer applies A's curation, not B's). This
		// is the anti-tautology twist: GetWidgetDef doesn't have an "actor"
		// arg, but the returned owner is what protects the invariant.
		a := mkDefSender(t, d, ctx, "gwd_xo_A")
		id, err := d.CreateWidgetDef(ctx, a, "X", json.RawMessage(`{"a":1}`))
		if err != nil {
			t.Fatal(err)
		}
		owner, _, ok, err := d.GetWidgetDef(ctx, id)
		if err != nil || !ok {
			t.Fatal(err)
		}
		if owner != a {
			t.Fatalf("owner leak: fetch by id returned %q, expected creator %q", owner, a)
		}
	})
}

func TestGetWidgetDefByName(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("cross-owner-isolation-A-name-not-visible-to-B", func(t *testing.T) {
		// The builder loads by name — B typing A's def name must NOT get A's
		// spec. Pins the WHERE username=$1 filter.
		a := mkDefSender(t, d, ctx, "gdbn_A")
		b := mkDefSender(t, d, ctx, "gdbn_B")
		if _, err := d.CreateWidgetDef(ctx, a, "SharedName", json.RawMessage(`{"o":"a"}`)); err != nil {
			t.Fatal(err)
		}
		def, ok, err := d.GetWidgetDefByName(ctx, b, "SharedName")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatalf("B saw A's def by name: %+v", def)
		}
	})

	t.Run("missing-name-returns-ok-false-no-error", func(t *testing.T) {
		user := mkDefSender(t, d, ctx, "gdbn_miss")
		_, ok, err := d.GetWidgetDefByName(ctx, user, "no-such")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatal("expected ok=false for missing name")
		}
	})

	t.Run("same-name-different-owner-loads-own-spec", func(t *testing.T) {
		// A and B both named their def "Card" — each must load their OWN spec.
		// If the query dropped the owner filter, either B would see A's or
		// they'd multi-row.
		a := mkDefSender(t, d, ctx, "gdbn_same_A")
		b := mkDefSender(t, d, ctx, "gdbn_same_B")
		if _, err := d.CreateWidgetDef(ctx, a, "Card", json.RawMessage(`{"tag":"a"}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := d.CreateWidgetDef(ctx, b, "Card", json.RawMessage(`{"tag":"b"}`)); err != nil {
			t.Fatal(err)
		}
		aDef, ok, _ := d.GetWidgetDefByName(ctx, a, "Card")
		if !ok {
			t.Fatal("A cannot load own def")
		}
		var av struct{ Tag string }
		_ = json.Unmarshal(aDef.Spec, &av)
		if av.Tag != "a" {
			t.Fatalf("A loaded B's spec: %s", aDef.Spec)
		}
		bDef, ok, _ := d.GetWidgetDefByName(ctx, b, "Card")
		if !ok {
			t.Fatal("B cannot load own def")
		}
		var bv struct{ Tag string }
		_ = json.Unmarshal(bDef.Spec, &bv)
		if bv.Tag != "b" {
			t.Fatalf("B loaded A's spec: %s", bDef.Spec)
		}
	})
}

func TestListWidgetDefs(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("cross-owner-isolation-A-defs-not-in-B-list", func(t *testing.T) {
		a := mkDefSender(t, d, ctx, "lwd_A")
		b := mkDefSender(t, d, ctx, "lwd_B")
		aID, err := d.CreateWidgetDef(ctx, a, "AOnly", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		bList, err := d.ListWidgetDefs(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		for _, def := range bList {
			if def.DefID == aID {
				t.Fatal("B saw A's def in List")
			}
		}
	})

	t.Run("empty-user-returns-empty-slice-not-nil", func(t *testing.T) {
		// Handler json-encodes the return — [] and null render differently to
		// the FE. Pins the `out := []WidgetDef{}` initialization.
		user := mkDefSender(t, d, ctx, "lwd_empty")
		got, err := d.ListWidgetDefs(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatal("List should return non-nil empty slice for empty user")
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 defs, got %d", len(got))
		}
	})

	t.Run("orders-most-recently-updated-first", func(t *testing.T) {
		// UI depends on ORDER BY updated_at DESC. Create A, then B, then
		// UPDATE A → A should come back first.
		user := mkDefSender(t, d, ctx, "lwd_order")
		if _, err := d.CreateWidgetDef(ctx, user, "First", json.RawMessage(`{"v":1}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := d.CreateWidgetDef(ctx, user, "Second", json.RawMessage(`{"v":2}`)); err != nil {
			t.Fatal(err)
		}
		// bump First's updated_at
		if ok, err := d.UpdateWidgetDef(ctx, user, "First", json.RawMessage(`{"v":1,"bumped":true}`)); err != nil || !ok {
			t.Fatalf("bump update: ok=%v err=%v", ok, err)
		}
		list, err := d.ListWidgetDefs(ctx, user)
		if err != nil || len(list) < 2 {
			t.Fatalf("list: %+v err=%v", list, err)
		}
		if list[0].Name != "First" {
			t.Fatalf("expected 'First' at head after update, got %q first (list=%+v)", list[0].Name, list)
		}
	})
}

func TestUpdateWidgetDef(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("replace-all-not-partial", func(t *testing.T) {
		// The bead is explicit: UpdateWidgetDef is REPLACE-ALL, not partial
		// merge. If someone changes to jsonb_set/COALESCE, this test flags it.
		user := mkDefSender(t, d, ctx, "uwd_replace")
		if _, err := d.CreateWidgetDef(ctx, user, "R", json.RawMessage(`{"a":1,"b":2,"c":3}`)); err != nil {
			t.Fatal(err)
		}
		ok, err := d.UpdateWidgetDef(ctx, user, "R", json.RawMessage(`{"z":9}`))
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		got, ok, _ := d.GetWidgetDefByName(ctx, user, "R")
		if !ok {
			t.Fatal("def vanished")
		}
		var m map[string]any
		if err := json.Unmarshal(got.Spec, &m); err != nil {
			t.Fatal(err)
		}
		if _, keep := m["a"]; keep {
			t.Fatalf("REPLACE-ALL violated: old key 'a' still present in %s", got.Spec)
		}
		if v, ok := m["z"]; !ok || v.(float64) != 9 {
			t.Fatalf("expected {z:9}, got %s", got.Spec)
		}
	})

	t.Run("nonexistent-name-returns-ok-false-no-error", func(t *testing.T) {
		user := mkDefSender(t, d, ctx, "uwd_nope")
		ok, err := d.UpdateWidgetDef(ctx, user, "no-such", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatal("expected ok=false for missing name")
		}
	})

	t.Run("cross-owner-update-does-not-touch-victim", func(t *testing.T) {
		// B calls Update(name="X") — A's row must be unchanged. Pins the
		// username filter on UPDATE.
		a := mkDefSender(t, d, ctx, "uwd_xo_A")
		b := mkDefSender(t, d, ctx, "uwd_xo_B")
		if _, err := d.CreateWidgetDef(ctx, a, "X", json.RawMessage(`{"v":"a"}`)); err != nil {
			t.Fatal(err)
		}
		ok, err := d.UpdateWidgetDef(ctx, b, "X", json.RawMessage(`{"v":"hijack"}`))
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatal("B's update on A's def name should return ok=false")
		}
		def, _, _ := d.GetWidgetDefByName(ctx, a, "X")
		var m map[string]string
		_ = json.Unmarshal(def.Spec, &m)
		if m["v"] != "a" {
			t.Fatalf("A's spec was hijacked: %s", def.Spec)
		}
	})
}

func TestDeleteWidgetDef(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("delete-idempotence-second-delete-ok-false-no-error", func(t *testing.T) {
		// Two DELETEs: 1st reports true, 2nd reports false, both no error.
		// Pins the idempotent-delete contract HTTP 204 depends on.
		user := mkDefSender(t, d, ctx, "dwd_idem")
		if _, err := d.CreateWidgetDef(ctx, user, "D", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		ok, err := d.DeleteWidgetDef(ctx, user, "D")
		if err != nil || !ok {
			t.Fatalf("1st delete: ok=%v err=%v", ok, err)
		}
		ok, err = d.DeleteWidgetDef(ctx, user, "D")
		if err != nil {
			t.Fatalf("2nd delete: %v", err)
		}
		if ok {
			t.Fatal("2nd delete on missing row should return ok=false")
		}
	})

	t.Run("cross-owner-delete-does-not-remove-victim", func(t *testing.T) {
		// B calls Delete(name="X") — A's row survives. Pins the username
		// filter on DELETE. This is the load-bearing isolation for the
		// admin panel's "Delete" button.
		a := mkDefSender(t, d, ctx, "dwd_xo_A")
		b := mkDefSender(t, d, ctx, "dwd_xo_B")
		if _, err := d.CreateWidgetDef(ctx, a, "X", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		ok, err := d.DeleteWidgetDef(ctx, b, "X")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatal("B's delete on A's def should report ok=false")
		}
		// A's row must still be present.
		if _, ok, _ := d.GetWidgetDefByName(ctx, a, "X"); !ok {
			t.Fatal("A's def was removed by B's delete")
		}
	})

	t.Run("cancelled-ctx-surfaces-error-not-ok-false", func(t *testing.T) {
		// Pins that a NON-ErrNoRows error propagates (rather than silently
		// masking to ok=false). A cancelled context is the easiest way to
		// coax the pool into a driver error without a mock.
		user := mkDefSender(t, d, ctx, "dwd_ctxerr")
		if _, err := d.CreateWidgetDef(ctx, user, "C", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := d.DeleteWidgetDef(cancelledCtx, user, "C"); err == nil {
			t.Fatal("expected error from cancelled ctx delete")
		}
		if _, err := d.UpdateWidgetDef(cancelledCtx, user, "C", json.RawMessage(`{}`)); err == nil {
			t.Fatal("expected error from cancelled ctx update")
		}
		if _, err := d.ListWidgetDefs(cancelledCtx, user); err == nil {
			t.Fatal("expected error from cancelled ctx list")
		}
		if _, _, err := d.GetWidgetDefByName(cancelledCtx, user, "C"); err == nil {
			t.Fatal("expected error from cancelled ctx get-by-name")
		}
		if _, _, _, err := d.GetWidgetDef(cancelledCtx, uuid.New()); err == nil {
			t.Fatal("expected error from cancelled ctx get")
		}
	})

	t.Run("delete-then-recreate-mints-new-uuid", func(t *testing.T) {
		// Delete + Create → NEW def_id. Ensures uuid_generate_v4 fires each
		// time — a stable id here would be a serious bug (old embeds would
		// silently start pointing at a new spec).
		user := mkDefSender(t, d, ctx, "dwd_recreate")
		id1, err := d.CreateWidgetDef(ctx, user, "R", json.RawMessage(`{"v":1}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.DeleteWidgetDef(ctx, user, "R"); err != nil {
			t.Fatal(err)
		}
		id2, err := d.CreateWidgetDef(ctx, user, "R", json.RawMessage(`{"v":2}`))
		if err != nil {
			t.Fatal(err)
		}
		if id1 == id2 {
			t.Fatal("recreate should mint a new uuid (embeds must break)")
		}
	})
}
