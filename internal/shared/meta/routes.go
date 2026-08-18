package meta

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/openapi"
	"github.com/labstack/echo/v5"
)

// Register wires the meta domain's routes onto e. Called from
// internal/server/server.go as `meta.Register(e, h.Meta)` — replacing the
// inline registerMetaRoutes + registerLogRoutes helpers that used to live
// in the server package. Registration order is preserved verbatim from the
// pre-refactor server.go so tests + traffic see the exact same matching.
//
// All routes here are intentionally UNAUTHENTICATED at the router layer;
// the handlers themselves gate on the Authorization header (ServerLogs) or
// the refresh_token cookie (ServerLogsWS). /healthz, /api/v1/version, and
// /api/v1/changelog are public by design — see the individual handler
// files for rationale.
func Register(e *echo.Echo, h *Handler) {
	// Meta cluster: version disclosure, embedded changelog, health probe,
	// plus the self-hosted OpenAPI spec + Swagger UI (gaka-lfc). The
	// OpenAPI registration doesn't touch the meta Handler — it's colocated
	// here because it shares the "public transparency" audience.
	e.GET("/api/v1/version", h.Version)
	e.GET("/api/v1/changelog", h.Changelog)
	e.GET("/healthz", h.Healthz)
	// Public client-config advertisement (gaka-93f.1.1): auth provider,
	// registration/billing/beta switches the FE needs at boot. Non-sensitive,
	// same public audience as /version + /healthz.
	e.GET("/api/v1/config/public", h.PublicConfig)
	openapi.Register(e)

	// Server-log stream cluster (gaka-awh.2): REST tail (Authorization-gated)
	// and the live WebSocket (refresh_token-cookie-gated because WS handshakes
	// can't carry an Authorization header).
	e.GET("/api/v1/logs", h.ServerLogs)
	e.GET("/api/v1/logs/ws", h.ServerLogsWS)
}
