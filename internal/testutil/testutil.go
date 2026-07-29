// Package testutil is the external (out-of-package) test harness for boomtime.
// It provisions and migrates the ISOLATED boomtime_test database, builds a real
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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
)

const defaultTestDatabaseURL = "postgres://test:test@localhost:5432/boomtime_test?sslmode=disable"

var (
	provisionOnce sync.Once
	provisioned   bool
	provisionErr  error
)

// DatabaseURL resolves the isolated test DB DSN (BOOM_TEST_DATABASE_URL override).
func DatabaseURL() string {
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
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
// test when Postgres is unreachable, unless BOOM_REQUIRE_DB=1 (then it Fatals).
//
// Accepts testing.TB so both the stdlib TestXxx flow (*testing.T) and the
// ginkgo mirror suite (GinkgoT(), which implements testing.TB via
// FullGinkgoTInterface) can drive the same harness.
func OpenDB(t HarnessT) *db.DB {
	t.Helper()
	if err := ensure(); err != nil {
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			t.Fatalf("test DB required but unavailable: %v", err)
		}
		t.Skipf("skipping: isolated test DB unavailable: %v", err)
	}
	database, err := db.New(context.Background(), DatabaseURL())
	if err != nil {
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			t.Fatalf("connect test DB: %v", err)
		}
		t.Skipf("skipping: connect test DB: %v", err)
	}
	t.Cleanup(database.Close)
	return database
}

// OpenIsolatedDB provisions/migrates then connects to a DEDICATED database
// named "<testdb>_<suffix>". Tests that mutate global state (the whole-DB
// backup restore TRUNCATEs every table) must use this instead of OpenDB —
// `go test ./...` runs packages in parallel against the shared test DB, so a
// TRUNCATE there would race other packages' seeds.
func OpenIsolatedDB(t HarnessT, suffix string) *db.DB {
	t.Helper()
	url := maintenanceURLFor(DatabaseURL(), dbNameFromURL(DatabaseURL())+"_"+suffix)
	ctx := context.Background()
	skipOrFatal := func(format string, args ...any) {
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			t.Fatalf(format, args...)
		}
		t.Skipf("skipping: "+format, args...)
	}
	if err := ensureDatabase(ctx, url); err != nil {
		skipOrFatal("isolated test DB %s unavailable: %v", dbNameFromURL(url), err)
	}
	if err := db.MigrateURL(ctx, url); err != nil {
		skipOrFatal("migrate isolated test DB %s: %v", dbNameFromURL(url), err)
	}
	database, err := db.New(ctx, url)
	if err != nil {
		skipOrFatal("connect isolated test DB %s: %v", dbNameFromURL(url), err)
	}
	t.Cleanup(database.Close)
	return database
}

// Harness bundles a live Handler + DB for HTTP integration tests.
type Harness struct {
	T   HarnessT
	DB  *db.DB
	H   *handler.Handler
	Cfg *config.Config
}

// HarnessT is the subset of *testing.T that the harness requires. It is
// designed to be also satisfied by ginkgo's GinkgoT() proxy — testing.TB
// itself carries an unexported method that prevents third-party impls, so we
// declare the intersection of methods we actually use.
type HarnessT interface {
	Helper()
	Fatalf(format string, args ...any)
	Skipf(format string, args ...any)
	Logf(format string, args ...any)
	Cleanup(func())
	Errorf(format string, args ...any)
}

// NewHarness builds a Handler wired to the isolated DB with a discardable logger
// and an empty importer Hub. Registration is enabled so /auth/register works.
func NewHarness(t HarnessT) *Harness {
	t.Helper()
	return NewHarnessWithDB(t, OpenDB(t))
}

