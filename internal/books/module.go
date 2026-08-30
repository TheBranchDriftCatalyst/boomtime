// Package books is the catalyst-books domain module (boom-zp2s). It implements
// catalyst.Module end-to-end: the encrypted/backup column contract (so
// key-rotation + backups are registry-driven), the HTTP surface (RegisterRoutes →
// internal/books/api), the admin surface (RegisterAdminRoutes → internal/books/admin),
// and the job kinds + schedules (RegisterJobs → internal/books/jobs). The same
// packages mount into the full boomtime host (via the shared registry) AND the
// standalone cmd/catalyst-books image, unchanged.
package books

import (
	"context"
	"log/slog"

	"github.com/labstack/echo/v5"

	booksadmin "github.com/TheBranchDriftCatalyst/boomtime/internal/books/admin"
	booksapi "github.com/TheBranchDriftCatalyst/boomtime/internal/books/api"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/hardcover"
	booksjobs "github.com/TheBranchDriftCatalyst/boomtime/internal/books/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/liberate"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/catalyst"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/domaincols"
)

// Module implements catalyst.Module for catalyst-books. It is STATEFUL: RegisterRoutes
// constructs the books HTTP handler and stashes it, so the host can late-wire the job
// enqueuer + inline Hardcover push onto the SAME instance after the jobs subsystem is
// built — byte-identical to the pre-registry god-handler flow (route closures capture
// the handler pointer; the setters mutate its fields). Always used via *Module (the
// composition root builds it with New and threads the one instance through the
// registry), so the registry stores a pointer.
type Module struct {
	catalyst.BaseModule
	h   *booksapi.Handler
	svc *booksjobs.Services
}

// New constructs an empty books Module. The HTTP handler is built lazily in
// RegisterRoutes, so a registry that only aggregates columns (rotate/backup) or drives
// admin routes never allocates one.
func New() *Module { return &Module{} }

func (*Module) Name() string { return "books" }

// Enabled gates the whole domain on BOOM_FEATURE_BOOKS.
func (*Module) Enabled(cfg *config.Config) bool { return cfg != nil && cfg.FeatureBooks }

// EncryptedColumns: the Amazon device credential + Hardcover key (both per-user
// AES-GCM secrets). Derived from the single-source domains registry.
func (*Module) EncryptedColumns() []domaincols.EncryptedColumn {
	return domaincols.EncryptedColumnsFor("amazon", "hardcover")
}

// BackupColumns: the same secrets + their status/metadata siblings, included in the
// whole-DB export.
func (*Module) BackupColumns() []domaincols.BackupColumns {
	return domaincols.BackupColumnsFor("amazon", "hardcover")
}

// RegisterRoutes builds the catalyst-books HTTP handler and mounts its surface
// (booksapi.Register), stashing the handler for post-construction late-wiring. Behavior
// is byte-identical to the pre-move booksapi.Register(e, h.Books) call the god-handler
// drove.
func (m *Module) RegisterRoutes(e *echo.Echo, d catalyst.Deps) {
	m.h = booksapi.New(d.DB, d.Cfg, d.Logger)
	booksapi.Register(e, m.h)
}

// SetJobEnqueuer forwards the wired jobs enqueuer onto the stashed handler (book
// ingest / curation-push enqueue). No-op until RegisterRoutes has run — worker/drain
// roles register no routes, so h stays nil and this is a safe no-op.
func (m *Module) SetJobEnqueuer(enq jobs.Enqueuer) {
	if m.h != nil {
		m.h.SetJobEnqueuer(enq)
	}
}

// HasJobEnqueuer reports whether the stashed HTTP handler currently holds a jobs
// enqueuer. Exists so a test can prove that late-wired state SURVIVES a second
// registration pass — building the OpenAPI documentation router used to run
// RegisterRoutes again on the live registry and silently replace the handler this
// wiring landed on, which took every background-job enqueue down in production
// while the jobs page still looked healthy.
func (m *Module) HasJobEnqueuer() bool {
	return m.h != nil && m.h.JobEnqueuer != nil
}

// SetHardcoverPush forwards the inline curation-push service (the per-row sync button)
// onto the stashed handler. No-op until RegisterRoutes has run.
func (m *Module) SetHardcoverPush(p *hardcover.PushService) {
	if m.h != nil {
		m.h.SetHardcoverPush(p)
	}
}

// RegisterAdminRoutes mounts the catalyst-books admin HTTP surface (diagnostics +
// reading-monitor) onto g — the host anchors g at /api/v1/admin. Delegates to the
// per-domain internal/books/admin seam folder.
func (*Module) RegisterAdminRoutes(g *echo.Group, d catalyst.Deps) {
	booksadmin.Register(g, booksadmin.New(d.DB, d.Cfg, d.Logger))
}

// RegisterJobs registers the catalyst-books job KINDS + fleet caps (internal/books/jobs)
// and stashes the shared services for late-wiring. It runs at the SAME point the old
// inline cmd/boomtime block did — before the jobs provider exists — so the enqueuer is
// late-bound via WireJobEnqueuer; the inline Hardcover-push service is handed to the
// HTTP handler here (nil-safe on worker roles where no handler was built). Byte-identical
// to the pre-extraction wiring.
func (m *Module) RegisterJobs(ctx context.Context, d catalyst.Deps) error {
	m.svc = booksjobs.Register(d.Jobs, d.DB, d.Cfg, d.Notify, d.Logger)
	m.SetHardcoverPush(m.svc.CurationPush())
	// Hand the HTTP handler the SAME liberation service the job handlers use, so
	// a book liberated from the UI and one liberated by the sweep run identical
	// code. nil when liberation is off, which is what the routes check.
	m.SetLiberation(m.svc.Liberation())
	return nil
}

// SetLiberation forwards the shared liberation service onto the stashed handler.
// No-op until RegisterRoutes has run — worker/drain roles register no routes, so
// h stays nil and this is a safe no-op (mirrors SetHardcoverPush).
func (m *Module) SetLiberation(s *liberate.Service) {
	if m.h != nil {
		m.h.SetLiberation(s)
	}
}

// WireJobEnqueuer late-binds the jobs provider (a jobs.Enqueuer) once it exists:
// onto the audiobooks service (finished-book Hardcover pushes route to the capped
// queue) AND onto the HTTP handler (book ingest / curation-push enqueue). Both are
// nil-safe — mirrors the pre-extraction `audioSvc.SetEnqueuer(provider)` (any role) +
// `h.Books.SetJobEnqueuer(provider)` (server role only).
func (m *Module) WireJobEnqueuer(enq jobs.Enqueuer) {
	m.svc.WireEnqueuer(enq)
	m.SetJobEnqueuer(enq)
}

// RegisterSchedules registers the books leader-singleton schedules on sched, gated
// exactly as the pre-extraction host block. The host owns the outer "build the
// scheduler at all" condition + go sched.Run.
func (*Module) RegisterSchedules(ctx context.Context, sched *jobs.Scheduler, cfg *config.Config, logger *slog.Logger) {
	booksjobs.RegisterSchedules(ctx, sched, cfg, logger)
}
