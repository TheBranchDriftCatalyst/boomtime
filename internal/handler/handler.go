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
	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/backfilljobs"
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
	// ImageJobQueue is the in-memory registry backing the durable
	// per-label regen queue (gaka-8bz). nil = feature disabled; the
	// admin handler + WS endpoint check for nil and 503 accordingly.
	// The registry itself owns the pool feed channel; the pool is
	// constructed and started at server startup in cmd/boomtime.
	ImageJobQueue *imagejobs.Registry
	// BackfillJobQueue is the in-memory registry backing the git-history
	// backfill CLI flow (gaka-vh8). Non-nil in every configuration —
	// unlike ImageJobQueue there is no feature flag, the registry is
	// cheap and only holds rows when a CLI is actively streaming.
	BackfillJobQueue *backfilljobs.Registry

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

// SetImageJobQueue wires the imagejobs.Registry after construction. Called
// by cmd/boomtime when the label-images feature is on so the admin regen
// endpoint + WS stream have somewhere to enqueue jobs. Nil = feature off.
func (h *Handler) SetImageJobQueue(r *imagejobs.Registry) {
	h.ImageJobQueue = r
	if h.Admin != nil {
		h.Admin.SetImageJobQueue(r)
	}
}

// SetBackfillJobQueue wires the backfilljobs.Registry (gaka-vh8). Always
// non-nil in prod; kept as a setter for symmetry with SetImageJobQueue
// and so tests can inject a per-test registry with tight retention.
func (h *Handler) SetBackfillJobQueue(r *backfilljobs.Registry) {
	h.BackfillJobQueue = r
	if h.Admin != nil {
		h.Admin.SetBackfillJobQueue(r)
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
