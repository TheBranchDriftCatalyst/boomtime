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
	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/worker/labelimages"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
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
//
// Most routes go through the typed apiroute seam so their request/response Go types
// land in the OpenAPI spec. Four stay on plain echo, deliberately:
//   - GET /api/v1/labels/:id/image      — c.Blob (raw image bytes, not JSON).
//   - POST /api/v1/admin/label-images/regenerate — binds at a 256 KiB limit; the seam
//     binds at BodyLimitSmall (4 KiB), which would 413 the FE's catalog snapshot.
//   - POST /import/wakatime-range       — answers {hasData:false} on the no-key/lookup
//     -failure paths and the full 5-field AllTimeRange on success; one struct cannot
//     express both without changing the bytes on one of them.
//   - GET /import/jobs/:id/ws           — WebSocket upgrade.
func Register(e *echo.Echo, h *Handler) {
	// boom-myv: PUBLIC label image bytes (no auth) — label content is fixed catalog
	// data, not per-user data. Reads do NOT check the feature flag so already-generated
	// images keep serving after a flag flip.
	e.GET("/api/v1/labels/:id/image", h.LabelImage)

	// boom-myv / boom-8bz: label-images admin cluster — authed AND admin-gated
	// (requireAdmin, in-handler). Info + Regenerate + the per-label DB-queue status
	// poll (BOOM_JOBS_UNIFIED). The WS auths via the refresh_token cookie in-handler.
	apiroute.GET(e, "/api/v1/admin/label-images", h.AdminLabelImagesInfo)
	e.POST("/api/v1/admin/label-images/regenerate", h.AdminLabelImagesRegenerate)
	apiroute.GET(e, "/api/v1/admin/label-images/status", h.AdminLabelImagesStatus)

	// Durable, resumable wakatime.com import jobs. auth-dry Phase 2: starting an import
	// is gated by CapImport route middleware (importCap); the other endpoints use the
	// shared bearer-token flow, and the WS uses the refresh_token cookie.
	// BodyLimitNone: this bound with plain c.Bind before the seam, and
	// import_cluster_test pins that deliberately so adding a cap stays an
	// explicit decision with its own test, not a refactor side effect.
	apiroute.POSTLimit(e, "/import", apiroute.BodyLimitNone, h.ImportRequest, importCap(h)...)
	apiroute.GET(e, "/import/config", h.ImportConfig)
	e.POST("/import/wakatime-range", h.WakatimeRange)
	apiroute.GET(e, "/import/jobs", h.ImportJobs)
	apiroute.GET(e, "/import/jobs/:id", h.ImportJob)
	apiroute.POSTNoBody(e, "/import/jobs/:id/cancel", h.ImportJobCancel)
	apiroute.GET(e, "/import/jobs/:id/logs", h.ImportJobLogs)
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
