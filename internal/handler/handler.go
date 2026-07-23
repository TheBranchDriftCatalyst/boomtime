// Package handler holds the Echo HTTP handlers. Handlers stay thin: they parse
// requests, delegate to db/stats, and render the exact hakatime JSON shapes.
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logging"
	"github.com/labstack/echo/v5"
)

// Handler bundles shared dependencies for all HTTP handlers.
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
	Worker *importer.Worker
	Hub    *importer.Hub
	// LogHub streams the server process's own slog records to the Logs tab.
	LogHub *logging.LogHub
	Cache  *cache.TTL
	// StartTime is set at handler construction; /healthz reports uptime from it.
	StartTime time.Time
}

// New constructs a Handler. logHub streams server-process slog records to the
// Logs tab; pass nil to disable (Logs endpoints handle a nil hub — see
// handler/logs.go).
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, worker *importer.Worker, hub *importer.Hub, logHub *logging.LogHub) *Handler {
	return &Handler{
		DB:        database,
		Cfg:       cfg,
		Logger:    logger,
		Worker:    worker,
		Hub:       hub,
		LogHub:    logHub,
		Cache:     cache.New(statsCacheTTL()),
		StartTime: time.Now(),
	}
}

// statsCacheTTL is the TTL for cached aggregation payloads (stats/timeline/
// projects/leaderboards). Default 30s; tunable via BOOM_STATS_CACHE_TTL (seconds,
// 0 disables). Short enough that dashboards stay near-live, long enough to absorb
// repeated loads and re-renders.
func statsCacheTTL() time.Duration {
	if v := os.Getenv("BOOM_STATS_CACHE_TTL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 30 * time.Second
}

// cacheKeyTimeBucket is the granularity time.Time parts are truncated to when
// building cache keys. Without bucketing, default-range requests (whose end is
// time.Now()) mint a fresh key every second, so the TTL cache never hits and
// only accumulates dead entries. Aligned with the default 30s stats TTL. Only
// the KEY is bucketed — the actual query range is untouched.
const cacheKeyTimeBucket = 30 * time.Second

// cacheKey builds a stable cache key: "owner|name|part|part...". time.Time
// parts are truncated to cacheKeyTimeBucket (see above).
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

// cachedJSON serves a cached payload for key, or computes+caches it. On a compute
// error it logs and renders the generic error envelope.
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
// key, or compute+cache it. Used by the public widget SVG endpoint — the key
// is owner-prefixed, so invalidateOwnerCache busts stale widget renders after
// curation changes just like it busts dashboard payloads.
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

// resolveOwnerFromCookie resolves the owner from the HttpOnly refresh_token
// cookie (used by /auth/refresh_token, /auth/users/current, and the WebSocket
// handshake, which cannot carry an Authorization header). missingErr is the
// error returned when the cookie is absent — the auth endpoints report
// MissingRefreshTokenCookie while the WS handshake treats an absent cookie the
// same as an expired one. An unknown/expired token is always ExpiredRefreshToken.
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
// or returns MissingAuth (400) when absent (matches Err.missingAuthError).
func tokenFromHeader(c *echo.Context) (string, *apierr.Error) {
	tkn, ok := auth.ParseAuthHeader(c.Request().Header.Get(echo.HeaderAuthorization))
	if !ok || tkn == "" {
		return "", apierr.MissingAuth()
	}
	return tkn, nil
}

// resolveUser maps a token to its owning username (Db.getUserByToken).
// Returns InvalidToken (403) if the token has no owner (UnknownApiToken).
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

// Body-size caps for authed JSON writes (gaka-bi2). These bucket the request
// body reads into three sizes so a hostile client can't force the server to
// materialize a huge blob and then run an expensive verify step (argon2 on
// change-password was the motivating amplifier — a 10 MiB body pinned ~256 MiB
// per verify). Applied per-handler via BindJSONWithLimit so the cap sits next
// to the deserialization, not hidden in middleware.
//
// Buckets:
//   - Small (4 KiB): auth credentials, single-field secrets, small JSON toggles.
//   - Medium (64 KiB): JSON-config endpoints that can carry a modest list of
//     rules, member sets, or spec blobs (curation, spaces, widget-defs).
//   - Heartbeat/telemetry ingest is left uncapped here — those bulk endpoints
//     legitimately carry batched telemetry and are handled elsewhere.
const (
	BodyLimitSmall  int64 = 4 * 1024
	BodyLimitMedium int64 = 64 * 1024
)

// BindJSONWithLimit wraps c.Bind with a http.MaxBytesReader cap on the request
// body. On oversize input the Body read fails FAST — before json.Decode has to
// allocate the tail — and we render 413 Payload Too Large with the exact limit
// the client blew. On normal parse errors the returned *apierr.Error is a 400
// so callers can keep their existing "invalid request body" response text.
//
// The cap MUST be set BEFORE c.Bind runs because Echo's binder reads the body
// eagerly. MaxBytesReader also caps ContentLength when present, so a hostile
// client that lies about size still gets rejected on the first read.
func BindJSONWithLimit(c *echo.Context, dst any, limit int64) *apierr.Error {
	r := c.Request()
	// echo v5's *Response satisfies http.ResponseWriter; MaxBytesReader uses
	// it only to allow closing the connection after a limit breach.
	r.Body = http.MaxBytesReader(c.Response(), r.Body, limit)
	if err := c.Bind(dst); err != nil {
		// http.MaxBytesReader returns an error whose message contains
		// "http: request body too large" when the cap is exceeded. Echo's
		// binder surfaces that verbatim from json.Decoder.Decode.
		if strings.Contains(err.Error(), "request body too large") {
			return apierr.New(http.StatusRequestEntityTooLarge, "payload too large", ptrStr(fmt.Sprintf("limit=%d", limit)))
		}
		return apierr.BadRequest("Invalid request body")
	}
	return nil
}

// ptrStr is a tiny helper to embed a scalar in the apierr Extra field.
func ptrStr(s string) *string { return &s }

// internalErr logs the underlying error with request context and renders the
// generic 500 envelope. Use it wherever an internal failure would otherwise be
// swallowed silently — the raw error never reaches the client.
func (h *Handler) internalErr(c *echo.Context, msg string, err error) error {
	h.Logger.Error(msg, "path", c.Request().URL.Path, "err", err)
	return respondErr(c, apierr.Generic())
}

// httpClient is the shared client for all outbound HTTP calls (shields.io,
// GitHub, remote-write). http.DefaultClient has no timeout and can hang a
// handler forever on a stuck upstream.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// invalidateOwnerCache drops all cached aggregation payloads for a user so hide/
// rename changes take effect immediately.
func (h *Handler) invalidateOwnerCache(owner string) {
	if h.Cache != nil {
		h.Cache.InvalidatePrefix(owner + "|")
	}
}

// ---- Date-range helpers (shared by stats/projects/leaderboards) ----

func removeDays(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -n)
}

