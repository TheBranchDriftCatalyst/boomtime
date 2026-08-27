// Package openapi — Echo handler adapters.
//
// Two small handlers plus a route registrar so internal/server can wire up
// /api/openapi.json + /api/docs alongside the other route groups.
package openapi

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
)

// specDocument is the DECLARED shape of GET /api/openapi.json's success body:
// the top level of the OpenAPI 3 document this server publishes. The field
// names mirror openapi3.T's json tags exactly; the nested values are left open
// because their shapes are the OpenAPI meta-schema, not boomtime's.
//
// It is DECLARED rather than encoded because SpecHandler writes pre-marshalled
// bytes (the document is built once and cached, then served verbatim with a
// Cache-Control header) — the same reason apiroute.WritesJSON exists for the
// CachedJSON routes. Without it the spec documents its own endpoint as a bare
// {"type":"object"}.
type specDocument struct {
	OpenAPI      string           `json:"openapi"`
	Info         map[string]any   `json:"info"`
	Paths        map[string]any   `json:"paths"`
	Components   map[string]any   `json:"components,omitempty"`
	Security     []map[string]any `json:"security,omitempty"`
	Servers      []map[string]any `json:"servers,omitempty"`
	Tags         []map[string]any `json:"tags,omitempty"`
	ExternalDocs map[string]any   `json:"externalDocs,omitempty"`
}

// SpecHandler serves the built OpenAPI 3 spec as JSON. The spec is built
// once (see Spec()) and cached; subsequent calls just write the same bytes.
func SpecHandler(c *echo.Context) error {
	_, b, err := Spec()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "spec build failed"})
	}
	c.Response().Header().Set(echo.HeaderContentType, "application/json; charset=utf-8")
	// Small cache: the spec is deterministic and only changes at binary swap.
	c.Response().Header().Set("Cache-Control", "public, max-age=300")
	return c.Blob(http.StatusOK, "application/json; charset=utf-8", b)
}

// DocsHandler serves the embedded Swagger UI at /api/docs (index) and
// /api/docs/* (static assets). The prefix must match the registered route.
//
// Sets X-Frame-Options: SAMEORIGIN so a hostile site can't iframe the docs
// and trick a logged-in operator into clicking the token-mint FAB
// (clickjacking). Combined with the refresh_token cookie's SameSite=Strict
// policy, this closes the two CSRF paths a malicious embed could exploit.
func DocsHandler(prefix string) echo.HandlerFunc {
	h := UIHandler(prefix)
	return func(c *echo.Context) error {
		c.Response().Header().Set("X-Frame-Options", "SAMEORIGIN")
		c.Response().Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
		h.ServeHTTP(c.Response(), c.Request())
		return nil
	}
}

// Register wires the two docs endpoints onto e. Called from
// internal/server/server.go's registerMetaRoutes so the existing route
// bookkeeping stays in one place.
func Register(e *echo.Echo) {
	// Record this router as the source for the spec's auto-derive pass (boom-lfc
	// option A). Register runs mid-registration, so we store the pointer and
	// read e.Router().Routes() lazily at build time — by then every domain's
	// routes are wired. Invalidates any cached spec (see setRouterEcho).
	setRouterEcho(e)
	apiroute.WritesJSON[specDocument](e, http.MethodGet, "/api/openapi.json", SpecHandler).
		Doc("This OpenAPI 3 document",
			"The machine-readable description of every route on this server, and the document "+
				"powering the explorer at /api/docs. Fully self-contained: no external $refs "+
				"and no CDN URLs, so it can be saved and fed to a client generator offline. "+
				"Built once per process from the live echo router — restart the server to pick "+
				"up a route change — and served from cache with Cache-Control: public, "+
				"max-age=300. Unauthenticated, like /api/docs itself.")
	// Serve both /api/docs and /api/docs/* — the latter catches the static
	// asset requests SwaggerUI makes for the CSS/JS/favicons.
	docs := DocsHandler("/api/docs")
	e.GET("/api/docs", docs)
	e.GET("/api/docs/*", docs)
}
