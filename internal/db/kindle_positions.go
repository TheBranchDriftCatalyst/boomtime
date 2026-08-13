// kindle_positions.go: siloed Kindle last-page-read POSITION samples
// (gaka-books). The raw time-series the forward reading-TIME composition
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
