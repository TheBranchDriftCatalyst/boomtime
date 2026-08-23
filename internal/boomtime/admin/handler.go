// Package admin is the boomtime (code/wakatime) domain's ADMIN + operator HTTP
// surface (boom-zp2s), extracted from the central internal/admin god-package so
// the boomtime domain owns its own operator endpoints — the peer of internal/books/
// admin. It carries the label-image regeneration cluster (+ the public label-image
// GET that pairs with it) and the durable wakatime.com import-job cluster.
//
// Its route prefixes are mixed (/api/v1/admin/label-images/*, the public
// /api/v1/labels/:id/image, and /import/*), so it is mounted via
// boomtime.Module.RegisterRoutes(e, deps) on the full echo instance rather than the
// /api/v1/admin group. Route strings + middleware are byte-identical to the pre-move
// registrations that lived in internal/admin/routes.go.
package admin

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/queue/imagejobs"
	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/worker/labelimages"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// Handler bundles the deps the boomtime admin endpoints read. Core deps (DB/Cfg/
// Logger) come from the Module Deps at construction; the workers/queues/job store are
// wired AFTER construction (by cmd/boomtime, via the boomtime.Module setters) once the
// features initialize — nil is a supported "feature disabled" configuration, and the
// handlers detect nil and return 503.
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
	// wakatime.com import cluster: the durable import-job worker + fan-out hub.
	Worker *importer.Worker
	Hub    *importer.Hub
	// label-images regeneration.
	LabelImagesWorker *labelimages.Worker
	ImageJobQueue     imagejobs.Enqueuer
	ImageJobEvents    imagejobs.EventSource
	// catalyst-go-jobs store + enqueuer — read by AdminLabelImagesStatus (the
	// BOOM_JOBS_UNIFIED per-label status poll). nil = jobs subsystem not wired.
	JobStore    *jobs.Store
	JobEnqueuer jobs.Enqueuer
}

// New constructs a boomtime-admin Handler with the shared core deps.
func New(database *db.DB, cfg *config.Config, logger *slog.Logger) *Handler {
	return &Handler{DB: database, Cfg: cfg, Logger: logger}
}

// SetImportWorker wires the wakatime.com import-job worker + hub after construction.
func (h *Handler) SetImportWorker(w *importer.Worker, hub *importer.Hub) {
	h.Worker = w
	h.Hub = hub
}

// SetLabelImagesWorker wires the label-images worker after construction. nil is fine
// when the feature is disabled — handlers detect the nil worker and return 503.
func (h *Handler) SetLabelImagesWorker(w *labelimages.Worker) { h.LabelImagesWorker = w }

// SetImageJobQueue wires the image-job Enqueuer after construction; also wires
// ImageJobEvents when e satisfies EventSource (broker=inprocess). See the matching
// doc on the god-handler's setter for the rationale.
func (h *Handler) SetImageJobQueue(e imagejobs.Enqueuer) {
	h.ImageJobQueue = e
	if es, ok := e.(imagejobs.EventSource); ok {
		h.ImageJobEvents = es
	}
}

// SetImageJobEvents wires the image-job EventSource after construction (only needed
// when it differs from ImageJobQueue — the broker=rabbitmq producer+mirror split).
func (h *Handler) SetImageJobEvents(ev imagejobs.EventSource) { h.ImageJobEvents = ev }

// SetJobs wires the catalyst-go-jobs Store + Enqueuer (read by the per-label
// BOOM_JOBS_UNIFIED status poll). Nil = jobs not wired; the poll degrades.
func (h *Handler) SetJobs(store *jobs.Store, enq jobs.Enqueuer) {
	h.JobStore = store
	h.JobEnqueuer = enq
}

// requireAdmin: 401 without a token, 403 when not on the admin allowlist. Returns the
// resolved owner on success. Byte-identical to the copy on the other per-domain admin
// handlers — a shared helper would need DI scaffolding bigger than the 8-line body.
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

// Register mounts the boomtime admin/operator endpoints onto e (the full echo — the
// prefixes are mixed). Route strings + middleware are byte-identical to the pre-move
// registrations in internal/admin/routes.go.
func Register(e *echo.Echo, h *Handler) {
	// boom-myv: PUBLIC label image bytes (no auth) — label content is fixed catalog
	// data, not per-user data. Reads do NOT check the feature flag so already-generated
	// images keep serving after a flag flip.
	e.GET("/api/v1/labels/:id/image", h.LabelImage)

	// boom-myv / boom-8bz: label-images admin cluster — authed AND admin-gated
	// (requireAdmin, in-handler). Info + Regenerate + the per-label DB-queue status
	// poll (BOOM_JOBS_UNIFIED). The WS auths via the refresh_token cookie in-handler.
	e.GET("/api/v1/admin/label-images", h.AdminLabelImagesInfo)
	e.POST("/api/v1/admin/label-images/regenerate", h.AdminLabelImagesRegenerate)
	e.GET("/api/v1/admin/label-images/status", h.AdminLabelImagesStatus)
	e.GET("/api/v1/admin/label-images/ws", h.AdminLabelImagesWS)

	// Durable, resumable wakatime.com import jobs. auth-dry Phase 2: starting an import
	// is gated by CapImport route middleware (importCap); the other endpoints use the
	// shared bearer-token flow, and the WS uses the refresh_token cookie.
	e.POST("/import", h.ImportRequest, importCap(h)...)
	e.GET("/import/config", h.ImportConfig)
	e.POST("/import/wakatime-range", h.WakatimeRange)
	e.GET("/import/jobs", h.ImportJobs)
	e.GET("/import/jobs/:id", h.ImportJob)
	e.POST("/import/jobs/:id/cancel", h.ImportJobCancel)
	e.GET("/import/jobs/:id/logs", h.ImportJobLogs)
	e.GET("/import/jobs/:id/ws", h.ImportJobWS)
}

// importCap returns the CapImport route middleware, or nil when h is nil. The nil case
// exists only for the OpenAPI drift router, which registers routes with a zero handler
// to enumerate paths and never serves them — so h.DB must not be dereferenced then.
func importCap(h *Handler) []echo.MiddlewareFunc {
	if h == nil {
		return nil
	}
	return []echo.MiddlewareFunc{apihelpers.RequireCap(h.DB, auth.CapImport, "import")}
}
