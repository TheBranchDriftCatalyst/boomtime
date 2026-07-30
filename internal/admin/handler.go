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
// cap the body via apihelpers.BindJSONWithLimit / http.MaxBytesReader. The
// destructive restore endpoint (DBImport) demands an explicit
// ?confirm=replace-all-data sentinel — the belt-and-braces guard used
// throughout boomtime for TRUNCATE-shaped writes.
//
// Shared helpers live in internal/apihelpers/ — this package imports
// that instead of carrying per-file shims (gaka-8tn phase 8 collapse).
package admin

import (
	"log/slog"
	"net/http"

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
	DB                *db.DB
	Cfg               *config.Config
	Logger            *slog.Logger
	Cache             *cache.TTL
	Worker            *importer.Worker
	Hub               *importer.Hub
	LabelImagesWorker *labelimages.Worker
	ImageJobQueue     *imagejobs.Registry
	BackfillJobQueue  *backfilljobs.Registry
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

// requireAdmin: 401 without a token, 403 when not on the admin allowlist.
// Returns the resolved owner on success. Mirror of the same method on
// *identity.Handler / *curation.Handler — the admin label-images +
// backfill endpoints gate on it. Three byte-identical copies survive
// because each domain guards a distinct endpoint and a shared helper
// would need dependency-injection scaffolding bigger than the 8-line
// body itself.
//
// The 403 path deliberately does NOT distinguish "unknown admin config"
// from "not on the list" — both look like a plain 403 to the client.
func (h *Handler) requireAdmin(c *echo.Context) (string, *apierr.Error) {
	_, owner, aerr := apihelpers.ResolveUser(h.DB, c)
	if aerr != nil {
		return "", aerr
	}
	if !h.Cfg.IsAdmin(owner) {
		return "", apierr.New(http.StatusForbidden, "admin only", nil)
	}
	return owner, nil
}
