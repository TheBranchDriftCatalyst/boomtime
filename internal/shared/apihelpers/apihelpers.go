// Package apihelpers holds the small HTTP-plumbing helpers every handler
// domain needs: auth resolution, error responses, JSON body binding with
// caps, cached-payload flow, and query-param parsing.
//
// It exists so the per-domain packages under internal/<domain>/ (ingest,
// curation, stats, ...) don't each re-declare a local copy of the same 8
// functions — a DRY violation the meta phase surfaced (gaka-8tn phase 1
// shipped with byte-identical local shims of resolveUser / respondErr /
// queryInt64 in internal/meta/handler.go).
//
// Everything here is a free function taking its dependencies explicitly
// so per-domain Handler structs (which hold only the subset of state they
// need) can call these without inheriting the god-type shape from
// internal/handler.Handler.
package apihelpers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// ---- Body-size caps for authed JSON writes (gaka-bi2) --------------------
//
// Three buckets so a hostile client can't force the server to materialize a
// huge blob and then run an expensive verify step (argon2 on change-password
// was the motivating amplifier — a 10 MiB body pinned ~256 MiB per verify).
// Applied per-handler via BindJSONWithLimit so the cap sits next to the
// deserialization, not hidden in middleware.
//
//   - Small (4 KiB): auth credentials, single-field secrets, small JSON toggles.
//   - Medium (64 KiB): JSON-config endpoints that can carry a modest list of
//     rules, member sets, or spec blobs (curation, spaces, widget-defs).
//   - Large (8 MiB): batched telemetry ingest (heartbeats/workouts/health_samples
//     bulk endpoints).
const (
	BodyLimitSmall  int64 = 4 * 1024
	BodyLimitMedium int64 = 64 * 1024
	BodyLimitLarge  int64 = 8 * 1024 * 1024
)

// ---- Pure (state-free) helpers -------------------------------------------

// RespondErr renders an apierr.Error onto the context.
func RespondErr(c *echo.Context, e *apierr.Error) error {
	return e.Write(c)
}

// TokenFromHeader extracts the base64(uuid) token from the Authorization header,
// or returns MissingAuth (400) when absent.
func TokenFromHeader(c *echo.Context) (string, *apierr.Error) {
	tkn, ok := auth.ParseAuthHeader(c.Request().Header.Get(echo.HeaderAuthorization))
	if !ok || tkn == "" {
		return "", apierr.MissingAuth()
	}
	return tkn, nil
}

// QueryInt64 parses an int64 query parameter with a default.
func QueryInt64(c *echo.Context, name string, def int64) int64 {
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

// TimeLimit reads the optional timeLimit param (minutes), defaulting to 15.
func TimeLimit(c *echo.Context) int64 {
	return QueryInt64(c, "timeLimit", 15)
}

// BindJSONWithLimit wraps c.Bind with a http.MaxBytesReader cap on the request
// body. On oversize input the Body read fails FAST — before json.Decode has to
// allocate the tail — and we render 413 Payload Too Large with the exact limit
// the client blew. On normal parse errors the returned *apierr.Error is a 400
// so callers can keep their existing "invalid request body" response text.
func BindJSONWithLimit(c *echo.Context, dst any, limit int64) *apierr.Error {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, limit)
	if err := c.Bind(dst); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			return apierr.New(http.StatusRequestEntityTooLarge, "payload too large",
				ptrStr(fmt.Sprintf("limit=%d", limit)))
		}
		return apierr.BadRequest("Invalid request body")
	}
	return nil
}

// ---- Time-range parsing (shared by stats/projects/leaderboards) ----------

