// Package admin owns the HTTP surface for operator/observability
// endpoints: label-image regeneration (gaka-myv / gaka-8bz), whole-DB
// backup + destructive restore, the wakatime.com import cluster,
// source-health observability, and the public label-image GET (the
// read-only face of the same subsystem).
//
// Extracted from internal/handler/ as part of gaka-8tn phase 7. Domain
// scope covers the operator/admin/observability surface plus the small
// public read that pairs with the label-images admin flow. Anything
// that WRITES heartbeats via SaveHeartbeats (the importer) stays a leaf
// under internal/importer — the admin domain USES it, it does not OWN it.
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

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/queue/imagejobs"
	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/worker/labelimages"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/objstore"
)

// Handler bundles the SUBSET of the god-type handler.Handler's
// dependencies that the admin domain actually reads. Everything else
// stays out of this package.
//
//   - DB     — every admin/import/backup read + write, plus the
//     public label-image GET and source-health list
//   - Cfg    — admin allowlist (IsAdmin), label-images feature flags,
//     ComfyUI shim config, and the server-side Wakatime API key fallback
//     used by the import cluster
//   - Logger — export/restore success + failure logging, warn lines from
//     the wakatime range/token lookups, import job progress + WS blips
//   - Cache  — busted on the RestoreAll path so dashboards + widgets
//     pick up the new state on the next fetch
//   - Worker + Hub — the durable import-job worker + fan-out hub for the
//     wakatime.com import cluster. Both non-nil in production; the god
//     type's New wires them unconditionally
//   - LabelImagesWorker + ImageJobQueue — set AFTER construction by
//     cmd/boomtime once the label-images feature initializes (nil is a
//     supported production configuration = feature disabled; the
//     handlers detect nil and return 503)
type Handler struct {
	DB                *db.DB
	Cfg               *config.Config
	Logger            *slog.Logger
	Cache             *cache.TTL
	Worker            *importer.Worker
	Hub               *importer.Hub
	LabelImagesWorker *labelimages.Worker
	// ImageJobQueue / ImageJobEvents — see the doc comments on the
	// matching fields in handler.Handler (this package's fields exist so
	// the admin domain doesn't need to import the god-type).
	ImageJobQueue  imagejobs.Enqueuer
	ImageJobEvents imagejobs.EventSource
	// JobStore / JobEnqueuer — catalyst-go-jobs (gaka-hney.2). Set after
	// construction; the admin Jobs tab lists from JobStore and triggers/retries
	// via JobEnqueuer. Nil = jobs subsystem not wired (handlers 503).
	JobStore    *jobs.Store
	JobEnqueuer jobs.Enqueuer
	// JobRegistry is the same registry the workers consult — the source of truth
	// for the full set of known kinds (Kinds) and their fleet-wide concurrency
	// caps (Concurrency). The queue-overview endpoint merges these onto the
	// jobs-table aggregates so at-cap back-pressure is visible and idle-but-known
	// kinds still show. Nil = not wired (overview omits caps / registered kinds).
	JobRegistry *jobs.Registry
	// JobLogStore is the object store the GET/DELETE .../logs endpoints read +
	// delete a FINISHED job's persisted log stream from (gaka-hney). Nil = S3 not
	// configured; GET returns 404 (no stored logs) and DELETE is a no-op. Set
	// after construction via SetJobLogStore.
	JobLogStore objstore.Store
}

// New constructs an admin.Handler with the passed-in shared deps.
// LabelImagesWorker / ImageJobQueue are wired
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

// SetImageJobQueue wires the image-job Enqueuer after construction. Called
// by cmd/boomtime when the label-images feature is on so the admin regen
// endpoint has somewhere to enqueue jobs. Nil = feature off. Also wires
// ImageJobEvents when e satisfies EventSource too — see the matching
// handler.Handler.SetImageJobQueue doc comment for the full rationale.
func (h *Handler) SetImageJobQueue(e imagejobs.Enqueuer) {
	h.ImageJobQueue = e
	if es, ok := e.(imagejobs.EventSource); ok {
		h.ImageJobEvents = es
	}
}

// SetImageJobEvents wires the image-job EventSource after construction.
// Only needed when it differs from ImageJobQueue (broker=rabbitmq's
// producer+mirror split).
func (h *Handler) SetImageJobEvents(ev imagejobs.EventSource) {
	h.ImageJobEvents = ev
}

// SetJobs wires the catalyst-go-jobs Store + Enqueuer + Registry after
// construction (gaka-hney.2) so the admin Jobs tab can list history +
// trigger/retry and render the per-kind queue overview. Nil = jobs not wired;
// the handlers return 503.
func (h *Handler) SetJobs(store *jobs.Store, enq jobs.Enqueuer, reg *jobs.Registry) {
	h.JobStore = store
	h.JobEnqueuer = enq
	h.JobRegistry = reg
}

// SetJobLogStore wires the object store the per-job log endpoints read + delete
// from (gaka-hney). Nil = S3 not configured (GET .../logs 404s, DELETE no-ops).
// Only assign a non-nil concrete store here — a typed-nil would defeat the
// nil-check in the handlers.
func (h *Handler) SetJobLogStore(s objstore.Store) { h.JobLogStore = s }

// requireAdmin: 401 without a token, 403 when not on the admin allowlist.
// Returns the resolved owner on success. Mirror of the same method on
// *identity.Handler / *curation.Handler — the admin label-images
// endpoints gate on it. Three byte-identical copies survive
// because each domain guards a distinct endpoint and a shared helper
// would need dependency-injection scaffolding bigger than the 8-line
// body itself.
//
// The 403 path deliberately does NOT distinguish "unknown admin config"
// from "not on the list" — both look like a plain 403 to the client.
func (h *Handler) requireAdmin(c *echo.Context) (string, *apierr.Error) {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return "", aerr
	}
	if !h.Cfg.IsAdmin(owner) {
		return "", apierr.New(http.StatusForbidden, "admin only", nil)
	}
	return owner, nil
}
