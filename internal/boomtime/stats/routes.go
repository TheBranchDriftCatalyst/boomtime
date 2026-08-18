// routes.go — Echo route registrations for the stats domain
// (gaka-8tn phase 6). Extracted from internal/server/server.go's
// registerStatsRoutes and the projects/active_files subset that lived
// in registerMiscRoutes / registerStatsRoutes.
//
// URL patterns are byte-identical to the pre-refactor set — this is a
// pure package move, not a route rename. The tests already assert
// specific 404s / 400s / status-code invariants against these strings;
// changing any of them is out of scope for phase 6.
//
// The DB backup routes (/db/export, /db/import) and the source-health
// route stay in internal/handler/ — they're admin/observability endpoints
// that migrate to internal/admin/ in phase 7 alongside sources.go +
// backup.go. Same for the widget/label-image routes that are in
// registerMiscRoutes but owned by widgets/curation/admin domains.
package stats

import "github.com/labstack/echo/v5"

// Register wires the stats-domain endpoints onto e. Handler must be
// non-nil. Registration order preserves the pre-refactor sequence inside
// registerStatsRoutes + the stats-owned subset of registerMiscRoutes so
// any test that hit these routes previously still hits them in the same
// order — Echo picks the first registered matcher for overlapping
// patterns, so preserving order preserves matching. In particular
// /projects/:project must stay in the same registration slot so the
// FE's /projects call still resolves without shadowing.
//
// Route inventory (six clusters — derived health + core stats + big-bet
// aggregations + files + projects + leaderboards + commits):
//
//	GET    /api/v1/users/current/derived/status         (h.DerivedStatus)
//	POST   /api/v1/users/current/derived/resync         (h.DerivedResync)
//	GET    /api/v1/users/current/stats                  (h.Stats)
//	GET    /api/v1/users/current/timeline               (h.Timeline)
//	GET    /api/v1/users/current/statusbar/today        (h.StatusbarToday)
//	GET    /api/v1/users/current/stats/punchcard        (h.Punchcard)
//	GET    /api/v1/users/current/stats/sessions         (h.Sessions)
//	GET    /api/v1/users/current/stats/momentum         (h.Momentum)
//	GET    /api/v1/users/current/stats/ai               (h.AIActivity)
//	GET    /api/v1/users/current/stats/loc              (h.Loc)
//	GET    /api/v1/users/current/stats/health           (h.HealthActivity)
//	GET    /api/v1/users/current/workouts               (h.WorkoutList)
//	GET    /api/v1/users/current/files                  (h.ActiveFiles)
//	GET    /api/v1/users/current/projects/:project      (h.ProjectStats)
//	GET    /api/v1/projects                             (h.ProjectList)
//	GET    /api/v1/leaderboards                         (h.Leaderboards)
//	GET    /api/v1/commits/:project/report              (h.Commits)
func Register(e *echo.Echo, h *Handler) {
	// Derived-data health (gap_seconds + rollup status / resync)
	e.GET("/api/v1/users/current/derived/status", h.DerivedStatus)
	e.POST("/api/v1/users/current/derived/resync", h.DerivedResync)

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

	// gaka-yfg: lines-of-code (total + per-project + over-time) from file_lines.
	e.GET("/api/v1/users/current/stats/loc", h.Loc)

	// HealthKit metrics feed (Wellness card + Wellness page).
	e.GET("/api/v1/users/current/stats/health", h.HealthActivity)

	// Per-workout event list + per-label breakdown (Wellness events breakdown).
	e.GET("/api/v1/users/current/workouts", h.WorkoutList)

	// Cross-project active files (shared lynchpins spanning multiple projects)
	e.GET("/api/v1/users/current/files", h.ActiveFiles)

	// Projects
	e.GET("/api/v1/users/current/projects/:project", h.ProjectStats)
	e.GET("/api/v1/projects", h.ProjectList)

	// Leaderboards
	e.GET("/api/v1/leaderboards", h.Leaderboards)

	// Commits
	e.GET("/api/v1/commits/:project/report", h.Commits)
}
