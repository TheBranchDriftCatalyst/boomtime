// widget_links_test.go — stdlib tests for the DB accessors in widgets.go
// (gaka-se2.7). Sibling of widgets_test.go which tests the pure helpers.
// Every t.Run pins ONE named invariant. Cross-owner isolation and the
// origin-cap semantics are the load-bearing checks.
package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// mkWidgetSender inserts a users row and registers cleanup for widget_links,
// dashboard_layouts, widget_defs, projects, spaces, users (widget tests can
// touch any of those). newSender in harness_test.go covers most, but we also
// need widget_defs / dashboard_layouts to be scrubbed.
func mkWidgetSender(t *testing.T, d *DB, ctx context.Context, prefix string) string {
	t.Helper()
	name := prefix + "_" + mkStamp()
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`,
		name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM widget_links WHERE username=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM widget_defs WHERE username=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM dashboard_layouts WHERE owner=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM spaces WHERE owner=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, name)
	})
	return name
}

func mkProject(t *testing.T, d *DB, ctx context.Context, owner, name string) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO projects (owner, name) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		owner, name); err != nil {
		t.Fatal(err)
	}
}

func TestCreateWidgetLink(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("upsert-idempotence-same-user-same-scope-same-uuid", func(t *testing.T) {
		// Pins the "stable embeds" contract: re-clicking Mint for the same
		// project must return the SAME link_id (not a fresh row via ON CONFLICT
		// DO NOTHING RETURNING nil).
		user := mkWidgetSender(t, d, ctx, "cwl_stable")
		id1, err := d.CreateWidgetLink(ctx, user, WidgetScopeUser, "")
		if err != nil {
			t.Fatal(err)
		}
		id2, err := d.CreateWidgetLink(ctx, user, WidgetScopeUser, "")
		if err != nil {
			t.Fatal(err)
		}
		if id1 != id2 {
			t.Fatalf("expected stable uuid on re-mint: %s vs %s", id1, id2)
		}
	})

	t.Run("different-scope-refs-are-independent-uuids", func(t *testing.T) {
		// Same user, different project refs → two DIFFERENT link_ids.
		// Pins that the unique key is (user, scope_type, scope_ref) — not just user.
		user := mkWidgetSender(t, d, ctx, "cwl_diff")
		mkProject(t, d, ctx, user, "proj-a")
		mkProject(t, d, ctx, user, "proj-b")
		a, err := d.CreateWidgetLink(ctx, user, WidgetScopeProject, "proj-a")
		if err != nil {
			t.Fatal(err)
		}
		b, err := d.CreateWidgetLink(ctx, user, WidgetScopeProject, "proj-b")
		if err != nil {
			t.Fatal(err)
		}
		if a == b {
			t.Fatal("expected distinct uuids for distinct project scopes")
		}
	})

	t.Run("scope-type-check-constraint-rejects-invalid", func(t *testing.T) {
		// Pins the schema CHECK constraint (scope_type IN 'user','project','space').
		// If someone drops that CHECK, this test flags it before a bad handler
		// mints garbage.
		user := mkWidgetSender(t, d, ctx, "cwl_check")
		if _, err := d.CreateWidgetLink(ctx, user, "not_a_real_scope", "x"); err == nil {
			t.Fatal("expected CHECK constraint to reject invalid scope_type")
		}
	})
}

func TestGetWidgetLinkInfo(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("missing-link-returns-ok-false-nil-error", func(t *testing.T) {
		// Pins the missing-row convention (no leaky pgx.ErrNoRows). Handler
		// depends on this to 404 without a 500.
		_, _, _, ok, err := d.GetWidgetLinkInfo(ctx, uuid.New())
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if ok {
			t.Fatal("expected ok=false for random uuid")
		}
	})

	t.Run("resolves-created-link-to-user-scope-triple", func(t *testing.T) {
		// The one non-tautological round-trip we NEED: the public SVG endpoint
		// takes a uuid and must recover (user, scope_type, scope_ref) to run
		// the render + curation for the right owner.
		user := mkWidgetSender(t, d, ctx, "gwli_get")
		id, err := d.CreateWidgetLink(ctx, user, WidgetScopeUser, "")
		if err != nil {
			t.Fatal(err)
		}
		u, st, sr, ok, err := d.GetWidgetLinkInfo(ctx, id)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if u != user || st != WidgetScopeUser || sr != "" {
			t.Fatalf("mismatched triple: got (%q,%q,%q)", u, st, sr)
		}
	})
}

func TestListWidgetLinks(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("cross-owner-isolation-user-A-links-not-in-user-B-list", func(t *testing.T) {
		// Critical invariant. A mints, B lists — must NOT see A's row.
		a := mkWidgetSender(t, d, ctx, "lwl_A")
		b := mkWidgetSender(t, d, ctx, "lwl_B")
		mkProject(t, d, ctx, a, "proj-a")
		aID, err := d.CreateWidgetLink(ctx, a, WidgetScopeProject, "proj-a")
		if err != nil {
			t.Fatal(err)
		}
		bList, err := d.ListWidgetLinks(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		for _, wl := range bList {
			if wl.LinkID == aID {
				t.Fatalf("B saw A's link %s in list", aID)
			}
		}
	})

	t.Run("space-scope-carries-space-name-project-scope-echoes-ref", func(t *testing.T) {
		// LEFT JOIN spaces resolves ScopeName; the CASE in the SELECT is
		// load-bearing (space name != scope_ref which is the space id).
		user := mkWidgetSender(t, d, ctx, "lwl_scope")
		mkProject(t, d, ctx, user, "myproj")
		// insert a space and grab its id
		var spaceID int
		if err := d.Pool.QueryRow(ctx,
			`INSERT INTO spaces (owner, name) VALUES ($1,'MarketingSpace') RETURNING id`,
			user).Scan(&spaceID); err != nil {
			t.Fatal(err)
		}
		if _, err := d.CreateWidgetLink(ctx, user, WidgetScopeProject, "myproj"); err != nil {
			t.Fatal(err)
		}
		if _, err := d.CreateWidgetLink(ctx, user, WidgetScopeSpace, itoa(spaceID)); err != nil {
			t.Fatal(err)
		}

		list, err := d.ListWidgetLinks(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		var sawProj, sawSpace bool
		for _, wl := range list {
			switch wl.ScopeType {
			case WidgetScopeProject:
				sawProj = true
				if wl.ScopeName != "myproj" {
					t.Fatalf("project ScopeName should echo ref, got %q", wl.ScopeName)
				}
			case WidgetScopeSpace:
				sawSpace = true
				if wl.ScopeName != "MarketingSpace" {
					t.Fatalf("space ScopeName should be joined name, got %q", wl.ScopeName)
				}
			}
		}
		if !sawProj || !sawSpace {
			t.Fatalf("missing scopes: proj=%v space=%v (list=%d)", sawProj, sawSpace, len(list))
		}
	})

	t.Run("orphan-tolerance-project-deleted-link-still-lists", func(t *testing.T) {
		// Widget link outlives its project — the FE row must still render (the
		// admin needs to see the leftover to remove it). Pins the "no cascade"
		// design choice for widget_links.
		user := mkWidgetSender(t, d, ctx, "lwl_orph")
		mkProject(t, d, ctx, user, "temp")
		id, err := d.CreateWidgetLink(ctx, user, WidgetScopeProject, "temp")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1 AND name=$2`, user, "temp"); err != nil {
			t.Fatal(err)
		}
		list, err := d.ListWidgetLinks(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, wl := range list {
			if wl.LinkID == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("orphaned widget_link was hidden from List after project delete")
		}
	})
}