// ParseTimeParam parses an RFC3339 query parameter; returns (zero,false) if absent.
func ParseTimeParam(c *echo.Context, name string) (time.Time, bool) {
	v := c.QueryParam(name)
	if v == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999Z07:00", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// DefaultRange resolves the start/end query params, filling missing side(s)
// with a `days`-long window ending now. Supports "All time" via explicit
// wide ranges (no 1-year clamp).
func DefaultRange(c *echo.Context, days int) (time.Time, time.Time) {
	now := time.Now().UTC()
	t0, has0 := ParseTimeParam(c, "start")
	t1, has1 := ParseTimeParam(c, "end")
	switch {
	case !has0 && !has1:
		return removeDays(now, days), now
	case !has0 && has1:
		return removeDays(t1, days), t1
	case has0 && !has1:
		return t0, addDays(t0, days)
	default:
		return t0, t1
	}
}

// DefaultWeekRange = last 7 days.
func DefaultWeekRange(c *echo.Context) (time.Time, time.Time) { return DefaultRange(c, 7) }

// DefaultMonthRange = last 30 days.
func DefaultMonthRange(c *echo.Context) (time.Time, time.Time) { return DefaultRange(c, 30) }

// NoContent renders a 204.
func NoContent(c *echo.Context) error { return c.NoContent(http.StatusNoContent) }

// ---- Stateful helpers (take explicit deps instead of a god-type receiver) --

// ResolveOwnerFromCookie resolves the owner from the HttpOnly refresh_token
// cookie (used by /auth/refresh_token, /auth/users/current, and the WebSocket
// handshake, which cannot carry an Authorization header). missingErr is the
// error returned when the cookie is absent — the auth endpoints report
// MissingRefreshTokenCookie while the WS handshake treats an absent cookie
// the same as an expired one. An unknown/expired token is always
// ExpiredRefreshToken.
func ResolveOwnerFromCookie(database *db.DB, logger *slog.Logger, c *echo.Context, missingErr *apierr.Error) (string, *apierr.Error) {
	refresh, ok := auth.ParseRefreshCookie(c.Request().Header.Get("Cookie"))
	if !ok {
		return "", missingErr
	}
	owner, ok, err := database.GetUserByRefreshToken(c.Request().Context(), refresh)
	if err != nil {
		logger.Error("refresh token lookup failed", "path", c.Request().URL.Path, "err", err)
		return "", apierr.Generic()
	}
	if !ok {
		return "", apierr.ExpiredRefreshToken()
	}
	return owner, nil
}

// ResolveUser maps a token to its owning username (Db.getUserByToken).
// Returns InvalidToken (401) if the token has no owner (UnknownApiToken).
func ResolveUser(database *db.DB, c *echo.Context) (string, string, *apierr.Error) {
	tkn, aerr := TokenFromHeader(c)
	if aerr != nil {
		return "", "", aerr
	}
	owner, ok, err := database.GetUserByToken(c.Request().Context(), tkn)
	if err != nil {
		return "", "", apierr.Generic()
	}
	if !ok {
		return "", "", apierr.InvalidToken()
	}
	return tkn, owner, nil
}

// Identify resolves the caller's *auth.Identity from the bearer token — the
// user-model seam (gaka-0oe.1). With BOOM_FEATURE_USER_MODEL OFF it returns an
// all-capability Identity, so no gate ever fires and behavior is identical to
// the pre-substrate ResolveUser path. With the flag ON it loads the user's
// role + capabilities and fails closed on a disabled account.
//
// This is the shape the 70-site handler refactor (gaka-0oe.4–.9) migrates onto;
// it is intentionally additive — ResolveUser stays for callers not yet
// converted. The user-model switch is read from the process-global
// auth.UserModelEnabled() (set once at boot from cfg.FeatureUserModel) rather
// than passed in, so this package stays config-free/cycle-free AND the ~103
// call sites don't have to thread the flag through three different handler
// config shapes.
func Identify(database *db.DB, c *echo.Context) (*auth.Identity, *apierr.Error) {
	// Standalone single-tenant short-circuit (gaka-zp2s books-standalone). When the
	// STANDALONE catalyst-books binary pins a fixed owner (auth.SetStandaloneOwner,
	// from BOOM_STANDALONE_OWNER), there is NO auth stack — no tokens, cookies, or
	// users-model to resolve against — so every caller IS that one owner. Return a
	// synthetic all-caps Identity for it WITHOUT any header parse or DB lookup.
	// The boomtime HOST never sets a standalone owner, so StandaloneOwner() is
	// false there and this branch is dead — the host path below is 100% unchanged.
	if owner, ok := auth.StandaloneOwner(); ok {
		return auth.AllCapsIdentity(owner), nil
	}
	// auth-dry Phase 1: the auth middleware (internal/server) resolves the
	// bearer token → Identity ONCE per request and stashes it via SetIdentity.
	// Return that instead of resolving again — it eliminates the second
	// GetUserByToken (+ GetUserFullByName when user-model is on) round-trip that
	// this call used to make on top of the middleware's own resolution.
	if ident, ok := cachedIdentity(c); ok {
		return ident, nil
	}
	// Fallback (no cache): the middleware didn't run (bare-context unit tests),
	// or the credential was absent/invalid — the middleware only stashes a
	// SUCCESSFUL resolution, so the MissingAuth / InvalidToken envelope is
	// (re)produced here. apihelpers owns header parsing (it has the echo
	// context); the active auth provider (gaka-0oe.2) owns token→Identity.
	token, aerr := TokenFromHeader(c)
	if aerr != nil {
		return nil, aerr
	}
	return auth.CurrentResolver().ResolveBearer(c.Request().Context(), database, token)
}

// identityCtxKey is the echo per-request store key under which the auth
// middleware stashes the resolved Identity. Namespaced to avoid collisions with
// other c.Set users.
const identityCtxKey = "boomtime.auth.identity"

// SetIdentity stashes a request's resolved Identity so Identify() can return it
// without a second token→DB resolution. Called once per request by the auth
// middleware in internal/server. Passing a nil identity is a no-op.
func SetIdentity(c *echo.Context, ident *auth.Identity) {
	if ident == nil {
		return
	}
	c.Set(identityCtxKey, ident)
}

// cachedIdentity returns the middleware-stashed Identity for this request, if
// one was set. The bool is false when no middleware ran or the request was
// unauthenticated (Identify then falls back to a live resolve).
func cachedIdentity(c *echo.Context) (*auth.Identity, bool) {
	if v := c.Get(identityCtxKey); v != nil {
		if ident, ok := v.(*auth.Identity); ok && ident != nil {
			return ident, true
		}
	}
	return nil, false
}

// IdentifyFromCookie is the refresh-cookie counterpart of Identify (used by
// the /auth/users/current + WebSocket paths that can't send an Authorization
// header). Same flag semantics; missingErr is returned when the cookie is
// absent. logger is retained for signature compatibility with the callers.
func IdentifyFromCookie(database *db.DB, logger *slog.Logger, c *echo.Context, missingErr *apierr.Error) (*auth.Identity, *apierr.Error) {
	_ = logger
	refresh, ok := auth.ParseRefreshCookie(c.Request().Header.Get("Cookie"))
	if !ok {
		return nil, missingErr
	}
	return auth.CurrentResolver().ResolveCookie(c.Request().Context(), database, refresh)
}

// RequireCap is per-route authorization middleware (auth-dry Phase 2). Attach
// it at route registration so a tier-gated capability is a DECLARATIVE
// annotation on the route instead of hand-rolled Identify + `if !ident.Can(cap)`
// boilerplate in every handler body:
//
//	ingest write routes:  h.Register(e) → e.POST(path, h.HeartbeatBulk,
//	    apihelpers.RequireCap(h.DB, auth.CapIngestHeartbeats, "ingest data"))
//
// It reuses the middleware-cached Identity (Phase 1), so it costs no extra DB
// round-trip on the happy path. Semantics are byte-identical to the old inline
// gate: authentication failures surface as the usual MissingAuth (400) /
// InvalidToken (401) from Identify; a resolved-but-unpermitted identity gets
// apierr.Forbidden ("this account is not permitted to <action>"). Enforcing
// here (before the handler binds the body) is a strict improvement — an
// unauthorized caller's payload is never parsed. Flag off ⇒ all-caps ⇒ every
// RequireCap passes, exactly as before.
func RequireCap(database *db.DB, capability auth.Capability, action string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			ident, aerr := Identify(database, c)
			if aerr != nil {
				return RespondErr(c, aerr)
			}
			if !ident.Can(capability) {
				return RespondErr(c, apierr.Forbidden("this account is not permitted to "+action))
			}
			return next(c)
		}
	}
}

