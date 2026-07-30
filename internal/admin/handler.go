// Package admin owns the HTTP surface for operator/observability
// endpoints: label-image regeneration (gaka-myv / gaka-8bz), git-history
// backfill (gaka-vh8), whole-DB backup + destructive restore, the
// wakatime.com import cluster, source-health observability, and the
// public label-image GET (the read-only face of the same subsystem).
//
// Extracted from internal/handler/ as part of gaka-8tn phase 7. Domain
// scope covers the operator/admin/observability surface plus the small
// public read that pairs with the label-images admin flow. Anything
// that WRITES heartbeats via SaveHeartbeats (importer, backfill worker)
// stays a leaf under internal/importer or internal/backfill/git — the
// admin domain USES those, it does not OWN them.
//
// SECURITY POSTURE: every admin-gated endpoint runs requireAdmin BEFORE
// reading the body so a non-admin request never costs a body allocation.
// Non-admin JSON endpoints (import cluster, backup export/import) still
// cap the body via BindJSONWithLimit / http.MaxBytesReader. The
// destructive restore endpoint (DBImport) demands an explicit
// ?confirm=replace-all-data sentinel — the belt-and-braces guard used
// throughout boomtime for TRUNCATE-shaped writes.
//
// DB QUERIES STAY IN internal/db/: the receiver methods this package
// calls (ListLabelImagesMeta, TruncateLabelImages, GetLabel, ListLabels,
// GetLabelImage; GetBackfillConfig / SetBackfillConfig / BackfillStatsFor
// / InsertBackfillBatch / PreviewBackfillBatch / DeleteBackfilledHeartbeats;
// DumpAll / RestoreAll / Senders / ResyncDerived / HasActiveImportJobs;
// GetEncryptedWakatimeKey; CreateImportJob / GetJobsByOwner / GetJobByID /
// GetJobLogs / CancelJob / MarkRunningJobsFailed; ListSourceHealth)
// remain on *db.DB because they either have non-admin callers (Senders,
// ResyncDerived, GetLabel, ListLabels are called from ingest/curation/
// worker paths; GetEncryptedWakatimeKey is also called from the
// importer worker) or share unexported helpers across packages. Only
// handlers move here in phase 7 — the DB slice defers to phase 8
// collapse, mirroring the identity/awards/ingest/curation/stats
// precedents.
//
// Shared helpers live in internal/apihelpers/ — this package imports
// that instead of carrying per-file shims.
package admin

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/backfilljobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/imagejobs"
	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/worker/labelimages"
)

// Handler bundles the SUBSET of the god-type handler.Handler's
// dependencies that the admin domain actually reads. Everything else
// stays out of this package.
//
//   - DB     — every admin/backfill/import/backup read + write, plus the
//     public label-image GET and source-health list
//   - Cfg    — admin allowlist (IsAdmin), label-images feature flags,
//     ComfyUI shim config, and the server-side Wakatime API key fallback
//     used by the import cluster
//   - Logger — export/restore success + failure logging, warn lines from
//     the wakatime range/token lookups, import job progress + WS blips
//   - Cache  — busted on backfill writes (heartbeat inserts + backfill
//     row deletions) and on the RestoreAll path so dashboards + widgets
//     pick up the new state on the next fetch
//   - Worker + Hub — the durable import-job worker + fan-out hub for the
//     wakatime.com import cluster. Both non-nil in production; the god
//     type's New wires them unconditionally
//   - LabelImagesWorker + ImageJobQueue — set AFTER construction by
//     cmd/boomtime once the label-images feature initializes (nil is a
//     supported production configuration = feature disabled; the
//     handlers detect nil and return 503)
//   - BackfillJobQueue — the in-memory registry backing the git-history
//     backfill CLI flow (gaka-vh8). Always non-nil in prod; kept as an
//     injected pointer for symmetry with the label-images queue and so
//     tests can wire a per-test registry with tight retention
type Handler struct {
	DB               *db.DB
	Cfg              *config.Config
	Logger           *slog.Logger
	Cache            *cache.TTL
	Worker           *importer.Worker
	Hub              *importer.Hub
	LabelImagesWorker *labelimages.Worker
	ImageJobQueue    *imagejobs.Registry
	BackfillJobQueue *backfilljobs.Registry
}

// New constructs an admin.Handler with the passed-in shared deps.
// LabelImagesWorker / ImageJobQueue / BackfillJobQueue are wired
// AFTER construction via the corresponding Set* methods (called from
// cmd/boomtime once the workers/queues initialize). Every other field
// is required in production; nil-checks are the caller's responsibility
// (the god-type's New wires them unconditionally).
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, cch *cache.TTL, worker *importer.Worker, hub *importer.Hub) *Handler {
	return &Handler{
		DB:     database,
		Cfg:    cfg,
		Logger: logger,
		Cache:  cch,
		Worker: worker,
		Hub:    hub,
	}
}

