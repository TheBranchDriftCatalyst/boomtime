package db

import (
	"strings"
	"testing"
	"time"
)

// redact_entities_test.go pins the RedactEntities invariants that no prior
// test exercised. RedactEntities is the destructive counterpart to
// ListEntitiesByType (Entity Explorer's "purge" button). Its contract:
//
//   1. Owner-scoped: A cannot redact B's entities. (No test.)
//   2. Case-insensitive: passing "src/Main.go" must also redact rows stored
//      as "src/main.go" or "SRC/MAIN.GO" — mirrors the case-folded list view
//      so a user redacting what they see actually clears every case variant.
//   3. Non-destructive on the HB row: `entity` becomes `''`, but the row
//      survives (project/language/machine totals still count).
//   4. ty-scoped: passing ty='file' must NOT touch a ty='url' row whose
//      entity string matches.

func TestRedactEntitiesCaseInsensitiveAndOwnerScoped(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	// Two users. A intends to redact "src/main.go" — this must NOT touch B.
	a := newSender(t, d, "redA")
	b := newSender(t, d, "redB")

	day := time.Date(2025, 6, 3, 10, 0, 0, 0, time.UTC)
	ensureProjects(t, d, a.Ctx(), a.Sender(), "P")
	ensureProjects(t, d, b.Ctx(), b.Sender(), "P")

	// A: three case variants of the same entity + one distinct entity.
	insertSeed(t, d, a.Ctx(), a.Sender(), hbSeed{project: "P", entity: "src/main.go", ts: day, gap: 60})
	insertSeed(t, d, a.Ctx(), a.Sender(), hbSeed{project: "P", entity: "src/Main.go", ts: day.Add(time.Minute), gap: 60})
	insertSeed(t, d, a.Ctx(), a.Sender(), hbSeed{project: "P", entity: "SRC/MAIN.GO", ts: day.Add(2 * time.Minute), gap: 60})
	insertSeed(t, d, a.Ctx(), a.Sender(), hbSeed{project: "P", entity: "keep.go", ts: day.Add(3 * time.Minute), gap: 60})

	// B: identical case variant that MUST survive (owner scoping).
	insertSeed(t, d, b.Ctx(), b.Sender(), hbSeed{project: "P", entity: "src/main.go", ts: day, gap: 60})

	// A redacts via one casing — all three variants of A's rows must clear.
	n, err := d.RedactEntities(a.Ctx(), a.Sender(), "file", []string{"src/main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("RedactEntities affected = %d, want 3 (three case variants of A's)", n)
	}

	// The heartbeat ROWS must survive (RedactEntities is non-destructive on rows).
	var totalAfter int
	if err := d.Pool.QueryRow(a.Ctx(),
		`SELECT count(*) FROM heartbeats WHERE sender=$1`, a.Sender()).Scan(&totalAfter); err != nil {
		t.Fatal(err)
	}
	if totalAfter != 4 {
		t.Fatalf("A row count after redact = %d, want 4 (rows preserved)", totalAfter)
	}

	// All three of A's src/main.go variants now have entity = ''.
	var blank int
	if err := d.Pool.QueryRow(a.Ctx(),
		`SELECT count(*) FROM heartbeats WHERE sender=$1 AND entity=''`, a.Sender()).Scan(&blank); err != nil {
		t.Fatal(err)
	}
	if blank != 3 {
		t.Fatalf("A blanked entities = %d, want 3", blank)
	}
	// The 'keep.go' row is untouched.
	var keep int
	if err := d.Pool.QueryRow(a.Ctx(),
		`SELECT count(*) FROM heartbeats WHERE sender=$1 AND entity='keep.go'`, a.Sender()).Scan(&keep); err != nil {
		t.Fatal(err)
	}
	if keep != 1 {
		t.Fatalf("A keep.go rows = %d, want 1 (non-matching entity untouched)", keep)
	}

	// B's src/main.go is UNTOUCHED — owner scoping invariant.
	var bMain int
	if err := d.Pool.QueryRow(b.Ctx(),
		`SELECT count(*) FROM heartbeats WHERE sender=$1 AND lower(entity)=lower($2)`,
		b.Sender(), "src/main.go").Scan(&bMain); err != nil {
		t.Fatal(err)
	}
	if bMain != 1 {
		t.Fatalf("B src/main.go rows = %d, want 1 (A's redact must not touch B)", bMain)
	}
}

// TestRedactEntitiesTyScoped: a redact on ty='file' must NOT touch ty='url' or
// ty='domain' rows whose entity string happens to match. This is the tenant
// invariant of the ty axis — Entity Explorer's per-type table view depends on
// each ty maintaining its own visible entity set.
func TestRedactEntitiesTyScoped(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "red_ty")
	sender := f.Sender()
	ctx := f.Ctx()
	f.Projects("P")

	day := time.Date(2025, 6, 4, 10, 0, 0, 0, time.UTC)
	// Same string on file + url; only file should be affected.
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "https://ex.com/x",
		ty: "file", ts: day, gap: 60})
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "https://ex.com/x",
		ty: "url", ts: day.Add(time.Minute), gap: 60})

	n, err := d.RedactEntities(ctx, sender, "file", []string{"https://ex.com/x"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("RedactEntities affected = %d, want 1 (only ty='file' row)", n)
	}

	// url row survives.
	var alive int
	if err := d.Pool.QueryRow(ctx,
		`SELECT count(*) FROM heartbeats WHERE sender=$1 AND ty='url' AND entity=$2`,
		sender, "https://ex.com/x").Scan(&alive); err != nil {
		t.Fatal(err)
	}
	if alive != 1 {
		t.Fatalf("url row survived count = %d, want 1 (redact must be ty-scoped)", alive)
	}
}

