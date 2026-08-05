// backfill_lastcontext.go: `boomtime backfill last-context` — one-shot
// resolution of stored WakaTime `<<LAST_PROJECT>>` / `<<LAST_BRANCH>>` /
// `<<LAST_LANGUAGE>>` template tokens across the whole heartbeats table.
//
// WakaTime editors/apps send these tokens for activity with no code context,
// expecting the server to substitute the user's last-known real value per axis
// (wakatime.com does this). boomtime now resolves them at ingest, but rows
// written before that shipped hold the literal token. This command mirrors the
// ingest rule against historical rows: per axis, per sender, a placeholder is
// rewritten to the most recent real value at an earlier time_sent; a
// placeholder with no prior real value is dropped to NULL.
//
// Runs server-side against the DB (like rotate-encryption-key), so operators
// invoke it inside the pod:
//
//	kubectl exec <pod> -- boomtime backfill last-context --dry-run   # preview
//	kubectl exec <pod> -- boomtime backfill last-context             # apply
//
// After applying, the command rebuilds hb_rollup_daily for every affected
// sender (the axis rewrites shift rollup buckets). If a rollup rebuild fails,
// the row rewrites are already committed — the command reports the exact
// senders still needing a manual resync and exits non-zero.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/spf13/cobra"
)

func backfillLastContextCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "last-context",
		Short: "Resolve stored <<LAST_PROJECT/BRANCH/LANGUAGE>> placeholders to each sender's prior real value",
		Long: `Rewrite historical heartbeats whose project/branch/language field holds a
WakaTime "<<LAST_*>>" template token. Per axis, per sender: a placeholder is
set to the most recent real value at an earlier time; a placeholder with no
prior real value is dropped to NULL (the literal is never left in place).

All row rewrites commit in a single transaction. On success, hb_rollup_daily is
rebuilt for every affected sender so dashboards reflect the change.

  # Preview counts, write nothing
  boomtime backfill last-context --dry-run

  # Apply, then rebuild affected rollups
  boomtime backfill last-context`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := config.Load()
			ctx := context.Background()
			database, err := db.New(ctx, cfg.DatabaseURL())
			if err != nil {
				return fmt.Errorf("db connect: %w", err)
			}
			defer database.Close()
			return runBackfillLastContext(ctx, database, dryRun, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report per-axis counts without writing")
	// Smart completion: the command takes no positional args — suppress file
	// completion so <TAB> after `last-context` offers flags only, not paths.
	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

// runBackfillLastContext is the extracted body so a smoke test can drive the
// full pipeline against an in-process DB without cobra + config.Load.
func runBackfillLastContext(ctx context.Context, database *db.DB, dryRun bool, out interface{ Write([]byte) (int, error) }) error {
	res, err := database.BackfillLastContext(ctx, dryRun)
	if err != nil {
		return fmt.Errorf("backfill last-context: %w", err)
	}

	label := "resolved"
	if dryRun {
		label = "would resolve (dry-run)"
	}
	fmt.Fprintf(out, "last-context backfill — %s:\n", label)
	fmt.Fprintf(out, "  project:  substituted=%d nulled=%d\n", res.ProjectSubstituted, res.ProjectNulled)
	fmt.Fprintf(out, "  language: substituted=%d nulled=%d\n", res.LanguageSubstituted, res.LanguageNulled)
	fmt.Fprintf(out, "  branch:   substituted=%d nulled=%d\n", res.BranchSubstituted, res.BranchNulled)
	fmt.Fprintf(out, "  affected senders: %d\n", len(res.AffectedSenders))

	if dryRun {
		fmt.Fprintln(out, "dry-run: no rows written, no rollups rebuilt.")
		return nil
	}

	// Rebuild rollups for every sender we touched. Axis rewrites change
	// hb_rollup_daily's project/branch/language buckets (gap_seconds is
	// time-only and unaffected, so a rollup refresh — not a full gap resync —
	// is the right rebuild). Each RefreshRollup opens its own tx; a failure
	// mid-loop leaves the committed row rewrites intact and is reported so the
	// operator can resync the named senders.
	epoch := time.Unix(0, 0).UTC()
	var failed []string
	for _, sender := range res.AffectedSenders {
		if err := database.RefreshRollup(ctx, sender, epoch); err != nil {
			fmt.Fprintf(out, "  ! rollup rebuild failed for %q: %v\n", sender, err)
			failed = append(failed, sender)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("row rewrites committed, but rollup rebuild failed for %d sender(s): %v — "+
			"re-run `boomtime backfill last-context` (idempotent) or resync those senders manually", len(failed), failed)
	}
	fmt.Fprintf(out, "rebuilt rollups for %d affected sender(s).\n", len(res.AffectedSenders))
	return nil
}
