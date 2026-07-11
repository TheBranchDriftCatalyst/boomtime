package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// dbReady is set by TestMain once the isolated test database exists and has been
// migrated. When false, DB-backed tests Skip (unless BOOM_REQUIRE_DB=1).
var (
	dbReady    bool
	dbSkipMsg  string
	testDBName = "boomtime_test"
)

// defaultTestDatabaseURL points at an ISOLATED database, NOT the dev `test` DB,
// so tests never touch real/panda data. Override with BOOM_TEST_DATABASE_URL.
const defaultTestDatabaseURL = "postgres://test:test@localhost:5432/boomtime_test?sslmode=disable"

// testDatabaseURL resolves the isolated test DB DSN.
func testDatabaseURL() string {
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultTestDatabaseURL
}

// maintenanceURLFor returns a DSN to a maintenance database (used to run
// CREATE DATABASE) by swapping the target DB name in `dsn` for `maintDB`.
func maintenanceURLFor(dsn, maintDB string) string {
	// dsn form: postgres://user:pass@host:port/dbname?params
	q := ""
	if i := strings.IndexByte(dsn, '?'); i >= 0 {
		q = dsn[i:]
		dsn = dsn[:i]
	}
	slash := strings.LastIndexByte(dsn, '/')
	if slash < 0 {
		return dsn + q
	}
	return dsn[:slash+1] + maintDB + q
}

// dbNameFromURL extracts the database name from a DSN.
func dbNameFromURL(dsn string) string {
	if i := strings.IndexByte(dsn, '?'); i >= 0 {
		dsn = dsn[:i]
	}
	if slash := strings.LastIndexByte(dsn, '/'); slash >= 0 {
		return dsn[slash+1:]
	}
	return ""
}

// TestMain provisions the isolated test database once for the whole package:
// connect to a maintenance DB, CREATE DATABASE boomtime_test (idempotent), then
// migrate it. If Postgres is unreachable the DB tests Skip, unless BOOM_REQUIRE_DB=1.
func TestMain(m *testing.M) {
	setup()
	os.Exit(m.Run())
}

func setup() {
	require := os.Getenv("BOOM_REQUIRE_DB") == "1"
	url := testDatabaseURL()
	name := dbNameFromURL(url)
	if name != "" {
		testDBName = name
	}

	fail := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if require {
			fmt.Fprintln(os.Stderr, "BOOM_REQUIRE_DB=1 but test DB setup failed:", msg)
			os.Exit(1)
		}
		dbSkipMsg = msg
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. Ensure the isolated database exists (via a maintenance connection).
	if err := ensureTestDatabase(ctx, url); err != nil {
		fail("could not ensure %s exists: %v", testDBName, err)
		return
	}

	// 2. Migrate the isolated database.
	if err := MigrateURL(ctx, url); err != nil {
		fail("could not migrate %s: %v", testDBName, err)
		return
	}

	dbReady = true
}

// ensureTestDatabase creates the target database if it does not already exist,
// using a maintenance connection (tries `postgres`, then `test`).
func ensureTestDatabase(ctx context.Context, targetURL string) error {
	target := dbNameFromURL(targetURL)
	if target == "" {
		return fmt.Errorf("could not determine database name from URL")
	}

	var lastErr error
	for _, maint := range []string{"postgres", "test"} {
		maintURL := maintenanceURLFor(targetURL, maint)
		pool, err := pgxpool.New(ctx, maintURL)
		if err != nil {
			lastErr = err
			continue
		}
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			lastErr = err
			continue
		}
		// CREATE DATABASE can't run in a transaction; Exec runs it directly.
		_, err = pool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", quoteIdent(target)))
		pool.Close()
		if err == nil || isAlreadyExists(err) {
			return nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no reachable maintenance database")
	}
	return lastErr
}

// isAlreadyExists reports whether err is Postgres "database already exists"
// (SQLSTATE 42P04) — swallowed to keep provisioning idempotent.
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "already exists") || strings.Contains(s, "42P04")
}

// quoteIdent double-quotes an SQL identifier (the test DB name is trusted, from
// config, but quote defensively).
func quoteIdent(id string) string {
	return `"` + strings.ReplaceAll(id, `"`, `""`) + `"`
}

// openTestDB connects to the ISOLATED test database (boomtime_test), Skipping the
// test when the DB is unavailable (unless BOOM_REQUIRE_DB=1 already failed setup).
// This can NEVER hit the dev/panda `test` DB.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	if !dbReady {
		t.Skipf("skipping: isolated test database unavailable: %s", dbSkipMsg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database, err := New(ctx, testDatabaseURL())
	if err != nil {
		t.Skipf("skipping: could not open %s: %v", testDBName, err)
	}
	return database
}

// truncateAll wipes every mutable table in the isolated test DB for a clean
// slate. Safe because the DB is dedicated to tests. Order respects FK deps
// (children before parents); RESTART IDENTITY resets serials.
func truncateAll(t *testing.T, d *DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// CASCADE + one statement handles the FK graph regardless of order.
	_, err := d.Pool.Exec(ctx, `TRUNCATE
		import_job_logs, import_jobs,
		hb_rollup_daily, heartbeats,
		badges, space_rules, spaces,
		curation_rules, projects,
		auth_tokens, refresh_tokens, users
		RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncateAll: %v", err)
	}
}
