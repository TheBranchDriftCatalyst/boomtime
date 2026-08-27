// Package meta owns the tiny, cross-cutting HTTP surface: build/version
// disclosure, the embedded changelog, the public /healthz probe, and the
// server-log stream endpoints. All routes are extracted from the god-type
// handler.Handler as part of boom-8tn phase 1 so a domain (meta) owns its
// handler struct + routes + tests as one folder.
//
// Both /api/v1/version and /api/v1/changelog are intentionally unauthenticated.
// Rationale: version disclosure on a self-hosted app is low-risk, and it's the
// same posture as /badge/*. Flip to session-auth if we ever front-end a shared
// instance to third parties.
package meta

import (
	"log/slog"
	"net/http"
	"time"

	boomtime "github.com/TheBranchDriftCatalyst/boomtime"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/logging"
	"github.com/labstack/echo/v5"
)

// Handler bundles the SUBSET of the god-type handler.Handler's dependencies
// that the meta domain actually reads. Everything else stays out of this
// package.
//
//   - Cfg          — Version / Branch / Commit / BuildTime for /healthz + /version
//   - Logger       — internal log target for the resolveOwnerFromCookie helper
//   - Cache        — retained for parity with the god-type facade (not read here)
//   - LogHub       — live in-memory ring buffer for the /api/v1/logs endpoints
//   - DB           — needed by /healthz (Ping / SchemaVersion) and by the
//     auth resolvers /api/v1/logs uses
//   - StartTime    — captured at construction; /healthz reports uptime from it
type Handler struct {
	Cfg       *config.Config
	Logger    *slog.Logger
	Cache     *cache.TTL
	LogHub    *logging.LogHub
	DB        *db.DB
	StartTime time.Time
}

// versionResponse is the JSON shape returned by GET /api/v1/version.
type versionResponse struct {
	Version string `json:"version"`
}

// Version returns the running app version (git-describe string stamped by
// ldflags — see cmd/boomtime/main.go). Falls back to "dev" for un-stamped
// dev builds.
func (h *Handler) Version(c *echo.Context) (versionResponse, error) {
	v := h.Cfg.Version
	if v == "" {
		v = "dev"
	}
	return versionResponse{Version: v}, nil
}

// Changelog serves the embedded CHANGELOG.md verbatim as text/markdown. The FE
// parses it client-side (see web/shared/lib/changelog.ts) so the response format
// stays deterministic and the payload stays cache-friendly (identical bytes
// for every request until the next release).
func (h *Handler) Changelog(c *echo.Context) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/markdown; charset=utf-8")
	return c.Blob(http.StatusOK, "text/markdown; charset=utf-8", boomtime.ChangelogMD)
}

// Shared helpers (RespondErr / TokenFromHeader / ResolveUser /
// ResolveOwnerFromCookie / QueryInt64 / BindJSONWithLimit) live in
// internal/apihelpers/ — every domain package imports that instead of
// carrying a local shim. See boom-8tn shared-helpers extraction commit
// following phase 1.
