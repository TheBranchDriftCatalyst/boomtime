// routes.go — Echo route registrations for the goals domain
// (boom-8tn phase 2b). Extracted from internal/server/server.go's
// registerGoalRoutes so the server's route file collapses to N
// domain-Register calls.
//
// URL patterns are byte-identical to the pre-refactor set. The
// /goals/progress batched endpoint MUST be registered BEFORE the
// /goals/:id param route so Echo picks the exact-match handler for
// path collisions (spaces/preview pins the same invariant).
//
// Registration goes through internal/shared/apiroute rather than the
// bare e.GET/e.POST helpers: that is what captures each handler's
// request and response TYPES for the OpenAPI spec. Walking the router
// afterwards yields only paths, so a plain e.POST can never be
// documented as more than `{"type":"object"}`.
package goals

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
)

// Register wires the goals domain endpoints onto e. Handler must be
// non-nil.
func Register(e *echo.Echo, h *Handler) {
	apiroute.GET(e, "/api/v1/users/current/goals", h.ListGoals)
	apiroute.POST(e, "/api/v1/users/current/goals", h.CreateGoal)
	apiroute.GET(e, "/api/v1/users/current/goals/progress", h.GetAllGoalProgress)
	apiroute.GET(e, "/api/v1/users/current/goals/:id", h.GetGoal)
	apiroute.PATCH(e, "/api/v1/users/current/goals/:id", h.UpdateGoal)
	// 204 on success — DeleteGoal writes no body, so it registers through
	// the NoContent form rather than inventing a response type it never
	// returns.
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/goals/:id", h.DeleteGoal)
	apiroute.POST(e, "/api/v1/users/current/goals/:id/toggle", h.ToggleGoal)
	apiroute.GET(e, "/api/v1/users/current/goals/:id/progress", h.GetGoalProgress)
}
