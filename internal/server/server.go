// Package server wires the Echo router, registers all routes in hakatime's order
// (Api.hs), and serves the embedded SPA as a fallback for non-API routes.
package server

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/awards"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/goals"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logging"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/meta"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/spaces"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/widgets"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

//go:embed dist
var distFS embed.FS

// New builds a configured Echo server. logHub streams server-process slog
// records to the Logs tab; pass nil to disable that endpoint's live stream.
//
// (See NewWithHandler if the caller needs to attach additional dependencies
// to the constructed *handler.Handler — this shape is preserved for
// backward compatibility with existing tests that only care about the
// Echo instance.)
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, worker *importer.Worker, hub *importer.Hub, logHub *logging.LogHub) *echo.Echo {
	e, _ := NewWithHandler(database, cfg, logger, worker, hub, logHub)
	return e
}

// NewWithHandler is New but also returns the constructed *handler.Handler
// so callers (cmd/boomtime) can wire post-construction dependencies like
// the label-images worker.
func NewWithHandler(database *db.DB, cfg *config.Config, logger *slog.Logger, worker *importer.Worker, hub *importer.Hub, logHub *logging.LogHub) (*echo.Echo, *handler.Handler) {
	e := echo.New()

	e.Use(middleware.Recover())
	// gaka-n5r: CORS is credentialed (AllowCredentials=true is required so the
	// refresh_token cookie flows behind the Vite proxy), which means the
	// Access-Control-Allow-Origin value MUST be a checked allowlist entry — the
	// previous reflect-any-origin behaviour let attacker pages read the login
	// response body (and its fresh access token). Origins come from
	// BOOM_CORS_ALLOWED_ORIGINS; if unset in dev we fall back to localhost:5173
	// + localhost:8080; if unset in prod we already refused to start in
	// cmd/boomtime, so allowedOrigins here is guaranteed non-empty in that case.
	allowedOrigins := parseAllowedOrigins(os.Getenv("BOOM_CORS_ALLOWED_ORIGINS"), logger)
	if len(allowedOrigins) == 0 {
		allowedOrigins = defaultDevAllowedOrigins
		logger.Warn("BOOM_CORS_ALLOWED_ORIGINS not set — falling back to localhost dev origins",
			"origins", allowedOrigins,
			"remediation", "set BOOM_CORS_ALLOWED_ORIGINS=https://your.domain in prod")
	} else {
		logger.Info("CORS allowlist configured", "origins", allowedOrigins)
	}
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		// Exact-match allowlist (see internal/server/cors.go). We stay on
		// UnsafeAllowOriginFunc rather than AllowOrigins because echo's default
		// matcher uses strings.EqualFold, and we want case-sensitive scheme
		// checks (an attacker who registers HTTP://LOCALHOST:5173 shouldn't
		// squeak through a case-fold match).
		UnsafeAllowOriginFunc: func(_ *echo.Context, origin string) (string, bool, error) {
			if isOriginAllowed(origin, allowedOrigins) {
				return origin, true, nil
			}
			return "", false, nil
		},
		AllowHeaders:     []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAuthorization, "X-Machine-Name"},
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowCredentials: true,
	}))
	if cfg.HTTPLog {
		e.Use(requestLogger(logger))
	}
	if cfg.DBN1Threshold > 0 || cfg.DBN1DupThresh > 0 {
		e.Use(n1Middleware(logger, cfg.DBN1Threshold, cfg.DBN1DupThresh))
	}
	// Universal rate limit (gaka-jk6 / gaka-ddp / gaka-awh.1). Installed
	// AFTER CORS (so preflight can short-circuit inside the middleware
	// without ever counting against a bucket) and BEFORE the handler
	// registration (so it wraps every route, including auth writes and
	// wakatime_key probe endpoints). See internal/server/ratelimit.go for
	// bucket sizing, testing hook (BOOM_DISABLE_RATE_LIMIT=1), and TTL /
	// cleanup notes.
	installRateLimit(e, logger, database)
	// gaka-ar7: stash resolved owner in ctx so the pgx tracer can tag its DEBUG
	// SQL records with "user" — LogHub's FilterForUser then gates them per tenant.
	e.Use(userCtxMiddleware(database))

	h := handler.New(database, cfg, logger, worker, hub, logHub)
	registerRoutes(e, h)
	registerStatic(e, cfg, logger)
	return e, h
}

