// Package domain is the pluggable-app framework core (gaka-zp2s, "Django for Go").
// A domain (catalyst-books, boomtime/wakatime, github, health) implements Module
// and registers itself once; the host (or a standalone image) iterates the Registry
// to mount routes, schedule jobs, drive key-rotation + backups, and gate on the
// feature flag — instead of every domain being hand-wired into god-files.
//
// This package holds the FULL contract (it imports echo/jobs/db — the framework
// deps). The dependency-FREE column contract stays in internal/domains so
// internal/db can read it without an import cycle; Module surfaces those same types.
// See docs/design/catalyst-domains-spike.md §3.
//
// Phase 1 (skeleton): the interface + Registry + IdentityProvider seam land here and
// existing domains register as thin stubs delegating to today's wiring — zero logic
// moves, behavior byte-identical. Later phases move real logic behind the seam.
package domain

import (
	"context"
	"io/fs"
	"log/slog"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/domaincols"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/notify"
)

// IdentityProvider is the seam that lets a Module run WITHOUT importing a concrete
// users table: embedded/host mode injects the shared multi-user identity; a
// standalone (self-hosted, single-user) image injects a trivial one-owner stub.
// Kept intentionally tiny — a domain only needs to resolve "who owns this request"
// and enumerate owners to fan a scheduled job over.
type IdentityProvider interface {
	// Owner resolves the authenticated owner (username) for a request, or an error
	// if unauthenticated.
	Owner(c *echo.Context) (string, error)
	// Owners lists every owner the host knows about (for fan-out jobs). A
	// single-user standalone impl returns just its one owner.
	Owners(ctx context.Context) ([]string, error)
}

// Deps is the bundle the host hands each Module at registration. Everything a
// domain needs to wire itself, provided by the composition root (cmd entrypoint).
type Deps struct {
	DB       *db.DB
	Cfg      *config.Config
	Jobs     *jobs.Registry
	Sched    *jobs.Scheduler
	Enqueuer jobs.Enqueuer
	Notify   *notify.Hub
	Identity IdentityProvider
	Logger   *slog.Logger
}

// Module is one pluggable data domain, end-to-end. Every method is optional in
// spirit — a P1 stub can no-op the ones it hasn't formalized yet — but the
// signatures are fixed so the Registry can drive all hosts uniformly.
type Module interface {
	// Name is the stable domain id ("books", "boomtime", "github").
	Name() string
	// Enabled reports whether this domain is turned on for the running config
	// (its BOOM_FEATURE_* gate). A disabled domain contributes nothing.
	Enabled(cfg *config.Config) bool

	// EncryptedColumns / BackupColumns surface the domain's per-user AES-GCM
	// secrets + backup-owned columns, using the dependency-free types the
	// rotate-encryption-key command and whole-DB export already iterate.
	EncryptedColumns() []domaincols.EncryptedColumn
	BackupColumns() []domaincols.BackupColumns

	// RegisterRoutes mounts the domain's HTTP surface (push/pull ingest + query).
	RegisterRoutes(e *echo.Echo, d Deps)
	// RegisterJobs registers the domain's job kinds + schedule intervals.
	RegisterJobs(ctx context.Context, d Deps) error

	// Migrations returns the domain's own embedded goose migration FS, or nil when
	// the domain rides the shared core migration sequence (embedded mode, P1–P3).
	Migrations() fs.FS
}

// BaseModule provides no-op defaults for the optional seams so a domain stub only
// implements what it actually contributes (Name/Enabled + whatever columns/routes/
// jobs it owns). Embed it and override selectively. Keeps stubs tight — and gives
// the future ORM/DAL consolidation one obvious place to grow shared behavior.
type BaseModule struct{}

func (BaseModule) EncryptedColumns() []domaincols.EncryptedColumn { return nil }
func (BaseModule) BackupColumns() []domaincols.BackupColumns      { return nil }
func (BaseModule) RegisterRoutes(*echo.Echo, Deps)                {}
func (BaseModule) RegisterJobs(context.Context, Deps) error       { return nil }
func (BaseModule) Migrations() fs.FS                              { return nil }

// Registry is the ordered set of Modules wired once at the composition root. The
// host iterates it for rotate/dump/route/job wiring; a standalone image registers
// only its one domain.
type Registry struct {
	modules []Module
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Add appends a Module (registration order is preserved for deterministic wiring).
func (r *Registry) Add(m Module) { r.modules = append(r.modules, m) }

// Modules returns the registered Modules in order.
func (r *Registry) Modules() []Module {
	return append([]Module(nil), r.modules...)
}

// Enabled returns only the Modules whose feature gate is on for cfg.
func (r *Registry) Enabled(cfg *config.Config) []Module {
	var out []Module
	for _, m := range r.modules {
		if m.Enabled(cfg) {
			out = append(out, m)
		}
	}
	return out
}

// EncryptedColumns aggregates every registered domain's encrypted columns — the
// list the key-rotation command re-encrypts. A new domain appears here the moment
// it's registered, closing the "secret silently stranded on rotation" gap.
func (r *Registry) EncryptedColumns() []domaincols.EncryptedColumn {
	var out []domaincols.EncryptedColumn
	for _, m := range r.modules {
		out = append(out, m.EncryptedColumns()...)
	}
	return out
}

// BackupColumns aggregates every registered domain's backup-owned columns for the
// whole-DB export.
func (r *Registry) BackupColumns() []domaincols.BackupColumns {
	var out []domaincols.BackupColumns
	for _, m := range r.modules {
		out = append(out, m.BackupColumns()...)
	}
	return out
}
