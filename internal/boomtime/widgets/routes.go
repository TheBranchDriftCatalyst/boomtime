package widgets

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
)

// Register wires the widget + widget-def + badge endpoints onto the passed-in
// Echo instance. The route strings + registration ORDER are byte-identical
// to the pre-Phase-3 mix in internal/server/server.go (registerMiscRoutes) —
// the /widget/svg/:uuid/named route MUST be registered BEFORE the generic
// /widget/svg/:uuid/:kind so it wins path matching (Echo picks the first
// registered matcher for overlapping patterns).
//
// The JSON endpoints go through apiroute so the OpenAPI spec is generated from
// the Go response types. The SVG endpoints stay on plain e.GET: they answer
// image/svg+xml via c.Blob / apihelpers.CachedBlob, which has no JSON schema to
// describe. The two widget-def WRITE routes also stay on plain echo — see the
// note next to them.
func Register(e *echo.Echo, h *Handler) {
	// Badges
	apiroute.GET(e, "/badge/link/:project", h.BadgeLink)
	// BadgeSvg proxies shields.io and answers c.Blob(image/svg+xml) — not JSON.
	e.GET("/badge/svg/:svg", h.BadgeSvg)

	// Embeddable widgets (boom-hsj)
	apiroute.GET(e, "/api/v1/users/current/widgets/link", h.WidgetLink)
	apiroute.GET(e, "/api/v1/users/current/widgets/links", h.WidgetLinkList)
	// Roll takes no request body — the link id is in the path.
	apiroute.POSTNoBody(e, "/api/v1/users/current/widgets/link/:id/roll", h.WidgetLinkRoll)

	// Named/saved custom widget defs (boom-3nu) — /named MUST come before /:kind.
	// Both render SVG through apihelpers.CachedBlob; no JSON body either way.
	e.GET("/widget/svg/:uuid/named", h.WidgetDefSvg)
	e.GET("/widget/svg/:uuid/:kind", h.WidgetSvg)

	// Widget-def CRUD
	apiroute.GET(e, "/api/v1/users/current/widget-defs", h.ListWidgetDefs)
	// Create + Update deliberately stay on plain echo. Both bind their body with
	// apihelpers.BodyLimitMedium (64 KiB) because the inline widget spec is
	// allowed up to widgetDefMax (32 KiB); apiroute's body registrars bind at
	// BodyLimitSmall (4 KiB), which would start rejecting saved compositions
	// that are legal today with a 413. Moving them onto the seam needs a
	// limit-aware registrar first.
	e.POST("/api/v1/users/current/widget-defs", h.CreateWidgetDef)
	e.PATCH("/api/v1/users/current/widget-defs/:name", h.UpdateWidgetDef)
	// Delete carries no body, so the 204 registrar fits as-is.
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/widget-defs/:name", h.DeleteWidgetDef)
}