func TestRollWidgetLink(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("cross-owner-roll-does-not-mutate-victim-row", func(t *testing.T) {
		// B tries to roll A's link id — must return (nil, false, nil) AND
		// A's link_id must be unchanged (both the original id AND the row still
		// exists). Pins the owner-scoped WHERE username=$1.
		a := mkWidgetSender(t, d, ctx, "rwl_A")
		b := mkWidgetSender(t, d, ctx, "rwl_B")
		aID, err := d.CreateWidgetLink(ctx, a, WidgetScopeUser, "")
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := d.RollWidgetLink(ctx, b, aID)
		if err != nil {
			t.Fatalf("cross-owner roll should be silent 404, got err=%v", err)
		}
		if ok {
			t.Fatalf("expected ok=false when B rolls A's link, got new id %s", got)
		}
		// A's original link must still resolve unchanged.
		u, _, _, ok2, err := d.GetWidgetLinkInfo(ctx, aID)
		if err != nil || !ok2 || u != a {
			t.Fatalf("A's link was mutated by B's roll: ok=%v u=%q err=%v", ok2, u, err)
		}
	})

	t.Run("owner-roll-mints-new-id-old-id-404s", func(t *testing.T) {
		// The whole point of Roll: leaked URL revocation. Old id → 404, new id
		// → resolves to same user/scope.
		user := mkWidgetSender(t, d, ctx, "rwl_own")
		oldID, err := d.CreateWidgetLink(ctx, user, WidgetScopeUser, "")
		if err != nil {
			t.Fatal(err)
		}
		newID, ok, err := d.RollWidgetLink(ctx, user, oldID)
		if err != nil || !ok {
			t.Fatalf("owner roll: ok=%v err=%v", ok, err)
		}
		if newID == oldID {
			t.Fatal("roll should mint a NEW uuid")
		}
		if _, _, _, ok, _ := d.GetWidgetLinkInfo(ctx, oldID); ok {
			t.Fatal("old id must 404 after roll")
		}
		u, _, _, ok, err := d.GetWidgetLinkInfo(ctx, newID)
		if err != nil || !ok || u != user {
			t.Fatalf("new id should resolve to same owner: ok=%v u=%q err=%v", ok, u, err)
		}
	})

	t.Run("nonexistent-id-returns-ok-false-no-error", func(t *testing.T) {
		got, ok, err := d.RollWidgetLink(ctx, "nobody", uuid.New())
		if err != nil {
			t.Fatalf("expected silent miss, got %v", err)
		}
		if ok || got != uuid.Nil {
			t.Fatalf("expected (Nil,false,nil), got (%s,%v)", got, ok)
		}
	})
}

