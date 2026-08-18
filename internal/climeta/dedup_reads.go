// dedup_reads.go — `boomtime hardcover dedup-reads --user X [--dry-run]`. Prunes
// duplicate / junk reads that accumulated on a user's Hardcover account: the empty
// "— → —" reads (auto-created by status pushes) and exact-duplicate dated reads,
// while KEEPING legitimate distinct reads (a real re-read has a different finish
// date). Deletes on Hardcover (batched) AND prunes the local reading_events mirror.
//
// DESTRUCTIVE + dry-run-supported: a web run defaults to dry-run (report only);
// applying requires the confirm sentinel. Needs the user's Hardcover token
// (BOOM_ENCRYPTION_KEY to decrypt) — a --user with no linked Hardcover is a no-op.
package climeta

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/hardcover"
)

// NewDedupReadsCmd builds the `hardcover dedup-reads` command def.
func NewDedupReadsCmd() *cobra.Command {
	var user string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "dedup-reads",
		Short: "Remove duplicate / empty reads on a user's Hardcover (keeps legit re-reads)",
		// Deletes data, but classed MUTATING (not destructive) so it can run from the
		// admin Commands UI — the web hard-blocks the destructive class entirely. The
		// dry-run default + confirm-sentinel-to-apply is the safety here.
		Annotations: map[string]string{WebAnnotation: ClassMutating},
		Long: `Scan a user's Hardcover shelf for books with more than one read and delete the
noise: empty dateless reads (auto-created by status pushes) and exact-duplicate
dated reads. Legitimate distinct reads (a real re-read has a different finish date)
are KEPT. Deletes on Hardcover AND prunes the local reading_events mirror.

  # PREVIEW what would be deleted (no writes)
  boomtime hardcover dedup-reads --user alice --dry-run

  # APPLY the deletes
  boomtime hardcover dedup-reads --user alice`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if user == "" {
				return fmt.Errorf("--user is required (dedup is per-user; it needs their Hardcover token)")
			}
			cfg := config.Load()
			if err := auth.LoadKeyFromEnv(); err != nil {
				return fmt.Errorf("cannot decrypt the Hardcover token: %w", err)
			}
			ctx := context.Background()
			database, err := db.New(ctx, cfg.DatabaseURL())
			if err != nil {
				return fmt.Errorf("db connect: %w", err)
			}
			defer database.Close()
			return RunDedupReads(ctx, hardcover.NewStore(database), database, user, dryRun, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "the user whose Hardcover reads to dedup (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be deleted without deleting")
	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	_ = cmd.RegisterFlagCompletionFunc("user", CompleteUsernames)
	return cmd
}

const dedupDeleteChunk = 50

