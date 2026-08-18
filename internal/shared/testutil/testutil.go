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

	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
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
// Accepts HarnessT (Skipf/Fatalf/Cleanup are all on TB) so ginkgo callers
// using GinkgoTB() work uniformly with legacy *testing.T callers.
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

// HarnessT is the subset of *testing.T that the harness requires. It is
// designed to be also satisfied by ginkgo's GinkgoT() proxy — HarnessT
// itself carries an unexported method that prevents third-party impls, so
// we declare the intersection of methods we actually use. Both *testing.T
// and ginkgo.FullGinkgoTInterface satisfy this.
type HarnessT interface {
	Helper()
	Fatalf(format string, args ...any)
	Skipf(format string, args ...any)
	Logf(format string, args ...any)
	Cleanup(func())
	Errorf(format string, args ...any)
}

// Harness bundles a live Handler + DB for HTTP integration tests.
type Harness struct {
	T   HarnessT
	DB  *db.DB
	H   *handler.Handler
	Cfg *config.Config
}

// NewHarness builds a Handler wired to the isolated DB with a discardable logger
// and an empty importer Hub. Registration is enabled so /auth/register works.
//
// Accepts HarnessT so ginkgo callers can pass ginkgo.GinkgoTB() (which
// implements HarnessT) — legacy *testing.T callers keep working since
// *testing.T satisfies HarnessT.
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
	// auth — gaka-8tn phase 4a: receivers moved to h.Identity (internal/identity).
	e.POST("/auth/login", h.Identity.Login)
	e.POST("/auth/register", h.Identity.Register)
	e.POST("/auth/refresh_token", h.Identity.RefreshToken)
	// curation — gaka-8tn phase 5b: receivers moved to h.Curation (internal/curation).
	e.GET("/api/v1/users/current/curation", h.Curation.ListCuration)
	e.POST("/api/v1/users/current/curation", h.Curation.CreateCuration)
	e.DELETE("/api/v1/users/current/curation/:id", h.Curation.DeleteCuration)
	e.GET("/api/v1/users/current/curation/:id/affected", h.Curation.CurationAffected)
	// gaka-d6x.handler: extra curation routes (preview/apply/purge/toggle)
	// so the full curation cluster is testable via testutil.Router without
	// per-file re-wiring (which would duplicate + panic).
	e.GET("/api/v1/users/current/curation/:id/preview", h.Curation.ApplyRenamePreview)
	e.POST("/api/v1/users/current/curation/:id/apply", h.Curation.ApplyRename)
	e.POST("/api/v1/users/current/curation/:id/purge", h.Curation.PurgeHidden)
	e.POST("/api/v1/users/current/curation/:id/toggle", h.Curation.ToggleCuration)
	// labels catalog (public GET + admin CRUD) — gaka-8tn phase 5b: h.Curation.
	e.GET("/api/v1/labels/catalog", h.Curation.LabelsCatalog)
	e.POST("/api/v1/admin/labels", h.Curation.AdminCreateLabel)
	e.PATCH("/api/v1/admin/labels/:id", h.Curation.AdminUpdateLabel)
	e.DELETE("/api/v1/admin/labels/:id", h.Curation.AdminDeleteLabel)
	e.PATCH("/api/v1/admin/label-gen-config", h.Curation.AdminUpdateLabelGenConfig)
	e.GET("/api/v1/admin/labels/seed.sql", h.Curation.AdminLabelsSeedSQL)
	// spaces — gaka-8tn phase 2a: receivers moved to h.Spaces (internal/spaces).
	e.GET("/api/v1/users/current/spaces", h.Spaces.ListSpaces)
	e.POST("/api/v1/users/current/spaces", h.Spaces.CreateSpace)
	e.GET("/api/v1/users/current/spaces/preview", h.Spaces.SpacePreview)
	e.GET("/api/v1/users/current/spaces/:id", h.Spaces.GetSpace)
	e.PATCH("/api/v1/users/current/spaces/:id", h.Spaces.UpdateSpace)
	e.DELETE("/api/v1/users/current/spaces/:id", h.Spaces.DeleteSpace)
	e.POST("/api/v1/users/current/spaces/:id/rules", h.Spaces.AddSpaceRule)
	e.DELETE("/api/v1/users/current/spaces/:id/rules/:rid", h.Spaces.DeleteSpaceRule)
	// whole-database backup (dump download + destructive restore).
	// gaka-8tn phase 7: receivers moved to h.Admin (internal/admin).
	e.GET("/api/v1/users/current/db/export", h.Admin.DBExport)
	e.POST("/api/v1/users/current/db/import", h.Admin.DBImport)
	// stats / aggregations — gaka-8tn phase 6: receivers moved to h.Stats
	// (internal/stats).
	e.GET("/api/v1/users/current/stats", h.Stats.Stats)
	e.GET("/api/v1/users/current/stats/momentum", h.Stats.Momentum)
	e.GET("/api/v1/users/current/files", h.Stats.ActiveFiles)
	e.GET("/api/v1/users/current/projects/:project", h.Stats.ProjectStats)
	e.GET("/api/v1/projects", h.Stats.ProjectList)
	// embeddable widgets (auth'd link CRUD + public SVG) — gaka-8tn phase 3:
	// domain lives at internal/widgets; test harness re-points to h.Widgets.X.
	e.GET("/api/v1/users/current/widgets/link", h.Widgets.WidgetLink)
	e.GET("/api/v1/users/current/widgets/links", h.Widgets.WidgetLinkList)
	e.POST("/api/v1/users/current/widgets/link/:id/roll", h.Widgets.WidgetLinkRoll)
	e.GET("/widget/svg/:uuid/:kind", h.Widgets.WidgetSvg)
	// gaka-wpb: goals CRUD + toggle + progress (per-goal + batched).
	// /goals/progress registered BEFORE /goals/:id to win path matching
	// (Echo picks the first registered match for overlapping patterns).
	// gaka-8tn phase 2b: repointed to the goals-domain handler bag.
	e.GET("/api/v1/users/current/goals", h.Goals.ListGoals)
	e.POST("/api/v1/users/current/goals", h.Goals.CreateGoal)
	e.GET("/api/v1/users/current/goals/progress", h.Goals.GetAllGoalProgress)
	e.GET("/api/v1/users/current/goals/:id", h.Goals.GetGoal)
	e.PATCH("/api/v1/users/current/goals/:id", h.Goals.UpdateGoal)
	e.DELETE("/api/v1/users/current/goals/:id", h.Goals.DeleteGoal)
	e.POST("/api/v1/users/current/goals/:id/toggle", h.Goals.ToggleGoal)
	e.GET("/api/v1/users/current/goals/:id/progress", h.Goals.GetGoalProgress)
	// gaka-wpb: heartbeat ingest so we can prove the invalidation hook
	// (SaveHeartbeats → InvalidateGoalsForOwner) clears cached
	// progress. Just the bulk endpoint — single- and bulk-shaped
	// requests go through the same storeAndRespond path.
	e.POST("/api/v1/users/current/heartbeats.bulk", h.Ingest.HeartbeatBulk)
	// gaka-d6x.handler: full ingest cluster (heartbeat single, workouts,
	// health samples, explore reads). Wired so the ingest cluster tests
	// exercise the real HTTP paths without re-registering routes.
	e.POST("/api/v1/users/current/heartbeats", h.Ingest.Heartbeat)
	e.GET("/api/v1/users/current/heartbeats", h.Ingest.HeartbeatsList)
	e.GET("/api/v1/users/current/heartbeats/latest", h.Ingest.HeartbeatsLatest)
	e.GET("/api/v1/users/current/heartbeats/group", h.Ingest.HeartbeatsGroup)
	e.POST("/api/v1/users/current/workouts", h.Ingest.Workouts)
	e.POST("/api/v1/users/current/workouts.bulk", h.Ingest.WorkoutsBulk)
	e.POST("/api/v1/users/current/health_samples", h.Ingest.HealthSamples)
	e.POST("/api/v1/users/current/health_samples.bulk", h.Ingest.HealthSamplesBulk)
	// gaka-9v4: per-user chibi avatar. Regenerate/status are auth'd
	// self-only, UserAvatar is public — the harness registers all three
	// so a single handler test covers the full surface.
	// gaka-8tn phase 4a: receivers moved to h.Identity (internal/identity).
	e.POST("/api/v1/users/current/avatar/regenerate", h.Identity.RegenerateAvatar)
	e.GET("/api/v1/users/current/avatar/status", h.Identity.GetAvatarStatus)
	e.GET("/api/v1/users/:username/avatar", h.Identity.UserAvatar)
	e.POST("/api/v1/admin/avatar/synthesize-prompt", h.Identity.SynthesizeAvatarPrompt)
	// gaka-hc6.3 + gaka-hc6.5.1: server-side award evaluation + historical
	// backfill. Public/own variants + the backfill entry point.
	// gaka-8tn phase 4b: receivers moved to h.Awards.* (awards extracted).
	e.GET("/api/v1/users/current/awards", h.Awards.OwnAwards)
	e.GET("/api/public/profile/:slug/awards", h.Awards.PublicAwards)
	e.POST("/api/v1/users/current/awards/backfill", h.Awards.AwardsBackfill)
	// gaka-mwp-streaks: streak walker + ledger inspector — needed for the
	// integration test's ledger-write assertion.
	e.GET("/api/v1/users/current/awards/streaks", h.Awards.AwardsStreaks)
	e.GET("/api/v1/users/current/awards/ledger", h.Awards.AwardsLedger)
	// gaka-0vp.18 (DRY audit): folded 8 per-file routerWithXxx helpers into
	// the central Router() below. The per-file builders were 5-11 LOC each,
	// existed in stdlib + ginkgo pairs (byte-identical), and were the
	// biggest single source of test-code duplication before this fold.
	// Every one is now a single route line here.
	e.POST("/api/v1/users/current/password", h.Identity.ChangePassword) // gaka-8tn phase 4a: h.Identity
	e.GET("/api/v1/labels/:id/image", h.Admin.LabelImage)               // gaka-8tn phase 7: h.Admin
	// gaka-8tn phase 3: widget-def CRUD extracted to internal/widgets.
	e.GET("/api/v1/users/current/widget-defs", h.Widgets.ListWidgetDefs)
	e.POST("/api/v1/users/current/widget-defs", h.Widgets.CreateWidgetDef)
	e.PATCH("/api/v1/users/current/widget-defs/:name", h.Widgets.UpdateWidgetDef)
	e.DELETE("/api/v1/users/current/widget-defs/:name", h.Widgets.DeleteWidgetDef)
	e.GET("/widget/svg/:uuid/named", h.Widgets.WidgetDefSvg)
	e.GET("/api/v1/logs", h.Meta.ServerLogs)                                   // gaka-8tn phase 1: meta domain
	e.GET("/api/v1/users/current/timezone", h.Identity.GetTimezone)            // gaka-8tn phase 4a: h.Identity
	e.PATCH("/api/v1/users/current/timezone", h.Identity.UpdateTimezone)       // gaka-8tn phase 4a: h.Identity
	e.GET("/api/v1/users/current/profile", h.Identity.GetPublicProfile)        // gaka-8tn phase 4a: h.Identity
	e.PUT("/api/v1/users/current/profile", h.Identity.PutPublicProfile)        // gaka-8tn phase 4a: h.Identity
	e.GET("/api/public/profile/:slug", h.Identity.PublicProfile)               // gaka-8tn phase 4a: h.Identity
	e.GET("/api/public/profile/:slug/og.png", h.Identity.PublicProfileOGImage) // gaka social-card: OG image
	// gaka-anh Phase 2: GitHub stats endpoints (authed cache-or-sync + public
	// cache-only). Registered unconditionally in the test router so the suites
	// can drive them; production gates them behind Cfg.GithubConnectEnabled().
	// (The /github connection + disconnect routes are registered by the
	// github_oauth_test suite itself, so they are NOT registered here to avoid
	// Echo's duplicate-route panic.)
	e.GET("/api/v1/users/current/github/stats", h.Identity.GetGithubStats)
	e.GET("/api/public/profile/:slug/github/stats", h.Identity.PublicGithubStats)
	e.GET("/api/v1/users/current/dashboard/:scope", h.Spaces.GetDashboardLayout)       // gaka-8tn phase 2a: moved to h.Spaces
	e.PUT("/api/v1/users/current/dashboard/:scope", h.Spaces.PutDashboardLayout)       // gaka-8tn phase 2a
	e.DELETE("/api/v1/users/current/dashboard/:scope", h.Spaces.DeleteDashboardLayout) // gaka-8tn phase 2a
	e.POST("/api/v1/users/current/wakatime_key", h.Identity.SaveWakatimeKey)           // gaka-8tn phase 4a: h.Identity
	// gaka-d6x.handler: admin cluster (label-images regeneration). Wired here so
	// per-file test suites don't have to re-register (Echo panics on duplicate
	// route registration).
	// gaka-8tn phase 7: receivers moved to h.Admin (internal/admin).
	e.GET("/api/v1/admin/label-images", h.Admin.AdminLabelImagesInfo)
	e.POST("/api/v1/admin/label-images/regenerate", h.Admin.AdminLabelImagesRegenerate)
	e.GET("/api/v1/admin/label-images/ws", h.Admin.AdminLabelImagesWS)
	// gaka-d6x.handler misc cluster: routes previously only in the production
	// router (internal/server), now mirrored here so tests don't hand-register
	// and hit the duplicate-route panic.
	// gaka-8tn phase 1: meta domain endpoints. Router still mirrors the
	// production route table but the receivers are now on h.Meta.
	e.GET("/healthz", h.Meta.Healthz)
	e.GET("/api/v1/version", h.Meta.Version)
	e.GET("/api/v1/changelog", h.Meta.Changelog)
	e.GET("/api/v1/users/current/heartbeats/entities", h.Ingest.ListEntitiesByType)
	e.POST("/api/v1/users/current/heartbeats/entities/redact", h.Ingest.RedactEntities)
	// gaka-8tn phase 7: receivers moved to h.Admin (internal/admin).
	e.GET("/api/v1/users/current/sources/health", h.Admin.SourceHealth)
	// gaka-8tn phase 6: receivers moved to h.Stats (internal/stats).
	e.GET("/api/v1/users/current/timeline", h.Stats.Timeline)
	e.GET("/api/v1/users/current/statusbar/today", h.Stats.StatusbarToday)
	e.GET("/api/v1/users/current/derived/status", h.Stats.DerivedStatus)
	e.POST("/api/v1/users/current/derived/resync", h.Stats.DerivedResync)
	// gaka-8tn phase 3: badges extracted to internal/widgets.
	e.GET("/badge/link/:project", h.Widgets.BadgeLink)
	e.GET("/badge/svg/:svg", h.Widgets.BadgeSvg)
	// gaka-8tn phase 6: leaderboards + commits moved to h.Stats.
	e.GET("/api/v1/leaderboards", h.Stats.Leaderboards)
	e.GET("/api/v1/commits/:project/report", h.Stats.Commits)
	// gaka-174.q: cross-domain query DSL endpoint. Mirrors the production
	// registration (queryapi.Register) so the HTTP suites can drive it.
	e.POST("/api/v1/query", h.Query.RunQuery)
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
			// gaka-d6x.handler: health_samples + health_rollup_daily are
			// owner-scoped and are populated by the ingest cluster tests.
			// workout_details cascades on heartbeats delete (FK CASCADE)
			// so it doesn't need an explicit row here.
			`DELETE FROM health_samples WHERE owner=$1`,
			`DELETE FROM health_rollup_daily WHERE owner=$1`,
			`DELETE FROM heartbeats WHERE sender=$1`,
			`DELETE FROM curation_rules WHERE sender=$1`,
			`DELETE FROM hb_rollup_daily WHERE sender=$1`,
			`DELETE FROM spaces WHERE owner=$1`,
			`DELETE FROM badges WHERE username=$1`,
			`DELETE FROM widget_links WHERE username=$1`,
			// gaka-anh Phase 2: per-user GitHub stats cache.
			`DELETE FROM github_stats_cache WHERE username=$1`,
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
