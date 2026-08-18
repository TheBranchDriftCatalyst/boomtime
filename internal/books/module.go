// Package books is the catalyst-books domain. Phase 1 seeds only the Module
// registration (the pluggable-app contract, gaka-zp2s); the domain logic still
// lives in internal/{hardcover,amazon,domains/*} + the book files in
// internal/identity and is pulled in here in Phase 2. The Module currently surfaces
// just the encrypted/backup column contract (so key-rotation + backups are
// registry-driven) and no-ops routes/jobs, which keep their existing wiring in P1.
package books

import (
	"github.com/labstack/echo/v5"

	booksadmin "github.com/TheBranchDriftCatalyst/boomtime/internal/books/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/catalyst"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/domaincols"
)

// Module implements catalyst.Module for catalyst-books.
type Module struct{ catalyst.BaseModule }

func (Module) Name() string { return "books" }

// Enabled gates the whole domain on BOOM_FEATURE_BOOKS.
func (Module) Enabled(cfg *config.Config) bool { return cfg != nil && cfg.FeatureBooks }

// EncryptedColumns: the Amazon device credential + Hardcover key (both per-user
// AES-GCM secrets). Derived from the single-source domains registry.
func (Module) EncryptedColumns() []domaincols.EncryptedColumn {
	return domaincols.EncryptedColumnsFor("amazon", "hardcover")
}

// BackupColumns: the same secrets + their status/metadata siblings, included in the
// whole-DB export.
func (Module) BackupColumns() []domaincols.BackupColumns {
	return domaincols.BackupColumnsFor("amazon", "hardcover")
}

// RegisterAdminRoutes mounts the catalyst-books admin HTTP surface (diagnostics +
// reading-monitor) onto g — the host anchors g at /api/v1/admin. Delegates to the
// per-domain internal/books/admin seam folder; behavior is byte-identical to the
// pre-move registrations that lived in internal/admin/routes.go.
func (Module) RegisterAdminRoutes(g *echo.Group, d catalyst.Deps) {
	booksadmin.Register(g, booksadmin.New(d.DB, d.Cfg, d.Logger))
}
