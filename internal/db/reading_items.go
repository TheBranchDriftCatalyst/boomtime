// reading_items.go: siloed storage for catalyst-books/audiobooks synced reading
// state (gaka-books). This is the ONLY place book/audiobook data lives — it does
// not write into heartbeats/stats/any core model. A user can view it
// (ListReadingItems) and wipe it on request (DeleteReadingItems); it also
// cascade-deletes with the user. See migrations/00058.
package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
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

	// Full-field metadata (migration 00060). All optional; a low-fidelity source
	// leaves them at their zero value and the COALESCE guard preserves a richer
	// value written earlier.
	Subtitle        string
	Narrators       string     // audio narrators[].name CSV
	Series          string     // series[0].title
	RuntimeMin      *int       // runtime_length_min
	PurchaseDate    *time.Time // library purchase_date
	ISBN            string
	AmazonASIN      string
	Genres          []byte   // JSON array of genre strings, or nil
	GoodreadsRating *float64 // community average, NOT the user's rating
}

// UpsertReadingItem inserts or updates one item (keyed by owner+source+ASIN).
// Dates/rating/metadata use COALESCE so a source that omits them doesn't clobber
// a value backfilled from Goodreads/Amazon-export (or a higher-fidelity source)
// later. Text metadata columns are NOT NULL DEFAULT ” so they always overwrite.
func (d *DB) UpsertReadingItem(ctx context.Context, it ReadingItem) error {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO reading_items
		   (owner, source, external_id, title, authors, cover_url, status,
		    progress_percent, finished, started_at, finished_at, rating, raw_meta,
		    subtitle, narrators, series, runtime_min, purchase_date, isbn,
		    amazon_asin, genres, goodreads_rating, synced_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,
		         $14,$15,$16,$17,$18,$19,$20,$21,$22, now())
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
		    subtitle         = EXCLUDED.subtitle,
		    narrators        = EXCLUDED.narrators,
		    series           = EXCLUDED.series,
		    runtime_min      = COALESCE(EXCLUDED.runtime_min, reading_items.runtime_min),
		    purchase_date    = COALESCE(EXCLUDED.purchase_date, reading_items.purchase_date),
		    isbn             = EXCLUDED.isbn,
		    amazon_asin      = EXCLUDED.amazon_asin,
		    genres           = COALESCE(EXCLUDED.genres, reading_items.genres),
		    goodreads_rating = COALESCE(EXCLUDED.goodreads_rating, reading_items.goodreads_rating),
		    synced_at        = now()`,
		it.Owner, it.Source, it.ExternalID, it.Title, it.Authors, it.CoverURL, it.Status,
		it.ProgressPercent, it.Finished, it.StartedAt, it.FinishedAt, it.Rating, it.RawMeta,
		it.Subtitle, it.Narrators, it.Series, it.RuntimeMin, it.PurchaseDate, it.ISBN,
		it.AmazonASIN, it.Genres, it.GoodreadsRating)
	return err
}

// FinishedReadingItem is the metadata a finished-sweep needs to publish an event
// and push the finish out to Hardcover.
type FinishedReadingItem struct {
	ExternalID string
	Title      string
	Authors    string
	ISBN       string
	AmazonASIN string
}

// MarkReadingItemFinished flips a row to finished (status='read', finished=true,
// finished_at) for owner+source+asin and reports whether this was a transition
// (the stored finished flag was previously false). finished_at is COALESCE'd so a
// re-run never moves an already-recorded finish date. Returns (transitioned,
// meta, found). When the row does not exist yet (finished before its library row
// synced) found is false and the caller skips it. This is the false→true edge
// that powers finished-detection → events.
func (d *DB) MarkReadingItemFinished(ctx context.Context, owner, source, asin string, finishedAt time.Time) (bool, FinishedReadingItem, bool, error) {
	var (
		wasFinished bool
		meta        FinishedReadingItem
	)
	meta.ExternalID = asin
	err := d.Pool.QueryRow(ctx,
		`WITH prev AS (
		    SELECT finished FROM reading_items
		     WHERE owner=$1 AND source=$2 AND external_id=$3
		 )
		 UPDATE reading_items ri SET
		    finished    = true,
		    status      = 'read',
		    finished_at = COALESCE(ri.finished_at, $4),
		    synced_at   = now()
		  WHERE ri.owner=$1 AND ri.source=$2 AND ri.external_id=$3
		 RETURNING (SELECT finished FROM prev), ri.title, ri.authors, ri.isbn, ri.amazon_asin`,
		owner, source, asin, finishedAt).
		Scan(&wasFinished, &meta.Title, &meta.Authors, &meta.ISBN, &meta.AmazonASIN)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, meta, false, nil
		}
		return false, meta, false, err
	}
	return !wasFinished, meta, true, nil
}

// ListReadingItems returns a user's synced items (source=="" → all sources),
// unfinished first then alphabetical. Never returns raw_meta (the view payload).
func (d *DB) ListReadingItems(ctx context.Context, owner, source string) ([]ReadingItem, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT owner, source, external_id, title, authors, cover_url, status,
		        progress_percent, finished, started_at, finished_at, rating,
		        subtitle, narrators, series, runtime_min, goodreads_rating, synced_at
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
			&it.FinishedAt, &it.Rating, &it.Subtitle, &it.Narrators, &it.Series,
			&it.RuntimeMin, &it.GoodreadsRating, &it.SyncedAt); err != nil {
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
