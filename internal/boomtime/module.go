// Package boomtime is the wakatime/coding-analytics domain (the app's original
// domain). Phase 1 seeds only the Module registration (gaka-zp2s); the coding
// ingest/stats/curation logic stays in its current internal/* packages and is lifted
// here in Phase 3. The Module surfaces the wakatime per-user secret so key-rotation
// is registry-driven; routes/jobs keep their existing wiring in P1.
package boomtime

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/domains"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/domain"
)

// Module implements domain.Module for the boomtime (wakatime/code) domain.
type Module struct{ domain.BaseModule }

func (Module) Name() string { return "boomtime" }

// Enabled: the code domain is the app's base — always on (no feature gate).
func (Module) Enabled(*config.Config) bool { return true }

// EncryptedColumns: the imported Wakatime API key (per-user AES-GCM secret).
func (Module) EncryptedColumns() []domains.EncryptedColumn {
	return domains.EncryptedColumnsFor("waka")
}
