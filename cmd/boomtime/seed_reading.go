// seed_reading.go: `boomtime seed-reading-demo --user <username>` — a dev/test
// only fixture seeder that populates a RICH, deterministic reading dataset
// (reading_items + reading_activity) for one owner so the Reading dashboard,
// Books Explore surface, and the e2e reading specs have real data WITHOUT an
// Amazon sync. reading_items/reading_activity normally only arrive via the
// Audible/Amazon sync path (there is no HTTP ingest for them), so demo + e2e
// cannot seed them over the API — this command seeds them operator-side.
//
// Safety: refuses to run unless BOOM_ENV is dev/test (case-insensitive) or
// --force is passed, so it can never populate a production DB by accident.
// Idempotent — every row is an ON CONFLICT upsert (see db.UpsertReadingItem /
// db.UpsertReadingActivity), so re-running just refreshes the same fixture.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/spf13/cobra"
)

const seedReadingSource = "audible"

func seedReadingDemoCmd() *cobra.Command {
	var user string
	var force bool
	cmd := &cobra.Command{
		Use:   "seed-reading-demo",
		Short: "Seed a rich, deterministic reading fixture (dev/test only) for one user",
		Long: `Populate reading_items + reading_activity with a deterministic demo library for
--user so the Reading dashboard, Books Explore, and the e2e reading specs have
data without an Amazon sync (there is no HTTP ingest for reading data).

Seeds ~40 books across genres + multi-book series (mostly finished, spread over
the last 12 months, a handful genuinely in-progress, a few on the want list),
12 monthly listening buckets, and recent daily buckets. Every write is an
idempotent upsert.

Refuses to run unless BOOM_ENV is dev/test or --force is passed — it must never
touch a production database.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(user) == "" {
				return fmt.Errorf("--user is required")
			}
			cfg := config.Load()
			if !seedReadingAllowed(cfg.Env, force) {
				return fmt.Errorf(
					"refusing to seed: BOOM_ENV=%q is not dev/test (pass --force to override)", cfg.Env)
			}
			ctx := context.Background()
			return runSeedReadingDemo(ctx, cfg.DatabaseURL(), user, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVarP(&user, "user", "u", "", "The owner (username) to seed reading data for")
	cmd.Flags().BoolVar(&force, "force", false, "Seed even when BOOM_ENV is not dev/test (NEVER use against prod)")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.RegisterFlagCompletionFunc("user", completeUsernames)
	return cmd
}

// seedReadingAllowed gates the command to dev/test unless --force is set.
func seedReadingAllowed(env string, force bool) bool {
	if force {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "dev", "test":
		return true
	}
	return false
}

// runSeedReadingDemo is the extracted body so a smoke test can drive the whole
// pipeline against an in-process DB without shelling through cobra/config.
func runSeedReadingDemo(ctx context.Context, databaseURL, user string, out io.Writer) error {
	database, err := db.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("db connect: %w", err)
	}
	defer database.Close()

	// Fail early + clearly if the owner row is missing — reading_items.owner is
	// a FK to users(username), so an insert would otherwise fail deep in pgx.
	if exists, err := userExists(ctx, database, user); err != nil {
		return fmt.Errorf("check user %q: %w", user, err)
	} else if !exists {
		return fmt.Errorf("user %q does not exist — create it first (boomtime create-user -u %s)", user, user)
	}

	now := time.Now().UTC()
	items := buildDemoReadingItems(user, now)
	for _, it := range items {
		if err := database.UpsertReadingItem(ctx, it); err != nil {
			return fmt.Errorf("upsert reading_item %q: %w", it.Title, err)
		}
	}

	activity := buildDemoReadingActivity(user, now)
	for _, a := range activity {
		if err := database.UpsertReadingActivity(ctx, a); err != nil {
			return fmt.Errorf("upsert reading_activity %s: %w", a.BucketDate.Format("2006-01-02"), err)
		}
	}

	fmt.Fprintf(out, "Seeded reading demo for %q: %d reading_items, %d reading_activity buckets.\n",
		user, len(items), len(activity))
	return nil
}

func userExists(ctx context.Context, database *db.DB, user string) (bool, error) {
	var exists bool
	err := database.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)`, user).Scan(&exists)
	return exists, err
}

// demoBook is the intrinsic (data-only) definition of one fixture book. Status,
// rating, progress, and dates are derived deterministically in
// buildDemoReadingItems so the intrinsic table stays readable.
type demoBook struct {
	Title      string
	Subtitle   string
	Authors    string
	Narrators  string
	Series     string
	Genre      string   // primary genre (matches the FE genre facets)
	Subgenres  []string // extra genre-array entries beyond Genre
	RuntimeMin int
	Goodreads  float64
	Status     string  // "read" | "reading" | "want"
	Progress   int     // for "reading": 20..80; ignored otherwise
	Rating     float64 // user rating for "read" (0 = leave null)
}

