package spaces

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
)

// Register wires the spaces + dashboard-layout domain's routes onto e. Called
// from internal/server/server.go as `spaces.Register(e, h.Spaces)` — replacing
// the inline registerSpaceRoutes helper plus the dashboard-layout routes that
// used to be scattered inside registerAuthRoutes. Registration order preserved
// verbatim from the pre-refactor server.go so tests + traffic see the same
// matching (in particular, the static /spaces/preview must register BEFORE
// /spaces/:id — Echo picks the first matcher for overlapping patterns).
//
// Most routes go through apiroute so their request/response TYPES reach the
// OpenAPI generator. The two that stay on plain echo are annotated at their
// handlers with the exact behaviour the seam would have changed.
func Register(e *echo.Echo, h *Handler) {
	// Spaces (named, scoped dashboards). Static /preview registered BEFORE
	// param /:id so it is not shadowed.
	apiroute.GET(e, "/api/v1/users/current/spaces", h.ListSpaces)
	apiroute.POST(e, "/api/v1/users/current/spaces", h.CreateSpace)
	apiroute.GET(e, "/api/v1/users/current/spaces/preview", h.SpacePreview)
	apiroute.GET(e, "/api/v1/users/current/spaces/:id", h.GetSpace)
	apiroute.NoContentBody(e, http.MethodPatch, "/api/v1/users/current/spaces/:id", h.UpdateSpace)
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/spaces/:id", h.DeleteSpace)
	// Plain echo: AddSpaceRule binds under BodyLimitMedium (64 KiB) and the
	// seam's body registrars hard-code BodyLimitSmall (4 KiB).
	e.POST("/api/v1/users/current/spaces/:id/rules", h.AddSpaceRule)
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/spaces/:id/rules/:rid", h.DeleteSpaceRule)

	// Dashboard layout persistence (boom-keb). Per-user, per-scope. Scope
	// today is "public_profile"; the handler enforces the small allowlist so
	// a stale FE can't squat rows for future scopes.
	apiroute.GET(e, "/api/v1/users/current/dashboard/:scope", h.GetDashboardLayout)
	// Plain echo: PutDashboardLayout hand-rolls its decode to stay
	// Content-Type-lenient and keep its own 400 text. See the handler comment.
	e.PUT("/api/v1/users/current/dashboard/:scope", h.PutDashboardLayout)
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/dashboard/:scope", h.DeleteDashboardLayout)
}