func TestRecordWidgetLinkHit(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("nonexistent-link-no-op-no-error", func(t *testing.T) {
		// Fire-and-forget: caller doesn't own the id, we don't panic. Pins the
		// pgx.ErrNoRows swallow branch.
		if err := d.RecordWidgetLinkHit(ctx, uuid.New(), "https://example.com"); err != nil {
			t.Fatalf("bogus hit should no-op, got %v", err)
		}
	})

	t.Run("first-hit-populates-last-used-and-appends-origin", func(t *testing.T) {
		user := mkWidgetSender(t, d, ctx, "rec_first")
		id, err := d.CreateWidgetLink(ctx, user, WidgetScopeUser, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := d.RecordWidgetLinkHit(ctx, id, "https://blog.example"); err != nil {
			t.Fatal(err)
		}
		list, err := d.ListWidgetLinks(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		wl := findLink(list, id)
		if wl == nil {
			t.Fatal("link disappeared from list after hit")
		}
		if wl.LastUsedAt == nil {
			t.Fatal("LastUsedAt not set after first hit")
		}
		if len(wl.Origins) != 1 || wl.Origins[0].Origin != "https://blog.example" || wl.Origins[0].Count != 1 {
			t.Fatalf("expected 1 origin count=1, got %+v", wl.Origins)
		}
	})

	t.Run("empty-origin-collapses-to-direct", func(t *testing.T) {
		// GitHub camo strips Referer — every second hit shows "direct". Pins
		// the `if origin == "" { origin = "direct" }` normalization.
		user := mkWidgetSender(t, d, ctx, "rec_direct")
		id, err := d.CreateWidgetLink(ctx, user, WidgetScopeUser, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := d.RecordWidgetLinkHit(ctx, id, ""); err != nil {
			t.Fatal(err)
		}
		list, _ := d.ListWidgetLinks(ctx, user)
		wl := findLink(list, id)
		if wl == nil || len(wl.Origins) != 1 || wl.Origins[0].Origin != "direct" {
			t.Fatalf("empty origin should become 'direct', got %+v", wl.Origins)
		}
	})

	t.Run("repeat-origin-bumps-count-not-length", func(t *testing.T) {
		// Same origin twice → count=2, one entry (no dup rows).
		user := mkWidgetSender(t, d, ctx, "rec_bump")
		id, err := d.CreateWidgetLink(ctx, user, WidgetScopeUser, "")
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			if err := d.RecordWidgetLinkHit(ctx, id, "https://one"); err != nil {
				t.Fatal(err)
			}
		}
		list, _ := d.ListWidgetLinks(ctx, user)
		wl := findLink(list, id)
		if wl == nil || len(wl.Origins) != 1 || wl.Origins[0].Count != 3 {
			t.Fatalf("expected 1 origin count=3, got %+v", wl.Origins)
		}
	})

	t.Run("origins-capped-at-20-drop-oldest-by-last-seen", func(t *testing.T) {
		// 21 distinct origins → 20 stored, and the earliest is the one
		// dropped (sorted by LastSeen DESC). Pins the originsCap invariant.
		user := mkWidgetSender(t, d, ctx, "rec_cap")
		id, err := d.CreateWidgetLink(ctx, user, WidgetScopeUser, "")
		if err != nil {
			t.Fatal(err)
		}
		// Insert 21 distinct origins; each hit is naturally more recent, so
		// origin #0 becomes stalest and should be evicted.
		for i := 0; i < 21; i++ {
			if err := d.RecordWidgetLinkHit(ctx, id, "https://o"+itoa(i)); err != nil {
				t.Fatalf("hit %d: %v", i, err)
			}
			// nudge clock forward so LastSeen ordering is well-defined
			time.Sleep(1 * time.Millisecond)
		}
		list, _ := d.ListWidgetLinks(ctx, user)
		wl := findLink(list, id)
		if wl == nil {
			t.Fatal("link missing")
		}
		if len(wl.Origins) != 20 {
			t.Fatalf("expected 20 origins after cap, got %d", len(wl.Origins))
		}
		for _, o := range wl.Origins {
			if o.Origin == "https://o0" {
				t.Fatal("stalest origin (o0) should have been evicted by LastSeen DESC cap")
			}
		}
	})
}

func TestProjectExists(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("cross-owner-A-project-invisible-to-B", func(t *testing.T) {
		// A's project "shared" — B asking about "shared" gets false. Pins
		// the ownership half of the WHERE.
		a := mkWidgetSender(t, d, ctx, "px_A")
		b := mkWidgetSender(t, d, ctx, "px_B")
		mkProject(t, d, ctx, a, "shared")
		ok, err := d.ProjectExists(ctx, b, "shared")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatal("B sees A's project 'shared'")
		}
		// Sanity: A DOES see it.
		if ok, _ := d.ProjectExists(ctx, a, "shared"); !ok {
			t.Fatal("A should see own project")
		}
	})

	t.Run("case-insensitive-lookup", func(t *testing.T) {
		// The mint handler is called with whatever case the user typed;
		// existence lookup must be case-fold. Pins the lower(name) = lower($2).
		user := mkWidgetSender(t, d, ctx, "px_case")
		mkProject(t, d, ctx, user, "MyProject")
		ok, err := d.ProjectExists(ctx, user, "myproject")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("case-insensitive lookup failed")
		}
	})

	t.Run("nonexistent-returns-false-no-error", func(t *testing.T) {
		user := mkWidgetSender(t, d, ctx, "px_none")
		ok, err := d.ProjectExists(ctx, user, "no-such-project")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatal("expected false for missing project")
		}
	})
}

