// Package stats also owns the HTTP surface for boomtime's dashboard
// aggregations (gaka-8tn phase 6): the /stats + /timeline + /statusbar/today
// triad, /derived/status + /derived/resync (rollup health), /stats/{punchcard,
// sessions, momentum, ai, health}, /workouts (event list), /files (cross-project
// active files), /projects + /projects/:project (project scope + list),
// /leaderboards, and /commits/:project/report.
//
// Extracted from internal/handler/ as part of gaka-8tn phase 6 — the
// highest-risk phase because it touches the dashboardScope receiver (used by
// every stats endpoint) and the cache-key semantics that every stats endpoint
// bucketizes against.
//
// PRE-EXISTING PACKAGE: internal/stats already existed as the payload-shaping
// library (ToStatsPayload / ToTimelinePayload / ToLeaderboardsPayload /
// ToProjectStatistics / ToPunchcardPayload / ToSessionsPayload /
// ToMomentumPayload / ToActiveFilesPayload / Grade / CompoundDuration).
// This phase adds the HTTP surface on top of that library so the domain is
// self-contained: handler + shaping + DB reads (handled by *db.DB) in one
// package.
//
// DB QUERIES STAY IN internal/db/: the receiver methods this package calls
// (GetUserActivity / GetUserActivityRollup / GetCategoryDaily / GetTimeline /
// GetTotalTimeToday / GetPunchcard / GetSessions / GetMomentum / GetAIActivity /
// GetHealthActivity / GetWorkouts / GetActiveFiles / GetProjectStats /
// GetProjectExtras / GetAllProjects / GetLeaderboards / GetTotalTimeBetween /
// GetDerivedStatus / ResyncDerived / CheckProjectDisplayOwner /
// LoadHiddenSets / LoadRenameSets / LoadMemberSets / GetUserTimezone) remain
// on *db.DB because they have cross-domain callers (identity + awards +
// widgets + spaces + curation all reach into curation loaders + tz; the
// backfill worker calls ResyncDerived). Only handlers move here in phase 6 —
// the DB slice defers to phase 8 collapse, mirroring the identity/awards/
// ingest/curation precedents.
//
// CACHE-KEY SEMANTICS ARE LOAD-BEARING: cacheKey / cacheKeyTimeBucket are
// re-implemented here as a package-local copy (byte-identical to the god-
// type's original) so the same input produces the same key bytes before
// and after this extraction. A key-format regression silently causes every
// request to miss the cache — see phase 6 verification note in the plan.
package stats

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

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// Config is the SUBSET of *config.Config the stats HTTP surface reads. It
// exists as a local interface so stats can hold live config state (needed
// so tests that mutate `hz.Cfg.GithubToken` after handler construction
// still take effect) WITHOUT importing internal/config — internal/config
// imports internal/stats (for stats.GradeConfig), and the reverse import
// would form a cycle. Any config accessor stats needs must show up here.
type Config interface {
	// GithubTokenValue returns the GitHub token used by commits.fetchCommits
	// (Basic auth against api.github.com). Read live per-request so tests
	// that mutate Cfg after construction observe the change.
	GithubTokenValue() string
	// DefaultTimezoneValue returns the operator-configured default IANA
	// zone used by resolveUserTZ's 3-level fallback chain.
	DefaultTimezoneValue() string
}

// Handler bundles the SUBSET of the god-type handler.Handler's dependencies
// that the stats HTTP surface actually reads. Everything else stays out of
// this package.
//
//   - DB     — every stats/timeline/derived/projects/leaderboards/momentum/
//     bigbets/active_files/commits query, plus curation loaders + tz resolver
//   - Cfg    — Config interface (see type doc for import-cycle rationale) —
//     read live so tests that mutate config post-construction observe the
//     change (commits_test relies on this)
//   - Logger — aggregation query failure log lines, derived status/resync log
//     lines, commits fetch warn line, tz resolver fallback warn
//   - Cache  — cachedJSON store for stats/timeline/projects/leaderboards
//     aggregation payloads; busted from derived.resync via invalidateOwnerCache
type Handler struct {
	DB     *db.DB
	Cfg    Config
	Logger *slog.Logger
	Cache  *cache.TTL
}

// New constructs a stats.Handler with the passed-in shared deps. Every
// field is required in production; nil-checks are the caller's responsibility
// (the god-type's New wires all four unconditionally).
func New(database *db.DB, cfg Config, logger *slog.Logger, cch *cache.TTL) *Handler {
	return &Handler{
		DB:     database,
		Cfg:    cfg,
		Logger: logger,
		Cache:  cch,
	}
}

// ---- Adapter shims over apihelpers ---------------------------------------
//
// Each shim is receiver-shaped so the extracted handler files keep their
// pre-refactor call sites unchanged (`h.resolveUser(c)` / `respondErr(c, ...)`
// / etc.). Follow-up phase 8 will collapse call sites to the apihelpers-
// qualified form and delete these shims.

// resolveUser is the stats-domain adapter over apihelpers.ResolveUser.
func (h *Handler) resolveUser(c *echo.Context) (string, string, *apierr.Error) {
	return apihelpers.ResolveUser(h.DB, c)
}

