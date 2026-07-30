// routes.go — Echo route registrations for the ingest domain
// (gaka-8tn phase 5a). Extracted from internal/server/server.go's
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

import "github.com/labstack/echo/v5"

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
}
