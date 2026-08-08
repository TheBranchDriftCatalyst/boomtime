// routes.go — Echo route registrations for the admin domain
// (gaka-8tn phase 7). Extracted from internal/server/server.go's
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

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
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

	// gaka-93f.6: admin caps dashboard — users + roles/tiers + effective
	// capabilities. Admin-gated in the handler (requireAdmin).
	e.GET("/api/v1/admin/users", h.ListUsers)
	// gaka-8bz: durable WS stream of the image-job queue lifecycle.
	// Auth uses the refresh_token cookie inside the handler (see
	// AdminLabelImagesWS) — WS handshakes can't carry Authorization.
	e.GET("/api/v1/admin/label-images/ws", h.AdminLabelImagesWS)

	// Durable, resumable wakatime.com import jobs. Auth is the shared
	// bearer-token flow for the JSON endpoints; the WS uses the
	// refresh_token cookie (WS handshakes can't carry Authorization).
	// Registration order preserves pre-refactor registerImportRoutes.
	// auth-dry Phase 2: starting a Wakatime import is gated by CapImport (the
	// bulk historical pull is expensive). Declared as route middleware instead
	// of an inline check in ImportRequest. Flag off ⇒ all-caps ⇒ allowed.
	// importCap is nil-safe: the OpenAPI drift router registers routes with a
	// nil handler to enumerate paths, so h.DB must not be dereferenced there.
	e.POST("/import", h.ImportRequest, importCap(h)...)
	e.GET("/import/config", h.ImportConfig)
	e.POST("/import/wakatime-range", h.WakatimeRange)
	e.GET("/import/jobs", h.ImportJobs)
	e.GET("/import/jobs/:id", h.ImportJob)
	e.POST("/import/jobs/:id/cancel", h.ImportJobCancel)
	e.GET("/import/jobs/:id/logs", h.ImportJobLogs)
	e.GET("/import/jobs/:id/ws", h.ImportJobWS)
}

// importCap returns the CapImport route middleware, or nil when h is nil. The
// nil case exists only for the OpenAPI drift router, which registers routes
// with a nil handler to enumerate paths and never serves them — so h.DB must
// not be dereferenced at registration time.
func importCap(h *Handler) []echo.MiddlewareFunc {
	if h == nil {
		return nil
	}
	return []echo.MiddlewareFunc{apihelpers.RequireCap(h.DB, auth.CapImport, "import")}
}
