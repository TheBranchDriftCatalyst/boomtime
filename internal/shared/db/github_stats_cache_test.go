package db

// Integration coverage for github_stats_cache.go (gaka-anh Phase 2). The
// load-bearing property is IDEMPOTENCY: the table is one row per user and every
// write is an upsert-replace, so a re-sync can never accrue duplicate stats.
// These tests pin that (row count stays 1, values REPLACE not accumulate) plus
// the JSON round-trip and clear semantics. They need the isolated boomtime_test
// DB (openTestDB skips when unavailable).

import (
	"context"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

func sampleStatsRow(user string) GithubStatsCacheRow {
	return GithubStatsCacheRow{
		Username: user,
		Login:    "octocat",
		Totals: model.GithubTotals{
			Commits:            42,
			PullRequests:       7,
			PullRequestReviews: 5,
			Issues:             3,
			Repositories:       9,
			RestrictedPrivate:  11,
			TotalContributions: 100,
			Followers:          10,
			Following:          4,
			Stars:              150,
			PublicRepos:        6,
			PublicGists:        2,
			AccountAgeDays:     4000,
		},
		ContributionGrid: []model.GithubContributionDay{
			{Date: "2026-01-01", Count: 3},
			{Date: "2026-01-02", Count: 0},
			{Date: "2026-01-03", Count: 5},
		},
		TopRepos: []model.GithubTopRepo{
			{Name: "repo-a", Stars: 100, Language: "Go", URL: "https://github.com/octocat/repo-a"},
		},
		Languages: []model.GithubLanguage{
			{Name: "Go", Bytes: 12345},
			{Name: "TypeScript", Bytes: 5000},
		},
		FetchedAt: time.Now().UTC().Truncate(time.Second),
	}
}

func countStatsRows(t *testing.T, d *DB, ctx context.Context, user string) int {
	t.Helper()
	var n int
	if err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM github_stats_cache WHERE username=$1`, user).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func TestGithubStatsCache_RoundTrip(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	f := newSender(t, d, "ghsc_rt")
	row := sampleStatsRow(f.name)

	if err := d.UpsertGithubStatsCache(ctx, row); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, ok, err := d.GetGithubStatsCache(ctx, f.name)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Login != "octocat" {
		t.Errorf("login = %q, want octocat", got.Login)
	}
	if got.Totals.Commits != 42 || got.Totals.PullRequestReviews != 5 || got.Totals.Stars != 150 {
		t.Errorf("totals round-trip mismatch: %+v", got.Totals)
	}
	if len(got.ContributionGrid) != 3 || got.ContributionGrid[2].Count != 5 {
		t.Errorf("grid round-trip mismatch: %+v", got.ContributionGrid)
	}
	if len(got.Languages) != 2 || got.Languages[0].Bytes != 12345 {
		t.Errorf("languages round-trip mismatch: %+v", got.Languages)
	}
}

// The idempotency contract at the DB layer: two upserts of the SAME user yield
// exactly ONE row, and a second upsert with DIFFERENT values REPLACES (never
// adds/accumulates) — proving a re-sync is a no-op-on-shape.
func TestGithubStatsCache_UpsertIsReplaceNotAccumulate(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	f := newSender(t, d, "ghsc_idem")
	row := sampleStatsRow(f.name)

	// First write.
	if err := d.UpsertGithubStatsCache(ctx, row); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if n := countStatsRows(t, d, ctx, f.name); n != 1 {
		t.Fatalf("after first upsert row count = %d, want 1", n)
	}

	// Identical second write — still exactly one row, values unchanged.
	if err := d.UpsertGithubStatsCache(ctx, row); err != nil {
		t.Fatalf("second (identical) upsert: %v", err)
	}
	if n := countStatsRows(t, d, ctx, f.name); n != 1 {
		t.Fatalf("after identical re-upsert row count = %d, want 1 (accrued duplicates!)", n)
	}
	got, _, _ := d.GetGithubStatsCache(ctx, f.name)
	if got.Totals.Commits != 42 {
		t.Errorf("commits after identical re-upsert = %d, want 42 (must not double)", got.Totals.Commits)
	}

	// Third write with DIFFERENT values — replaces in place, still one row.
	changed := row
	changed.Totals.Commits = 99
	changed.Login = "octocat2"
	changed.ContributionGrid = []model.GithubContributionDay{{Date: "2026-02-01", Count: 1}}
	if err := d.UpsertGithubStatsCache(ctx, changed); err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	if n := countStatsRows(t, d, ctx, f.name); n != 1 {
		t.Fatalf("after value-changing upsert row count = %d, want 1", n)
	}
	got, _, _ = d.GetGithubStatsCache(ctx, f.name)
	if got.Totals.Commits != 99 || got.Login != "octocat2" || len(got.ContributionGrid) != 1 {
		t.Errorf("replace failed: commits=%d login=%q grid=%d", got.Totals.Commits, got.Login, len(got.ContributionGrid))
	}
}

func TestGithubStatsCache_GetAbsentAndClear(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	f := newSender(t, d, "ghsc_clear")

	if _, ok, err := d.GetGithubStatsCache(ctx, f.name); ok || err != nil {
		t.Fatalf("absent get: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if err := d.UpsertGithubStatsCache(ctx, sampleStatsRow(f.name)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := d.ClearGithubStatsCache(ctx, f.name); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, ok, _ := d.GetGithubStatsCache(ctx, f.name); ok {
		t.Fatalf("row still present after clear")
	}
	// Clear is idempotent — no error on an already-absent row.
	if err := d.ClearGithubStatsCache(ctx, f.name); err != nil {
		t.Fatalf("second clear errored: %v", err)
	}
}
