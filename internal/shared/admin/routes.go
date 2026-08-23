// routes.go — Echo route registrations for the admin domain
// (boom-8tn phase 7). Extracted from internal/server/server.go's
// registerImportRoutes + the admin/backup/label-images/sources chunks
// inside registerMiscRoutes + registerStatsRoutes + registerHeartbeat
// Routes so those functions collapse toward N domain-Register calls.
//
// URL patterns are byte-identical to the pre-refactor set — this is a
// pure package move, not a route rename. The tests already assert
// specific 404s / 400s / status-code invariants against these strings;
// changing any of them is out of scope for phase 7.
package admin

import (
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/objstore"
)

// Register wires the admin-domain endpoints onto e. Handler must be
// non-nil. Registration order preserves the pre-refactor sequence:
//
//   - source-health (from registerHeartbeatRoutes)
//   - whole-DB backup export + destructive restore (from registerStatsRoutes)
//   - public label-image GET (from registerMiscRoutes)
//   - label-images admin cluster (info / regenerate / WS)
//   - wakatime.com import cluster (create + config + range + jobs list +
//     one-job / cancel / logs / WS)
//
// Route inventory:
//
//	GET    /api/v1/users/current/sources/health                     (h.SourceHealth)
//	GET    /api/v1/users/current/db/export                          (h.DBExport)
//	POST   /api/v1/users/current/db/import                          (h.DBImport)
//	GET    /api/v1/labels/:id/image                                 (h.LabelImage)               PUBLIC
//	GET    /api/v1/admin/label-images                               (h.AdminLabelImagesInfo)
//	POST   /api/v1/admin/label-images/regenerate                    (h.AdminLabelImagesRegenerate)
//	GET    /api/v1/admin/label-images/ws                            (h.AdminLabelImagesWS)
//	POST   /import                                                  (h.ImportRequest)
//	GET    /import/config                                           (h.ImportConfig)
//	POST   /import/wakatime-range                                   (h.WakatimeRange)
//	GET    /import/jobs                                             (h.ImportJobs)
//	GET    /import/jobs/:id                                         (h.ImportJob)
//	POST   /import/jobs/:id/cancel                                  (h.ImportJobCancel)
//	GET    /import/jobs/:id/logs                                    (h.ImportJobLogs)
//	GET    /import/jobs/:id/ws                                      (h.ImportJobWS)
//	GET    /api/v1/admin/cli/spec                                   (h.CLISpec)      only when FeatureAdminCLI
//	POST   /api/v1/admin/cli/run                                    (h.CLIRun)       only when FeatureAdminCLI
//	GET    /api/v1/admin/cli/run/ws                                 (h.CLIRunWS)     only when FeatureAdminCLI
//	POST   /api/v1/admin/cli/complete                               (h.CLIComplete)  only when FeatureAdminCLI
func Register(e *echo.Echo, h *Handler) {
	// Source health (per plugin/editor/machine last check-in — ingestion
	// health). Owner-scoped read, cached like other reads. Was previously
	// registered under registerHeartbeatRoutes (kept there through phase
	// 5a with a comment pointing at phase 7); the ingest.Register
	// deliberately left this behind for the admin phase to lift.
	e.GET("/api/v1/users/current/sources/health", h.SourceHealth)

	// Whole-database backup: streaming dump download + destructive
	// restore (requires ?confirm=replace-all-data; see backup.go). Was
	// previously registered inside registerStatsRoutes with a comment
	// pointing at phase 7.
	e.GET("/api/v1/users/current/db/export", h.DBExport)
	e.POST("/api/v1/users/current/db/import", h.DBImport)

	// boom-93f.6: admin caps dashboard — users + roles/tiers + effective
	// capabilities. Admin-gated in the handler (requireAdmin).
	e.GET("/api/v1/admin/users", h.ListUsers)

	// boom-zp2s: the per-DOMAIN admin surfaces moved OUT of this god-package —
	// catalyst-books (/api/v1/admin/books/*) to internal/books/admin (mounted via
	// books.Module.RegisterAdminRoutes), and boomtime's label-image regeneration
	// (/api/v1/admin/label-images/* + the public /api/v1/labels/:id/image) plus the
	// wakatime.com import cluster (/import/*) to internal/boomtime/admin (mounted via
	// boomtime.Module.RegisterRoutes). Route strings + gating are byte-identical; this
	// package now imports no data domain.

	// catalyst-go-jobs admin (boom-hney.2): the WHOLE jobs-admin HTTP surface
	// (list/queues/schedules/trigger/retry/cancel + per-job & bulk log clear) now
	// lives in the jobs package as a PORTABLE plugin — jobs.RegisterAdminRoutes.
	// This is the thin host mount: it owns the URL prefix + route-level CapAdmin
	// middleware, and injects the boomtime seam via jobs.Deps — the live job
	// subsystem accessors (wired AFTER this runs, hence functions), boomtime's
	// requireAdmin as the in-handler guard, and the logger. The plugin never
	// imports boomtime auth. Route strings + behavior are byte-identical to the
	// pre-move set (503 when the subsystem isn't wired).
	if h != nil && h.DB != nil {
		jobsCap := apihelpers.RequireCap(h.DB, auth.CapAdmin, "view admin jobs")
		g := e.Group("/api/v1/admin/jobs", jobsCap)
		jobs.RegisterAdminRoutes(g, jobs.Deps{
			Store:    func() *jobs.Store { return h.JobStore },
			Enqueuer: func() jobs.Enqueuer { return h.JobEnqueuer },
			Registry: func() *jobs.Registry { return h.JobRegistry },
			ObjStore: func() objstore.Store { return h.JobLogStore },
			// Adapt boomtime's requireAdmin (CapAdmin + IsAdmin) to the plugin's
			// plain-error guard seam. The returned *apierr.Error keeps its exact
			// JSON shape via the plugin's guardErr.
			Guard: func(c *echo.Context) (string, error) {
				owner, aerr := h.requireAdmin(c)
				if aerr != nil {
					return "", aerr
				}
				return owner, nil
			},
			Logger: h.Logger,
		})
	}

	// boom-metrics: generic in-memory rate-metric registry snapshot (router
	// request rates + per-kind job rate-limiter + external-API call rates).
	// requireAdmin in-handler; CapAdmin route middleware for defense-in-depth,
	// same posture as the jobs cluster. Nil-safe for the OpenAPI drift router.
	if h != nil && h.DB != nil {
		metricsCap := apihelpers.RequireCap(h.DB, auth.CapAdmin, "view admin metrics")
		e.GET("/api/v1/admin/metrics", h.AdminMetrics, metricsCap)
	}

	// Admin CLI-runner (BOOM_FEATURE_ADMIN_CLI, default off): flag off ⇒
	// the routes are NEVER registered, so the endpoints 404 like any
	// unknown path — the feature is fully inert. Flag on ⇒ every route is
	// double-gated: CapAdmin route middleware (defense-in-depth; inert
	// until BOOM_FEATURE_USER_MODEL) + requireAdmin inside each handler
	// (the BOOM_ADMIN_USERS hard gate, which runs before any body read).
	// The nil-guard mirrors importCap: the OpenAPI drift router registers
	// with a nil handler to enumerate paths and must not dereference h.
	if h != nil && h.Cfg != nil && h.Cfg.FeatureAdminCLI {
		cliCap := apihelpers.RequireCap(h.DB, auth.CapAdmin, "use the admin CLI runner")
		e.GET("/api/v1/admin/cli/spec", h.CLISpec, cliCap)
		e.POST("/api/v1/admin/cli/run", h.CLIRun, cliCap)
		e.POST("/api/v1/admin/cli/complete", h.CLIComplete, cliCap)
		// Streaming twin of /cli/run (boom-hney.5). Cookie-authed + admin-gated
		// in-handler like the other WS routes (a WS handshake can't carry the
		// cap middleware's header), so no cliCap here.
		e.GET("/api/v1/admin/cli/run/ws", h.CLIRunWS)
	}
}
