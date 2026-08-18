// Package books is the catalyst-books domain. Phase 1 seeds only the Module
// registration (the pluggable-app contract, gaka-zp2s); the domain logic still
// lives in internal/{hardcover,amazon,domains/*} + the book files in
// internal/identity and is pulled in here in Phase 2. The Module currently surfaces
// just the encrypted/backup column contract (so key-rotation + backups are
// registry-driven) and no-ops routes/jobs, which keep their existing wiring in P1.
package books

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/domain"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/domaincols"
)

// Module implements domain.Module for catalyst-books.
type Module struct{ domain.BaseModule }

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
