// migrate_standalone_test.go — unit coverage for the books-only migration set
// applied via shared/db.MigrateURLFS (gaka-zp2s). This is the schema-focused
// layer: it exercises ONLY the migration function against a throwaway database
// (no HTTP, no seed, no server). It proves three things the composition tests
// can't isolate:
//
//   1. the books-only tables are created (reading_items, reading_activity, …);
//   2. the owner→users FK on reading_items is RESTORED — asserted at the catalog
//      level AND behaviorally (a bogus-owner INSERT is rejected). If the FK were
//      re-stripped, the behavioral half fails;
//   3. the schema is genuinely books-ONLY — host tables (heartbeats) and the
//      full users model (encrypted_wakatime_key) are ABSENT. If the standalone
//      accidentally applied the host migration set, these negatives fail.
//
// DB-backed: it provisions its own isolated `boomtime_books_schema_test`
// database (DROP + CREATE for a pristine schema each run) via a maintenance
// connection, following the repo's LAN-IP test-DB convention. When Postgres is
// unreachable it Skips (unless BOOM_REQUIRE_DB=1) — the assertions still compile
// and run wherever the test DB is available.
package db_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	booksdb "github.com/TheBranchDriftCatalyst/boomtime/internal/books/db"
	shareddb "github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// booksSchemaDBName is the dedicated isolated DB for this schema test. Distinct
// from the shared boomtime_test DB so it is unambiguously books-only.
const booksSchemaDBName = "boomtime_books_schema_test"

// baseTestURL is the base DSN (defaults to the localhost tilt-shadowed one, which
// the repo convention Skips; override with BOOM_TEST_DATABASE_URL pointing at the
// LAN-IP test Postgres).
func baseTestURL() string {
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://test:test@localhost:5432/boomtime_test?sslmode=disable"
}

// swapDBName returns dsn with its database-name segment replaced by name.
func swapDBName(dsn, name string) string {
	q := ""
	if i := strings.IndexByte(dsn, '?'); i >= 0 {
		q = dsn[i:]
		dsn = dsn[:i]
	}
	if slash := strings.LastIndexByte(dsn, '/'); slash >= 0 {
		return dsn[:slash+1] + name + q
	}
	return dsn + "/" + name + q
}

// provisionBooksSchemaDB DROPs + CREATEs the isolated books schema DB (pristine
// each run) and returns its DSN. Skips the test if Postgres is unreachable.
func provisionBooksSchemaDB(t *testing.T, ctx context.Context) string {
	t.Helper()
	base := baseTestURL()
	maintURL := swapDBName(base, "postgres")
	pool, err := pgxpool.New(ctx, maintURL)
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		if pool != nil {
			pool.Close()
		}
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			t.Fatalf("BOOM_REQUIRE_DB=1 but Postgres unreachable at %s: %v", maintURL, err)
		}
		t.Skipf("test Postgres unreachable (%v) — set BOOM_TEST_DATABASE_URL to the LAN-IP test DB", err)
	}
	defer pool.Close()

	// Terminate any leftover connections, then DROP + CREATE for a pristine schema.
	_, _ = pool.Exec(ctx,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`,
		booksSchemaDBName)
	if _, err := pool.Exec(ctx, `DROP DATABASE IF EXISTS "`+booksSchemaDBName+`"`); err != nil {
		t.Fatalf("drop %s: %v", booksSchemaDBName, err)
	}
	if _, err := pool.Exec(ctx, `CREATE DATABASE "`+booksSchemaDBName+`"`); err != nil {
		t.Fatalf("create %s: %v", booksSchemaDBName, err)
	}
	return swapDBName(base, booksSchemaDBName)
}

func TestBooksOnlyMigrationSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	targetURL := provisionBooksSchemaDB(t, ctx)

	// The function under test: apply the books-only FS to the fresh DB.
	if err := shareddb.MigrateURLFS(ctx, targetURL, booksdb.MigrationsFS); err != nil {
		t.Fatalf("MigrateURLFS(booksdb.MigrationsFS): %v", err)
	}

	pool, err := pgxpool.New(ctx, targetURL)
	if err != nil {
		t.Fatalf("connect to migrated DB: %v", err)
	}
	defer pool.Close()

	tableExists := func(name string) bool {
		var ok bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			   WHERE table_schema = 'public' AND table_name = $1)`, name).Scan(&ok); err != nil {
			t.Fatalf("table-exists probe %q: %v", name, err)
		}
		return ok
	}
	columnExists := func(table, col string) bool {
		var ok bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns
			   WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2)`,
			table, col).Scan(&ok); err != nil {
			t.Fatalf("column-exists probe %q.%q: %v", table, col, err)
		}
		return ok
	}

	// (1) Books-only tables present.
	for _, tbl := range []string{
		"users", "reading_items", "reading_activity", "book_sync_state",
		"kindle_reading_insights", "kindle_reading_positions", "reading_events",
	} {
		if !tableExists(tbl) {
			t.Errorf("books schema is missing expected table %q", tbl)
		}
	}

	// (2a) The owner→users FK on reading_items exists at the catalog level.
	var fkCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		JOIN information_schema.constraint_column_usage ccu
		  ON tc.constraint_name = ccu.constraint_name AND tc.table_schema = ccu.table_schema
		WHERE tc.constraint_type = 'FOREIGN KEY'
		  AND tc.table_schema = 'public'
		  AND tc.table_name = 'reading_items'
		  AND kcu.column_name = 'owner'
		  AND ccu.table_name = 'users'
		  AND ccu.column_name = 'username'`).Scan(&fkCount); err != nil {
		t.Fatalf("FK catalog probe: %v", err)
	}
	if fkCount == 0 {
		t.Error("reading_items.owner → users(username) FK is missing — the restored FK was stripped")
	}

	// (2b) Behavioral proof the FK is ENFORCED, not just declared: an INSERT with
	// an owner that has no users row must be rejected. This is the assertion that
	// fails loudest if the FK were re-stripped (the catalog probe could pass on a
	// differently-shaped constraint; this cannot).
	_, ferr := pool.Exec(ctx,
		`INSERT INTO reading_items (owner, source, external_id) VALUES ($1,$2,$3)`,
		"ghost-owner-not-seeded", "kindle", "asin-x")
	if ferr == nil {
		t.Error("INSERT with an unknown owner SUCCEEDED — the owner→users FK is not enforced")
	} else if !strings.Contains(strings.ToLower(ferr.Error()), "foreign key") {
		t.Errorf("expected a foreign-key violation, got: %v", ferr)
	}

	// (3) Books-ONLY: host tables + the full users model must be ABSENT. If the
	// standalone ever applied the host migration set instead, these fail.
	for _, hostOnly := range []string{"heartbeats", "durations", "spaces", "dashboard_layouts"} {
		if tableExists(hostOnly) {
			t.Errorf("host-only table %q present — the books schema is NOT books-only", hostOnly)
		}
	}
	if columnExists("users", "encrypted_wakatime_key") {
		t.Error("users.encrypted_wakatime_key present — the full host users model leaked into the books stub")
	}
	if columnExists("users", "password_hash") {
		t.Error("users.password_hash present — the books users stub must carry NO auth columns")
	}
}