// RunDedupReads is the extracted body (also the admin CLI-runner entry). Loads the
// user's Hardcover client, computes the reads to delete per the keep-policy, and —
// unless dryRun — batch-deletes them on Hardcover + prunes the local mirror.
func RunDedupReads(ctx context.Context, store *hardcover.Store, database *db.DB, owner string, dryRun bool, out io.Writer) error {
	client, ok, err := store.ClientForUser(ctx, owner)
	if err != nil {
		return fmt.Errorf("load hardcover client: %w", err)
	}
	if !ok {
		fmt.Fprintf(out, "%s has no linked Hardcover — nothing to dedup\n", owner)
		return nil
	}
	userID, err := client.Me(ctx)
	if err != nil {
		return fmt.Errorf("hardcover me: %w", err)
	}
	books, err := client.UserBooks(ctx, userID)
	if err != nil {
		return fmt.Errorf("hardcover user_books: %w", err)
	}

	var deleteIDs []int64
	booksTouched := 0
	for _, b := range books {
		if len(b.Reads) < 2 {
			continue
		}
		toDelete := readsToDelete(b.Reads)
		if len(toDelete) == 0 {
			continue
		}
		booksTouched++
		if dryRun && booksTouched <= 40 {
			fmt.Fprintf(out, "  %-44s keep %d / delete %d\n",
				truncate(b.Title, 44), len(b.Reads)-len(toDelete), len(toDelete))
		}
		deleteIDs = append(deleteIDs, toDelete...)
	}

	if len(deleteIDs) == 0 {
		fmt.Fprintf(out, "no duplicate/empty reads found for %s (%d books scanned)\n", owner, len(books))
		return nil
	}

	if dryRun {
		fmt.Fprintf(out, "\nDRY RUN — would delete %d read(s) across %d book(s). Re-run without --dry-run to apply.\n",
			len(deleteIDs), booksTouched)
		return nil
	}

	// Apply: batch-delete on Hardcover, then prune the local reading_events mirror.
	deleted := 0
	extIDs := make([]string, 0, len(deleteIDs))
	for start := 0; start < len(deleteIDs); start += dedupDeleteChunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := start + dedupDeleteChunk
		if end > len(deleteIDs) {
			end = len(deleteIDs)
		}
		chunk := deleteIDs[start:end]
		if derr := client.BulkDeleteUserBookReads(ctx, chunk); derr != nil {
			return fmt.Errorf("bulk delete (chunk at %d): %w", start, derr)
		}
		for _, id := range chunk {
			extIDs = append(extIDs, strconv.FormatInt(id, 10))
		}
		deleted += len(chunk)
		fmt.Fprintf(out, "  deleted %d/%d…\n", deleted, len(deleteIDs))
	}
	localPruned, lerr := database.DeleteReadingEventsByExternalIDs(ctx, owner, db.ReadingEventOriginHardcover, extIDs)
	if lerr != nil {
		fmt.Fprintf(out, "  (warning: local reading_events prune failed: %v)\n", lerr)
	}
	fmt.Fprintf(out, "dedup complete: deleted %d Hardcover read(s) across %d book(s); pruned %d local event(s)\n",
		deleted, booksTouched, localPruned)
	return nil
}

// readsToDelete applies the keep-policy to one book's reads and returns the read
// ids to delete. Policy: if any DATED read exists, drop every dateless read and
// collapse exact-duplicate dated reads (same start+finish) to one; if ALL reads are
// dateless, keep exactly one. The kept read in a group is the lowest id (stable).
func readsToDelete(reads []hardcover.UserBookRead) []int64 {
	dated := make([]hardcover.UserBookRead, 0, len(reads))
	dateless := make([]hardcover.UserBookRead, 0, len(reads))
	for _, r := range reads {
		if r.StartedAt != nil || r.FinishedAt != nil {
			dated = append(dated, r)
		} else {
			dateless = append(dateless, r)
		}
	}

	var del []int64
	if len(dated) > 0 {
		// Every dateless read is junk when a real dated read exists.
		for _, r := range dateless {
			del = append(del, int64(r.ID))
		}
		// Collapse exact-duplicate dated reads (keep the lowest id per date key).
		seen := map[string]int{}
		for _, r := range dated {
			key := dateKey(r)
			if keepID, ok := seen[key]; ok {
				// duplicate date → delete the higher id, keep the lower.
				if r.ID < keepID {
					del = append(del, int64(keepID))
					seen[key] = r.ID
				} else {
					del = append(del, int64(r.ID))
				}
			} else {
				seen[key] = r.ID
			}
		}
	} else {
		// All dateless: keep the lowest id, delete the rest.
		ids := make([]int, 0, len(dateless))
		for _, r := range dateless {
			ids = append(ids, r.ID)
		}
		sort.Ints(ids)
		for _, id := range ids[1:] {
			del = append(del, int64(id))
		}
	}
	return del
}

func dateKey(r hardcover.UserBookRead) string {
	s, f := "", ""
	if r.StartedAt != nil {
		s = r.StartedAt.UTC().Format("2006-01-02")
	}
	if r.FinishedAt != nil {
		f = r.FinishedAt.UTC().Format("2006-01-02")
	}
	return s + "|" + f
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