// demoBooks is the deterministic fixture: 40 titles across 6 genres and 3
// multi-book series, with 28 read / 5 reading / 7 want. Ordering is stable so
// finished_at assignment (below) spreads reads across all 12 recent months.
func demoBooks() []demoBook {
	const (
		gSFF  = "Science Fiction & Fantasy"
		gLit  = "Literature & Fiction"
		gRom  = "Romance"
		gMys  = "Mystery Thriller & Suspense"
		gBiz  = "Business & Careers"
		gTeen = "Teen & Young Adult"
	)
	return []demoBook{
		// ── Series 1: The Expanse (4 books, all read) ───────────────────────
		{Title: "Leviathan Wakes", Authors: "James S.A. Corey", Narrators: "Jefferson Mays", Series: "The Expanse", Genre: gSFF, Subgenres: []string{"Space Opera"}, RuntimeMin: 1230, Goodreads: 4.3, Status: "read", Rating: 5},
		{Title: "Caliban's War", Authors: "James S.A. Corey", Narrators: "Jefferson Mays", Series: "The Expanse", Genre: gSFF, Subgenres: []string{"Space Opera"}, RuntimeMin: 1290, Goodreads: 4.3, Status: "read", Rating: 5},
		{Title: "Abaddon's Gate", Authors: "James S.A. Corey", Narrators: "Jefferson Mays", Series: "The Expanse", Genre: gSFF, Subgenres: []string{"Space Opera"}, RuntimeMin: 1200, Goodreads: 4.2, Status: "read", Rating: 4},
		{Title: "Cibola Burn", Authors: "James S.A. Corey", Narrators: "Jefferson Mays", Series: "The Expanse", Genre: gSFF, Subgenres: []string{"Space Opera"}, RuntimeMin: 1260, Goodreads: 4.1, Status: "read", Rating: 4},

		// ── Series 2: Mistborn (3 books: 2 read, 1 reading) ─────────────────
		{Title: "The Final Empire", Subtitle: "Mistborn Book 1", Authors: "Brandon Sanderson", Narrators: "Michael Kramer", Series: "Mistborn", Genre: gSFF, Subgenres: []string{"Epic Fantasy"}, RuntimeMin: 1470, Goodreads: 4.5, Status: "read", Rating: 5},
		{Title: "The Well of Ascension", Subtitle: "Mistborn Book 2", Authors: "Brandon Sanderson", Narrators: "Michael Kramer", Series: "Mistborn", Genre: gSFF, Subgenres: []string{"Epic Fantasy"}, RuntimeMin: 1500, Goodreads: 4.4, Status: "read", Rating: 4},
		{Title: "The Hero of Ages", Subtitle: "Mistborn Book 3", Authors: "Brandon Sanderson", Narrators: "Michael Kramer", Series: "Mistborn", Genre: gSFF, Subgenres: []string{"Epic Fantasy"}, RuntimeMin: 1480, Goodreads: 4.5, Status: "reading", Progress: 42},

		// ── Series 3: Bridgerton (4 books: 2 read, 2 want) ──────────────────
		{Title: "The Duke and I", Subtitle: "Bridgerton Book 1", Authors: "Julia Quinn", Narrators: "Rosalyn Landor", Series: "Bridgerton", Genre: gRom, Subgenres: []string{"Historical Romance"}, RuntimeMin: 690, Goodreads: 3.9, Status: "read", Rating: 4},
		{Title: "The Viscount Who Loved Me", Subtitle: "Bridgerton Book 2", Authors: "Julia Quinn", Narrators: "Rosalyn Landor", Series: "Bridgerton", Genre: gRom, Subgenres: []string{"Historical Romance"}, RuntimeMin: 720, Goodreads: 4.2, Status: "read", Rating: 5},
		{Title: "An Offer From a Gentleman", Subtitle: "Bridgerton Book 3", Authors: "Julia Quinn", Narrators: "Rosalyn Landor", Series: "Bridgerton", Genre: gRom, Subgenres: []string{"Historical Romance"}, RuntimeMin: 700, Goodreads: 4.1, Status: "want"},
		{Title: "Romancing Mister Bridgerton", Subtitle: "Bridgerton Book 4", Authors: "Julia Quinn", Narrators: "Rosalyn Landor", Series: "Bridgerton", Genre: gRom, Subgenres: []string{"Historical Romance"}, RuntimeMin: 710, Goodreads: 4.3, Status: "want"},

		// ── Science Fiction & Fantasy standalones ───────────────────────────
		{Title: "Project Hail Mary", Authors: "Andy Weir", Narrators: "Ray Porter", Genre: gSFF, Subgenres: []string{"Hard Science Fiction"}, RuntimeMin: 970, Goodreads: 4.5, Status: "read", Rating: 5},
		{Title: "The Fifth Season", Authors: "N.K. Jemisin", Narrators: "Robin Miles", Genre: gSFF, Subgenres: []string{"Epic Fantasy"}, RuntimeMin: 940, Goodreads: 4.3, Status: "read", Rating: 5},
		{Title: "A Memory Called Empire", Authors: "Arkady Martine", Narrators: "Amy Landon", Genre: gSFF, Subgenres: []string{"Space Opera"}, RuntimeMin: 950, Goodreads: 4.1, Status: "read", Rating: 4},
		{Title: "The Name of the Wind", Authors: "Patrick Rothfuss", Narrators: "Nick Podehl", Genre: gSFF, Subgenres: []string{"Epic Fantasy"}, RuntimeMin: 1660, Goodreads: 4.5, Status: "read", Rating: 5},
		{Title: "Gideon the Ninth", Authors: "Tamsyn Muir", Narrators: "Moira Quirk", Genre: gSFF, Subgenres: []string{"Science Fantasy"}, RuntimeMin: 970, Goodreads: 4.2, Status: "reading", Progress: 33},
		{Title: "Dungeon Crawler Carl", Authors: "Matt Dinniman", Narrators: "Jeff Hays", Genre: gSFF, Subgenres: []string{"LitRPG"}, RuntimeMin: 900, Goodreads: 4.6, Status: "want"},

		// ── Literature & Fiction ────────────────────────────────────────────
		{Title: "The Midnight Library", Authors: "Matt Haig", Narrators: "Carey Mulligan", Genre: gLit, Subgenres: []string{"Contemporary Fiction"}, RuntimeMin: 510, Goodreads: 4.0, Status: "read", Rating: 4},
		{Title: "A Little Life", Authors: "Hanya Yanagihara", Narrators: "Oliver Wyman", Genre: gLit, Subgenres: []string{"Literary Fiction"}, RuntimeMin: 1880, Goodreads: 4.3, Status: "read", Rating: 5},
		{Title: "Cloud Cuckoo Land", Authors: "Anthony Doerr", Narrators: "Marin Ireland", Genre: gLit, Subgenres: []string{"Literary Fiction"}, RuntimeMin: 880, Goodreads: 4.1, Status: "read", Rating: 4},
		{Title: "Tomorrow, and Tomorrow, and Tomorrow", Authors: "Gabrielle Zevin", Narrators: "Jennifer Kim", Genre: gLit, Subgenres: []string{"Contemporary Fiction"}, RuntimeMin: 810, Goodreads: 4.2, Status: "read", Rating: 5},
		{Title: "Demon Copperhead", Authors: "Barbara Kingsolver", Narrators: "Charlie Thurston", Genre: gLit, Subgenres: []string{"Literary Fiction"}, RuntimeMin: 1290, Goodreads: 4.5, Status: "reading", Progress: 58},
		{Title: "The Overstory", Authors: "Richard Powers", Narrators: "Suzanne Toren", Genre: gLit, Subgenres: []string{"Literary Fiction"}, RuntimeMin: 1290, Goodreads: 4.1, Status: "want"},

		// ── Mystery, Thriller & Suspense ────────────────────────────────────
		{Title: "The Silent Patient", Authors: "Alex Michaelides", Narrators: "Louise Brealey", Genre: gMys, Subgenres: []string{"Psychological Thriller"}, RuntimeMin: 510, Goodreads: 4.1, Status: "read", Rating: 4},
		{Title: "Gone Girl", Authors: "Gillian Flynn", Narrators: "Julia Whelan", Genre: gMys, Subgenres: []string{"Psychological Thriller"}, RuntimeMin: 1170, Goodreads: 4.1, Status: "read", Rating: 5},
		{Title: "The Thursday Murder Club", Authors: "Richard Osman", Narrators: "Lesley Manville", Genre: gMys, Subgenres: []string{"Cozy Mystery"}, RuntimeMin: 720, Goodreads: 4.0, Status: "read", Rating: 4},
		{Title: "The Girl with the Dragon Tattoo", Authors: "Stieg Larsson", Narrators: "Simon Vance", Genre: gMys, Subgenres: []string{"Crime Fiction"}, RuntimeMin: 1620, Goodreads: 4.2, Status: "read", Rating: 4},
		{Title: "The Guest List", Authors: "Lucy Foley", Narrators: "Jot Davies", Genre: gMys, Subgenres: []string{"Whodunit"}, RuntimeMin: 510, Goodreads: 3.9, Status: "reading", Progress: 71},
		{Title: "Verity", Authors: "Colleen Hoover", Narrators: "Vanessa Johansson", Genre: gMys, Subgenres: []string{"Psychological Thriller"}, RuntimeMin: 480, Goodreads: 4.3, Status: "want"},

		// ── Business & Careers ──────────────────────────────────────────────
		{Title: "Atomic Habits", Subtitle: "An Easy & Proven Way to Build Good Habits", Authors: "James Clear", Narrators: "James Clear", Genre: gBiz, Subgenres: []string{"Personal Development"}, RuntimeMin: 330, Goodreads: 4.4, Status: "read", Rating: 5},
		{Title: "The Lean Startup", Authors: "Eric Ries", Narrators: "Eric Ries", Genre: gBiz, Subgenres: []string{"Entrepreneurship"}, RuntimeMin: 510, Goodreads: 4.1, Status: "read", Rating: 4},
		{Title: "Thinking, Fast and Slow", Authors: "Daniel Kahneman", Narrators: "Patrick Egan", Genre: gBiz, Subgenres: []string{"Behavioral Economics"}, RuntimeMin: 1200, Goodreads: 4.2, Status: "read", Rating: 4},
		{Title: "Deep Work", Subtitle: "Rules for Focused Success in a Distracted World", Authors: "Cal Newport", Narrators: "Jeff Bottoms", Genre: gBiz, Subgenres: []string{"Productivity"}, RuntimeMin: 460, Goodreads: 4.2, Status: "read", Rating: 5},
		{Title: "Never Split the Difference", Authors: "Chris Voss", Narrators: "Michael Kramer", Genre: gBiz, Subgenres: []string{"Negotiation"}, RuntimeMin: 490, Goodreads: 4.4, Status: "reading", Progress: 26},
		{Title: "The Hard Thing About Hard Things", Authors: "Ben Horowitz", Narrators: "Kevin Kenerly", Genre: gBiz, Subgenres: []string{"Leadership"}, RuntimeMin: 480, Goodreads: 4.2, Status: "want"},

		// ── Teen & Young Adult ──────────────────────────────────────────────
		{Title: "Six of Crows", Authors: "Leigh Bardugo", Narrators: "Jay Snyder", Genre: gTeen, Subgenres: []string{"Fantasy"}, RuntimeMin: 900, Goodreads: 4.5, Status: "read", Rating: 5},
		{Title: "The Hunger Games", Authors: "Suzanne Collins", Narrators: "Tatiana Maslany", Genre: gTeen, Subgenres: []string{"Dystopian"}, RuntimeMin: 660, Goodreads: 4.3, Status: "read", Rating: 5},
		{Title: "They Both Die at the End", Authors: "Adam Silvera", Narrators: "Michael Crouch", Genre: gTeen, Subgenres: []string{"Contemporary"}, RuntimeMin: 510, Goodreads: 4.0, Status: "read", Rating: 4},
		{Title: "Children of Blood and Bone", Authors: "Tomi Adeyemi", Narrators: "Bahni Turpin", Genre: gTeen, Subgenres: []string{"Fantasy"}, RuntimeMin: 1050, Goodreads: 4.1, Status: "read", Rating: 4},
		{Title: "One of Us Is Lying", Authors: "Karen M. McManus", Narrators: "Kim Mai Guest", Genre: gTeen, Subgenres: []string{"Mystery"}, RuntimeMin: 640, Goodreads: 4.0, Status: "want"},
		{Title: "Legendborn", Authors: "Tracy Deonn", Narrators: "Joniece Abbott-Pratt", Genre: gTeen, Subgenres: []string{"Fantasy"}, RuntimeMin: 940, Goodreads: 4.2, Status: "want"},
	}
}

