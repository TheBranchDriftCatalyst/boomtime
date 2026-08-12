// reading_items.go: siloed storage for catalyst-books/audiobooks synced reading
// state (gaka-books). This is the ONLY place book/audiobook data lives — it does
// not write into heartbeats/stats/any core model. A user can view it
// (ListReadingItems) and wipe it on request (DeleteReadingItems); it also
// cascade-deletes with the user. See migrations/00058.
package db

import (
	"context"
	"time"
)

// ReadingItem is one synced book/audiobook (a row of reading_items).
type ReadingItem struct {
	Owner           string
	Source          string // "audible" | "kindle"
	ExternalID      string // ASIN
	Title           string
	Authors         string
	CoverURL        string
	Status          string
	ProgressPercent int
	Finished        bool
	StartedAt       *time.Time
	FinishedAt      *time.Time
	Rating          *float64
	RawMeta         []byte // JSON of the source item
	SyncedAt        time.Time
}

// UpsertReadingItem inserts or updates one item (keyed by owner+source+ASIN).
// Dates/rating use COALESCE so a source that omits them doesn't clobber a value
// backfilled from Goodreads/Amazon-export later.
func (d *DB) UpsertReadingItem(ctx context.Context, it ReadingItem) error {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO reading_items
		   (owner, source, external_id, title, authors, cover_url, status,
		    progress_percent, finished, started_at, finished_at, rating, raw_meta, synced_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13, now())
		 ON CONFLICT (owner, source, external_id) DO UPDATE SET
		    title            = EXCLUDED.title,
		    authors          = EXCLUDED.authors,
		    cover_url        = EXCLUDED.cover_url,
		    status           = EXCLUDED.status,
		    progress_percent = EXCLUDED.progress_percent,
		    finished         = EXCLUDED.finished,
		    started_at       = COALESCE(EXCLUDED.started_at, reading_items.started_at),
		    finished_at      = COALESCE(EXCLUDED.finished_at, reading_items.finished_at),
		    rating           = COALESCE(EXCLUDED.rating, reading_items.rating),
		    raw_meta         = EXCLUDED.raw_meta,
		    synced_at        = now()`,
		it.Owner, it.Source, it.ExternalID, it.Title, it.Authors, it.CoverURL, it.Status,
		it.ProgressPercent, it.Finished, it.StartedAt, it.FinishedAt, it.Rating, it.RawMeta)
	return err
}

// ListReadingItems returns a user's synced items (source=="" → all sources),
// unfinished first then alphabetical. Never returns raw_meta (the view payload).
func (d *DB) ListReadingItems(ctx context.Context, owner, source string) ([]ReadingItem, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT owner, source, external_id, title, authors, cover_url, status,
		        progress_percent, finished, started_at, finished_at, rating, synced_at
		   FROM reading_items
		  WHERE owner = $1 AND ($2 = '' OR source = $2)
		  ORDER BY finished, title`,
		owner, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadingItem
	for rows.Next() {
		var it ReadingItem
		if err := rows.Scan(&it.Owner, &it.Source, &it.ExternalID, &it.Title, &it.Authors,
			&it.CoverURL, &it.Status, &it.ProgressPercent, &it.Finished, &it.StartedAt,
			&it.FinishedAt, &it.Rating, &it.SyncedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// DeleteReadingItems wipes a user's synced items (source=="" → all sources) —
// the "delete my book data" path. Returns the number of rows removed.
func (d *DB) DeleteReadingItems(ctx context.Context, owner, source string) (int64, error) {
	tag, err := d.Pool.Exec(ctx,
		`DELETE FROM reading_items WHERE owner = $1 AND ($2 = '' OR source = $2)`, owner, source)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CountReadingItems counts a user's synced items (source=="" → all).
func (d *DB) CountReadingItems(ctx context.Context, owner, source string) (int, error) {
	var n int
	err := d.Pool.QueryRow(ctx,
		`SELECT count(*) FROM reading_items WHERE owner = $1 AND ($2 = '' OR source = $2)`,
		owner, source).Scan(&n)
	return n, err
}