// registerRoutes wires all API routes, one registration func per domain. The
// call order (and the order within each func) preserves the original flat
// registration sequence.
func registerRoutes(e *echo.Echo, h *handler.Handler) {
	registerHeartbeatRoutes(e, h)
	registerCurationRoutes(e, h)
	registerStatsRoutes(e, h)
	registerMiscRoutes(e, h)
	registerImportRoutes(e, h)
	// gaka-8tn phase 1: meta + logs registration is now owned by the meta
	// domain package. `meta.Register` fans out /api/v1/version,
	// /api/v1/changelog, /healthz, the OpenAPI spec + Swagger UI, and the
	// /api/v1/logs REST + WS endpoints. Order preserved: same effective
	// route set as pre-refactor registerLogRoutes + registerMetaRoutes.
	meta.Register(e, h.Meta)
	registerGoalRoutes(e, h)
	// gaka-8tn phase 2a: spaces + dashboard-layout registration is now
	// owned by the spaces domain package. `spaces.Register` fans out the
	// eight /spaces/... routes (formerly registerSpaceRoutes) plus the
	// three /dashboard/:scope routes (formerly buried in registerAuthRoutes).
	// Order preserved: /spaces/preview still registers BEFORE /spaces/:id
	// so the static route wins path matching against Echo's param matcher.
	spaces.Register(e, h.Spaces)
	// gaka-8tn phase 4a: identity (auth + password + profile + timezone +
	// wakatime_key + avatar) extracted into internal/identity.
	identity.Register(e, h.Identity)
	// gaka-8tn phase 4b: awards cluster (streak ledger + evaluator +
	// backfill — 7 routes) extracted into internal/awards. Registered
	// AFTER identity so /awards/* auth checks resolve against the
	// identity-owned session middleware in the same order as pre-refactor.
	awards.Register(e, h.Awards)
}

// registerGoalRoutes: user-defined composite goals (gaka-wpb). CRUD +
// toggle + per-goal progress + batched progress (one round trip for
// every enabled goal, used by dashboards). Owner-scoped; cross-owner
// id access returns 404, never 403 (no oracle). The /goals/progress
// batched endpoint is registered BEFORE /goals/:id so it isn't
// shadowed by the param route (same pattern as spaces/preview).
//
// gaka-8tn phase 2b: routes now delegate to the goals-domain handler
// (h.Goals) — see internal/goals/handler.go.
func registerGoalRoutes(e *echo.Echo, h *handler.Handler) {
	goals.Register(e, h.Goals)
}

// registerHeartbeatRoutes: ingest, the read-only explorer, and source health.
func registerHeartbeatRoutes(e *echo.Echo, h *handler.Handler) {
	// Heartbeats (ingest)
	e.POST("/api/v1/users/current/heartbeats", h.Heartbeat)
	e.POST("/api/v1/users/current/heartbeats.bulk", h.HeartbeatBulk)

	// HealthKit / Apple Watch ingest (extensions/boomtime-watch/).
	// Workouts flow through the heartbeats table (ty='workout') so existing
	// time-spent aggregations pick them up; raw samples land in health_samples.
	e.POST("/api/v1/users/current/workouts", h.Workouts)
	e.POST("/api/v1/users/current/workouts.bulk", h.WorkoutsBulk)
	e.POST("/api/v1/users/current/health_samples", h.HealthSamples)
	e.POST("/api/v1/users/current/health_samples.bulk", h.HealthSamplesBulk)

	// Heartbeats Explorer (read-only audit views)
	e.GET("/api/v1/users/current/heartbeats/group", h.HeartbeatsGroup)
	e.GET("/api/v1/users/current/heartbeats/latest", h.HeartbeatsLatest)
	e.GET("/api/v1/users/current/heartbeats", h.HeartbeatsList)

	// Entity Explorer (gaka-90x): per-ty flat list + per-entity redact (blanks
	// the entity column on matching heartbeat rows — row itself stays,
	// contributing to project/language/machine totals). Redact requires
	// ?confirm=redact-entities as an accident guard.
	e.GET("/api/v1/users/current/heartbeats/entities", h.ListEntitiesByType)
	e.POST("/api/v1/users/current/heartbeats/entities/redact", h.RedactEntities)

	// Source health (per plugin/editor/machine last check-in — ingestion health)
	e.GET("/api/v1/users/current/sources/health", h.SourceHealth)
}

