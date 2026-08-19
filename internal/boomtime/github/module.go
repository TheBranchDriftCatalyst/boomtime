// module.go — github as a catalyst.Module (gaka-zp2s Phase 1). internal/github is
// already the cleanest self-contained domain; here it just registers its per-user
// secret so key-rotation is registry-driven. Its stats-refresh job + fe-only widget
// keep their existing wiring in P1; the full Module (routes/jobs) is formalized when
// github lifts into internal/domains/github (spike P1a).
package github

import (
	"context"
	"errors"
	"fmt"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/catalyst"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/domaincols"
)

// Module implements catalyst.Module for the github domain.
type Module struct{ catalyst.BaseModule }

func (Module) Name() string { return "github" }

// Enabled gates on BOOM_FEATURE_GITHUB_STATS.
func (Module) Enabled(cfg *config.Config) bool { return cfg != nil && cfg.FeatureGithubStats }

// EncryptedColumns: the per-user GitHub token.
func (Module) EncryptedColumns() []domaincols.EncryptedColumn {
	return domaincols.EncryptedColumnsFor("github")
}

// RegisterJobs registers the github-stats-refresh kind + its fleet cap (gaka-zp2s),
// lifted verbatim from cmd/boomtime. It fans over every user with a linked GitHub
// token and refreshes each; a rate-limit fails the batch so it retries later, while a
// per-user error is logged + skipped. Registered UNCONDITIONALLY (only the SCHEDULE is
// gated on FeatureGithubStats, still owned by the host) and capped at 2 — byte-identical
// to the old inline wiring. The jobs package stays domain-free: the handler closes over
// the github Service built here.
func (Module) RegisterJobs(_ context.Context, d catalyst.Deps) error {
	svc := NewService(d.DB, d.Logger)
	d.Jobs.Register(GithubStatsRefreshKind, jobs.HandlerFunc(func(jctx context.Context, _ jobs.Job) error {
		users, uerr := d.DB.ListUsersWithGithubToken(jctx)
		if uerr != nil {
			return uerr
		}
		for _, u := range users {
			if _, serr := svc.SyncUser(jctx, u); serr != nil {
				if errors.Is(serr, ErrRateLimited) {
					return fmt.Errorf("github rate limited at user %q: %w", u, serr)
				}
				d.Logger.Warn("github refresh: user sync failed", "user", u, "err", serr)
			}
		}
		d.Logger.Info("github refresh: batch complete", "users", len(users))
		return nil
	}))
	d.Jobs.SetConcurrency(GithubStatsRefreshKind, 2) // github-stats-refresh
	return nil
}
