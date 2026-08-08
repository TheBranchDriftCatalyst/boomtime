// backfill.go: the `boomtime backfill` parent command — a namespace for
// one-shot historical-data maintenance subcommands.
//
// The subcommand constructors + run bodies live in internal/climeta so the
// admin CLI-runner (BOOM_FEATURE_ADMIN_CLI) can introspect and invoke the
// SAME definitions in-process:
//   - `backfill last-context`  (climeta.NewBackfillLastContextCmd) — resolve
//     stored <<LAST_PROJECT/BRANCH/LANGUAGE>> WakaTime placeholder tokens.
//   - `backfill github-stats`  (climeta.NewBackfillGithubStatsCmd) — refresh
//     the per-user GitHub stats cache.
//
// (The former `backfill git` git-history experiment — gaka-vh8 — was removed;
// it synthesized fake heartbeats and never graduated past an experiment.)

package main

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/climeta"
	"github.com/spf13/cobra"
)

// backfillCmd registers `boomtime backfill` and its maintenance subcommands.
func backfillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "One-shot historical-data maintenance (last-context, github-stats)",
	}
	cmd.AddCommand(climeta.NewBackfillLastContextCmd())
	cmd.AddCommand(climeta.NewBackfillGithubStatsCmd())
	return cmd
}