// buildDemoReadingItems turns the intrinsic demoBooks table into db.ReadingItem
// rows for owner, deriving status-dependent fields deterministically:
//   - read:    finished=true, finished_at spread across the last 12 months
//     (each read book to a distinct month, cycling), started_at a couple weeks
//     before its finish. progress_percent=100.
//   - reading: finished=false, progress from the table (20..80 — genuinely
//     in-progress, NOT >95%), started_at recent.
//   - want:    no dates, progress 0.
func buildDemoReadingItems(owner string, now time.Time) []db.ReadingItem {
	books := demoBooks()
	out := make([]db.ReadingItem, 0, len(books))
	readIdx := 0
	for i, b := range books {
		asin := fmt.Sprintf("DEMOASIN%04d", i+1)
		genres := append([]string{b.Genre}, b.Subgenres...)
		genresJSON, _ := json.Marshal(genres)

		it := db.ReadingItem{
			Owner:           owner,
			Source:          seedReadingSource,
			ExternalID:      asin,
			Title:           b.Title,
			Authors:         b.Authors,
			CoverURL:        fmt.Sprintf("https://picsum.photos/seed/%s/200/300", asin),
			Status:          b.Status,
			RawMeta:         []byte("{}"),
			Subtitle:        b.Subtitle,
			Narrators:       b.Narrators,
			Series:          b.Series,
			ISBN:            fmt.Sprintf("978%010d", 1000000+i),
			AmazonASIN:      asin,
			Genres:          genresJSON,
			GoodreadsRating: floatPtr(b.Goodreads),
		}
		if b.RuntimeMin > 0 {
			rt := b.RuntimeMin
			it.RuntimeMin = &rt
		}
		// A deterministic purchase_date a bit before any reading started.
		pd := monthStart(now, (i%12)+1)
		it.PurchaseDate = &pd

		switch b.Status {
		case "read":
			it.Finished = true
			it.ProgressPercent = 100
			// Spread finishes across the last 12 months: month = readIdx%12,
			// day walks so multiple-per-month land on different days.
			monthsAgo := readIdx % 12
			day := (readIdx*5)%24 + 2
			fin := monthStart(now, monthsAgo).AddDate(0, 0, day-1).Add(19 * time.Hour)
			if fin.After(now) {
				fin = now.Add(-24 * time.Hour)
			}
			it.FinishedAt = &fin
			start := fin.AddDate(0, 0, -(14 + readIdx%10))
			it.StartedAt = &start
			if b.Rating > 0 {
				it.Rating = floatPtr(b.Rating)
			}
			readIdx++
		case "reading":
			it.Finished = false
			prog := b.Progress
			if prog <= 0 || prog >= 95 {
				prog = 45 // safety: never let a "reading" book look finished
			}
			it.ProgressPercent = prog
			start := now.AddDate(0, 0, -(3 + i%9))
			it.StartedAt = &start
		default: // "want"
			it.Finished = false
			it.ProgressPercent = 0
		}
		out = append(out, it)
	}
	return out
}

