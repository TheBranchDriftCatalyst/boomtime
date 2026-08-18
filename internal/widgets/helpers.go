// Package widgets holds the widget + widget-def + badge HTTP endpoints
// (gaka-8tn phase 3). Split off from internal/handler as a self-contained
// domain package: its own Handler struct, its own route registrar, and its
// own tests.
//
// The renderer library used to produce SVG bytes still lives at
// internal/widget/ (singular) — this package (plural) is the HTTP surface;
// widget (singular) is the pure rendering primitives.
//
// Shared HTTP helpers (RespondErr / ResolveUser / QueryInt64 /
// BindJSONWithLimit / CachedBlob / CacheKey / ResolveUserTZ / ...) live
// in internal/apihelpers/ — this package imports that instead of
// carrying per-file shims (gaka-8tn phase 8 collapse).
package widgets

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// Handler bundles the shared dependencies the widgets HTTP surface needs.
// Fields are the subset of the god Handler this domain actually reads:
// DB for widget/badge/link/def CRUD + curation lookups + timezone resolution,
// Cfg for BadgeURL + DefaultTimezone, Cache for the cached-SVG blob store,
// Logger for error/debug logs. Nothing else — no importer, no hub, no worker.
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

// httpClient is the shared client for all outbound HTTP calls (shields.io,
// GitHub, remote-write). http.DefaultClient has no timeout and can hang a
// handler forever on a stuck upstream.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// removeDays subtracts n days from t, snapped to UTC midnight. Thin
// package-local shim over apihelpers.RemoveDays — kept only because the
// widget renderer files pass t.Add(-days)/-form dates around and calling
// `apihelpers.RemoveDays(t, n)` at every callsite would balloon the diff
// for one-line arithmetic that shipped byte-identical here at extraction.
func removeDays(t time.Time, n int) time.Time { return apihelpers.RemoveDays(t, n) }
