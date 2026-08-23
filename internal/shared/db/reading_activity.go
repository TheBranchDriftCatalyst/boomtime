// reading_activity.go: siloed daily/monthly reading/listening time-series
// (boom-books). This is the grain the fusion layer overlays on the coding
// calendar. Like reading_items it does NOT write into heartbeats/stats/any core
// model; it cascade-deletes with the user and has a per-user wipe path. See
// migrations/00061.
package db

import (
	"context"
	"time"
)

// ReadingActivity is one time-bucket of reading/listening activity.
type ReadingActivity struct {
	Owner            string
	Source           string    // "audible" | "kindle" | "amazon-export"
	Granularity      string    // "day" | "month"
	BucketDate       time.Time // bucket start date
	ListeningSeconds int64
	Pages            *int
	SyncedAt         time.Time
}

// UpsertReadingActivity inserts or updates one bucket (keyed by
// owner+source+bucket_date+granularity). Idempotent — the backfill can re-run a
// window and simply overwrite the same bucket.
func (d *DB) UpsertReadingActivity(ctx context.Context, a ReadingActivity) error {
	if a.Granularity == "" {
		a.Granularity = "day"
	}
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO reading_activity
		   (owner, source, granularity, bucket_date, listening_seconds, pages, synced_at)
		 VALUES ($1,$2,$3,$4,$5,$6, now())
		 ON CONFLICT (owner, source, bucket_date, granularity) DO UPDATE SET
		    listening_seconds = EXCLUDED.listening_seconds,
		    pages             = COALESCE(EXCLUDED.pages, reading_activity.pages),
		    synced_at         = now()`,
		a.Owner, a.Source, a.Granularity, a.BucketDate, a.ListeningSeconds, a.Pages)
	return err
}

// ListReadingActivity returns a user's buckets for a source within [from, to]
// (source=="" → all sources), oldest first.
func (d *DB) ListReadingActivity(ctx context.Context, owner, source string, from, to time.Time) ([]ReadingActivity, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT owner, source, granularity, bucket_date, listening_seconds, pages, synced_at
		   FROM reading_activity
		  WHERE owner = $1 AND ($2 = '' OR source = $2)
		    AND bucket_date >= $3 AND bucket_date <= $4
		  ORDER BY bucket_date`,
		owner, source, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadingActivity
	for rows.Next() {
		var a ReadingActivity
		if err := rows.Scan(&a.Owner, &a.Source, &a.Granularity, &a.BucketDate,
			&a.ListeningSeconds, &a.Pages, &a.SyncedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteReadingActivity wipes a user's activity buckets (source=="" → all) — the
// "delete my book data" path. Returns the number of rows removed.
func (d *DB) DeleteReadingActivity(ctx context.Context, owner, source string) (int64, error) {
	tag, err := d.Pool.Exec(ctx,
		`DELETE FROM reading_activity WHERE owner = $1 AND ($2 = '' OR source = $2)`, owner, source)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
