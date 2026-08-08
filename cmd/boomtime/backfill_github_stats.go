// backfill_github_stats.go: `boomtime backfill github-stats [--user X]` —
// server-side refresh of the per-user GitHub stats cache (gaka-anh Phase 2).
//
// Iterates every user with a linked GitHub token (or just --user) and runs
// github.Service.SyncUser, which upserts ONE row per user (replace-on-conflict).
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
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/github"
)

func backfillGithubStatsCmd() *cobra.Command {
	var user string
	cmd := &cobra.Command{
		Use:   "github-stats",
		Short: "Refresh the per-user GitHub stats cache (idempotent upsert per user)",
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
			svc := github.NewService(database, logger)
			return runBackfillGithubStats(ctx, database, svc, user, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "only sync this user (default: all users with a linked GitHub token)")
	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

// runBackfillGithubStats is the extracted body so a test can drive the loop
// against an in-process DB + mock-GitHub service without cobra + config.Load.
func runBackfillGithubStats(ctx context.Context, database *db.DB, svc *github.Service, user string, out interface{ Write([]byte) (int, error) }) error {
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
			if errors.Is(err, github.ErrNoToken) {
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