// registerCurationRoutes: data curation (hide / rename labels).
//
// The /curation/:id/{preview,apply,purge} triplet operates on the same
// curation_rules table as the CRUD endpoints, but for the DESTRUCTIVE
// rewrite/delete paths:
//   - /preview: dispatches on rule.action; renames get the apply-preview
//     shape (UPDATE + rule-delete SQL), hides get the purge-preview shape
//     (DELETE heartbeats + rule-delete SQL). One preview, two payloads.
//   - /apply: rename rules only. UPDATE heartbeats + DELETE rule (one tx).
//   - /purge: hide rules only. DELETE heartbeats + DELETE rule (one tx).
// Cross-action requests return 400 (apply-on-hide, purge-on-rename). See
// internal/handler/curation.go for the SQL contract + regression tests
// that guard preview===run string identity for both destructive paths.
func registerCurationRoutes(e *echo.Echo, h *handler.Handler) {
	e.GET("/api/v1/users/current/curation", h.ListCuration)
	e.POST("/api/v1/users/current/curation", h.CreateCuration)
	e.DELETE("/api/v1/users/current/curation/:id", h.DeleteCuration)
	e.GET("/api/v1/users/current/curation/:id/affected", h.CurationAffected)
	e.GET("/api/v1/users/current/curation/:id/preview", h.ApplyRenamePreview)
	e.POST("/api/v1/users/current/curation/:id/apply", h.ApplyRename)
	e.POST("/api/v1/users/current/curation/:id/purge", h.PurgeHidden)
	// gaka-dfd: pause/resume a rule without deleting it. Body optional —
	// empty POST flips, {"enabled":true|false} sets an exact value.
	e.POST("/api/v1/users/current/curation/:id/toggle", h.ToggleCuration)
}

// registerStatsRoutes: derived-data health plus every dashboard aggregation
// (stats, timeline, big bets, active files, projects).
func registerStatsRoutes(e *echo.Echo, h *handler.Handler) {
	// Derived-data health (gap_seconds + rollup status / resync)
	e.GET("/api/v1/users/current/derived/status", h.DerivedStatus)
	e.POST("/api/v1/users/current/derived/resync", h.DerivedResync)

	// Whole-database backup: streaming dump download + destructive restore
	// (requires ?confirm=replace-all-data; see handler/backup.go).
	e.GET("/api/v1/users/current/db/export", h.DBExport)
	e.POST("/api/v1/users/current/db/import", h.DBImport)

	// Stats
	e.GET("/api/v1/users/current/stats", h.Stats)
	e.GET("/api/v1/users/current/timeline", h.Timeline)
	e.GET("/api/v1/users/current/statusbar/today", h.StatusbarToday)

	// Stats — big-bet aggregations (council visualizations)
	e.GET("/api/v1/users/current/stats/punchcard", h.Punchcard)
	e.GET("/api/v1/users/current/stats/sessions", h.Sessions)
	e.GET("/api/v1/users/current/stats/momentum", h.Momentum)

	// gaka-1l9: wakatime.com AI-assistance metrics (heartbeats.ai_*).
	e.GET("/api/v1/users/current/stats/ai", h.AIActivity)

	// HealthKit metrics feed (Wellness card + Wellness page).
	e.GET("/api/v1/users/current/stats/health", h.HealthActivity)

	// Per-workout event list + per-label breakdown (Wellness events breakdown).
	e.GET("/api/v1/users/current/workouts", h.WorkoutList)

	// Cross-project active files (shared lynchpins spanning multiple projects)
	e.GET("/api/v1/users/current/files", h.ActiveFiles)

	// Projects
	e.GET("/api/v1/users/current/projects/:project", h.ProjectStats)
	e.GET("/api/v1/projects", h.ProjectList)
}

