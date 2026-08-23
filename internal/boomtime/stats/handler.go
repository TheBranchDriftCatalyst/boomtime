// Package stats also owns the HTTP surface for boomtime's dashboard
// aggregations (boom-8tn phase 6): the /stats + /timeline + /statusbar/today
// triad, /derived/status + /derived/resync (rollup health), /stats/{punchcard,
// sessions, momentum, ai, health}, /workouts (event list), /files (cross-project
// active files), /projects + /projects/:project (project scope + list),
// /leaderboards, and /commits/:project/report.
//
// PRE-EXISTING PACKAGE: internal/stats already existed as the payload-shaping
// library (ToStatsPayload / ToTimelinePayload / ToLeaderboardsPayload /
// ToProjectStatistics / ToPunchcardPayload / ToSessionsPayload /
// ToMomentumPayload / ToActiveFilesPayload / Grade / CompoundDuration).
// This phase adds the HTTP surface on top of that library so the domain is
// self-contained: handler + shaping + DB reads (handled by *db.DB) in one
// package.
//
// SHARED HELPERS live in internal/apihelpers/ — cache-key semantics, tz
// resolution, JSON caching, body-limit bucketing, etc. This package
// imports that instead of carrying per-file shims (boom-8tn phase 8
// collapse). The bytes produced by apihelpers.CacheKey for a given
// input match the pre-refactor per-domain cacheKey byte-for-byte, so
// cross-phase cache warm-up is preserved.
package stats

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
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
	// zone used by apihelpers.ResolveUserTZ's 3-level fallback chain.
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
//     aggregation payloads; busted from derived.resync via
//     apihelpers.InvalidateOwnerCache
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
