// module.go — github as a domain.Module (gaka-zp2s Phase 1). internal/github is
// already the cleanest self-contained domain; here it just registers its per-user
// secret so key-rotation is registry-driven. Its stats-refresh job + fe-only widget
// keep their existing wiring in P1; the full Module (routes/jobs) is formalized when
// github lifts into internal/domains/github (spike P1a).
package github

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/domains"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/domain"
)

// Module implements domain.Module for the github domain.
type Module struct{ domain.BaseModule }

func (Module) Name() string { return "github" }

// Enabled gates on BOOM_FEATURE_GITHUB_STATS.
func (Module) Enabled(cfg *config.Config) bool { return cfg != nil && cfg.FeatureGithubStats }

// EncryptedColumns: the per-user GitHub token.
func (Module) EncryptedColumns() []domains.EncryptedColumn {
	return domains.EncryptedColumnsFor("github")
}
