package widgets

import "github.com/labstack/echo/v5"

// Register wires the widget + widget-def + badge endpoints onto the passed-in
// Echo instance. The route strings + registration ORDER are byte-identical
// to the pre-Phase-3 mix in internal/server/server.go (registerMiscRoutes) —
// the /widget/svg/:uuid/named route MUST be registered BEFORE the generic
// /widget/svg/:uuid/:kind so it wins path matching (Echo picks the first
// registered matcher for overlapping patterns).
func Register(e *echo.Echo, h *Handler) {
	// Badges
	e.GET("/badge/link/:project", h.BadgeLink)
	e.GET("/badge/svg/:svg", h.BadgeSvg)

	// Embeddable widgets (boom-hsj)
	e.GET("/api/v1/users/current/widgets/link", h.WidgetLink)
	e.GET("/api/v1/users/current/widgets/links", h.WidgetLinkList)
	e.POST("/api/v1/users/current/widgets/link/:id/roll", h.WidgetLinkRoll)

	// Named/saved custom widget defs (boom-3nu) — /named MUST come before /:kind.
	e.GET("/widget/svg/:uuid/named", h.WidgetDefSvg)
	e.GET("/widget/svg/:uuid/:kind", h.WidgetSvg)

	// Widget-def CRUD
	e.GET("/api/v1/users/current/widget-defs", h.ListWidgetDefs)
	e.POST("/api/v1/users/current/widget-defs", h.CreateWidgetDef)
	e.PATCH("/api/v1/users/current/widget-defs/:name", h.UpdateWidgetDef)
	e.DELETE("/api/v1/users/current/widget-defs/:name", h.DeleteWidgetDef)
}
