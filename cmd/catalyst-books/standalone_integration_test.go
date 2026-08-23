// standalone_integration_test.go — Layer-2 integration coverage for the
// STANDALONE catalyst-books composition (boom-zp2s).
//
// This is the layer the unit tests can't reach: it stands up the WHOLE
// standalone HTTP surface exactly as main() wires it — books-only migration
// (MigrateURLFS) + single-owner seed + auth.SetStandaloneOwner + booksapi.Register
// + /healthz + the embedded SPA — against a real, isolated books-only Postgres,
// and drives it over HTTP with NO auth header as the single owner. It proves the
// pieces compose: the auth short-circuit lets an unauthenticated caller read the
// owner's library (200, not 401), a write→read round-trip confirms the restored
// owner→users FK holds for the seeded owner, the SPA serves the shell, and the
// API is NOT shadowed by the SPA "/*" catch-all.
//
// DB-backed: provisions an isolated `boomtime_books_integration_test` DB
// (DROP+CREATE, books-only schema) via a maintenance connection, per the repo's
// LAN-IP test-DB convention. Skips when Postgres is unreachable (unless
// BOOM_REQUIRE_DB=1); the wiring still compiles everywhere.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	booksapi "github.com/TheBranchDriftCatalyst/boomtime/internal/books/api"
	booksdb "github.com/TheBranchDriftCatalyst/boomtime/internal/books/db"
	booksweb "github.com/TheBranchDriftCatalyst/boomtime/internal/books/web"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

const integrationDBName = "boomtime_books_integration_test"

func integrationBaseURL() string {
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://test:test@localhost:5432/boomtime_test?sslmode=disable"
}

