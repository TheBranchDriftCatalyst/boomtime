// routes.go — Echo route registrations for the ingest domain
// (boom-8tn phase 5a). Extracted from internal/server/server.go's
// registerHeartbeatRoutes so that function collapses toward N domain-
// Register calls.
//
// URL patterns are byte-identical to the pre-refactor set — this is a
// pure package move, not a route rename. The tests already assert
// specific 404s / 400s / status-code invariants against these strings;
// changing any of them is out of scope for phase 5a.
//
// SourceHealth (GET /sources/health) is deliberately LEFT in
// registerHeartbeatRoutes because it's an admin/observability endpoint,
// not part of the ingest write path — it moves to internal/admin/ in
// phase 7 alongside sources.go.
package ingest

import (
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
)

// Register wires the ingest domain endpoints onto e. Handler must be
// non-nil. Registration order preserves the pre-refactor sequence inside
// registerHeartbeatRoutes so any test that hit these routes previously
// still hits them in the same order — Echo picks the first registered
// matcher for overlapping patterns, so preserving order preserves matching.
//
// Route inventory (five clusters — heartbeats ingest + workouts/health
// ingest + heartbeats explorer + entity explorer):
//
//	POST   /api/v1/users/current/heartbeats                       (h.Heartbeat)
//	POST   /api/v1/users/current/heartbeats.bulk                  (h.HeartbeatBulk)
//	POST   /api/v1/users/current/workouts                         (h.Workouts)
//	POST   /api/v1/users/current/workouts.bulk                    (h.WorkoutsBulk)
//	POST   /api/v1/users/current/health_samples                   (h.HealthSamples)
//	POST   /api/v1/users/current/health_samples.bulk              (h.HealthSamplesBulk)
//	GET    /api/v1/users/current/heartbeats/group                 (h.HeartbeatsGroup)
//	GET    /api/v1/users/current/heartbeats/latest                (h.HeartbeatsLatest)
//	GET    /api/v1/users/current/heartbeats                       (h.HeartbeatsList)
//	GET    /api/v1/users/current/heartbeats/entities              (h.ListEntitiesByType)
//	POST   /api/v1/users/current/heartbeats/entities/redact       (h.RedactEntities)
func Register(e *echo.Echo, h *Handler) {
	// auth-dry Phase 2: the ingest WRITE path is gated by CapIngestHeartbeats.
	// Declared once here as route middleware instead of re-checked inside each
	// store* handler — enforced before the body is bound. Flag off ⇒ all-caps ⇒
	// always allowed (byte-identical to the old inline gate). ingestCap() is nil
	// when h is nil (the OpenAPI drift router registers routes with nil handlers
	// purely to enumerate paths; it never serves them), so registration never
	// dereferences a nil handler's DB.
	ingestCap := ingestWriteCap(h)

	// Heartbeats (ingest)
	e.POST("/api/v1/users/current/heartbeats", h.Heartbeat, ingestCap...)
	e.POST("/api/v1/users/current/heartbeats.bulk", h.HeartbeatBulk, ingestCap...)

	// HealthKit / Apple Watch ingest (extensions/boomtime-watch/).
	// Workouts flow through the heartbeats table (ty='workout') so existing
	// time-spent aggregations pick them up; raw samples land in health_samples.
	e.POST("/api/v1/users/current/workouts", h.Workouts, ingestCap...)
	e.POST("/api/v1/users/current/workouts.bulk", h.WorkoutsBulk, ingestCap...)
	e.POST("/api/v1/users/current/health_samples", h.HealthSamples, ingestCap...)
	e.POST("/api/v1/users/current/health_samples.bulk", h.HealthSamplesBulk, ingestCap...)

	// Heartbeats Explorer (read-only audit views)
	e.GET("/api/v1/users/current/heartbeats/group", h.HeartbeatsGroup)
	e.GET("/api/v1/users/current/heartbeats/latest", h.HeartbeatsLatest)
	e.GET("/api/v1/users/current/heartbeats", h.HeartbeatsList)

	// Entity Explorer (boom-90x): per-ty flat list + per-entity redact (blanks
	// the entity column on matching heartbeat rows — row itself stays,
	// contributing to project/language/machine totals). Redact requires
	// ?confirm=redact-entities as an accident guard.
	e.GET("/api/v1/users/current/heartbeats/entities", h.ListEntitiesByType)
	e.POST("/api/v1/users/current/heartbeats/entities/redact", h.RedactEntities)
}

// ingestWriteCap returns the CapIngestHeartbeats route middleware for the write
// endpoints, or nil when h is nil. The nil case exists only for the OpenAPI
// drift router (newRouterForDrift), which registers every route with nil
// handlers to enumerate (method, path) pairs and never serves a request — so
// evaluating h.DB there would nil-panic. In the real server h is always
// non-nil, so the gate is always attached.
func ingestWriteCap(h *Handler) []echo.MiddlewareFunc {
	if h == nil {
		return nil
	}
	return []echo.MiddlewareFunc{apihelpers.RequireCap(h.DB, auth.CapIngestHeartbeats, "ingest data")}
}
