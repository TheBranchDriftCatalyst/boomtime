// kindle_insights.go: siloed storage for the raw Kindle Reading-Insights
// snapshot (gaka-books). ONE row per user (owner PK) holding the whole
// /kindle/reading/insights/data payload as JSONB, so streaks/goals/achievements
// are retained for a future surface without a schema churn now. Like
// reading_items / reading_activity it does NOT write into heartbeats/stats/any
// core model; it cascade-deletes with the user and has a per-user wipe path. See
// migrations/00067.
package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// KindleReadingInsights is a user's stored insights snapshot.
type KindleReadingInsights struct {
	Owner     string
	Raw       []byte // the full insights response body (JSONB)
	FetchedAt time.Time
}

// UpsertKindleReadingInsights stores (or replaces) the user's current insights
// snapshot. Keyed by owner — a re-fetch overwrites the single row and stamps a
// fresh fetched_at. Idempotent.
func (d *DB) UpsertKindleReadingInsights(ctx context.Context, owner string, raw []byte) error {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO kindle_reading_insights (owner, raw, fetched_at)
		 VALUES ($1, $2, now())
		 ON CONFLICT (owner) DO UPDATE SET
		    raw        = EXCLUDED.raw,
		    fetched_at = now()`,
		owner, raw)
	return err
}

// GetKindleReadingInsights returns the user's stored snapshot, or (nil, false)
// when they have none yet.
func (d *DB) GetKindleReadingInsights(ctx context.Context, owner string) (*KindleReadingInsights, bool, error) {
	k := &KindleReadingInsights{Owner: owner}
	err := d.Pool.QueryRow(ctx,
		`SELECT raw, fetched_at FROM kindle_reading_insights WHERE owner = $1`,
		owner).Scan(&k.Raw, &k.FetchedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return k, true, nil
}

// DeleteKindleReadingInsights wipes a user's insights snapshot — the "delete my
// book data" path. Returns the number of rows removed (0 or 1).
func (d *DB) DeleteKindleReadingInsights(ctx context.Context, owner string) (int64, error) {
	tag, err := d.Pool.Exec(ctx,
		`DELETE FROM kindle_reading_insights WHERE owner = $1`, owner)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
