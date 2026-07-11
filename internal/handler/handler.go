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
	"github.com/labstack/echo/v5"
)

// Handler bundles shared dependencies for all HTTP handlers.
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
	Worker *importer.Worker
	Hub    *importer.Hub
	Cache  *cache.TTL
}

// New constructs a Handler.
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, worker *importer.Worker, hub *importer.Hub) *Handler {
	return &Handler{DB: database, Cfg: cfg, Logger: logger, Worker: worker, Hub: hub, Cache: cache.New(statsCacheTTL())}
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

// cacheKey builds a stable cache key: "owner|name|part|part...".
func cacheKey(owner, name string, parts ...any) string {
	var b strings.Builder
	b.WriteString(owner)
	b.WriteByte('|')
	b.WriteString(name)
	for _, p := range parts {
		b.WriteByte('|')
		if t, ok := p.(time.Time); ok {
			fmt.Fprintf(&b, "%d", t.Unix())
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

// resolveUserFromCookie resolves the owner from the HttpOnly refresh_token cookie
// (used by the WebSocket handshake, which cannot carry an Authorization header).
func (h *Handler) resolveUserFromCookie(c *echo.Context) (string, bool, error) {
	refresh, ok := auth.ParseRefreshCookie(c.Request().Header.Get("Cookie"))
	if !ok {
		return "", false, nil
	}
	return h.DB.GetUserByRefreshToken(c.Request().Context(), refresh)
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

// defaultWeekRange = last 7 days (Stats.defaultTimeRange).
func defaultWeekRange(c *echo.Context) (time.Time, time.Time) {
	now := time.Now().UTC()
	t0, has0 := parseTimeParam(c, "start")
	t1, has1 := parseTimeParam(c, "end")
	switch {
	case !has0 && !has1:
		return removeDays(now, 7), now
	case !has0 && has1:
		return removeDays(t1, 7), t1
	case has0 && !has1:
		return t0, addDays(t0, 7)
	default:
		// Honor the explicit range (supports "All time"); no 1-year clamp.
		return t0, t1
	}
}

// defaultMonthRange = last 30 days (leaderboards / project list).
func defaultMonthRange(c *echo.Context) (time.Time, time.Time) {
	now := time.Now().UTC()
	t0, has0 := parseTimeParam(c, "start")
	t1, has1 := parseTimeParam(c, "end")
	switch {
	case !has0 && !has1:
		return removeDays(now, 30), now
	case !has0 && has1:
		return removeDays(t1, 30), t1
	case has0 && !has1:
		return t0, addDays(t0, 30)
	default:
		// Honor the explicit range (supports "All time"); no 1-year clamp.
		return t0, t1
	}
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
