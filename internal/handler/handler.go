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

	"github.com/TheBranchDriftCatalyst/boomtime/internal/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/awards"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/curation"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/goals"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/ingest"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logging"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/meta"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/imagejobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/spaces"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/widgets"
	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/worker/labelimages"
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
	// LabelImagesWorker drives on-demand image regeneration via the
	// admin endpoints (gaka-myv). nil = feature disabled; handlers
	// respond with 503 in that case.
	LabelImagesWorker *labelimages.Worker
	// ImageJobQueue accepts admin regen enqueues (gaka-8bz, transport-
	// generalized by the worker-topology decoupling). *imagejobs.Registry
	// under broker=inprocess (the registry owns the pool feed channel; the
	// pool is constructed and started at server startup in cmd/boomtime) or
	// *imagejobs.AMQPProducer under broker=rabbitmq. nil = feature
	// disabled; the admin handler checks for nil and 503s accordingly.
	ImageJobQueue imagejobs.Enqueuer
	// ImageJobEvents backs AdminLabelImagesWS's live stream + reconnect
	// snapshot. Usually the SAME underlying *imagejobs.Registry as
	// ImageJobQueue (broker=inprocess), or the broker=rabbitmq "mirror"
	// Registry fed by imagejobs.PumpBusIntoRegistry. nil = feature disabled.
	ImageJobEvents imagejobs.EventSource

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
}

// New constructs a Handler. logHub streams server-process slog records to the
// Logs tab; pass nil to disable (Logs endpoints handle a nil hub — see
// internal/meta/logs.go).
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, worker *importer.Worker, hub *importer.Hub, logHub *logging.LogHub) *Handler {
	sharedCache := cache.New(statsCacheTTL())
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
		Admin:    admin.New(database, cfg, logger, sharedCache, worker, hub),
	}
}

// SetLabelImagesWorker wires the label-images worker after construction.
// Called by cmd/boomtime once NewWorker succeeds; nil is fine when the
// feature is disabled — admin handlers detect the nil worker and return
// 503 Service Unavailable with a clear "feature disabled" message.
//
// Propagates the wired worker to h.Admin (phase 7) so the admin regen
// handler that lives on *admin.Handler picks it up.
func (h *Handler) SetLabelImagesWorker(w *labelimages.Worker) {
	h.LabelImagesWorker = w
	if h.Admin != nil {
		h.Admin.SetLabelImagesWorker(w)
	}
}

// SetImageJobQueue wires the image-job Enqueuer after construction. Called
// by cmd/boomtime when the label-images feature is on so the admin regen
// endpoint has somewhere to enqueue jobs. Nil = feature off.
//
// Convenience: when e also satisfies imagejobs.EventSource (true for
// *imagejobs.Registry — the broker=inprocess case, and every existing
// test), ImageJobEvents is wired to the same value so callers that only
// know about one queue object don't have to call both setters. The
// broker=rabbitmq split (an *imagejobs.AMQPProducer paired with a separate
// mirror Registry) calls SetImageJobEvents explicitly afterward — see
// cmd/boomtime/main.go.
func (h *Handler) SetImageJobQueue(e imagejobs.Enqueuer) {
	h.ImageJobQueue = e
	if es, ok := e.(imagejobs.EventSource); ok {
		h.ImageJobEvents = es
	}
	if h.Admin != nil {
		h.Admin.SetImageJobQueue(e)
	}
}

// SetImageJobEvents wires the image-job EventSource after construction.
// Only needed when it differs from ImageJobQueue (broker=rabbitmq's
// producer+mirror split) — SetImageJobQueue already wires it for the
// common case where one object satisfies both interfaces.
func (h *Handler) SetImageJobEvents(ev imagejobs.EventSource) {
	h.ImageJobEvents = ev
	if h.Admin != nil {
		h.Admin.SetImageJobEvents(ev)
	}
}

// SetJobs propagates the catalyst-go-jobs Store + Enqueuer to h.Admin
// (gaka-hney.2) so the admin Jobs tab can list + trigger/retry.
func (h *Handler) SetJobs(store *jobs.Store, enq jobs.Enqueuer) {
	if h.Admin != nil {
		h.Admin.SetJobs(store, enq)
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