func TestWidgetsCancelledCtx(t *testing.T) {
	// Force the non-ErrNoRows error paths on every widgets.go accessor via a
	// cancelled ctx. Pins that pool-level errors bubble up rather than being
	// masked as ok=false (which would make handler-side 500s invisible).
	d := openTestDB(t)
	ctx := context.Background()
	user := mkWidgetSender(t, d, ctx, "cancelled")
	id, err := d.CreateWidgetLink(ctx, user, WidgetScopeUser, "")
	if err != nil {
		t.Fatal(err)
	}
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()

	t.Run("GetWidgetLinkInfo", func(t *testing.T) {
		if _, _, _, _, err := d.GetWidgetLinkInfo(cancelledCtx, id); err == nil {
			t.Fatal("expected err")
		}
	})
	t.Run("ListWidgetLinks", func(t *testing.T) {
		if _, err := d.ListWidgetLinks(cancelledCtx, user); err == nil {
			t.Fatal("expected err")
		}
	})
	t.Run("RollWidgetLink", func(t *testing.T) {
		if _, _, err := d.RollWidgetLink(cancelledCtx, user, id); err == nil {
			t.Fatal("expected err")
		}
	})
	t.Run("RecordWidgetLinkHit", func(t *testing.T) {
		if err := d.RecordWidgetLinkHit(cancelledCtx, id, "https://example.com"); err == nil {
			t.Fatal("expected err")
		}
	})
	t.Run("ProjectExists", func(t *testing.T) {
		if _, err := d.ProjectExists(cancelledCtx, user, "any"); err == nil {
			t.Fatal("expected err")
		}
	})
}

