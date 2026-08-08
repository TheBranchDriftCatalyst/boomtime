// backfill.go: the `boomtime backfill` parent command — a namespace for
// one-shot historical-data maintenance subcommands.
//
// Subcommands live in their own files:
//   - `backfill last-context`  (backfill_lastcontext.go)  — resolve stored
//     <<LAST_PROJECT/BRANCH/LANGUAGE>> WakaTime placeholder tokens.
//   - `backfill github-stats`   (backfill_github_stats.go) — refresh the
//     per-user GitHub stats cache.
//
// (The former `backfill git` git-history experiment — gaka-vh8 — was removed;
// it synthesized fake heartbeats and never graduated past an experiment.)

package main

import "github.com/spf13/cobra"

// backfillCmd registers `boomtime backfill` and its maintenance subcommands.
func backfillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "One-shot historical-data maintenance (last-context, github-stats)",
	}
	cmd.AddCommand(backfillLastContextCmd())
	cmd.AddCommand(backfillGithubStatsCmd())
	return cmd
}
