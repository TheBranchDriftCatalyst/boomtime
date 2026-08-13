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

	// Hardcover linkage (migration 00063). All nullable — NULL until the
	// Hardcover match sync resolves this row. HardcoverBookID != nil means the
	// row is matched to a Hardcover book; HardcoverStatus is the last shelf
	// status we saw for it (once we've reconciled). The view layer uses these to
	// render an honest match-state badge + a direct-to-Hardcover link.
	HardcoverBookID    *int64
	HardcoverStatus    *string
	HardcoverMatchedAt *time.Time
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

// SetReadingItemHardcoverLink caches a resolved Hardcover match onto a
// reading_item (keyed by owner+source+external_id): hardcover_book_id /
// _edition_id (the resolved ids), hardcover_match_confidence (e.g. "asin"), and
// hardcover_matched_at = now(). This is the "resolve-once, cache-forever"
// linkage the catalyst-books Kindle ingest writes so a bare ASIN is pre-linked
// to Hardcover without a later re-fuzz. No-op when bookID <= 0 (no match). It
// touches ONLY the linkage columns — never the row's own source metadata.
func (d *DB) SetReadingItemHardcoverLink(ctx context.Context, owner, source, externalID string, bookID, editionID int64, confidence string) error {
	if bookID <= 0 {
		return nil
	}
	var edition *int64
	if editionID > 0 {
		edition = &editionID
	}
	var conf *string
	if confidence != "" {
		conf = &confidence
	}
	_, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET
		    hardcover_book_id          = $4,
		    hardcover_edition_id       = COALESCE($5, hardcover_edition_id),
		    hardcover_match_confidence = COALESCE($6, hardcover_match_confidence),
		    hardcover_matched_at       = now()
		  WHERE owner = $1 AND source = $2 AND external_id = $3`,
		owner, source, externalID, bookID, edition, conf)
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
		        subtitle, narrators, series, runtime_min, goodreads_rating,
		        isbn, amazon_asin, hardcover_book_id, hardcover_status,
		        hardcover_matched_at, synced_at
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
			&it.RuntimeMin, &it.GoodreadsRating, &it.ISBN, &it.AmazonASIN,
			&it.HardcoverBookID, &it.HardcoverStatus, &it.HardcoverMatchedAt,
			&it.SyncedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListUnmatchedReadingItems returns the owner's reading_items that are NOT yet
// linked to a Hardcover book (hardcover_book_id IS NULL) AND carry at least one
// matchable identity (an external_id/amazon_asin, an isbn, or a title). It is the
// worklist for the explicit `hardcover-match` pipeline stage — only the fields the
// match ladder needs (source, external_id, amazon_asin, isbn, title, authors) are
// projected. A row with no identity at all is skipped (it could never match).
func (d *DB) ListUnmatchedReadingItems(ctx context.Context, owner string) ([]ReadingItem, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT source, external_id, isbn, amazon_asin, title, authors
		   FROM reading_items
		  WHERE owner = $1
		    AND hardcover_book_id IS NULL
		    AND (external_id <> '' OR isbn <> '' OR title <> '')
		  ORDER BY source, external_id`,
		owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadingItem
	for rows.Next() {
		it := ReadingItem{Owner: owner}
		if err := rows.Scan(&it.Source, &it.ExternalID, &it.ISBN, &it.AmazonASIN,
			&it.Title, &it.Authors); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpdateReadingItemDisplayMeta backfills title/authors/cover_url onto a row that
// arrived with them blank (the bare-ASIN Kindle case), keyed by
// owner+source+external_id. Each column is written ONLY when it is currently empty
// (a NULLIF/COALESCE guard) so a later, higher-fidelity source is never clobbered
// by a Hardcover lookup — and an empty incoming value never blanks a good one.
// Returns the number of rows updated.
func (d *DB) UpdateReadingItemDisplayMeta(ctx context.Context, owner, source, externalID, title, authors, coverURL string) (int64, error) {
	tag, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET
		    title     = CASE WHEN title = ''     AND $4 <> '' THEN $4 ELSE title END,
		    authors   = CASE WHEN authors = ''   AND $5 <> '' THEN $5 ELSE authors END,
		    cover_url = CASE WHEN cover_url = '' AND $6 <> '' THEN $6 ELSE cover_url END,
		    synced_at = now()
		  WHERE owner = $1 AND source = $2 AND external_id = $3`,
		owner, source, externalID, title, authors, coverURL)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// HardcoverUserBookLink is the MINIMAL reconcile payload a Hardcover PULL writes
// back onto an already-linked reading_item (migration 00063). Per the no-mirror
// design we persist only the shelf status + Hardcover's own updated_at — never a
// copy of the book. BookID is the match key (reading_items.hardcover_book_id).
type HardcoverUserBookLink struct {
	BookID          int64
	Status          string
	RemoteUpdatedAt time.Time
}

// UpdateHardcoverLinkFromPull reconciles a pulled Hardcover shelf entry onto the
// local reading_item already linked to that Hardcover book (matched earlier by
// the push/match ladder, so hardcover_book_id is set). It updates ONLY the
// linkage columns — hardcover_status (from the pulled status_id) and
// hardcover_remote_updated_at (Hardcover's updated_at) — leaving the row's own
// source metadata untouched. Returns the number of rows updated: 0 means the
// user has this book on Hardcover but no matching local reading_item yet
// (inbound-origin creation is a documented follow-up — the caller logs it).
// Status=="" is skipped via COALESCE so an unknown upstream status_id never
// blanks a good value.
func (d *DB) UpdateHardcoverLinkFromPull(ctx context.Context, owner string, link HardcoverUserBookLink) (int64, error) {
	if link.BookID == 0 {
		return 0, nil
	}
	var status *string
	if link.Status != "" {
		status = &link.Status
	}
	var remote *time.Time
	if !link.RemoteUpdatedAt.IsZero() {
		t := link.RemoteUpdatedAt.UTC()
		remote = &t
	}
	tag, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET
		    hardcover_status            = COALESCE($3, hardcover_status),
		    hardcover_remote_updated_at = COALESCE($4, hardcover_remote_updated_at)
		  WHERE owner = $1 AND hardcover_book_id = $2`,
		owner, link.BookID, status, remote)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
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