// SetLabelImagesWorker wires the label-images worker after construction.
// Called by cmd/boomtime once NewWorker succeeds; nil is fine when the
// feature is disabled — admin handlers detect the nil worker and return
// 503 Service Unavailable with a clear "feature disabled" message.
func (h *Handler) SetLabelImagesWorker(w *labelimages.Worker) {
	h.LabelImagesWorker = w
}

// SetImageJobQueue wires the imagejobs.Registry after construction.
// Called by cmd/boomtime when the label-images feature is on so the
// admin regen endpoint + WS stream have somewhere to enqueue jobs.
// Nil = feature off.
func (h *Handler) SetImageJobQueue(r *imagejobs.Registry) {
	h.ImageJobQueue = r
}

// SetBackfillJobQueue wires the backfilljobs.Registry (gaka-vh8).
// Always non-nil in prod; kept as a setter for symmetry with
// SetImageJobQueue and so tests can inject a per-test registry with
// tight retention.
func (h *Handler) SetBackfillJobQueue(r *backfilljobs.Registry) {
	h.BackfillJobQueue = r
}

// resolveUser is the admin-domain adapter over apihelpers.ResolveUser —
// receiver-shaped so the extracted handlers keep their previous
// signature (`h.resolveUser(c)`) unchanged. Every call is line-identical
// to the god-type version.
func (h *Handler) resolveUser(c *echo.Context) (string, string, *apierr.Error) {
	return apihelpers.ResolveUser(h.DB, c)
}

// resolveOwnerFromCookie is the admin-domain adapter over
// apihelpers.ResolveOwnerFromCookie — receiver-shaped so per-handler
// call sites (WS handshake handlers, which cannot carry Authorization)
// stay identical.
func (h *Handler) resolveOwnerFromCookie(c *echo.Context, missingErr *apierr.Error) (string, *apierr.Error) {
	return apihelpers.ResolveOwnerFromCookie(h.DB, h.Logger, c, missingErr)
}

// internalErr is the admin-domain adapter over apihelpers.InternalErr —
// receiver-shaped so per-handler call sites stay identical.
func (h *Handler) internalErr(c *echo.Context, msg string, err error) error {
	return apihelpers.InternalErr(h.Logger, c, msg, err)
}

// invalidateOwnerCache is the admin-domain adapter over
// apihelpers.InvalidateOwnerCache — receiver-shaped so admin_backfill.go
// call sites stay identical.
func (h *Handler) invalidateOwnerCache(owner string) {
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
}

// requireAdmin: 401 without a token, 403 when not on the admin allowlist.
// Returns the resolved owner on success. Mirror of the same method on
// *handler.Handler and *curation.Handler / *identity.Handler —
// duplicated here because the admin label-images + backfill endpoints
// gate on it. All copies stay byte-identical until phase 8 collapses
// them into internal/apihelpers.
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

// queryInt64 is the admin-domain alias for apihelpers.QueryInt64. Kept
// as a package-local func (not receiver) so import.go's call sites
// (`queryInt64(c, "afterId", ...)`) stay identical.
func queryInt64(c *echo.Context, name string, def int64) int64 {
	return apihelpers.QueryInt64(c, name, def)
}

// cacheKeyTimeBucket / cacheKey mirror the shared helpers in
// internal/handler + internal/stats. Kept as a package-local copy so
// admin doesn't depend on the parent's private helpers during phase 7.
// A follow-up phase (8) will collapse all copies into
// internal/apihelpers/. The bytes produced by cacheKey MUST match the
// god-type's cacheKey for the same input.
const cacheKeyTimeBucket = 30 * time.Second

// cacheKey builds a stable cache key: "owner|name|part|part...".
// time.Time parts are truncated to cacheKeyTimeBucket. Byte-identical
// to the pre-refactor implementation in internal/handler/handler.go.
// SourceHealth is the only caller today (owner-prefixed, name-only —
// no time parts) but keep the general shape so a future admin read
// that wants a time-bucketed key doesn't need a second helper.
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

// BindJSONWithLimit / body-size limits: admin re-exports the shared
// helpers under package-local aliases so the extracted files keep their
// original call sites (`BindJSONWithLimit(c, &req, BodyLimitSmall)`).
// These are the SAME buckets defined in apihelpers — the aliases keep
// call-site diffs to zero.

// BodyLimitSmall / BodyLimitMedium / BodyLimitLarge: package-local
// aliases over apihelpers so admin handlers keep their pre-refactor
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

// cachedJSON serves a cached payload for key, or computes+caches it.
// Package-local receiver adapter over apihelpers.CachedJSON so
// sources.go's `h.cachedJSON(c, key, compute)` call site stays byte-
// identical.
func (h *Handler) cachedJSON(c *echo.Context, key string, compute func() (any, error)) error {
	return apihelpers.CachedJSON(h.Cache, h.Logger, c, key, compute)
}
