package api

import (
	"log/slog"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/liberate"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// Handler is the catalyst-books HTTP surface (boom-zp2s Phase 2), extracted from
// the core internal/identity god-handler. It owns the Amazon/Kindle/Audible connect
// + ingest triggers, the reading-items/work/curation/match endpoints, and the
// Hardcover connect/pull/search/push endpoints. Deps are the minimal set the book
// endpoints actually use (DB/Cfg/Logger + the job enqueuer + the inline Hardcover
// push service); it does NOT depend on the concrete users table — owner resolution
// goes through apihelpers.IdentifyOwner, which will become the IdentityProvider seam.
type Handler struct {
	DB          *db.DB
	Cfg         *config.Config
	Logger      *slog.Logger
	JobEnqueuer jobs.Enqueuer
	// HardcoverPush runs a per-item Hardcover curation push INLINE (the per-row sync
	// button); nil falls back to enqueuing.
	HardcoverPush *hardcover.PushService
	// Liberation is the SAME service instance the liberation job handlers use, so
	// a book liberated from the UI and one liberated by the sweep run identical
	// code. nil when liberation is not configured, which is what makes the
	// liberation routes 404 rather than 500.
	Liberation *liberate.Service
}

// New constructs a books.Handler with the shared core deps. Job enqueuer + Hardcover
// push service are wired after construction (SetJobEnqueuer / SetHardcoverPush).
func New(database *db.DB, cfg *config.Config, logger *slog.Logger) *Handler {
	return &Handler{DB: database, Cfg: cfg, Logger: logger}
}

// SetJobEnqueuer wires the jobs enqueuer after construction.
func (h *Handler) SetJobEnqueuer(e jobs.Enqueuer) { h.JobEnqueuer = e }

// SetHardcoverPush wires the inline curation-push service after construction.
func (h *Handler) SetHardcoverPush(p *hardcover.PushService) { h.HardcoverPush = p }

// SetLiberation wires the shared liberation service after construction. nil is a
// valid value meaning "liberation is off".
func (h *Handler) SetLiberation(s *liberate.Service) { h.Liberation = s }