// NewHarnessWithDB builds a Harness on an explicit database (e.g. an
// OpenIsolatedDB one for destructive whole-DB tests).
func NewHarnessWithDB(t HarnessT, database *db.DB) *Harness {
	t.Helper()
	cfg := &config.Config{
		Port:               8080,
		EnableRegistration: true,
		SessionExpiry:      24,
		DBPort:             5432,
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	// LogHub is optional for handlers under test — nil disables the Logs live
	// stream, which the harness router doesn't register anyway.
	h := handler.New(database, cfg, logger, nil, importer.NewHub(), nil)
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
	// whole-database backup (dump download + destructive restore)
	e.GET("/api/v1/users/current/db/export", h.DBExport)
	e.POST("/api/v1/users/current/db/import", h.DBImport)
	// stats / aggregations
	e.GET("/api/v1/users/current/stats", h.Stats)
	e.GET("/api/v1/users/current/stats/momentum", h.Momentum)
	e.GET("/api/v1/users/current/files", h.ActiveFiles)
	e.GET("/api/v1/users/current/projects/:project", h.ProjectStats)
	e.GET("/api/v1/projects", h.ProjectList)
	// embeddable widgets (auth'd link CRUD + public SVG)
	e.GET("/api/v1/users/current/widgets/link", h.WidgetLink)
	e.GET("/api/v1/users/current/widgets/links", h.WidgetLinkList)
	e.POST("/api/v1/users/current/widgets/link/:id/roll", h.WidgetLinkRoll)
	e.GET("/widget/svg/:uuid/:kind", h.WidgetSvg)
	// gaka-wpb: goals CRUD + toggle + progress (per-goal + batched).
	// /goals/progress registered BEFORE /goals/:id to win path matching
	// (Echo picks the first registered match for overlapping patterns).
	e.GET("/api/v1/users/current/goals", h.ListGoals)
	e.POST("/api/v1/users/current/goals", h.CreateGoal)
	e.GET("/api/v1/users/current/goals/progress", h.GetAllGoalProgress)
	e.GET("/api/v1/users/current/goals/:id", h.GetGoal)
	e.PATCH("/api/v1/users/current/goals/:id", h.UpdateGoal)
	e.DELETE("/api/v1/users/current/goals/:id", h.DeleteGoal)
	e.POST("/api/v1/users/current/goals/:id/toggle", h.ToggleGoal)
	e.GET("/api/v1/users/current/goals/:id/progress", h.GetGoalProgress)
	// gaka-wpb: heartbeat ingest so we can prove the invalidation hook
	// (SaveHeartbeats → InvalidateGoalsForOwner) clears cached
	// progress. Just the bulk endpoint — single- and bulk-shaped
	// requests go through the same storeAndRespond path.
	e.POST("/api/v1/users/current/heartbeats.bulk", h.HeartbeatBulk)
	// gaka-9v4: per-user chibi avatar. Regenerate/status are auth'd
	// self-only, UserAvatar is public — the harness registers all three
	// so a single handler test covers the full surface.
	e.POST("/api/v1/users/current/avatar/regenerate", h.RegenerateAvatar)
	e.GET("/api/v1/users/current/avatar/status", h.GetAvatarStatus)
	e.GET("/api/v1/users/:username/avatar", h.UserAvatar)
	e.POST("/api/v1/admin/avatar/synthesize-prompt", h.SynthesizeAvatarPrompt)
	// gaka-hc6.3 + gaka-hc6.5.1: server-side award evaluation + historical
	// backfill. Public/own variants + the backfill entry point.
	e.GET("/api/v1/users/current/awards", h.OwnAwards)
	e.GET("/api/public/profile/:slug/awards", h.PublicAwards)
	e.POST("/api/v1/users/current/awards/backfill", h.AwardsBackfill)
	// gaka-mwp-streaks: streak walker + ledger inspector — needed for the
	// integration test's ledger-write assertion.
	e.GET("/api/v1/users/current/awards/streaks", h.AwardsStreaks)
	e.GET("/api/v1/users/current/awards/ledger", h.AwardsLedger)
	// Cleanup: also clean up the goals table for the test's sender.
	// The parent Cleanup registered per MintUser catches every table
	// but goals — we extend the cleanup list separately here so
	// existing tests that don't touch goals don't need to change.
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
	created, err := hz.DB.InsertUser(ctx, db.StoredUser{Username: username, HashedPassword: hash, SaltUsed: salt, ArgonVersion: auth.ArgonVersionCurrent})
	if err != nil || !created {
		hz.T.Fatalf("insert user %s: created=%v err=%v", username, created, err)
	}
	token = auth.NewRawToken()
	if err := hz.DB.InsertAPIToken(ctx, username, token, ""); err != nil {
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
			`DELETE FROM widget_links WHERE username=$1`,
			// gaka-wpb: goals cascade on users delete via FK, but we
			// still clean explicitly so the between-run window with
			// FK checks doesn't leak.
			`DELETE FROM goals WHERE owner=$1`,
			// gaka-hc6.3.1: server-side award evaluation writes ledger
			// rows on every /awards read. Clean per-user so parallel
			// tests don't leak into each other's streak counts.
			`DELETE FROM award_ledger WHERE username=$1`,
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
