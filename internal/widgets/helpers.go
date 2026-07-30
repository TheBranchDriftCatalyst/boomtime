// Package widgets holds the widget + widget-def + badge HTTP endpoints
// (gaka-8tn phase 3). Split off from internal/handler as a self-contained
// domain package: its own Handler struct, its own route registrar, and its
// own tests.
//
// The renderer library used to produce SVG bytes still lives at
// internal/widget/ (singular) — this package (plural) is the HTTP surface;
// widget (singular) is the pure rendering primitives.
package widgets

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
)

// Handler bundles the shared dependencies the widgets HTTP surface needs.
// Fields are the subset of the god Handler this domain actually reads:
// DB for widget/badge/link/def CRUD + curation lookups + timezone resolution,
// Cfg for BadgeURL, Cache for the cached-SVG blob store, Logger for
// error/debug logs. Nothing else — no importer, no hub, no worker.
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
	Cache  *cache.TTL
}

// New constructs a widgets.Handler with the passed-in shared deps.
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, cache *cache.TTL) *Handler {
	return &Handler{
		DB:     database,
		Cfg:    cfg,
		Logger: logger,
		Cache:  cache,
	}
}

// ---- body-limit constants (mirror internal/handler; kept in sync) ----

const (
	BodyLimitSmall  int64 = 4 * 1024
	BodyLimitMedium int64 = 64 * 1024
	BodyLimitLarge  int64 = 8 * 1024 * 1024
)

// BindJSONWithLimit is the widgets-package copy of the shared helper: cap the
// request body via http.MaxBytesReader BEFORE calling c.Bind so the JSON
// decoder never has to materialize a hostile blob. On oversize input, renders
// 413 Payload Too Large with the exact limit the client blew. See the
// counterpart in internal/handler/handler.go for the full rationale.
func BindJSONWithLimit(c *echo.Context, dst any, limit int64) *apierr.Error {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, limit)
	if err := c.Bind(dst); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			return apierr.New(http.StatusRequestEntityTooLarge, "payload too large", ptrStr(fmt.Sprintf("limit=%d", limit)))
		}
		return apierr.BadRequest("Invalid request body")
	}
	return nil
}

func ptrStr(s string) *string { return &s }

// respondErr renders an apierr.Error onto the context.
func respondErr(c *echo.Context, e *apierr.Error) error {
	return e.Write(c)
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

// resolveUser maps a token to its owning username (DB.GetUserByToken).
// Returns InvalidToken (403) if the token has no owner.
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

// internalErr logs the underlying error with request context and renders the
// generic 500 envelope.
func (h *Handler) internalErr(c *echo.Context, msg string, err error) error {
	h.Logger.Error(msg, "path", c.Request().URL.Path, "err", err)
	return respondErr(c, apierr.Generic())
}

// invalidateOwnerCache drops all cached aggregation payloads for a user so
// hide/rename/roll changes take effect immediately.
func (h *Handler) invalidateOwnerCache(owner string) {
	if h.Cache != nil {
		h.Cache.InvalidatePrefix(owner + "|")
	}
}

// cacheKeyTimeBucket / cacheKey / cachedJSON / cachedBlob mirror the shared
// helpers in internal/handler. Kept as private copies here so the widgets
// package doesn't depend on the parent's private helpers during Phase 3.
// A follow-up phase will collapse all copies into internal/apihelpers/.

const cacheKeyTimeBucket = 30 * time.Second

func cacheKey(owner, name string, parts ...any) string {
	var b strings.Builder
	b.WriteString(owner)
	b.WriteByte('|')
	b.WriteString(name)
	for _, p := range parts {
		b.WriteByte('|')
		if t, ok := p.(time.Time); ok {
			fmt.Fprintf(&b, "%d", t.Truncate(cacheKeyTimeBucket).Unix())
		} else {
			fmt.Fprint(&b, p)
		}
	}
	return b.String()
}

// cachedJSON serves a cached JSON payload for key, or computes+caches it.
func (h *Handler) cachedJSON(c *echo.Context, key string, compute func() (any, error)) error {
	if b, ok := h.Cache.Get(key); ok {
		return c.JSONBlob(http.StatusOK, b)
	}
	payload, err := compute()
	if err != nil {
		h.Logger.Error("aggregation query failed", "key", key, "err", err)
		return respondErr(c, apierr.Generic())
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	h.Cache.Set(key, b)
	return c.JSONBlob(http.StatusOK, b)
}

// cachedBlob is cachedJSON's non-JSON sibling: serve a cached byte blob for
// key, or compute+cache it. Used by the public widget SVG endpoint.
func (h *Handler) cachedBlob(c *echo.Context, key, contentType string, compute func() ([]byte, error)) error {
	if b, ok := h.Cache.Get(key); ok {
		return c.Blob(http.StatusOK, contentType, b)
	}
	b, err := compute()
	if err != nil {
		h.Logger.Error("blob compute failed", "key", key, "err", err)
		return respondErr(c, apierr.Generic())
	}
	h.Cache.Set(key, b)
	return c.Blob(http.StatusOK, contentType, b)
}

// httpClient is the shared client for all outbound HTTP calls (shields.io,
// GitHub, remote-write). http.DefaultClient has no timeout and can hang a
// handler forever on a stuck upstream.
var httpClient = &http.Client{Timeout: 15 * time.Second}

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

// removeDays subtracts n days from t, snapped to UTC midnight.
func removeDays(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -n)
}

// resolveUserTZ returns the effective IANA name for a user's dow/hour/date
// buckets. NEVER returns "" — safe to thread into an AT TIME ZONE bind param.
func (h *Handler) resolveUserTZ(ctx context.Context, owner string) string {
	userTZ, err := h.DB.GetUserTimezone(ctx, owner)
	if err != nil {
		h.Logger.Warn("resolveUserTZ: users.timezone lookup failed; falling back to defaults",
			"user", owner, "err", err)
		userTZ = ""
	}
	return db.ResolveTimezone(userTZ, h.Cfg.DefaultTimezone)
}