// internalErr is the stats-domain adapter over apihelpers.InternalErr.
func (h *Handler) internalErr(c *echo.Context, msg string, err error) error {
	return apihelpers.InternalErr(h.Logger, c, msg, err)
}

// invalidateOwnerCache is the stats-domain adapter over
// apihelpers.InvalidateOwnerCache — receiver-shaped so derived.go's
// call site stays identical.
func (h *Handler) invalidateOwnerCache(owner string) {
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
}

// respondErr renders an apierr.Error onto the context. Package-local alias
// for apihelpers.RespondErr so the extracted handler files keep their
// existing `respondErr(c, ...)` call sites unchanged.
func respondErr(c *echo.Context, e *apierr.Error) error {
	return apihelpers.RespondErr(c, e)
}

// queryInt64 is the stats-domain alias for apihelpers.QueryInt64. Kept as a
// package-local func (not receiver) so bigbets.go + active_files.go +
// commits.go call sites (`queryInt64(c, "limit", ...)`) stay identical.
func queryInt64(c *echo.Context, name string, def int64) int64 {
	return apihelpers.QueryInt64(c, name, def)
}

// timeLimit reads the optional timeLimit param (minutes), defaulting to 15.
// Package-local alias for apihelpers.TimeLimit — keeps scope.go's
// `timeLimit(c)` call site identical.
func timeLimit(c *echo.Context) int64 {
	return apihelpers.TimeLimit(c)
}

// defaultRange resolves the start/end query params. Package-local alias
// for apihelpers.DefaultRange — keeps scope.go's `defaultRange(c, days)`
// call site identical.
func defaultRange(c *echo.Context, days int) (time.Time, time.Time) {
	return apihelpers.DefaultRange(c, days)
}

// ---- Cache-key semantics (byte-identical to god-type's original) ---------
//
// cacheKeyTimeBucket / cacheKey mirror the shared helpers in internal/handler.
// Kept as a package-local copy so the stats package doesn't depend on the
// parent's private helpers during phase 6. A follow-up phase (8) will
// collapse all copies into internal/apihelpers/. The bytes produced by
// cacheKey MUST match the god-type's cacheKey for the same input —
// verified in phase 6 by cross-package echo.

const cacheKeyTimeBucket = 30 * time.Second

// cacheKey builds a stable cache key: "owner|name|part|part...". time.Time
// parts are truncated to cacheKeyTimeBucket. Byte-identical to the
// pre-refactor implementation in internal/handler/handler.go.
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

// cachedJSON serves a cached payload for key, or computes+caches it. On a
// compute error it logs and renders the generic error envelope. Byte-
// identical body semantics to the god-type's cachedJSON.
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

// httpClient is the shared client for outbound HTTP calls from stats
// endpoints — currently only commits.go's GitHub fetcher.
// http.DefaultClient has no timeout and can hang a handler forever on a
// stuck upstream.
//
// Package-level var so tests can swap it out via SwapHTTPClientForTest.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// SwapHTTPClientForTest replaces the package-level httpClient (used by
// commits.fetchCommits) and returns a restore func the caller MUST defer.
// Test-only seam — production code never calls this.
func SwapHTTPClientForTest(c *http.Client) func() {
	prev := httpClient
	httpClient = c
	return func() { httpClient = prev }
}

// ---- resolveUserTZ ------------------------------------------------------
//
// Kept receiver-shaped and package-local because scope.go's dashboardScope
// constructor calls it AND statusbar reads it directly. The identity /
// awards / widgets copies are byte-identical — plan calls for collapse in
// phase 8.

// resolveUserTZ returns the effective IANA name for a user's dow/hour/date
// buckets. NEVER returns "" — safe to thread into an AT TIME ZONE bind
// param without further guarding. On a DB lookup failure we log and fall
// through to the operator default (or UTC) so a transient blip on the
// users row doesn't break every stats query for that request.
func (h *Handler) resolveUserTZ(ctx context.Context, owner string) string {
	userTZ, err := h.DB.GetUserTimezone(ctx, owner)
	if err != nil {
		h.Logger.Warn("resolveUserTZ: users.timezone lookup failed; falling back to defaults",
			"user", owner, "err", err)
		userTZ = ""
	}
	return db.ResolveTimezone(userTZ, h.Cfg.DefaultTimezoneValue())
}

// ---- loadSpace ----------------------------------------------------------
//
// Kept receiver-shaped and package-local because scope.go's dashSets.load
// calls it. Byte-identical to the pre-refactor copy in
// internal/handler/handler.go.

// loadSpace resolves the optional ?space=<id> scope for a dashboard request.
// It returns the space's MemberSets, whether a space was requested
// (spaceParam was a valid id), and any load error. An absent/blank/invalid
// param means "unscoped" (spaceRequested=false). Membership is loaded by
// id only; an id that isn't the requester's simply yields an empty
// MemberSets, which — with spaceRequested=true — scopes the dashboard to
// nothing (match-nothing), never another owner's data.
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