// buildDemoReadingActivity builds 12 monthly listening buckets (a rising-then-
// varying curve, a few to tens of hours each) plus 5 recent daily buckets in
// the last 7 days so the "listening this week / in range" tiles are non-zero.
func buildDemoReadingActivity(owner string, now time.Time) []db.ReadingActivity {
	out := make([]db.ReadingActivity, 0, 12+5)

	// Monthly buckets: month 0 (current) back through month 11.
	// Deterministic hours between ~4h and ~34h with a wave so the trend line
	// has shape rather than a flat fill.
	monthlyHours := []int{18, 12, 26, 9, 31, 15, 22, 6, 28, 11, 34, 20}
	for m := 0; m < 12; m++ {
		bucket := monthStart(now, m)
		hours := monthlyHours[m%len(monthlyHours)]
		out = append(out, db.ReadingActivity{
			Owner:            owner,
			Source:           seedReadingSource,
			Granularity:      "month",
			BucketDate:       bucket,
			ListeningSeconds: int64(hours) * 3600,
		})
	}

	// Daily buckets over the last 7 days (5 of them non-contiguous), 25–115 min
	// each so "listening this week" shows a real number.
	dailyMinutes := map[int]int{1: 45, 2: 25, 3: 90, 5: 60, 6: 115}
	for daysAgo, mins := range dailyMinutes {
		day := dayStart(now.AddDate(0, 0, -daysAgo))
		out = append(out, db.ReadingActivity{
			Owner:            owner,
			Source:           seedReadingSource,
			Granularity:      "day",
			BucketDate:       day,
			ListeningSeconds: int64(mins) * 60,
		})
	}
	return out
}

// monthStart returns the first day (00:00 UTC) of the month monthsAgo before
// now.
func monthStart(now time.Time, monthsAgo int) time.Time {
	y, mo, _ := now.AddDate(0, -monthsAgo, 0).Date()
	return time.Date(y, mo, 1, 0, 0, 0, 0, time.UTC)
}

// dayStart truncates to 00:00 UTC of t's date.
func dayStart(t time.Time) time.Time {
	y, mo, d := t.Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
}

func floatPtr(f float64) *float64 { return &f }
