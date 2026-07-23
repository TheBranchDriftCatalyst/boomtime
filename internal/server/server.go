// Package server wires the Echo router, registers all routes in hakatime's order
// (Api.hs), and serves the embedded SPA as a fallback for non-API routes.
package server

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logging"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/openapi"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

//go:embed dist
var distFS embed.FS

// New builds a configured Echo server. logHub streams server-process slog
// records to the Logs tab; pass nil to disable that endpoint's live stream.
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, worker *importer.Worker, hub *importer.Hub, logHub *logging.LogHub) *echo.Echo {
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
	return e
}

// registerRoutes wires all API routes, one registration func per domain. The
// call order (and the order within each func) preserves the original flat
// registration sequence.
func registerRoutes(e *echo.Echo, h *handler.Handler) {
	registerHeartbeatRoutes(e, h)
	registerCurationRoutes(e, h)
	registerSpaceRoutes(e, h)
	registerStatsRoutes(e, h)
	registerAuthRoutes(e, h)
	registerMiscRoutes(e, h)
	registerImportRoutes(e, h)
	registerLogRoutes(e, h)
	registerMetaRoutes(e, h)
}

// registerMetaRoutes: build/version disclosure + embedded changelog + the
// public health probe + self-hosted OpenAPI spec and Swagger UI (gaka-lfc).
// All are intentionally unauthenticated (see internal/handler/meta.go,
// internal/handler/healthz.go, internal/openapi).
func registerMetaRoutes(e *echo.Echo, h *handler.Handler) {
	e.GET("/api/v1/version", h.Version)
	e.GET("/api/v1/changelog", h.Changelog)
	e.GET("/healthz", h.Healthz)
	// OpenAPI 3 spec + embedded Swagger UI. Public: the spec + docs are
	// self-hosted transparency, not user data. Auth'd endpoints inside the
	// spec still require the Authorize dialog to be filled in for Try-it-out.
	openapi.Register(e)
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
func registerCurationRoutes(e *echo.Echo, h *handler.Handler) {
	e.GET("/api/v1/users/current/curation", h.ListCuration)
	e.POST("/api/v1/users/current/curation", h.CreateCuration)
	e.DELETE("/api/v1/users/current/curation/:id", h.DeleteCuration)
	e.GET("/api/v1/users/current/curation/:id/affected", h.CurationAffected)
}

// registerSpaceRoutes: spaces (named, scoped dashboards). The static
// `/spaces/preview` route is registered before `/spaces/:id` so it is not
// shadowed by the param route.
func registerSpaceRoutes(e *echo.Echo, h *handler.Handler) {
	e.GET("/api/v1/users/current/spaces", h.ListSpaces)
	e.POST("/api/v1/users/current/spaces", h.CreateSpace)
	e.GET("/api/v1/users/current/spaces/preview", h.SpacePreview)
	e.GET("/api/v1/users/current/spaces/:id", h.GetSpace)
	e.PATCH("/api/v1/users/current/spaces/:id", h.UpdateSpace)
	e.DELETE("/api/v1/users/current/spaces/:id", h.DeleteSpace)
	e.POST("/api/v1/users/current/spaces/:id/rules", h.AddSpaceRule)
	e.DELETE("/api/v1/users/current/spaces/:id/rules/:rid", h.DeleteSpaceRule)
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

// registerAuthRoutes: login/register/refresh + API token management.
func registerAuthRoutes(e *echo.Echo, h *handler.Handler) {
	e.POST("/auth/login", h.Login)
	e.POST("/auth/register", h.Register)
	e.POST("/auth/refresh_token", h.RefreshToken)
	e.POST("/auth/logout", h.Logout)
	e.POST("/auth/create_api_token", h.CreateAPIToken)
	e.GET("/auth/tokens", h.ListAPITokens)
	e.DELETE("/auth/token/:id", h.DeleteToken)
	e.POST("/auth/token", h.UpdateToken)
	e.GET("/auth/users/current", h.CurrentUser)
	// Change password (gaka-6jm): auth'd, re-verifies the current password,
	// re-hashes with argon2id, and revokes every refresh token for the owner
	// so other browsers get bounced. Registered under the users/current tree
	// (not /auth/) so it uses the same access-token auth as sibling
	// /api/v1/users/current/* endpoints.
	e.POST("/api/v1/users/current/password", h.ChangePassword)
	// Public profile (gaka-6jm.1): auth'd GET/PUT for the caller's own
	// enable-toggle + slug. The PUBLIC read endpoint that resolves the slug
	// lives in registerMiscRoutes near /widget/svg/ — same public-payload
	// audience, and both must apply the widget.Scrub scrubber.
	e.GET("/api/v1/users/current/profile", h.GetPublicProfile)
	e.PUT("/api/v1/users/current/profile", h.PutPublicProfile)
	// Encrypted-at-rest imported Wakatime API key (gaka-6jm.2). GET reports
	// only {"hasSavedKey": bool} — plaintext is never returned. POST persists
	// a user-supplied key under AES-256-GCM. DELETE clears it.
	e.GET("/api/v1/users/current/wakatime_key", h.GetWakatimeKey)
	e.POST("/api/v1/users/current/wakatime_key", h.SaveWakatimeKey)
	e.DELETE("/api/v1/users/current/wakatime_key", h.DeleteWakatimeKey)
}

// registerMiscRoutes: badges, widgets, leaderboards, and commits.
func registerMiscRoutes(e *echo.Echo, h *handler.Handler) {
	// Badges
	e.GET("/badge/link/:project", h.BadgeLink)
	e.GET("/badge/svg/:svg", h.BadgeSvg)

	// Embeddable widgets (gaka-hsj): auth'd link CRUD + PUBLIC SVG renderer.
	e.GET("/api/v1/users/current/widgets/link", h.WidgetLink)
	e.GET("/api/v1/users/current/widgets/links", h.WidgetLinkList)
	e.POST("/api/v1/users/current/widgets/link/:id/roll", h.WidgetLinkRoll)

	// Named/saved custom widget defs (gaka-3nu): auth'd CRUD + PUBLIC named
	// renderer. Register the "named" public route BEFORE the generic
	// :uuid/:kind route so it wins path matching. Ordering matters — Echo
	// picks the first registered matcher for overlapping patterns.
	e.GET("/widget/svg/:uuid/named", h.WidgetDefSvg)
	e.GET("/widget/svg/:uuid/:kind", h.WidgetSvg)

	// Public profile — resolves slug -> user, then renders a scrubbed
	// dashboard-shaped payload. UNAUTHENTICATED; the payload MUST go
	// through widget.Scrub before serialization. See internal/handler/profile.go.
	e.GET("/api/public/profile/:slug", h.PublicProfile)

	e.GET("/api/v1/users/current/widget-defs", h.ListWidgetDefs)
	e.POST("/api/v1/users/current/widget-defs", h.CreateWidgetDef)
	e.PATCH("/api/v1/users/current/widget-defs/:name", h.UpdateWidgetDef)
	e.DELETE("/api/v1/users/current/widget-defs/:name", h.DeleteWidgetDef)

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

// registerLogRoutes: server process logs (live slog stream + REST tail fallback).
func registerLogRoutes(e *echo.Echo, h *handler.Handler) {
	e.GET("/api/v1/logs", h.ServerLogs)
	e.GET("/api/v1/logs/ws", h.ServerLogsWS)
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
		if _, err := fs.Stat(fsys, reqPath); err != nil {
			// SPA fallback: serve index.html for unknown non-API paths.
			c.Request().URL.Path = "/"
		}
		fileServer.ServeHTTP(c.Response(), c.Request())
		return nil
	})
}