// TestRedactEntitiesEmptyInputIsNoop: passing an empty entity slice must not
// touch a single row (documented as a fast-return in RedactEntities).
func TestRedactEntitiesEmptyInputIsNoop(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "red_empty")
	sender := f.Sender()
	ctx := f.Ctx()
	f.Projects("P")

	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "x.go",
		ts: time.Date(2025, 6, 5, 10, 0, 0, 0, time.UTC), gap: 60})

	n, err := d.RedactEntities(ctx, sender, "file", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("RedactEntities([]) = %d, want 0 (must be a no-op)", n)
	}

	var blanks int
	if err := d.Pool.QueryRow(ctx,
		`SELECT count(*) FROM heartbeats WHERE sender=$1 AND entity=''`,
		sender).Scan(&blanks); err != nil {
		t.Fatal(err)
	}
	if blanks != 0 {
		t.Fatalf("blanked entities = %d, want 0", blanks)
	}
}

// TestListEntitiesByTypeExcludesRedacted: after a redact, the same entity must
// NOT re-appear as a phantom bucket (the WHERE `entity <> ''` filter in
// ListEntitiesByType pins this). Verifies redact + list stay in sync.
func TestListEntitiesByTypeExcludesRedacted(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	f := newSender(t, d, "red_list")
	sender := f.Sender()
	ctx := f.Ctx()
	f.Projects("P")

	day := time.Date(2025, 6, 6, 10, 0, 0, 0, time.UTC)
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "purgeme.go", ts: day, gap: 60})
	insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "keep.go", ts: day.Add(time.Minute), gap: 60})

	if _, err := d.RedactEntities(ctx, sender, "file", []string{"purgeme.go"}); err != nil {
		t.Fatal(err)
	}
	list, _, err := d.ListEntitiesByType(ctx, sender, "file", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list {
		if strings.EqualFold(e.Entity, "purgeme.go") {
			t.Fatalf("redacted entity %q still listed", e.Entity)
		}
		if e.Entity == "" {
			t.Fatalf("blank-entity bucket surfaced — the entity<>'' filter is broken")
		}
	}
	// keep.go still visible.
	seen := false
	for _, e := range list {
		if e.Entity == "keep.go" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("keep.go missing from list after redact")
	}
}
