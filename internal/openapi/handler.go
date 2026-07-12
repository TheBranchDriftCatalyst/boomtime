// Package openapi — Echo handler adapters.
//
// Two small handlers plus a route registrar so internal/server can wire up
// /api/openapi.json + /api/docs alongside the other route groups.
package openapi

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

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
func DocsHandler(prefix string) echo.HandlerFunc {
	h := UIHandler(prefix)
	return func(c *echo.Context) error {
		h.ServeHTTP(c.Response(), c.Request())
		return nil
	}
}

// Register wires the two docs endpoints onto e. Called from
// internal/server/server.go's registerMetaRoutes so the existing route
// bookkeeping stays in one place.
func Register(e *echo.Echo) {
	e.GET("/api/openapi.json", SpecHandler)
	// Serve both /api/docs and /api/docs/* — the latter catches the static
	// asset requests SwaggerUI makes for the CSS/JS/favicons.
	docs := DocsHandler("/api/docs")
	e.GET("/api/docs", docs)
	e.GET("/api/docs/*", docs)
}
