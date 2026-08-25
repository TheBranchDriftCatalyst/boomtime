// liberate_cmd.go — the `boomtime books liberate` / `liberation-status` CLI
// (boom-w20s.17). Lives in internal/books, like dedup_reads.go, so the admin
// CLI-runner can introspect and invoke the same definitions in-process.
//
// Per the standing CLI convention (bd memory boomtime-cli-smart-completion),
// BOTH commands wire dynamic completion: --user through the shared username
// completer, and the positional ASIN through a DBEntityCompleter over the
// caller's own unliberated library — so <TAB> offers the books that actually
// need liberating rather than nothing.
package books

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/liberate"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/climeta"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// NewLiberateCmd builds `boomtime books liberate`.
func NewLiberateCmd() *cobra.Command {
	var user string
	var all, force bool
	var limit int

	cmd := &cobra.Command{
		Use:   "liberate [ASIN]",
		Short: "Download and DRM-strip owned Audible titles into the local library",
		// MUTATING, not destructive: it only ever ADDS files to the library and
		// updates liberation state. It cannot delete a book.
		Annotations: map[string]string{climeta.WebAnnotation: climeta.ClassMutating},
		Long: `Liberate owned Audible audiobooks: request a content license, decrypt the
voucher, download the AAXC, strip the DRM, and write a chaptered, tagged M4B into
BOOM_BOOKS_LIBRARY_PATH.

Runs INLINE — unlike the web endpoints, which enqueue a background job. That makes
this the right tool for a one-off or a debugging run, and the wrong one for a whole
library over SSH: a single title can take many minutes.

Re-running is safe. A book already liberated, whose file is present at the recorded
size, is skipped; use --force to redo it.

  # one title
  boomtime books liberate B09GCYRZRQ --user alice

  # everything outstanding (this can be hundreds of GB — see --limit first)
  boomtime books liberate --all --user alice

  # dip a toe in
  boomtime books liberate --all --limit 3 --user alice`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if user == "" {
				return fmt.Errorf("--user is required (liberation is per-user; it needs their Amazon credential)")
			}
			if !all && len(args) == 0 {
				return fmt.Errorf("give an ASIN, or --all to liberate every outstanding title")
			}
			if all && len(args) > 0 {
				return fmt.Errorf("give an ASIN or --all, not both")
			}

			cfg := config.Load()
			if !cfg.LiberationEnabled() {
				// Name which of the three terms is missing — "disabled" alone
				// sends people hunting through config for the wrong one.
				return fmt.Errorf("liberation is not enabled: need BOOM_FEATURE_BOOKS=true, "+
					"BOOM_FEATURE_BOOKS_LIBERATION=true and BOOM_BOOKS_LIBRARY_PATH set "+
					"(currently books=%v liberation=%v libraryPath=%q)",
					cfg.FeatureBooks, cfg.FeatureBooksLiberation, cfg.BooksLibraryPath)
			}
			if err := auth.LoadKeyFromEnv(); err != nil {
				return fmt.Errorf("cannot decrypt the Amazon credential: %w", err)
			}

			ctx := context.Background()
			svc, database, err := buildCLIService(ctx, cfg)
			if err != nil {
				return err
			}
			defer database.Close()

			out := cmd.OutOrStdout()
			targets := args
			if all {
				targets, err = svc.LiberateAll(ctx, user, limit)
				if err != nil {
					return err
				}
				if len(targets) == 0 {
					fmt.Fprintln(out, "Nothing to liberate — every title is already done.")
					return nil
				}
				fmt.Fprintf(out, "Liberating %d title(s) into %s\n\n", len(targets), cfg.BooksLibraryPath)
			}

			var ok, failed int
			for i, asin := range targets {
				fmt.Fprintf(out, "[%d/%d] %s … ", i+1, len(targets), asin)
				res, lerr := svc.LiberateBook(ctx, user, asin, liberate.Options{Force: force})
				switch {
				case lerr != nil:
					failed++
					fmt.Fprintf(out, "FAILED (%s): %v\n", res.Status, lerr)
				case res.Skipped:
					fmt.Fprintf(out, "skipped (already liberated)\n")
				default:
					ok++
					fmt.Fprintf(out, "ok — %s (%s, %s)\n", res.RelPath, humanBytes(res.Bytes), res.Duration.Round(1e9))
				}
			}
			fmt.Fprintf(out, "\nDone: %d liberated, %d failed, %d total.\n", ok, failed, len(targets))
			if failed > 0 {
				// Non-zero exit so a scripted run notices.
				return fmt.Errorf("%d title(s) failed", failed)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&user, "user", "", "the user whose library to liberate (required)")
	cmd.Flags().BoolVar(&all, "all", false, "liberate every outstanding title for the user")
	cmd.Flags().BoolVar(&force, "force", false, "re-liberate even if the file is already present and intact")
	cmd.Flags().IntVar(&limit, "limit", 0, "with --all, cap how many titles to process (0 = no cap)")
	_ = cmd.RegisterFlagCompletionFunc("user", climeta.CompleteUsernames)
	cmd.ValidArgsFunction = climeta.DBEntityCompleter(ListUnliberatedASINs)
	return cmd
}

// NewLiberationStatusCmd builds `boomtime books liberation-status`.
func NewLiberationStatusCmd() *cobra.Command {
	var user string
	cmd := &cobra.Command{
		Use:         "liberation-status",
		Short:       "Show liberation progress for a user's Audible library",
		Annotations: map[string]string{climeta.WebAnnotation: climeta.ClassReadonly},
		Long: `Report how many of a user's Audible titles are liberated, pending, or failed,
broken down by status.

  boomtime books liberation-status --user alice`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if user == "" {
				return fmt.Errorf("--user is required")
			}
			cfg := config.Load()
			ctx := context.Background()
			database, err := db.New(ctx, cfg.DatabaseURL())
			if err != nil {
				return fmt.Errorf("db connect: %w", err)
			}
			defer database.Close()

			store := liberate.NewStore(database.Pool)
			counts, cerr := store.StatusCounts(ctx, user)
			if cerr != nil {
				return cerr
			}
			pending, perr := store.ListUnliberated(ctx, user, 0)
			if perr != nil {
				return perr
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Library:  %s\n", orNone(cfg.BooksLibraryPath))
			fmt.Fprintf(out, "Enabled:  %v\n\n", cfg.LiberationEnabled())
			if len(counts) == 0 {
				fmt.Fprintln(out, "No Audible titles for that user.")
				return nil
			}
			// Stable order so successive runs diff cleanly.
			for _, s := range []string{
				liberate.StatusLiberated, liberate.StatusFailed, liberate.StatusDenied,
				liberate.StatusUnsupportedCodec, liberate.StatusUnsupportedFormat,
				liberate.StatusPending, liberate.StatusLicensing, liberate.StatusDownloading,
				liberate.StatusConverting, liberate.StatusSkipped,
			} {
				if n := counts[s]; n > 0 {
					fmt.Fprintf(out, "  %-20s %d\n", s, n)
				}
			}
			if n := counts[""]; n > 0 {
				fmt.Fprintf(out, "  %-20s %d\n", "(never attempted)", n)
			}
			fmt.Fprintf(out, "\n%d title(s) outstanding.\n", len(pending))
			return nil
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "the user to report on (required)")
	_ = cmd.RegisterFlagCompletionFunc("user", climeta.CompleteUsernames)
	cmd.ValidArgsFunction = func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

// ListUnliberatedASINs is the completion query lambda: the ASINs that still need
// liberating, across all users. Shared by the shell and web completion
// transports (see climeta.DBLister).
//
// It is deliberately NOT user-scoped: completion runs before --user is
// necessarily known, and offering a superset is far more useful than offering
// nothing. Bounded so a <TAB> never dumps a thousand candidates.
var ListUnliberatedASINs climeta.DBLister = func(ctx context.Context, database *db.DB) ([]string, error) {
	rows, err := database.Pool.Query(ctx, `
		SELECT DISTINCT external_id
		FROM public.reading_items
		WHERE source = 'audible'
		  AND (liberation_status IS NULL
		       OR liberation_status NOT IN ('liberated','denied','unsupported_format','skipped'))
		ORDER BY external_id
		LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// buildCLIService assembles a liberation service for a one-shot CLI run. It
// mirrors the job-wiring path (internal/books/jobs.buildLiberation) rather than
// sharing it, because the CLI has no notify hub and wants ffmpeg absence to be a
// hard, immediate error rather than a logged warning — a person watching a
// terminal should be told now, not after a download.
func buildCLIService(ctx context.Context, cfg *config.Config) (*liberate.Service, *db.DB, error) {
	database, err := db.New(ctx, cfg.DatabaseURL())
	if err != nil {
		return nil, nil, fmt.Errorf("db connect: %w", err)
	}
	sink, serr := liberate.NewFSSink(cfg.BooksLibraryPath)
	if serr != nil {
		database.Close()
		return nil, nil, serr
	}
	dec := liberate.NewFFmpegDecryptor(cfg.BooksFfmpegPath)
	if aerr := dec.Available(ctx); aerr != nil {
		database.Close()
		return nil, nil, fmt.Errorf("ffmpeg is required for liberation: %w", aerr)
	}
	return &liberate.Service{
		Store:     liberate.NewStore(database.Pool),
		Amazon:    amazon.NewStore(database),
		Sink:      sink,
		Decryptor: dec,
		WorkDir:   cfg.BooksWorkPath,
		Template:  cfg.BooksNamingTemplate,
	}, database, nil
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(not configured)"
	}
	return s
}

// humanBytes renders a size for terminal output.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
