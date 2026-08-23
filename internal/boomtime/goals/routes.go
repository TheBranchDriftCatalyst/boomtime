// routes.go — Echo route registrations for the goals domain
// (boom-8tn phase 2b). Extracted from internal/server/server.go's
// registerGoalRoutes so the server's route file collapses to N
// domain-Register calls.
//
// URL patterns are byte-identical to the pre-refactor set. The
// /goals/progress batched endpoint MUST be registered BEFORE the
// /goals/:id param route so Echo picks the exact-match handler for
// path collisions (spaces/preview pins the same invariant).
package goals

import "github.com/labstack/echo/v5"

// Register wires the goals domain endpoints onto e. Handler must be
// non-nil.
func Register(e *echo.Echo, h *Handler) {
	e.GET("/api/v1/users/current/goals", h.ListGoals)
	e.POST("/api/v1/users/current/goals", h.CreateGoal)
	e.GET("/api/v1/users/current/goals/progress", h.GetAllGoalProgress)
	e.GET("/api/v1/users/current/goals/:id", h.GetGoal)
	e.PATCH("/api/v1/users/current/goals/:id", h.UpdateGoal)
	e.DELETE("/api/v1/users/current/goals/:id", h.DeleteGoal)
	e.POST("/api/v1/users/current/goals/:id/toggle", h.ToggleGoal)
	e.GET("/api/v1/users/current/goals/:id/progress", h.GetGoalProgress)
}