func swapDB(dsn, name string) string {
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

// provisionIntegrationDB DROP+CREATEs the isolated books DB and returns its DSN,
// or Skips if Postgres is unreachable.
func provisionIntegrationDB(t *testing.T, ctx context.Context) string {
	t.Helper()
	base := integrationBaseURL()
	pool, err := pgxpool.New(ctx, swapDB(base, "postgres"))
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		if pool != nil {
			pool.Close()
		}
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			t.Fatalf("BOOM_REQUIRE_DB=1 but Postgres unreachable: %v", err)
		}
		t.Skipf("test Postgres unreachable (%v) — set BOOM_TEST_DATABASE_URL to the LAN-IP test DB", err)
	}
	defer pool.Close()
	_, _ = pool.Exec(ctx,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`,
		integrationDBName)
	if _, err := pool.Exec(ctx, `DROP DATABASE IF EXISTS "`+integrationDBName+`"`); err != nil {
		t.Fatalf("drop %s: %v", integrationDBName, err)
	}
	if _, err := pool.Exec(ctx, `CREATE DATABASE "`+integrationDBName+`"`); err != nil {
		t.Fatalf("create %s: %v", integrationDBName, err)
	}
	return swapDB(base, integrationDBName)
}

// buildStandaloneApp reproduces cmd/catalyst-books/main's HTTP wiring (minus
// e.Start) so the test drives the exact same composition the binary serves.
func buildStandaloneApp(t *testing.T, database *db.DB) *echo.Echo {
	t.Helper()
	cfg := &config.Config{FeatureBooks: true} // BooksEnabled() gates the book routes on
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	e := echo.New()
	e.Use(middleware.Recover())
	e.GET("/healthz", func(c *echo.Context) error {
		status, code := "ok", http.StatusOK
		if err := database.Pool.Ping(c.Request().Context()); err != nil {
			status, code = "degraded", http.StatusServiceUnavailable
		}
		return c.JSON(code, map[string]string{"status": status})
	})
	h := booksapi.New(database, cfg, logger)
	booksapi.Register(e, h)
	if err := booksweb.RegisterSPA(e, logger); err != nil {
		t.Fatalf("RegisterSPA: %v", err)
	}
	return e
}

func TestStandaloneBooksComposition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	targetURL := provisionIntegrationDB(t, ctx)

	// Books-only schema + seed the single owner, exactly as main() does.
	if err := db.MigrateURLFS(ctx, targetURL, booksdb.MigrationsFS); err != nil {
		t.Fatalf("MigrateURLFS: %v", err)
	}
	database, err := db.New(ctx, targetURL)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	defer database.Close()

	const owner = "owner"
	if _, err := database.Pool.Exec(ctx,
		`INSERT INTO users (username) VALUES ($1) ON CONFLICT (username) DO NOTHING`, owner); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	// Pin the single owner so apihelpers.Identify short-circuits — the whole
	// reason an unauthenticated caller resolves to the owner below.
	auth.SetStandaloneOwner(owner)
	defer auth.SetStandaloneOwner("")

	app := buildStandaloneApp(t, database)

	// do issues a request with NO Authorization header (the standalone contract).
	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}

	t.Run("healthz is 200 ok (no auth)", func(t *testing.T) {
		rec := do(http.MethodGet, "/healthz")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /healthz = %d, want 200", rec.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("healthz body not JSON: %v (%q)", err, rec.Body.String())
		}
		if body["status"] != "ok" {
			t.Fatalf("healthz status = %q, want ok", body["status"])
		}
	})

	t.Run("reading-items is 200 for the unauthenticated owner (NOT 401)", func(t *testing.T) {
		// This is the composition proof: no auth header, yet the standalone owner
		// short-circuit + BooksEnabled gating + route ordering combine to return
		// the owner's (empty) library. A 401 here would mean the short-circuit
		// broke; a 404 would mean BooksEnabled was off or the route unshadowed.
		rec := do(http.MethodGet, "/api/v1/books/items")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/books/items = %d, want 200 (owner library, not 401)", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("reading-items Content-Type = %q, want application/json — the SPA catch-all shadowed the API", ct)
		}
		var body struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("reading-items body not JSON: %v (%q)", err, rec.Body.String())
		}
		if len(body.Items) != 0 {
			t.Fatalf("fresh library should be empty, got %d items", len(body.Items))
		}
	})

	t.Run("write→read round-trip proves the owner→users FK holds for the seed", func(t *testing.T) {
		// A write keyed to the SEEDED owner must be accepted by the FK, then read
		// back through the HTTP surface end-to-end. If the FK to the seeded owner
		// were broken (or the owner never seeded), this INSERT would fail.
		const title = "The Standalone Test Book"
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO reading_items (owner, source, external_id, title)
			 VALUES ($1,$2,$3,$4)`, owner, "kindle", "asin-standalone-1", title); err != nil {
			t.Fatalf("insert reading_item for seeded owner failed (FK broken?): %v", err)
		}

		rec := do(http.MethodGet, "/api/v1/books/items?source=kindle")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET reading-items = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), title) {
			t.Fatalf("owner's inserted book %q not returned by the API: %s", title, rec.Body.String())
		}
	})

	t.Run("SPA shell serves at root and on client routes, without shadowing the API", func(t *testing.T) {
		root := do(http.MethodGet, "/")
		if root.Code != http.StatusOK || !strings.Contains(root.Body.String(), `id="root"`) {
			t.Fatalf("GET / = %d, want 200 with SPA shell; body=%q", root.Code, root.Body.String())
		}
		// A client-side route with no file extension falls back to the shell …
		route := do(http.MethodGet, "/app/books")
		if route.Code != http.StatusOK || !strings.Contains(route.Body.String(), `id="root"`) {
			t.Fatalf("GET /app/books = %d, want SPA fallback shell", route.Code)
		}
		// … but /healthz and the API keep their JSON handlers (registered before
		// the "/*" catch-all). Assert the shell did NOT swallow them.
		if hz := do(http.MethodGet, "/healthz"); strings.Contains(hz.Body.String(), `id="root"`) {
			t.Fatal("/healthz served the SPA shell — the catch-all shadowed the explicit route")
		}
	})
}
