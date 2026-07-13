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
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		// Reflect the request origin so credentialed requests (refresh_token
		// cookie) work in dev behind the Vite proxy. echo v5 forbids the "*"
		// wildcard together with AllowCredentials=true.
		UnsafeAllowOriginFunc: func(_ *echo.Context, origin string) (string, bool, error) {
			return origin, true, nil
		},
		AllowHeaders:     []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAuthorization, "X-Machine-Name"},
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowCredentials: true,
	}))
	if cfg.HTTPLog {
		e.Use(requestLogger(logger))
	}
	if cfg.DBN1Threshold > 0 || cfg.DBN1DupThresh > 0 {
		e.Use(n1Middleware(logger, cfg.DBN1Threshold, cfg.DBN1DupThresh))
	}

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
// public health probe. All three are intentionally unauthenticated (see
// internal/handler/meta.go, internal/handler/healthz.go).
func registerMetaRoutes(e *echo.Echo, h *handler.Handler) {
	e.GET("/api/v1/version", h.Version)
	e.GET("/api/v1/changelog", h.Changelog)
	e.GET("/healthz", h.Healthz)
}

// registerHeartbeatRoutes: ingest, the read-only explorer, and source health.
func registerHeartbeatRoutes(e *echo.Echo, h *handler.Handler) {
	// Heartbeats (ingest)
	e.POST("/api/v1/users/current/heartbeats", h.Heartbeat)
	e.POST("/api/v1/users/current/heartbeats.bulk", h.HeartbeatBulk)

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
