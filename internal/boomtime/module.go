// Package boomtime is the wakatime/coding-analytics domain module (the app's original
// domain). It implements catalyst.Module: the per-user Wakatime secret column contract
// (so key-rotation is registry-driven) and — as of the boom-zp2s seam extraction — its
// admin/operator HTTP surface (label-image regeneration + the wakatime.com import
// cluster) via RegisterRoutes → internal/boomtime/admin. The remaining code/wakatime
// query routes (ingest/stats/curation/…) are lifted here in a later phase.
package boomtime

import (
	"github.com/labstack/echo/v5"

	boomtimeadmin "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/awards"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/curation"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/goals"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/ingest"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/queue/imagejobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/spaces"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/widgets"
	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/worker/labelimages"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/catalyst"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/domaincols"
)

// Module implements catalyst.Module for the boomtime (wakatime/code) domain. It is
// STATEFUL: RegisterRoutes constructs the boomtime admin/operator handler and stashes
// it so the host can late-wire the import worker, label-images worker, image-job queue,
// and jobs store onto the SAME instance after those subsystems initialize (mirrors the
// pre-registry god-handler flow, byte-identical). Always used via *Module.
type Module struct {
	catalyst.BaseModule
	admin *boomtimeadmin.Handler
}

// New constructs an empty boomtime Module. The admin handler is built lazily in
// RegisterRoutes.
func New() *Module { return &Module{} }

func (*Module) Name() string { return "boomtime" }

// Enabled: the code domain is the app's base — always on (no feature gate).
func (*Module) Enabled(*config.Config) bool { return true }

// EncryptedColumns: the imported Wakatime API key (per-user AES-GCM secret).
func (*Module) EncryptedColumns() []domaincols.EncryptedColumn {
	return domaincols.EncryptedColumnsFor("waka")
}

// RegisterRoutes mounts the boomtime domain's full HTTP surface (boom-zp2s): the
// admin/operator cluster (label-images + public label-image GET + wakatime.com import)
// PLUS the code/wakatime query + ingest routes lifted off the god-handler
// (ingest / curation / stats / widgets / goals / spaces / awards). Each per-domain
// handler "bag" is built here from Deps — the ONE shared stats cache arrives via
// d.Cache so a cache invalidation from any bag reaches every reader, byte-identical to
// the pre-move handler.New wiring. The admin handler is stashed for post-construction
// late-wiring (import worker, label-images worker, image-job queue, jobs store).
//
// Registration order preserves the pre-move relative sequence
// (ingest → curation → stats → widgets → goals → spaces → awards); the bags are
// non-overlapping across domains, so their global slot moving to this single
// Module.RegisterRoutes call leaves the routing tree — and the drift-guard route set —
// identical. The nil-guards inside each domain's Register keep the OpenAPI drift router
// (zero-value Deps) enumerating the same paths without dereferencing a nil DB/cache.
func (m *Module) RegisterRoutes(e *echo.Echo, d catalyst.Deps) {
	// Admin/operator surface (label-images cluster + public label-image GET +
	// wakatime.com import cluster). Mixed prefixes ⇒ mounted on the full echo, not
	// the /api/v1/admin group.
	m.admin = boomtimeadmin.New(d.DB, d.Cfg, d.Logger)
	boomtimeadmin.Register(e, m.admin)

	// Ingest (heartbeats + workouts + health_samples + explorer + entities).
	// Registered first among the data domains so /heartbeats.bulk stays the
	// fast-path first-match (its own Register preserves the intra-cluster order).
	ingest.Register(e, ingest.New(d.DB, d.Cfg, d.Logger, d.Cache))
	// Curation (hide/rename rules + destructive triplet + labels catalog admin).
	curation.Register(e, curation.New(d.DB, d.Cfg, d.Logger, d.Cache))
	// Stats (derived + core stats + big-bet aggregations + files + projects +
	// leaderboards + commits). stats.New takes the Config-subset interface, which
	// *config.Config satisfies.
	stats.Register(e, stats.New(d.DB, d.Cfg, d.Logger, d.Cache))
	// Badges + embeddable widgets + widget-def CRUD.
	widgets.Register(e, widgets.New(d.DB, d.Cfg, d.Logger, d.Cache))
	// User-defined composite goals (CRUD + toggle + progress). /goals/progress
	// registers BEFORE /goals/:id inside goals.Register to win path matching.
	goals.Register(e, &goals.Handler{DB: d.DB, Logger: d.Logger})
	// Spaces + dashboard-layout. /spaces/preview registers BEFORE /spaces/:id inside
	// spaces.Register to win path matching.
	spaces.Register(e, &spaces.Handler{DB: d.DB, Logger: d.Logger, Cache: d.Cache})
	// Awards cluster (streak ledger + evaluator + backfill).
	awards.Register(e, awards.New(d.DB, d.Cfg, d.Logger))
}

// SetImportWorker late-binds the wakatime.com import-job worker + hub onto the admin
// handler. No-op until RegisterRoutes has run (worker/drain roles register no routes).
func (m *Module) SetImportWorker(w *importer.Worker, hub *importer.Hub) {
	if m.admin != nil {
		m.admin.SetImportWorker(w, hub)
	}
}

// SetLabelImagesWorker late-binds the label-images worker (nil = feature disabled).
func (m *Module) SetLabelImagesWorker(w *labelimages.Worker) {
	if m.admin != nil {
		m.admin.SetLabelImagesWorker(w)
	}
}

// SetImageJobQueue late-binds the image-job Enqueuer (also wires ImageJobEvents when it
// satisfies EventSource — broker=inprocess).
func (m *Module) SetImageJobQueue(e imagejobs.Enqueuer) {
	if m.admin != nil {
		m.admin.SetImageJobQueue(e)
	}
}

// SetImageJobEvents late-binds the image-job EventSource (broker=rabbitmq split).
func (m *Module) SetImageJobEvents(ev imagejobs.EventSource) {
	if m.admin != nil {
		m.admin.SetImageJobEvents(ev)
	}
}

// SetJobs late-binds the catalyst-go-jobs Store + Enqueuer (read by the per-label
// BOOM_JOBS_UNIFIED status poll). Nil-safe on worker roles.
func (m *Module) SetJobs(store *jobs.Store, enq jobs.Enqueuer) {
	if m.admin != nil {
		m.admin.SetJobs(store, enq)
	}
}
