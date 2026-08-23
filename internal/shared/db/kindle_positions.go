// kindle_positions.go: siloed Kindle last-page-read POSITION samples
// (boom-books). The raw time-series the forward reading-TIME composition
// (internal/domains/books/reading_time.go) gap-sums into reading_activity
// (source='kindle') day buckets. Like reading_items / reading_activity it does
// NOT write into heartbeats/stats/any core model; it cascade-deletes with the
// user. See migrations/00068.
package db

import (
	"context"
	"time"
)

// KindleReadingPosition is one observed last-page-read position for a book at a
// point in time — the Kindle analogue of a coding heartbeat.
type KindleReadingPosition struct {
	Owner     string
	ASIN      string
	Position  int64
	SampledAt time.Time
}

// InsertKindleReadingPosition appends one position sample. Idempotent per
// (owner, asin, sampled_at) — an accidental double-poll at the same instant is a
// no-op (ON CONFLICT DO NOTHING) so sample capture never duplicates. Returns
// whether a new row was actually inserted.
func (d *DB) InsertKindleReadingPosition(ctx context.Context, owner, asin string, position int64, sampledAt time.Time) (bool, error) {
	tag, err := d.Pool.Exec(ctx,
		`INSERT INTO kindle_reading_positions (owner, asin, position, sampled_at)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (owner, asin, sampled_at) DO NOTHING`,
		owner, asin, position, sampledAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListKindleReadingPositions returns a book's position samples at or after
// `since`, oldest first — the ordered input the session composition consumes. A
// zero `since` naturally includes every row (>= the year-1 zero time).
func (d *DB) ListKindleReadingPositions(ctx context.Context, owner, asin string, since time.Time) ([]KindleReadingPosition, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT owner, asin, position, sampled_at
		   FROM kindle_reading_positions
		  WHERE owner = $1 AND asin = $2 AND sampled_at >= $3
		  ORDER BY sampled_at`,
		owner, asin, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KindleReadingPosition
	for rows.Next() {
		var p KindleReadingPosition
		if err := rows.Scan(&p.Owner, &p.ASIN, &p.Position, &p.SampledAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// KindleReadingPositionRow is a position sample joined with its book title — the
// row the admin reading-monitor RAW diagnostic endpoint streams. Title falls back
// to the ASIN when no reading_items row carries one.
type KindleReadingPositionRow struct {
	ASIN      string
	Title     string
	Position  int64
	SampledAt time.Time
}

// ListRecentKindleReadingPositions returns a user's most recent position samples
// across ALL books (newest-first, capped at `limit`), each joined to its
// reading_items title — the raw heartbeat/position stream the admin diagnostic
// page renders. The caller derives per-book Δlocation + interval from consecutive
// same-ASIN samples. A non-positive limit falls back to 200.
func (d *DB) ListRecentKindleReadingPositions(ctx context.Context, owner string, limit int) ([]KindleReadingPositionRow, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := d.Pool.Query(ctx,
		`SELECT p.asin,
		        COALESCE(NULLIF(ri.title, ''), p.asin) AS title,
		        p.position,
		        p.sampled_at
		   FROM kindle_reading_positions p
		   LEFT JOIN reading_items ri
		     ON ri.owner = p.owner AND ri.source = 'kindle' AND ri.external_id = p.asin
		  WHERE p.owner = $1
		  ORDER BY p.sampled_at DESC
		  LIMIT $2`,
		owner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KindleReadingPositionRow
	for rows.Next() {
		var r KindleReadingPositionRow
		if err := rows.Scan(&r.ASIN, &r.Title, &r.Position, &r.SampledAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteKindleReadingPositions wipes a user's position samples (asin=="" → all) —
// the "delete my book data" path, mirroring DeleteReadingActivity. Returns the
// number of rows removed.
func (d *DB) DeleteKindleReadingPositions(ctx context.Context, owner, asin string) (int64, error) {
	tag, err := d.Pool.Exec(ctx,
		`DELETE FROM kindle_reading_positions WHERE owner = $1 AND ($2 = '' OR asin = $2)`, owner, asin)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
