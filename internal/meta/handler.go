// Package meta owns the tiny, cross-cutting HTTP surface: build/version
// disclosure, the embedded changelog, the public /healthz probe, and the
// server-log stream endpoints. All routes are extracted from the god-type
// handler.Handler as part of gaka-8tn phase 1 so a domain (meta) owns its
// handler struct + routes + tests as one folder.
//
// Both /api/v1/version and /api/v1/changelog are intentionally unauthenticated.
// Rationale: version disclosure on a self-hosted app is low-risk, and it's the
// same posture as /badge/*. Flip to session-auth if we ever front-end a shared
// instance to third parties.
package meta

import (
	"net/http"
	"strconv"
	"time"

	boomtime "github.com/TheBranchDriftCatalyst/boomtime"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logging"
	"github.com/labstack/echo/v5"
	"log/slog"
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
//                    auth resolvers /api/v1/logs uses
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

// -- auth resolvers (domain-local copies of handler.Handler methods) --------
//
// Phase 8 promotes the shared auth helpers to internal/testutil/handlerhelpers/.
// Until then each domain-package that needs an auth resolution carries its
// own tiny copy — the alternative is a cross-domain lateral import that the
// depguard rule (also enabled in phase 8) is meant to forbid.

// resolveOwnerFromCookie resolves the owner from the HttpOnly refresh_token
// cookie (used by the WS handshake, which cannot carry an Authorization
// header). missingErr is the error returned when the cookie is absent — the
// WS handshake treats an absent cookie the same as an expired one. An
// unknown/expired token always yields ExpiredRefreshToken.
func (h *Handler) resolveOwnerFromCookie(c *echo.Context, missingErr *apierr.Error) (string, *apierr.Error) {
	refresh, ok := auth.ParseRefreshCookie(c.Request().Header.Get("Cookie"))
	if !ok {
		return "", missingErr
	}
	owner, ok, err := h.DB.GetUserByRefreshToken(c.Request().Context(), refresh)
	if err != nil {
		h.Logger.Error("refresh token lookup failed", "path", c.Request().URL.Path, "err", err)
		return "", apierr.Generic()
	}
	if !ok {
		return "", apierr.ExpiredRefreshToken()
	}
	return owner, nil
}

// tokenFromHeader extracts the base64(uuid) token from the Authorization header,
// or returns MissingAuth (400) when absent.
func tokenFromHeader(c *echo.Context) (string, *apierr.Error) {
	tkn, ok := auth.ParseAuthHeader(c.Request().Header.Get(echo.HeaderAuthorization))
	if !ok || tkn == "" {
		return "", apierr.MissingAuth()
	}
	return tkn, nil
}

// resolveUser maps a token to its owning username. Returns InvalidToken (403)
// if the token has no owner.
func (h *Handler) resolveUser(c *echo.Context) (string, string, *apierr.Error) {
	tkn, aerr := tokenFromHeader(c)
	if aerr != nil {
		return "", "", aerr
	}
	owner, ok, err := h.DB.GetUserByToken(c.Request().Context(), tkn)
	if err != nil {
		return "", "", apierr.Generic()
	}
	if !ok {
		return "", "", apierr.InvalidToken()
	}
	return tkn, owner, nil
}

// respondErr renders an apierr.Error onto the context.
func respondErr(c *echo.Context, e *apierr.Error) error {
	return e.Write(c)
}

// queryInt64 parses an int64 query parameter with a default.
func queryInt64(c *echo.Context, name string, def int64) int64 {
	v := c.QueryParam(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}
