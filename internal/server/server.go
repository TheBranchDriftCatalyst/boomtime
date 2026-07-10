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

	"github.com/TheBranchDriftCatalyst/gakatime/internal/config"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/handler"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/importer"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

//go:embed dist
var distFS embed.FS

// New builds a configured Echo server.
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, worker *importer.Worker, hub *importer.Hub) *echo.Echo {
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
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodOptions},
		AllowCredentials: true,
	}))
	if cfg.HTTPLog {
		e.Use(requestLogger(logger))
	}

	h := handler.New(database, cfg, logger, worker, hub)
	registerRoutes(e, h)
	registerStatic(e, cfg, logger)
	return e
}

func registerRoutes(e *echo.Echo, h *handler.Handler) {
	// Heartbeats (ingest)
	e.POST("/api/v1/users/current/heartbeats", h.Heartbeat)
	e.POST("/api/v1/users/current/heartbeats.bulk", h.HeartbeatBulk)

	// Heartbeats Explorer (read-only audit views)
	e.GET("/api/v1/users/current/heartbeats/group", h.HeartbeatsGroup)
	e.GET("/api/v1/users/current/heartbeats", h.HeartbeatsList)

	// Data curation (hide / rename labels)
	e.GET("/api/v1/users/current/curation", h.ListCuration)
	e.POST("/api/v1/users/current/curation", h.CreateCuration)
	e.DELETE("/api/v1/users/current/curation/:id", h.DeleteCuration)

	// Derived-data health (gap_seconds + rollup status / resync)
	e.GET("/api/v1/users/current/derived/status", h.DerivedStatus)
	e.POST("/api/v1/users/current/derived/resync", h.DerivedResync)

	// Stats
	e.GET("/api/v1/users/current/stats", h.Stats)
	e.GET("/api/v1/users/current/timeline", h.Timeline)
	e.GET("/api/v1/users/current/statusbar/today", h.StatusbarToday)

	// Projects & tags
	e.GET("/api/v1/users/current/projects/:project", h.ProjectStats)
	e.GET("/api/v1/users/current/tags/:tag", h.TagStats)
	e.POST("/api/v1/projects/:project/tags", h.SetProjectTags)
	e.GET("/api/v1/projects/:project/tags", h.GetProjectTags)
	e.GET("/api/v1/tags", h.GetUserTags)
	e.GET("/api/v1/projects", h.ProjectList)

	// Auth
	e.POST("/auth/login", h.Login)
	e.POST("/auth/register", h.Register)
	e.POST("/auth/refresh_token", h.RefreshToken)
	e.POST("/auth/logout", h.Logout)
	e.POST("/auth/create_api_token", h.CreateAPIToken)
	e.GET("/auth/tokens", h.ListAPITokens)
	e.DELETE("/auth/token/:id", h.DeleteToken)
	e.POST("/auth/token", h.UpdateToken)
	e.GET("/auth/users/current", h.CurrentUser)

	// Badges
	e.GET("/badge/link/:project", h.BadgeLink)
	e.GET("/badge/svg/:svg", h.BadgeSvg)

	// Leaderboards
	e.GET("/api/v1/leaderboards", h.Leaderboards)

	// Commits
	e.GET("/api/v1/commits/:project/report", h.Commits)

	// Import (durable, resumable jobs)
	e.POST("/import", h.ImportRequest)
	e.GET("/import/config", h.ImportConfig)
	e.POST("/import/wakatime-range", h.WakatimeRange)
	e.GET("/import/jobs", h.ImportJobs)
	e.GET("/import/jobs/:id", h.ImportJob)
	e.POST("/import/jobs/:id/cancel", h.ImportJobCancel)
	e.GET("/import/jobs/:id/logs", h.ImportJobLogs)
	e.GET("/import/jobs/:id/ws", h.ImportJobWS)
}

// registerStatic serves the SPA: from HAKA_DASHBOARD_PATH on disk if set, else
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