func addDays(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n)
}

// parseTimeParam parses an RFC3339 query parameter; returns (zero,false) if absent.
func parseTimeParam(c *echo.Context, name string) (time.Time, bool) {
	v := c.QueryParam(name)
	if v == "" {
		return time.Time{}, false
	}
	// Accept RFC3339 (with or without fractional seconds / Z).
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999Z07:00", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// defaultRange resolves the start/end query params, filling the missing side(s)
// with a `days`-long window ending now.
func defaultRange(c *echo.Context, days int) (time.Time, time.Time) {
	now := time.Now().UTC()
	t0, has0 := parseTimeParam(c, "start")
	t1, has1 := parseTimeParam(c, "end")
	switch {
	case !has0 && !has1:
		return removeDays(now, days), now
	case !has0 && has1:
		return removeDays(t1, days), t1
	case has0 && !has1:
		return t0, addDays(t0, days)
	default:
		// Honor the explicit range (supports "All time"); no 1-year clamp.
		return t0, t1
	}
}

// defaultWeekRange = last 7 days (Stats.defaultTimeRange).
func defaultWeekRange(c *echo.Context) (time.Time, time.Time) {
	return defaultRange(c, 7)
}

// defaultMonthRange = last 30 days (leaderboards / project list).
func defaultMonthRange(c *echo.Context) (time.Time, time.Time) {
	return defaultRange(c, 30)
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

// timeLimit reads the optional timeLimit param (minutes), defaulting to 15 (Utils.defaultLimit).
func timeLimit(c *echo.Context) int64 {
	return queryInt64(c, "timeLimit", 15)
}

// noContent renders a 204 (PostNoContent / DeleteNoContent).
func noContent(c *echo.Context) error {
	return c.NoContent(http.StatusNoContent)
}

// loadSpace resolves the optional ?space=<id> scope for a dashboard request. It
// returns the space's MemberSets, whether a space was requested (spaceParam was a
// valid id), and any load error. An absent/blank/invalid param means "unscoped"
// (spaceRequested=false). Membership is loaded by id only; an id that isn't the
// requester's simply yields an empty MemberSets, which — with spaceRequested=true —
// scopes the dashboard to nothing (match-nothing), never another owner's data.
func (h *Handler) loadSpace(ctx context.Context, spaceParam string) (db.MemberSets, bool, error) {
	if spaceParam == "" {
		return db.MemberSets{}, false, nil
	}
	id, err := strconv.Atoi(spaceParam)
	if err != nil {
		return db.MemberSets{}, false, nil
	}
	ms, err := h.DB.LoadMemberSets(ctx, id)
	if err != nil {
		return db.MemberSets{}, false, err
	}
	return ms, true, nil
}