// IdentifyOwner is the owner-only convenience over Identify (gaka-0oe.4–.9).
// For the many account/settings/read handlers that need the caller's username
// + the disabled-fail-closed guarantee but have NO tier-specific capability
// gate (every non-disabled identity may use them), this is a one-line swap for
// the old `_, owner, aerr := ResolveUser(...)`. Tier-gated handlers
// (ingest/import/backup/curate) use the full Identify + ident.Can(cap) instead.
func IdentifyOwner(database *db.DB, c *echo.Context) (string, *apierr.Error) {
	ident, aerr := Identify(database, c)
	if aerr != nil {
		return "", aerr
	}
	return ident.Username, nil
}

// IdentifyOwnerFromCookie is the refresh-cookie counterpart of IdentifyOwner.
func IdentifyOwnerFromCookie(database *db.DB, logger *slog.Logger, c *echo.Context, missingErr *apierr.Error) (string, *apierr.Error) {
	ident, aerr := IdentifyFromCookie(database, logger, c, missingErr)
	if aerr != nil {
		return "", aerr
	}
	return ident.Username, nil
}

// InternalErr logs the underlying error with request context and renders the
// generic 500 envelope. Use it wherever an internal failure would otherwise
// be swallowed silently — the raw error never reaches the client.
func InternalErr(logger *slog.Logger, c *echo.Context, msg string, err error) error {
	logger.Error(msg, "path", c.Request().URL.Path, "err", err)
	return RespondErr(c, apierr.Generic())
}