// registerMiscRoutes: badges, widgets, leaderboards, and commits.
func registerMiscRoutes(e *echo.Echo, h *handler.Handler) {
	// gaka-8tn phase 3: Badges + embeddable widgets + widget-def CRUD extracted
	// into internal/widgets; the route strings + registration order are
	// preserved verbatim inside widgets.Register.
	widgets.Register(e, h.Widgets)

	// gaka-8tn phase 4a: PublicProfile moved to identity.Register (see
	// registerRoutes' identity fan-out). Route strings preserved verbatim.

	// gaka-myv: shared per-archetype label image bytes. PUBLIC (no auth) —
	// label content is fixed catalog data, not per-user data. Cache-Control
	// is `immutable`; the FE busts via ?v=<generated_at.epoch>. Reads do
	// NOT check the feature flag so already-generated images keep serving
	// after a flag flip (only writes / the startup worker gate on
	// LabelImagesEnabled).
	e.GET("/api/v1/labels/:id/image", h.LabelImage)

	// gaka-myv: Admin tab endpoints — authed AND admin-gated (see
	// BOOM_ADMIN_USERS). Info returns config + row count; Regenerate takes
	// the caller-supplied {entries: [{id, prompt}, ...]} snapshot so the FE
	// catalog stays the source of truth.
	e.GET("/api/v1/admin/label-images", h.AdminLabelImagesInfo)
	e.POST("/api/v1/admin/label-images/regenerate", h.AdminLabelImagesRegenerate)
	// gaka-8bz: durable WS stream of the image-job queue lifecycle.
	// Auth uses the refresh_token cookie inside the handler (see
	// AdminLabelImagesWS) — WS handshakes can't carry Authorization.
	e.GET("/api/v1/admin/label-images/ws", h.AdminLabelImagesWS)

	// gaka-vh8: git-history backfill admin endpoints. Config
	// GET/PATCH, per-user stats, in-memory job registry (enqueue +
	// PATCH + per-session heartbeat push / preview + delete), and a
	// durable WS stream mirroring the label-images shape. All
	// admin-gated (see requireAdmin) — the WS uses the refresh_token
	// cookie because WS handshakes can't carry Authorization.
	e.GET("/api/v1/admin/backfill/config", h.AdminBackfillConfig)
	e.PATCH("/api/v1/admin/backfill/config", h.AdminBackfillConfigUpdate)
	e.GET("/api/v1/admin/backfill/stats", h.AdminBackfillStats)
	e.POST("/api/v1/admin/backfill/jobs", h.AdminBackfillEnqueueJob)
	e.PATCH("/api/v1/admin/backfill/jobs/:id", h.AdminBackfillJobPatch)
	e.POST("/api/v1/admin/backfill/jobs/:id/heartbeats", h.AdminBackfillJobHeartbeats)
	e.POST("/api/v1/admin/backfill/jobs/:id/preview", h.AdminBackfillJobPreview)
	e.DELETE("/api/v1/admin/backfill/heartbeats", h.AdminBackfillDeleteHeartbeats)
	e.GET("/api/v1/admin/backfill/ws", h.AdminBackfillWS)

	// gaka-8tn phase 4a: CHIBI avatar endpoints (synthesize-prompt SSE,
	// regenerate, status, public GET) moved to identity.Register. Route
	// strings preserved verbatim.

	// gaka-364.3: DB-backed labels catalog. Public GET returns the whole
	// catalog for the FE evaluator + admin table; admin CRUD lets a
	// whitelisted operator edit labels + the global gen-config live.
	e.GET("/api/v1/labels/catalog", h.LabelsCatalog)
	e.POST("/api/v1/admin/labels", h.AdminCreateLabel)
	e.PATCH("/api/v1/admin/labels/:id", h.AdminUpdateLabel)
	e.DELETE("/api/v1/admin/labels/:id", h.AdminDeleteLabel)
	e.PATCH("/api/v1/admin/label-gen-config", h.AdminUpdateLabelGenConfig)
	e.GET("/api/v1/admin/labels/seed.sql", h.AdminLabelsSeedSQL)

	// gaka-8tn phase 3: widget-def CRUD lives in internal/widgets and is
	// registered by widgets.Register at the top of this func.

	// Leaderboards
	e.GET("/api/v1/leaderboards", h.Leaderboards)

	// Commits
	e.GET("/api/v1/commits/:project/report", h.Commits)
}

