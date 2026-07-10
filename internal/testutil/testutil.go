// Package testutil is the external (out-of-package) test harness for gakatime.
// It provisions and migrates the ISOLATED gakatime_test database, builds a real
// *handler.Handler wired to an Echo router, mints auth tokens for seeded users,
// and offers the same seed builders the in-package internal/db tests use — so
// handler-level HTTP integration tests reuse one source of seeding truth.
//
// It imports internal/db (and handler/config/importer); nothing in those
// packages imports testutil, so there is no cycle. In-package `package db` tests
// keep their own co-located harness (internal/db/harness_test.go) because they
// cannot import a package that imports db.
package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/auth"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/config"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/handler"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/importer"
)

const defaultTestDatabaseURL = "postgres://test:test@localhost:5432/gakatime_test?sslmode=disable"

var (
	provisionOnce sync.Once
	provisioned   bool
	provisionErr  error
)

// DatabaseURL resolves the isolated test DB DSN (HAKA_TEST_DATABASE_URL override).
func DatabaseURL() string {
	if v := os.Getenv("HAKA_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultTestDatabaseURL
}

// ensure provisions + migrates the isolated DB exactly once per test binary.
func ensure() error {
	provisionOnce.Do(func() {
		ctx := context.Background()
		if err := ensureDatabase(ctx, DatabaseURL()); err != nil {
			provisionErr = err
			return
		}
		if err := db.MigrateURL(ctx, DatabaseURL()); err != nil {
			provisionErr = err
			return
		}
		provisioned = true
	})
	return provisionErr
}

// OpenDB provisions/migrates then connects to the isolated test DB. It Skips the
// test when Postgres is unreachable, unless HAKA_REQUIRE_DB=1 (then it Fatals).
func OpenDB(t *testing.T) *db.DB {
	t.Helper()
	if err := ensure(); err != nil {
		if os.Getenv("HAKA_REQUIRE_DB") == "1" {
			t.Fatalf("test DB required but unavailable: %v", err)
		}
		t.Skipf("skipping: isolated test DB unavailable: %v", err)
	}
	database, err := db.New(context.Background(), DatabaseURL())
	if err != nil {
		if os.Getenv("HAKA_REQUIRE_DB") == "1" {
			t.Fatalf("connect test DB: %v", err)
		}
		t.Skipf("skipping: connect test DB: %v", err)
	}
	t.Cleanup(database.Close)
	return database
}

// Harness bundles a live Handler + DB for HTTP integration tests.
type Harness struct {
	T   *testing.T
	DB  *db.DB
	H   *handler.Handler
	Cfg *config.Config
}

// NewHarness builds a Handler wired to the isolated DB with a discardable logger
// and an empty importer Hub. Registration is enabled so /auth/register works.
func NewHarness(t *testing.T) *Harness {
	t.Helper()
	database := OpenDB(t)
	cfg := &config.Config{
		Port:               8080,
		EnableRegistration: true,
		SessionExpiry:      24,
		DBPort:             5432,
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	h := handler.New(database, cfg, logger, nil, importer.NewHub())
	return &Harness{T: t, DB: database, H: h, Cfg: cfg}
}

// Router returns a fresh Echo router with the API routes used by the HTTP
// integration tests registered against this harness's Handler. It mirrors the
// production route table (internal/server) for the exercised endpoints; static
// assets are intentionally omitted (no web/dist needed under test).
func (hz *Harness) Router() *echo.Echo {
	e := echo.New()
	h := hz.H
	// auth
	e.POST("/auth/login", h.Login)
	e.POST("/auth/register", h.Register)
	e.POST("/auth/refresh_token", h.RefreshToken)
	// curation
	e.GET("/api/v1/users/current/curation", h.ListCuration)
	e.POST("/api/v1/users/current/curation", h.CreateCuration)
	e.DELETE("/api/v1/users/current/curation/:id", h.DeleteCuration)
	e.GET("/api/v1/users/current/curation/:id/affected", h.CurationAffected)
	// spaces
	e.GET("/api/v1/users/current/spaces", h.ListSpaces)
	e.POST("/api/v1/users/current/spaces", h.CreateSpace)
	e.GET("/api/v1/users/current/spaces/preview", h.SpacePreview)
	e.GET("/api/v1/users/current/spaces/:id", h.GetSpace)
	e.PATCH("/api/v1/users/current/spaces/:id", h.UpdateSpace)
	e.DELETE("/api/v1/users/current/spaces/:id", h.DeleteSpace)
	e.POST("/api/v1/users/current/spaces/:id/rules", h.AddSpaceRule)
	e.DELETE("/api/v1/users/current/spaces/:id/rules/:rid", h.DeleteSpaceRule)
	// stats / aggregations
	e.GET("/api/v1/users/current/stats", h.Stats)
	e.GET("/api/v1/users/current/stats/momentum", h.Momentum)
	e.GET("/api/v1/users/current/files", h.ActiveFiles)
	e.GET("/api/v1/users/current/projects/:project", h.ProjectStats)
	e.GET("/api/v1/projects", h.ProjectList)
	return e
}

// MintUser inserts a users row (unique username from prefix), registers cleanup,
// and mints a never-expiring API token. Returns (username, token).
func (hz *Harness) MintUser(prefix string) (username, token string) {
	hz.T.Helper()
	ctx := context.Background()
	username = prefix + "_" + time.Now().Format("150405.000000000")
	hz.Cleanup(username)
	hash, salt, err := auth.HashPassword("pw-" + username)
	if err != nil {
		hz.T.Fatalf("hash password: %v", err)
	}
	created, err := hz.DB.InsertUser(ctx, db.StoredUser{Username: username, HashedPassword: hash, SaltUsed: salt})
	if err != nil || !created {
		hz.T.Fatalf("insert user %s: created=%v err=%v", username, created, err)
	}
	token = auth.NewRawToken()
	if err := hz.DB.InsertAPIToken(ctx, username, token); err != nil {
		hz.T.Fatalf("insert api token: %v", err)
	}
	return username, token
}

// Cleanup registers deletion of every row a sender owns (children before parents).
func (hz *Harness) Cleanup(sender string) {
	ctx := context.Background()
	hz.T.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM heartbeats WHERE sender=$1`,
			`DELETE FROM curation_rules WHERE sender=$1`,
			`DELETE FROM hb_rollup_daily WHERE sender=$1`,
			`DELETE FROM spaces WHERE owner=$1`,
			`DELETE FROM badges WHERE username=$1`,
			`DELETE FROM projects WHERE owner=$1`,
			`DELETE FROM auth_tokens WHERE owner=$1`,
			`DELETE FROM refresh_tokens WHERE owner=$1`,
			`DELETE FROM users WHERE username=$1`,
		} {
			_, _ = hz.DB.Pool.Exec(ctx, q, sender)
		}
	})
}

// ---- provisioning internals (mirror internal/db/main_test.go) ----

func ensureDatabase(ctx context.Context, targetURL string) error {
	target := dbNameFromURL(targetURL)
	if target == "" {
		return fmt.Errorf("could not determine database name from URL")
	}
	var lastErr error
	for _, maint := range []string{"postgres", "test"} {
		pool, err := pgxpool.New(ctx, maintenanceURLFor(targetURL, maint))
		if err != nil {
			lastErr = err
			continue
		}
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			lastErr = err
			continue
		}
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

func maintenanceURLFor(dsn, maintDB string) string {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return dsn
	}
	rest := dsn[slash+1:]
	q := strings.Index(rest, "?")
	params := ""
	if q >= 0 {
		params = rest[q:]
	}
	return dsn[:slash+1] + maintDB + params
}

func dbNameFromURL(dsn string) string {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return ""
	}
	rest := dsn[slash+1:]
	if q := strings.Index(rest, "?"); q >= 0 {
		rest = rest[:q]
	}
	return rest
}

func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "already exists") || strings.Contains(s, "42P04")
}

func quoteIdent(id string) string {
	return `"` + strings.ReplaceAll(id, `"`, `""`) + `"`
}