// CachedJSON serves a cached payload for key, or computes+caches it. On a
// compute error it logs and renders the generic error envelope.
func CachedJSON(cch *cache.TTL, logger *slog.Logger, c *echo.Context, key string, compute func() (any, error)) error {
	if b, ok := cch.Get(key); ok {
		return c.JSONBlob(http.StatusOK, b)
	}
	payload, err := compute()
	if err != nil {
		logger.Error("aggregation query failed", "key", key, "err", err)
		return RespondErr(c, apierr.Generic())
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return RespondErr(c, apierr.Generic())
	}
	cch.Set(key, b)
	return c.JSONBlob(http.StatusOK, b)
}

// CachedBlob is CachedJSON's non-JSON sibling: serve a cached byte blob for
// key, or compute+cache it. Used by the public widget SVG endpoint — the key
// is owner-prefixed, so an invalidateOwnerCache upstream busts stale widget
// renders after curation changes just like it busts dashboard payloads.
func CachedBlob(cch *cache.TTL, logger *slog.Logger, c *echo.Context, key, contentType string, compute func() ([]byte, error)) error {
	if b, ok := cch.Get(key); ok {
		return c.Blob(http.StatusOK, contentType, b)
	}
	b, err := compute()
	if err != nil {
		logger.Error("blob compute failed", "key", key, "err", err)
		return RespondErr(c, apierr.Generic())
	}
	cch.Set(key, b)
	return c.Blob(http.StatusOK, contentType, b)
}

// InvalidateOwnerCache drops all cached aggregation payloads for a user so
// hide/rename changes take effect immediately.
func InvalidateOwnerCache(cch *cache.TTL, owner string) {
	if cch != nil {
		cch.InvalidatePrefix(owner + "|")
	}
}

