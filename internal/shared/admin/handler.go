// Package admin owns the DOMAIN-FREE operator/observability HTTP surface:
// whole-DB backup + destructive restore, source-health, the admin caps
// dashboard (users), the in-memory rate-metrics snapshot, the portable
// catalyst-go-jobs admin cluster, and the admin CLI-runner. It reads only
// DB/Cfg/Logger/Cache (+ the catalyst-go-jobs store, a domain-free plugin), so
// it imports no data domain — the boomtime-specific admin surface (label-image
// regeneration + the wakatime.com import cluster) moved to internal/boomtime/admin
// (boom-zp2s), and the books admin surface to internal/books/admin.
//
// SECURITY POSTURE: every admin-gated endpoint runs requireAdmin BEFORE
// reading the body. The destructive restore endpoint (DBImport) demands an
// explicit ?confirm=replace-all-data sentinel.
//
// Shared helpers live in internal/shared/apihelpers.
package admin

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/objstore"
)

// Handler bundles the domain-free deps the admin endpoints read.
//
//   - DB     — backup/restore + source-health + users list reads/writes
//   - Cfg    — admin allowlist (IsAdmin)
//   - Logger — export/restore + audit logging
//   - Cache  — busted on the RestoreAll path so dashboards pick up new state
//   - JobStore / JobEnqueuer / JobRegistry — catalyst-go-jobs (boom-hney.2), the
//     portable jobs-admin cluster (list/trigger/retry + queue overview). Set after
//     construction. Nil = jobs subsystem not wired (handlers 503).
//   - JobLogStore — object store the per-job log endpoints read + delete from.
type Handler struct {
	DB          *db.DB
	Cfg         *config.Config
	Logger      *slog.Logger
	Cache       *cache.TTL
	JobStore    *jobs.Store
	JobEnqueuer jobs.Enqueuer
	JobRegistry *jobs.Registry
	JobLogStore objstore.Store
}

// New constructs an admin.Handler with the passed-in shared deps. The
// catalyst-go-jobs store/enqueuer/registry + log store are wired AFTER construction
// via SetJobs / SetJobLogStore (called from cmd/boomtime once the jobs subsystem is up).
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, cch *cache.TTL) *Handler {
	return &Handler{
		DB:     database,
		Cfg:    cfg,
		Logger: logger,
		Cache:  cch,
	}
}

// SetJobs wires the catalyst-go-jobs Store + Enqueuer + Registry after construction
// (boom-hney.2) so the admin Jobs tab can list history + trigger/retry and render the
// per-kind queue overview. Nil = jobs not wired; the handlers return 503.
func (h *Handler) SetJobs(store *jobs.Store, enq jobs.Enqueuer, reg *jobs.Registry) {
	h.JobStore = store
	h.JobEnqueuer = enq
	h.JobRegistry = reg
}

// SetJobLogStore wires the object store the per-job log endpoints read + delete from
// (boom-hney). Nil = S3 not configured (GET .../logs 404s, DELETE no-ops).
func (h *Handler) SetJobLogStore(s objstore.Store) { h.JobLogStore = s }

// requireAdmin: 401 without a token, 403 when not on the admin allowlist. Returns the
// resolved owner on success.
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
