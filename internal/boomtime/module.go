// Package boomtime is the wakatime/coding-analytics domain module (the app's original
// domain). It implements catalyst.Module: the per-user Wakatime secret column contract
// (so key-rotation is registry-driven) and — as of the gaka-zp2s seam extraction — its
// admin/operator HTTP surface (label-image regeneration + the wakatime.com import
// cluster) via RegisterRoutes → internal/boomtime/admin. The remaining code/wakatime
// query routes (ingest/stats/curation/…) are lifted here in a later phase.
package boomtime

import (
	"github.com/labstack/echo/v5"

	boomtimeadmin "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/queue/imagejobs"
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

// RegisterRoutes builds the boomtime admin/operator handler and mounts its surface
// (label-images cluster + public label-image GET + wakatime.com import cluster) on the
// full echo — the prefixes are mixed, so this uses RegisterRoutes rather than the
// /api/v1/admin group. Stashes the handler for post-construction late-wiring.
func (m *Module) RegisterRoutes(e *echo.Echo, d catalyst.Deps) {
	m.admin = boomtimeadmin.New(d.DB, d.Cfg, d.Logger)
	boomtimeadmin.Register(e, m.admin)
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
