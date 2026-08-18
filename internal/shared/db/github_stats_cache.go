// github_stats_cache.go — storage for the per-user GitHub stats cache
// (gaka-anh Phase 2). See migrations/00049_github_stats_cache.sql for the
// columns + the idempotency rationale.
//
// IDEMPOTENCY BY CONSTRUCTION: the table is ONE ROW PER USER (username PRIMARY
// KEY) and UpsertGithubStatsCache is a single INSERT ... ON CONFLICT (username)
// DO UPDATE. Re-running a sync therefore REPLACES the row wholesale — the row
// count stays 1/user and the stored totals/grid equal a single fetch. Nothing
// here appends; there is no per-day/per-repo child table to accrue duplicates.
//
// No secret material lives on this row — only public GitHub aggregates. The
// decrypted access token never reaches this layer.
package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// GithubStatsCacheRow is one user's cached GitHub stats. The JSON-backed
// fields map 1:1 to the *_json JSONB columns: Totals to totals_json (a single
// extensible aggregate blob) and the three slices to their arrays. FetchedAt
// drives the handler-layer TTL.
type GithubStatsCacheRow struct {
	Username         string
	Login            string
	Totals           model.GithubTotals
	ContributionGrid []model.GithubContributionDay
	TopRepos         []model.GithubTopRepo
	Languages        []model.GithubLanguage
	FetchedAt        time.Time
}

// UpsertGithubStatsCache writes (or REPLACES) the single cache row for
// row.Username. This is the idempotency primitive: ON CONFLICT (username) DO
// UPDATE means a second sync of the same user overwrites — never doubles —
// every value. The three JSONB columns are marshaled from the typed slices; a
// nil slice is stored as an empty JSON array (never SQL NULL) so the NOT NULL
// constraints hold and reads never have to special-case NULL.
func (d *DB) UpsertGithubStatsCache(ctx context.Context, row GithubStatsCacheRow) error {
	if row.Username == "" {
		return errors.New("UpsertGithubStatsCache: empty username")
	}
	totalsJSON, err := json.Marshal(row.Totals)
	if err != nil {
		return err
	}
	gridJSON, err := marshalJSONArray(row.ContributionGrid)
	if err != nil {
		return err
	}
	reposJSON, err := marshalJSONArray(row.TopRepos)
	if err != nil {
		return err
	}
	langsJSON, err := marshalJSONArray(row.Languages)
	if err != nil {
		return err
	}
	fetchedAt := row.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now().UTC()
	}
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO github_stats_cache
		    (username, login, totals_json, contribution_grid_json, top_repos_json, languages_json, fetched_at)
		 VALUES ($1, $2, $3::jsonb, $4::jsonb, $5::jsonb, $6::jsonb, $7)
		 ON CONFLICT (username) DO UPDATE SET
		     login                  = EXCLUDED.login,
		     totals_json            = EXCLUDED.totals_json,
		     contribution_grid_json = EXCLUDED.contribution_grid_json,
		     top_repos_json         = EXCLUDED.top_repos_json,
		     languages_json         = EXCLUDED.languages_json,
		     fetched_at             = EXCLUDED.fetched_at`,
		row.Username, row.Login, totalsJSON, gridJSON, reposJSON, langsJSON, fetchedAt,
	)
	return err
}

// GetGithubStatsCache returns the cached row for username. The second return
// value is false when no row is present (never synced). Any other error is
// bubbled unchanged.
func (d *DB) GetGithubStatsCache(ctx context.Context, username string) (GithubStatsCacheRow, bool, error) {
	var row GithubStatsCacheRow
	if username == "" {
		return row, false, errors.New("GetGithubStatsCache: empty username")
	}
	var totalsJSON, gridJSON, reposJSON, langsJSON []byte
	err := d.Pool.QueryRow(ctx,
		`SELECT username, login, totals_json, contribution_grid_json, top_repos_json, languages_json, fetched_at
		   FROM github_stats_cache
		  WHERE username = $1`,
		username,
	).Scan(
		&row.Username, &row.Login, &totalsJSON, &gridJSON, &reposJSON, &langsJSON, &row.FetchedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GithubStatsCacheRow{}, false, nil
		}
		return GithubStatsCacheRow{}, false, err
	}
	if err := json.Unmarshal(totalsJSON, &row.Totals); err != nil {
		return GithubStatsCacheRow{}, false, err
	}
	if err := json.Unmarshal(gridJSON, &row.ContributionGrid); err != nil {
		return GithubStatsCacheRow{}, false, err
	}
	if err := json.Unmarshal(reposJSON, &row.TopRepos); err != nil {
		return GithubStatsCacheRow{}, false, err
	}
	if err := json.Unmarshal(langsJSON, &row.Languages); err != nil {
		return GithubStatsCacheRow{}, false, err
	}
	return row, true, nil
}

// ClearGithubStatsCache drops the cache row for username. Idempotent — no error
// when the row is absent. Called on GitHub disconnect so a stale cache can't
// outlive the token that produced it.
func (d *DB) ClearGithubStatsCache(ctx context.Context, username string) error {
	if username == "" {
		return errors.New("ClearGithubStatsCache: empty username")
	}
	_, err := d.Pool.Exec(ctx, `DELETE FROM github_stats_cache WHERE username = $1`, username)
	return err
}

// marshalJSONArray marshals a slice, coercing a nil slice to a non-nil empty
// slice first so the result is `[]` (never `null`) — the JSONB columns are NOT
// NULL and reads should never see a SQL/JSON null.
func marshalJSONArray[T any](s []T) ([]byte, error) {
	if s == nil {
		s = []T{}
	}
	return json.Marshal(s)
}
