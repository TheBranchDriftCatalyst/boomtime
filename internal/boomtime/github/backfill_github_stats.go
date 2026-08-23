// backfill_github_stats.go: `boomtime backfill github-stats [--user X]` —
// server-side refresh of the per-user GitHub stats cache (boom-anh Phase 2).
// Relocated from cmd/boomtime so the admin CLI-runner can introspect the
// command def and call the same body in-process.
//
// Iterates every user with a linked GitHub token (or just --user) and runs
// Service.SyncUser, which upserts ONE row per user (replace-on-conflict).
// SAFELY RE-RUNNABLE: a second run overwrites each row rather than accumulating,
// so re-running is a no-op on data — same idempotency guarantee as the on-demand
// endpoint. Runs against the DB in-process (like rotate-encryption-key), so
// operators invoke it inside the pod:
//
//	kubectl exec <pod> -- boomtime backfill github-stats            # all linked users
//	kubectl exec <pod> -- boomtime backfill github-stats --user me  # one user
//
// Gated on BOOM_FEATURE_GITHUB_STATS — the feature master switch. NEVER logs a
// token; per-user output is success / skip / error only.
package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/climeta"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// init registers the `backfill github-stats` command into the climeta web-run
// allowlist (boom-zp2s) — the CLI framework stays domain-free; the github domain
// contributes its own vetted command here. Fires whenever package github loads (the
// composition root pulls it in via internal/domainreg; test binaries blank-import it).
func init() {
	climeta.Register("backfill github-stats", climeta.RegistryEntry{
		Classification:  climeta.ClassMutating,
		DryRunSupported: false,
		RequiredCap:     auth.CapAdmin,
		NewCommand:      NewBackfillGithubStatsCmd,
		// Mirrors the CLI's own refusal to run with the feature off.
		Available:      func(cfg *config.Config) bool { return cfg != nil && cfg.FeatureGithubStats },
		FlagCompleters: map[string]cobra.CompletionFunc{"user": climeta.CompleteUsernames},
		FlagListers:    map[string]climeta.DBLister{"user": climeta.ListUsernames},
		Invoke: func(ctx context.Context, database *db.DB, args climeta.RunArgs, out io.Writer) error {
			// Fail fast with ONE clear error when the encryption key is missing —
			// mirrors the CLI RunE precheck. Without it every user would fail
			// individually at Decrypt inside the loop.
			if err := auth.LoadKeyFromEnv(); err != nil {
				return fmt.Errorf("cannot decrypt stored tokens: %w", err)
			}
			// The Service logger is discarded here: per-user outcomes already land in
			// out, and the admin endpoint owns audit logging.
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			svc := NewService(database, logger)
			return RunBackfillGithubStats(ctx, database, svc, args.Str("user"), out)
		},
	})
}

// NewBackfillGithubStatsCmd builds the `backfill github-stats` command def —
// shared by the CLI (cmd/boomtime) and the admin CLI-runner's introspection.
func NewBackfillGithubStatsCmd() *cobra.Command {
	var user string
	cmd := &cobra.Command{
		Use:   "github-stats",
		Short: "Refresh the per-user GitHub stats cache (idempotent upsert per user)",
		// Web allowlist (admin CLI-runner): mutating without --dry-run, so
		// every web run requires the confirm sentinel. Availability is
		// additionally gated on cfg.FeatureGithubStats in the registry.
		Annotations: map[string]string{climeta.WebAnnotation: climeta.ClassMutating},
		Long: `Sync each linked user's GitHub stats into github_stats_cache. One row per
user is upserted (replace-on-conflict), so re-running never accrues duplicates —
the command is safely re-runnable.

Requires BOOM_FEATURE_GITHUB_STATS=on and BOOM_ENCRYPTION_KEY (to decrypt each
stored token). Never logs tokens.

  # Refresh every user with a linked GitHub token
  boomtime backfill github-stats

  # Refresh one user
  boomtime backfill github-stats --user alice`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := config.Load()
			if !cfg.FeatureGithubStats {
				return errors.New("BOOM_FEATURE_GITHUB_STATS is off — refusing to run github-stats backfill")
			}
			// Fail early with a clear message if the encryption key is missing —
			// otherwise every SyncUser would error at Decrypt.
			if err := auth.LoadKeyFromEnv(); err != nil {
				return fmt.Errorf("cannot decrypt stored tokens: %w", err)
			}
			ctx := context.Background()
			database, err := db.New(ctx, cfg.DatabaseURL())
			if err != nil {
				return fmt.Errorf("db connect: %w", err)
			}
			defer database.Close()

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
			svc := NewService(database, logger)
			return RunBackfillGithubStats(ctx, database, svc, user, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "only sync this user (default: all users with a linked GitHub token)")
	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// Smart completion: TAB --user to pick an existing user from the DB.
	_ = cmd.RegisterFlagCompletionFunc("user", climeta.CompleteUsernames)
	return cmd
}

// RunBackfillGithubStats is the extracted body so a test — and the admin
// CLI-runner — can drive the loop against an in-process DB + mock-GitHub
// service without cobra + config.Load.
func RunBackfillGithubStats(ctx context.Context, database *db.DB, svc *Service, user string, out io.Writer) error {
	var users []string
	if user != "" {
		users = []string{user}
	} else {
		rows, err := database.ListEncryptedGithubTokens(ctx)
		if err != nil {
			return fmt.Errorf("list linked github tokens: %w", err)
		}
		for _, r := range rows {
			users = append(users, r.Username)
		}
	}
	if len(users) == 0 {
		fmt.Fprintln(out, "no users with a linked GitHub token — nothing to do")
		return nil
	}

	var ok, skipped, failed int
	for _, u := range users {
		if _, err := svc.SyncUser(ctx, u); err != nil {
			if errors.Is(err, ErrNoToken) {
				fmt.Fprintf(out, "  skip %s: no linked token\n", u)
				skipped++
				continue
			}
			// Errors are pre-sanitized by the github package (never carry the
			// token). Report + keep going so one bad user doesn't abort the run.
			fmt.Fprintf(out, "  error %s: %v\n", u, err)
			failed++
			continue
		}
		fmt.Fprintf(out, "  ok %s\n", u)
		ok++
	}
	fmt.Fprintf(out, "github-stats backfill: ok=%d skipped=%d failed=%d (total=%d)\n", ok, skipped, failed, len(users))
	if failed > 0 {
		return fmt.Errorf("%d user(s) failed to sync", failed)
	}
	return nil
}
