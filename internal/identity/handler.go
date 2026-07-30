// Package identity owns the auth-facing HTTP + profile + timezone +
// wakatime_key + avatar endpoints. Extracted from the god-type
// handler.Handler as part of gaka-8tn phase 4a.
//
// SECURITY POSTURE: this package covers the auth surface (Login, Register,
// RefreshToken, Logout, ChangePassword, API-token CRUD) plus the
// user-scoped settings (public profile, timezone, wakatime key, avatar).
// The auth flows are load-bearing security invariants; the assertions in
// the *_test.go files here are byte-identical to their pre-refactor
// counterparts on purpose — do NOT simplify them.
//
// The public-facing endpoints (GET /api/public/profile/:slug, the CHIBI
// avatar public GET) live here too because their auth policy + read paths
// share the users-table dependency. Every payload that leaves via the
// public route MUST go through widget.Scrub before serialization — see
// internal/widget/scrub.go for the contract.
//
// Shared helpers (RespondErr / TokenFromHeader / ResolveUser /
// ResolveOwnerFromCookie / QueryInt64 / BindJSONWithLimit / InternalErr
// / CachedJSON / CachedBlob / InvalidateOwnerCache) live in
// internal/apihelpers/ — this package imports that instead of carrying
// per-file shims (the pattern the meta phase surfaced and the spaces
// phase adopted).
package identity

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
)

// Handler bundles the SUBSET of the god-type handler.Handler's
// dependencies that the identity domain actually reads. Everything else
// stays out of this package.
//
//   - DB     — every users / auth_tokens / refresh_tokens read + write,
//     plus timezone / wakatime_key / public_profile / user_avatars
//   - Cfg    — cookie Secure/APIPrefix, argon2 policy, session expiry,
//     LLM/ComfyUI feature flags, DefaultTimezone, IsAdmin allowlist
//   - Logger — password-change / wakatime-save / avatar-regen log lines
//   - Cache  — invalidated on timezone change so dashboards re-bucket
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
	Cache  *cache.TTL
}

// New constructs an identity.Handler with the passed-in shared deps.
// Every field is required in production; nil-checks are the caller's
// responsibility (the god-type's New wires all four unconditionally).
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, cch *cache.TTL) *Handler {
	return &Handler{
		DB:     database,
		Cfg:    cfg,
		Logger: logger,
		Cache:  cch,
	}
}

// resolveUser is the identity-domain adapter over apihelpers.ResolveUser
// — a receiver method so the extracted handlers keep their previous
// signature (`h.resolveUser(c)`) unchanged. Every call is line-identical
// to the god-type version; only the target moves from *handler.Handler
// to *identity.Handler.
func (h *Handler) resolveUser(c *echo.Context) (string, string, *apierr.Error) {
	return apihelpers.ResolveUser(h.DB, c)
}

// resolveOwnerFromCookie is the identity-domain adapter over
// apihelpers.ResolveOwnerFromCookie — same rationale as resolveUser.
// Used by RefreshToken + CurrentUser + Logout.
func (h *Handler) resolveOwnerFromCookie(c *echo.Context, missingErr *apierr.Error) (string, *apierr.Error) {
	return apihelpers.ResolveOwnerFromCookie(h.DB, h.Logger, c, missingErr)
}

// internalErr is the identity-domain adapter over apihelpers.InternalErr
// — receiver-shaped so per-handler call sites stay identical.
func (h *Handler) internalErr(c *echo.Context, msg string, err error) error {
	return apihelpers.InternalErr(h.Logger, c, msg, err)
}

// invalidateOwnerCache is the identity-domain adapter over
// apihelpers.InvalidateOwnerCache — receiver-shaped so timezone.go's
// call site stays identical.
func (h *Handler) invalidateOwnerCache(owner string) {
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
}

// requireAdmin: 401 without a token, 403 when not on the admin allowlist.
// Returns the resolved owner on success. Mirror of the same method on
// *handler.Handler (defined in internal/handler/admin_label_images.go) —
// duplicated here because user_avatar.go's SynthesizeAvatarPrompt gates on
// it and the admin domain is a phase-7 extraction. The two definitions
// stay byte-identical until phase 8 collapses them.
//
// The 403 path deliberately does NOT distinguish "unknown admin config"
// from "not on the list" — both look like a plain 403 to the client.
func (h *Handler) requireAdmin(c *echo.Context) (string, *apierr.Error) {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return "", aerr
	}
	if !h.Cfg.IsAdmin(owner) {
		return "", apierr.New(http.StatusForbidden, "admin only", nil)
	}
	return owner, nil
}

// respondErr renders an apierr.Error onto the context. Package-local
// alias for apihelpers.RespondErr so the extracted handler files keep
// their existing `respondErr(c, ...)` call sites unchanged.
func respondErr(c *echo.Context, e *apierr.Error) error {
	return apihelpers.RespondErr(c, e)
}

// tokenFromHeader is the identity-domain alias for
// apihelpers.TokenFromHeader. Kept as a package-local func (not receiver)
// so auth.go's Logout call stays identical.
func tokenFromHeader(c *echo.Context) (string, *apierr.Error) {
	return apihelpers.TokenFromHeader(c)
}

// noContent renders a 204. Package-local alias for the shared helper —
// keeps identity's call sites terse.
func noContent(c *echo.Context) error {
	return c.NoContent(http.StatusNoContent)
}

// removeDays subtracts n days from t, snapped to UTC midnight. Local
// copy of the shared helper — used by profile.go's public dashboard
// window (last publicProfilePayloadDays days). Kept private because
// the identity package is the only caller here.
func removeDays(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -n)
}

// httpClient is the shared outbound HTTP client for the identity
// package: the wakatime.com probe (wakatime_key.go SaveWakatimeKey).
// A dedicated timeout avoids http.DefaultClient's unbounded default
// which would hang a save if wakatime.com locks up.
//
// EXPOSED as a package-level var so auth_seams_test.go can swap it out
// via SwapHTTPClientForTest. Not exported directly — tests use the
// SwapHTTPClientForTest seam to keep the mutation site auditable.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// BindJSONWithLimit / body-size limits: identity re-exports the shared
// helpers under package-local aliases so the extracted files keep their
// original call sites (`BindJSONWithLimit(c, &req, BodyLimitSmall)`).
// These are the SAME buckets defined in apihelpers — the aliases keep
// call-site diffs to zero.

// BodyLimitSmall / BodyLimitMedium / BodyLimitLarge: package-local
// aliases over apihelpers so identity handlers keep their pre-refactor
// call sites. Delete these once phase 8 collapses call sites to the
// apihelpers-qualified form.
const (
	BodyLimitSmall  = apihelpers.BodyLimitSmall
	BodyLimitMedium = apihelpers.BodyLimitMedium
	BodyLimitLarge  = apihelpers.BodyLimitLarge
)

// BindJSONWithLimit: package-local alias for apihelpers.BindJSONWithLimit.
func BindJSONWithLimit(c *echo.Context, dst any, limit int64) *apierr.Error {
	return apihelpers.BindJSONWithLimit(c, dst, limit)
}
