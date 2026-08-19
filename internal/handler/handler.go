// Package handler holds the top-level composition facade that wires the
// per-domain HTTP handler bags together and exposes them to the server
// registrar. Every route lives on one of the extracted per-domain
// packages (internal/{meta,spaces,goals,widgets,identity,awards,ingest,
// curation,stats,admin}) — this package is the seam that constructs and
// shares infrastructure (DB, cache, logger, queues, workers) across them
// so a cache invalidation from any domain reaches every reader.
//
// Shared HTTP helpers live in internal/apihelpers/ — every domain
// imports that instead of carrying per-file shims (gaka-8tn phase 8
// collapse). This package holds NO route handlers of its own — just the
// composition struct, its constructor, and the post-construction setters
// that propagate to h.Admin.
package handler

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/awards"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/curation"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/goals"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/ingest"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/spaces"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/widgets"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs/jobsevents"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/logging"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/meta"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/notify"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/objstore"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/queryapi"
)

// Handler bundles shared dependencies for all HTTP handlers. Post
// phase 8: the god type shrank to composition + setters — every route
// is served by one of the per-domain fields.
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
	// gaka-zp2s: the label-images worker / image-job queue + events moved off this
	// god-type onto boomtime.Module (its admin handler owns the regen endpoints).
	// The host late-wires them there directly.

	// Extracted per-domain handler bags (gaka-8tn). Each field points at
	// deps the domain actually reads (a subset of the god-type).
	Meta     *meta.Handler     // phase 1
	Spaces   *spaces.Handler   // phase 2a
	Goals    *goals.Handler    // phase 2b
	Widgets  *widgets.Handler  // phase 3
	Identity *identity.Handler // phase 4a
	Awards   *awards.Handler   // phase 4b
	Ingest   *ingest.Handler   // phase 5a
	Curation *curation.Handler // phase 5b
	Stats    *stats.Handler    // phase 6
	Admin    *admin.Handler    // phase 7
	Query    *queryapi.Handler // gaka-174.q: cross-domain query DSL over HTTP
	// gaka-zp2s: the catalyst-books HTTP surface is no longer a bag on this god-type
	// — it is owned by books.Module (RegisterRoutes builds + stashes it, the host
	// late-wires its enqueuer/Hardcover-push). This package no longer imports the books
	// domain.
}

// New constructs a Handler. logHub streams server-process slog records to the
// Logs tab; pass nil to disable (Logs endpoints handle a nil hub — see
// internal/meta/logs.go).
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, worker *importer.Worker, hub *importer.Hub, logHub *logging.LogHub) *Handler {
	sharedCache := cache.NewNamed(statsCacheTTL(), "stats")
	startTime := time.Now()
	return &Handler{
		DB:        database,
		Cfg:       cfg,
		Logger:    logger,
		Worker:    worker,
		Hub:       hub,
		LogHub:    logHub,
		Cache:     sharedCache,
		StartTime: startTime,
		// Per-domain handler bags (gaka-8tn). Each shares the SAME
		// underlying instances the god-type holds (DB, sharedCache,
		// logger, logHub) so cache invalidations from any domain reach
		// every reader.
		Meta: &meta.Handler{
			Cfg:       cfg,
			Logger:    logger,
			Cache:     sharedCache,
			LogHub:    logHub,
			DB:        database,
			StartTime: startTime,
		},
		Spaces: &spaces.Handler{
			DB:     database,
			Logger: logger,
			Cache:  sharedCache,
		},
		Goals:    &goals.Handler{DB: database, Logger: logger},
		Widgets:  widgets.New(database, cfg, logger, sharedCache),
		Identity: identity.New(database, cfg, logger, sharedCache),
		Awards:   awards.New(database, cfg, logger),
		Ingest:   ingest.New(database, cfg, logger, sharedCache),
		Curation: curation.New(database, cfg, logger, sharedCache),
		Stats:    stats.New(database, cfg, logger, sharedCache),
		Admin:    admin.New(database, cfg, logger, sharedCache),
		Query:    &queryapi.Handler{DB: database, Cfg: cfg, Logger: logger},
	}
}

// gaka-zp2s: SetLabelImagesWorker / SetImageJobQueue / SetImageJobEvents moved off this
// god-type — the label-images regen surface is owned by boomtime.Module (its admin
// handler), which cmd/boomtime late-wires directly via domainSet.Boomtime.

// SetJobs propagates the catalyst-go-jobs Store + Enqueuer + Registry to h.Admin
// (gaka-hney.2) so the admin Jobs tab can list + trigger/retry and render the
// per-kind queue overview (the registry supplies the concurrency caps + the
// full set of known kinds).
func (h *Handler) SetJobs(store *jobs.Store, enq jobs.Enqueuer, reg *jobs.Registry) {
	if h.Admin != nil {
		h.Admin.SetJobs(store, enq, reg)
	}
	if h.Identity != nil {
		h.Identity.SetJobEnqueuer(enq) // gaka-hney.7: avatar-render enqueue
	}
	// gaka-zp2s: the books enqueuer is wired onto books.Module directly by the
	// composition root (cmd/boomtime), not through this god-type.
}

// SetJobLogStore propagates the object store the per-job log endpoints read +
// delete a finished job's persisted logs from (gaka-hney) to h.Admin. Nil = S3
// not configured; the endpoints degrade to 404 / no-op.
func (h *Handler) SetJobLogStore(s objstore.Store) {
	if h.Admin != nil {
		h.Admin.SetJobLogStore(s)
	}
}

// SetJobEvents propagates the job-events push hub to h.Identity so the
// /api/v1/jobs/ws stream can subscribe (gaka-hney.6).
func (h *Handler) SetJobEvents(hub *jobsevents.Hub) {
	if h.Identity != nil {
		h.Identity.SetJobEvents(hub)
	}
}

// SetNotify propagates the domain-agnostic notification hub to h.Identity so the
// /api/v1/notify/ws stream can subscribe.
func (h *Handler) SetNotify(hub *notify.Hub) {
	if h.Identity != nil {
		h.Identity.SetNotify(hub)
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
