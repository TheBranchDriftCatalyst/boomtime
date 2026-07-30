// Package ingest owns the write-path for boomtime telemetry: heartbeats
// (single + bulk), workouts (single + bulk), health samples (single +
// bulk), and the read-side inspector endpoints that live alongside them —
// heartbeats latest / group / list (audit views for the Explorer) and the
// Entity Explorer (list + REDACT).
//
// Extracted from internal/handler/ as part of gaka-8tn phase 5a. Domain
// scope covers ONLY the ingest write surface and its adjacent explorer
// reads; broader dashboard / stats / rollup rebuild helpers stay put.
//
// SECURITY POSTURE: every write endpoint here binds JSON under a
// bounded MaxBytesReader cap (apihelpers.BodyLimitLarge for the batched
// telemetry writes; apihelpers.BodyLimitMedium for the redact body) so
// an authenticated hostile client cannot OOM the process by streaming
// a multi-GB body. The RedactEntities endpoint requires an explicit
// ?confirm=redact-entities sentinel — the belt-and-braces guard already
// used by the DB restore endpoint — so a stray fetch cannot scrub rows.
//
// DB QUERIES STAY IN internal/db/: the receiver methods this package
// calls (SaveHeartbeats, SaveWorkouts, SaveHealthSamples, LatestHeartbeat,
// GroupHeartbeats, ListHeartbeats, ListEntitiesByType, RedactEntities)
// remain on *db.DB because they either have non-ingest callers
// (SaveHeartbeats is called from internal/importer; RefreshRollup /
// RecomputeGaps / GetDerivedStatus / ResyncDerived are called from
// identity/timezone, handler/backup, handler/derived, and testutil/seed)
// or share unexported helpers (ExploreColumn, buildFilterClause) with
// cross-package callers in curation + spaces. Only handlers move here in
// phase 5a — the DB slice defers to phase 8 collapse, mirroring the
// identity phase 4a precedent.
//
// Shared helpers live in internal/apihelpers/ — this package imports
// that instead of carrying per-file shims.
package ingest

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
// dependencies that the ingest domain actually reads. Everything else
// stays out of this package.
//
//   - DB     — every heartbeat / workout / health_sample / entity read + write
//   - Cfg    — RemoteWrite forwarding target (best-effort mirror of ingest)
//   - Logger — persistent-store failure log lines, remote-write debug lines,
//     goal invalidation warnings
//   - Cache  — busted on the workouts / health-samples / entity-redact write
//     paths so the Wellness/Explorer cards pick up new state on next fetch
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
	Cache  *cache.TTL
}

// New constructs an ingest.Handler with the passed-in shared deps. Every
// field is required in production; nil-checks are the caller's responsibility
// (the god-type's New wires all four unconditionally).
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, cch *cache.TTL) *Handler {
	return &Handler{
		DB:     database,
		Cfg:    cfg,
		Logger: logger,
		Cache:  cch,
	}
}

// resolveUser is the ingest-domain adapter over apihelpers.ResolveUser —
// receiver-shaped so the extracted handlers keep their previous signature
// (`h.resolveUser(c)`) unchanged. Every call is line-identical to the
// god-type version.
func (h *Handler) resolveUser(c *echo.Context) (string, string, *apierr.Error) {
	return apihelpers.ResolveUser(h.DB, c)
}

// internalErr is the ingest-domain adapter over apihelpers.InternalErr —
// receiver-shaped so per-handler call sites stay identical.
func (h *Handler) internalErr(c *echo.Context, msg string, err error) error {
	return apihelpers.InternalErr(h.Logger, c, msg, err)
}

// invalidateOwnerCache is the ingest-domain adapter over
// apihelpers.InvalidateOwnerCache — receiver-shaped so workouts.go +
// health_samples.go + entities.go call sites stay identical.
func (h *Handler) invalidateOwnerCache(owner string) {
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
}

// respondErr renders an apierr.Error onto the context. Package-local
// alias for apihelpers.RespondErr so the extracted handler files keep
// their existing `respondErr(c, ...)` call sites unchanged.
func respondErr(c *echo.Context, e *apierr.Error) error {
	return apihelpers.RespondErr(c, e)
}

// queryInt64 is the ingest-domain alias for apihelpers.QueryInt64. Kept as
// a package-local func (not receiver) so entities.go + explore.go call
// sites (`queryInt64(c, "limit", ...)`) stay identical.
func queryInt64(c *echo.Context, name string, def int64) int64 {
	return apihelpers.QueryInt64(c, name, def)
}

// timeLimit reads the optional timeLimit param (minutes), defaulting to 15.
// Package-local alias for apihelpers.TimeLimit — keeps explore.go's
// `timeLimit(c)` call site identical.
func timeLimit(c *echo.Context) int64 {
	return apihelpers.TimeLimit(c)
}

// defaultWeekRange = last 7 days. Package-local alias for
// apihelpers.DefaultWeekRange — keeps explore.go's `defaultWeekRange(c)`
// call site identical.
func defaultWeekRange(c *echo.Context) (time.Time, time.Time) {
	return apihelpers.DefaultWeekRange(c)
}

// BindJSONWithLimit / body-size limits: ingest re-exports the shared
// helpers under package-local aliases so the extracted files keep their
// original call sites (`BindJSONWithLimit(c, &req, BodyLimitLarge)`).
// These are the SAME buckets defined in apihelpers — the aliases keep
// call-site diffs to zero.

// BodyLimitSmall / BodyLimitMedium / BodyLimitLarge: package-local
// aliases over apihelpers so ingest handlers keep their pre-refactor
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

// httpClient is the shared outbound HTTP client for the ingest package's
// remote-write forwarder (heartbeats.go remoteWrite). A dedicated
// timeout avoids http.DefaultClient's unbounded default which would hang
// the forwarder goroutine if the upstream locks up.
//
// EXPOSED as a package-level var so ingest-side tests can swap it out
// via SwapHTTPClientForTest. Not exported directly — tests use the
// SwapHTTPClientForTest seam to keep the mutation site auditable.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// SwapHTTPClientForTest replaces the package-level httpClient (used by
// remoteWrite) and returns a restore func the caller MUST defer. Test-only
// seam — production code never calls this.
func SwapHTTPClientForTest(c *http.Client) func() {
	prev := httpClient
	httpClient = c
	return func() { httpClient = prev }
}
