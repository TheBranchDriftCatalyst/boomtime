// Meta handlers: build/version disclosure and the embedded changelog.
//
// Both endpoints are unauthenticated. Rationale: version disclosure on a
// self-hosted app is low-risk, and it's the same posture as /badge/*. Flip to
// session-auth if we ever front-end a shared instance to third parties.
package handler

import (
	"net/http"

	boomtime "github.com/TheBranchDriftCatalyst/boomtime"
	"github.com/labstack/echo/v5"
)

// versionResponse is the JSON shape returned by GET /api/v1/version.
type versionResponse struct {
	Version string `json:"version"`
}

// Version returns the running app version (git-describe string stamped by
// ldflags — see cmd/boomtime/main.go). Falls back to "dev" for un-stamped
// dev builds.
func (h *Handler) Version(c *echo.Context) error {
	v := h.Cfg.Version
	if v == "" {
		v = "dev"
	}
	return c.JSON(http.StatusOK, versionResponse{Version: v})
}

// Changelog serves the embedded CHANGELOG.md verbatim as text/markdown. The FE
// parses it client-side (see web/src/lib/changelog.ts) so the response format
// stays deterministic and the payload stays cache-friendly (identical bytes
// for every request until the next release).
func (h *Handler) Changelog(c *echo.Context) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/markdown; charset=utf-8")
	return c.Blob(http.StatusOK, "text/markdown; charset=utf-8", boomtime.ChangelogMD)
}