// registerImportRoutes: durable, resumable import jobs.
func registerImportRoutes(e *echo.Echo, h *handler.Handler) {
	e.POST("/import", h.ImportRequest)
	e.GET("/import/config", h.ImportConfig)
	e.POST("/import/wakatime-range", h.WakatimeRange)
	e.GET("/import/jobs", h.ImportJobs)
	e.GET("/import/jobs/:id", h.ImportJob)
	e.POST("/import/jobs/:id/cancel", h.ImportJobCancel)
	e.GET("/import/jobs/:id/logs", h.ImportJobLogs)
	e.GET("/import/jobs/:id/ws", h.ImportJobWS)
}

// registerStatic serves the SPA: from BOOM_DASHBOARD_PATH on disk if set, else
// from the embedded dist FS. Non-API routes fall back to index.html.
func registerStatic(e *echo.Echo, cfg *config.Config, logger *slog.Logger) {
	var fsys fs.FS
	if cfg.DashboardPath != "" {
		logger.Info("serving dashboard from disk", "path", cfg.DashboardPath)
		fsys = os.DirFS(cfg.DashboardPath)
	} else {
		sub, err := fs.Sub(distFS, "dist")
		if err != nil {
			logger.Error("failed to open embedded dist", "err", err)
			return
		}
		fsys = sub
	}

	fileServer := http.FileServer(http.FS(fsys))
	e.GET("/*", func(c *echo.Context) error {
		reqPath := strings.TrimPrefix(c.Request().URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}
		servingShell := false
		if _, err := fs.Stat(fsys, reqPath); err != nil {
			// Missing file. Return 404 for anything that looks like an asset
			// (i.e. the last path segment has a file extension). Reason: a
			// stale-cached client requesting an old chunk hash like
			// /assets/Settings-OLDHASH.js would otherwise get index.html
			// served with 200 OK, then try to parse HTML as JavaScript and
			// silently fail — breaking the whole lazy-loaded route with no
			// user-visible error. Real routes (no extension in the last
			// segment) still fall back to index.html so client-side
			// routing keeps working.
			if strings.Contains(path.Base(reqPath), ".") {
				return echo.NewHTTPError(http.StatusNotFound, "not found")
			}
			c.Request().URL.Path = "/"
			servingShell = true
		} else if reqPath == "index.html" {
			servingShell = true
		}
		if servingShell {
			// The SPA shell embeds hashed chunk names via dynamic imports;
			// every deploy the shell must revalidate or clients ride the
			// stale hashes and lazy-loaded routes 404. Asset files keep
			// the default (immutable-ish via hashed filenames) — only the
			// shell revalidates. no-cache means "revalidate every load",
			// which is basically free because the shell is ~3 KB.
			c.Response().Header().Set("Cache-Control", "no-cache, must-revalidate")
		}
		fileServer.ServeHTTP(c.Response(), c.Request())
		return nil
	})
}