// ptrStr is a tiny helper to embed a scalar in the apierr Extra field.
func ptrStr(s string) *string { return &s }

// RemoveDays / AddDays: UTC-normalized date arithmetic. Exported so
// per-domain packages that build historical windows (identity public
// profile, awards evaluator, widgets renderer, admin backfill) can share
// one implementation.
func RemoveDays(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -n)
}
func AddDays(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n)
}

func removeDays(t time.Time, n int) time.Time { return RemoveDays(t, n) }
func addDays(t time.Time, n int) time.Time    { return AddDays(t, n) }

// ---- Cache-key helpers --------------------------------------------------

// CacheKeyTimeBucket is the granularity time.Time parts are truncated to
// when building cache keys. Without bucketing, default-range requests
// (whose end is time.Now()) mint a fresh key every second, so the TTL
// cache never hits and only accumulates dead entries. Aligned with the
// default 30s stats TTL. Only the KEY is bucketed — the actual query
// range is untouched.
const CacheKeyTimeBucket = 30 * time.Second

// CacheKey builds a stable cache key: "owner|name|part|part...". time.Time
// parts are truncated to CacheKeyTimeBucket. Byte-identical to the
// pre-refactor per-domain implementations that lived in
// internal/handler/handler.go, internal/widgets/helpers.go,
// internal/stats/handler.go, and internal/admin/handler.go — collapsed
// here so a key-format regression can only happen in one file.
func CacheKey(owner, name string, parts ...any) string {
	var b strings.Builder
	b.WriteString(owner)
	b.WriteByte('|')
	b.WriteString(name)
	for _, p := range parts {
		b.WriteByte('|')
		if t, ok := p.(time.Time); ok {
			fmt.Fprintf(&b, "%d", t.Truncate(CacheKeyTimeBucket).Unix())
		} else {
			fmt.Fprint(&b, p)
		}
	}
	return b.String()
}

// ---- Timezone resolver --------------------------------------------------

// ResolveUserTZ returns the effective IANA name for a user's dow/hour/date
// buckets. NEVER returns "" — safe to thread into an AT TIME ZONE bind
// param without further guarding. On a DB lookup failure we log and fall
// through to the operator default (or UTC) so a transient blip on the
// users row doesn't break every stats query for that request.
//
// Collapsed from per-domain copies that used to live on *handler.Handler,
// *stats.Handler, *widgets.Handler, and *awards.Handler — all four were
// byte-identical (gaka-dg7).
func ResolveUserTZ(database *db.DB, logger *slog.Logger, ctx context.Context, owner, defaultTZ string) string {
	userTZ, err := database.GetUserTimezone(ctx, owner)
	if err != nil {
		logger.Warn("resolveUserTZ: users.timezone lookup failed; falling back to defaults",
			"user", owner, "err", err)
		userTZ = ""
	}
	return db.ResolveTimezone(userTZ, defaultTZ)
}

// ---- Space loader --------------------------------------------------------

// LoadSpace resolves the optional ?space=<id> scope for a dashboard
// request. It returns the space's MemberSets, whether a space was
// requested (spaceParam was a valid id), and any load error. An
// absent/blank/invalid param means "unscoped" (spaceRequested=false).
// Membership is loaded by id only; an id that isn't the requester's
// simply yields an empty MemberSets, which — with spaceRequested=true —
// scopes the dashboard to nothing (match-nothing), never another owner's
// data.
//
// Collapsed from the byte-identical copies previously on *handler.Handler
// and *stats.Handler.
func LoadSpace(database *db.DB, ctx context.Context, spaceParam string) (db.MemberSets, bool, error) {
	if spaceParam == "" {
		return db.MemberSets{}, false, nil
	}
	id, err := strconv.Atoi(spaceParam)
	if err != nil {
		return db.MemberSets{}, false, nil
	}
	ms, err := database.LoadMemberSets(ctx, id)
	if err != nil {
		return db.MemberSets{}, false, err
	}
	return ms, true, nil
}
