package spaces

import (
	"github.com/labstack/echo/v5"
)

// Register wires the spaces + dashboard-layout domain's routes onto e. Called
// from internal/server/server.go as `spaces.Register(e, h.Spaces)` — replacing
// the inline registerSpaceRoutes helper plus the dashboard-layout routes that
// used to be scattered inside registerAuthRoutes. Registration order preserved
// verbatim from the pre-refactor server.go so tests + traffic see the same
// matching (in particular, the static /spaces/preview must register BEFORE
// /spaces/:id — Echo picks the first matcher for overlapping patterns).
func Register(e *echo.Echo, h *Handler) {
	// Spaces (named, scoped dashboards). Static /preview registered BEFORE
	// param /:id so it is not shadowed.
	e.GET("/api/v1/users/current/spaces", h.ListSpaces)
	e.POST("/api/v1/users/current/spaces", h.CreateSpace)
	e.GET("/api/v1/users/current/spaces/preview", h.SpacePreview)
	e.GET("/api/v1/users/current/spaces/:id", h.GetSpace)
	e.PATCH("/api/v1/users/current/spaces/:id", h.UpdateSpace)
	e.DELETE("/api/v1/users/current/spaces/:id", h.DeleteSpace)
	e.POST("/api/v1/users/current/spaces/:id/rules", h.AddSpaceRule)
	e.DELETE("/api/v1/users/current/spaces/:id/rules/:rid", h.DeleteSpaceRule)

	// Dashboard layout persistence (boom-keb). Per-user, per-scope. Scope
	// today is "public_profile"; the handler enforces the small allowlist so
	// a stale FE can't squat rows for future scopes.
	e.GET("/api/v1/users/current/dashboard/:scope", h.GetDashboardLayout)
	e.PUT("/api/v1/users/current/dashboard/:scope", h.PutDashboardLayout)
	e.DELETE("/api/v1/users/current/dashboard/:scope", h.DeleteDashboardLayout)
}
