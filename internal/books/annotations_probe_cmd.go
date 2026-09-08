// annotations_probe_cmd.go — `boomtime books probe-annotations` (boom-siwi.1).
//
// The operator front end for amazon.RunAnnotationProbes: load a user's Amazon
// device credential, pick (or accept) the ASINs to probe, run the sweep, and
// print a verdict.
//
// READONLY by classification and by construction — every call underneath is a
// GET. It is safe to point at production, which is the only place a real device
// credential lives.
//
// The probe's whole method rests on TWO ebook ASINs: one you know carries
// highlights and one you know does not. With a single title, "no annotations
// came back" is unattributable — the surface may not serve them, or that book
// may simply have none. --auto approximates the pair from reading_items
// (a finished/most-progressed title vs an untouched one), but naming them
// explicitly is stronger evidence.
package books

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/climeta"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// NewProbeAnnotationsCmd builds `boomtime books probe-annotations`.
func NewProbeAnnotationsCmd() *cobra.Command {
	var (
		user           string
		ebookASINs     []string
		audiobookASINs []string
		auto           bool
		cookies        bool
		dumpPath       string
		maxDatasets    int
	)

	cmd := &cobra.Command{
		Use:         "probe-annotations",
		Short:       "Probe whether our Amazon device credential can read Kindle highlights + Audible clips directly",
		Annotations: map[string]string{climeta.WebAnnotation: climeta.ClassReadonly},
		Long: `Probe the Amazon annotation surfaces with the device credential we already hold
(boom-siwi.1). Answers one question: does the fusion engine need Readwise as a
SOURCE of highlights, or can it read them itself?

Every call is a GET. Nothing is written, enqueued, or mutated.

Surfaces probed:
  - whispersync datasets — a census of EVERY namespace, including the ones the
    Kindle ingest currently discards
  - Fiona CDE sidecar (type=EBOK and type=AUDI) — the full record-type census,
    with the kindle.lpr filter that sidecar.go applies REMOVED
  - Audible /1.0/annotations/lastpositions
  - read.amazon.com/notebook — the highlights page, via exchanged website
    cookies (US marketplace only; skip with --no-cookies)

Supply two ebook ASINs — one you know has highlights, one you know does not —
so an empty result is attributable:

  boomtime books probe-annotations --user panda \
      --ebook-asin B01ABCDEFG --ebook-asin B09ZZZZZZZ \
      --audiobook-asin B0AUDIBLE1

Or let it pick from your library:

  boomtime books probe-annotations --user panda --auto

Response bodies carry your own highlight text, so they are captured only when
you ask for them:

  boomtime books probe-annotations --user panda --auto --dump ./probe.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(user) == "" {
				return fmt.Errorf("--user is required (the probe runs against that user's Amazon credential)")
			}
			if err := auth.LoadKeyFromEnv(); err != nil {
				return fmt.Errorf("cannot decrypt the Amazon credential: %w", err)
			}

			ctx := context.Background()
			database, err := db.New(ctx, config.Load().DatabaseURL())
			if err != nil {
				return fmt.Errorf("db connect: %w", err)
			}
			defer database.Close()

			cred, err := amazon.NewStore(database).Load(ctx, user)
			if err != nil {
				return fmt.Errorf("load Amazon credential for %q: %w", user, err)
			}

			out := cmd.OutOrStdout()
			if auto && len(ebookASINs) == 0 && len(audiobookASINs) == 0 {
				picked, perr := autoPickProbeASINs(ctx, database, user)
				if perr != nil {
					return fmt.Errorf("auto-pick ASINs: %w", perr)
				}
				ebookASINs, audiobookASINs = picked.ebooks, picked.audiobooks
				printPicks(out, picked)
			}

			report := amazon.RunAnnotationProbes(ctx, cred, amazon.AnnotationProbeOpts{
				EbookASINs:     ebookASINs,
				AudiobookASINs: audiobookASINs,
				Cookies:        cookies,
				MaxDatasets:    maxDatasets,
				KeepBodies:     dumpPath != "",
			})
			printReport(out, report)

			if dumpPath != "" {
				blob, merr := json.MarshalIndent(report, "", "  ")
				if merr != nil {
					return merr
				}
				if werr := os.WriteFile(dumpPath, blob, 0o600); werr != nil {
					return werr
				}
				fmt.Fprintf(out, "\nFull report (INCLUDING response bodies — this file contains your highlight text) written to %s\n", dumpPath)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&user, "user", "", "the user whose Amazon credential to probe (required)")
	cmd.Flags().StringSliceVar(&ebookASINs, "ebook-asin", nil, "Kindle ASIN to probe (repeatable; give one WITH highlights and one without)")
	cmd.Flags().StringSliceVar(&audiobookASINs, "audiobook-asin", nil, "Audible ASIN to probe for clips/bookmarks (repeatable)")
	cmd.Flags().BoolVar(&auto, "auto", true, "when no ASINs are given, pick candidates from the user's reading_items")
	cmd.Flags().BoolVar(&cookies, "cookies", true, "include the read.amazon.com/notebook probes (needs a US-marketplace credential)")
	cmd.Flags().StringVar(&dumpPath, "dump", "", "write the full report, including response bodies, to this JSON file")
	cmd.Flags().IntVar(&maxDatasets, "max-datasets", 8, "cap how many non-shelf whispersync datasets get their records pulled")

	_ = cmd.RegisterFlagCompletionFunc("user", climeta.CompleteUsernames)
	_ = cmd.RegisterFlagCompletionFunc("ebook-asin", climeta.DBEntityCompleter(ListKindleASINs))
	_ = cmd.RegisterFlagCompletionFunc("audiobook-asin", climeta.DBEntityCompleter(ListAudibleASINs))
	return cmd
}

// probePicks is the auto-selected ASIN set plus why each was chosen, so the
// operator can see whether the "has highlights" control really is one.
type probePicks struct {
	ebooks     []string
	audiobooks []string
	reasons    map[string]string
}

// autoPickProbeASINs approximates the two-ASIN method from stored state: the
// most-read Kindle title (most likely to carry highlights) and an untouched one
// (the negative control), plus a finished audiobook for the clips probe.
func autoPickProbeASINs(ctx context.Context, database *db.DB, owner string) (probePicks, error) {
	picks := probePicks{reasons: map[string]string{}}

	kindle, err := database.ListReadingItems(ctx, owner, "kindle")
	if err != nil {
		return picks, err
	}
	// Most-progressed first; finished counts as fully progressed.
	sort.SliceStable(kindle, func(i, j int) bool { return readProgress(kindle[i]) > readProgress(kindle[j]) })
	if len(kindle) > 0 && readProgress(kindle[0]) > 0 {
		top := kindle[0]
		picks.ebooks = append(picks.ebooks, top.ExternalID)
		picks.reasons[top.ExternalID] = fmt.Sprintf("most-read Kindle title (%d%%%s) — %s", readProgress(top), finishedNote(top), top.Title)
	}
	// Negative control: an owned title with no progress at all.
	for i := len(kindle) - 1; i >= 0; i-- {
		it := kindle[i]
		if readProgress(it) == 0 && it.ExternalID != "" && !contains(picks.ebooks, it.ExternalID) {
			picks.ebooks = append(picks.ebooks, it.ExternalID)
			picks.reasons[it.ExternalID] = "negative control: owned but 0% read — " + it.Title
			break
		}
	}

	audible, err := database.ListReadingItems(ctx, owner, "audible")
	if err != nil {
		return picks, err
	}
	sort.SliceStable(audible, func(i, j int) bool { return readProgress(audible[i]) > readProgress(audible[j]) })
	if len(audible) > 0 && readProgress(audible[0]) > 0 {
		top := audible[0]
		picks.audiobooks = append(picks.audiobooks, top.ExternalID)
		picks.reasons[top.ExternalID] = fmt.Sprintf("most-listened Audible title (%d%%%s) — %s", readProgress(top), finishedNote(top), top.Title)
	}
	return picks, nil
}

func readProgress(it db.ReadingItem) int {
	if it.Finished {
		return 100
	}
	return it.ProgressPercent
}

func finishedNote(it db.ReadingItem) string {
	if it.Finished {
		return ", finished"
	}
	return ""
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// ListKindleASINs / ListAudibleASINs are the completion query lambdas for the
// two ASIN flags (see the boomtime-cli-smart-completion convention).
var ListKindleASINs climeta.DBLister = func(ctx context.Context, database *db.DB) ([]string, error) {
	return asinSuggestions(ctx, database, "kindle")
}

var ListAudibleASINs climeta.DBLister = func(ctx context.Context, database *db.DB) ([]string, error) {
	return asinSuggestions(ctx, database, "audible")
}

// asinSuggestions renders "ASIN\tTitle" rows across every owner's items. A
// completer has no --user context to scope by (the flag may not be typed yet),
// so it offers all and lets the probe fail loudly on a mismatch.
func asinSuggestions(ctx context.Context, database *db.DB, source string) ([]string, error) {
	rows, err := database.Pool.Query(ctx,
		`SELECT external_id, title FROM reading_items
		  WHERE source = $1 AND external_id <> ''
		  ORDER BY finished DESC, progress_percent DESC
		  LIMIT 200`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var asin, title string
		if err := rows.Scan(&asin, &title); err != nil {
			return nil, err
		}
		out = append(out, asin+"\t"+title)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func printPicks(out io.Writer, p probePicks) {
	fmt.Fprintln(out, "Auto-picked probe targets:")
	for _, a := range append(append([]string{}, p.ebooks...), p.audiobooks...) {
		fmt.Fprintf(out, "  %-12s %s\n", a, p.reasons[a])
	}
	if len(p.ebooks) < 2 {
		fmt.Fprintln(out, "  NOTE: fewer than two ebook ASINs — an empty result will be unattributable.")
		fmt.Fprintln(out, "        Re-run with --ebook-asin for a title you KNOW you highlighted.")
	}
	fmt.Fprintln(out)
}

func printReport(out io.Writer, r amazon.AnnotationReport) {
	fmt.Fprintf(out, "Annotation-surface probe — marketplace %s, %d probes\n\n", r.Marketplace, len(r.Probes))
	for _, p := range r.Probes {
		fmt.Fprintf(out, "%s %s\n", verdictGlyph(p.Verdict), p.Name)
		if p.Endpoint != "" {
			fmt.Fprintf(out, "    %s [%s] HTTP %d\n", p.Endpoint, p.Transport, p.Status)
		}
		if p.Error != "" {
			fmt.Fprintf(out, "    error: %s\n", p.Error)
		}
		if p.Detail != "" {
			fmt.Fprintf(out, "    %s\n", p.Detail)
		}
		for _, k := range sortedKeys(p.Census) {
			line := fmt.Sprintf("      %-28s %d", k, p.Census[k])
			if f := p.Fields[k]; len(f) > 0 {
				line += "   fields: " + strings.Join(f, ", ")
			}
			fmt.Fprintln(out, line)
		}
		for _, s := range p.Samples {
			fmt.Fprintf(out, "      · %s\n", s)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "VERDICT: %s\n%s\n", strings.ToUpper(string(r.Verdict)), r.Summary)
}

func verdictGlyph(v amazon.AnnotationVerdict) string {
	switch v {
	case amazon.AnnotationPass:
		return "[PASS]"
	case amazon.AnnotationWarn:
		return "[warn]"
	case amazon.AnnotationFail:
		return "[FAIL]"
	default:
		return "[skip]"
	}
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