func TestProjectMemberSet(t *testing.T) {
	// Pure helper — no DB. Kept here (not in widgets_test.go) so the
	// widget_links_test.go file also exercises the DB-less builder.
	t.Run("lowercases-project-in-exact-slice", func(t *testing.T) {
		// The downstream predicate is `lower(col) = ANY(...)` — the input must
		// already be lowered so a "MyProject" scope matches raw "myproject" rows.
		ms := ProjectMemberSet("MixedCaseProject")
		got := ms.byAxis["project"].exact
		if len(got) != 1 || got[0] != "mixedcaseproject" {
			t.Fatalf("expected lowercased single-element slice, got %v", got)
		}
	})
}

func TestProjectMemberSetWithRenames(t *testing.T) {
	// Expansion: a project pinned in the scope must reach the raw heartbeats
	// stored under every rename SOURCE name too (gaka-xuc). This mirrors the
	// ginkgo test but pins it as stdlib coverage of widgets.go too.
	t.Run("scope-ref-plus-rename-sources-all-lowered-and-deduped", func(t *testing.T) {
		// mkRenames stores KEYS as-given (no lowering) but ExactSourcesFor
		// compares mapped==target with the given "target" string. Use
		// lowercase target so the raw sources are actually returned.
		rs := mkRenames("project", map[string]string{
			"hakatime": "boomtime",
			"catalyst": "boomtime",
			"boomtime": "boomtime", // identity — must NOT appear twice after dedup
		})
		ms := ProjectMemberSetWithRenames("boomtime", rs)
		got := ms.byAxis["project"].exact
		// First element is always the (lowered) scope-ref.
		if len(got) == 0 || got[0] != "boomtime" {
			t.Fatalf("expected scope-ref 'boomtime' at head, got %v", got)
		}
		// Dedup: 'boomtime' must appear exactly once (identity rename
		// source must not be re-appended after the scope-ref).
		count := 0
		for _, v := range got {
			if v == "boomtime" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected scope-ref deduped, saw %d copies in %v", count, got)
		}
		// hakatime and catalyst must both be in the expansion.
		seen := map[string]bool{}
		for _, v := range got {
			seen[v] = true
		}
		if !seen["hakatime"] || !seen["catalyst"] {
			t.Fatalf("missing rename sources in expansion: %v", got)
		}
	})

	t.Run("empty-rename-set-returns-single-lowered-element", func(t *testing.T) {
		ms := ProjectMemberSetWithRenames("MyProj", RenameSets{})
		got := ms.byAxis["project"].exact
		if len(got) != 1 || got[0] != "myproj" {
			t.Fatalf("expected [myproj], got %v", got)
		}
	})
}

// findLink is a small List helper — origin cap tests need Origins as decoded
// on the way out (LWL does the json.Unmarshal for us).
func findLink(list []WidgetLink, id uuid.UUID) *WidgetLink {
	for i := range list {
		if list[i].LinkID == id {
			return &list[i]
		}
	}
	return nil
}

// itoa is a shorter strconv.Itoa (avoid an import in test files that already
// have plenty).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// (unused imports guard so `strings`/`json` don't leak if refactored away —
// they're required today for scope strings + json literal payload.)
var _ = strings.EqualFold
var _ = json.Valid
