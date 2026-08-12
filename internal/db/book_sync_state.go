// book_sync_state.go: per-user/per-source forward-sync cursors for the
// catalyst-audiobooks/books domains (gaka-books). Cursors advance ONLY after a
// page/window is durably upserted, so a crash re-fetches the last window rather
// than skipping it (at-least-once; the idempotent upserts absorb the overlap).
// Cascade-deletes with the user. See migrations/00062.
package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// BookSyncState holds the forward-delta cursors for one owner+source.
type BookSyncState struct {
	Owner              string
	Source             string
	LastLibraryCursor  *time.Time // newest library purchase_date seen
	LastFinishedCursor *time.Time // newest finished-sweep event_timestamp seen
	LastActivityCursor *time.Time // last aggregates window filled (a DATE)
	LastBackfillAt     *time.Time // NULL until the one-shot backfill completes
	LastForwardAt      *time.Time
}

// GetBookSyncState returns the stored cursors for owner+source, or a zero-valued
// state (all cursors nil) when none exists yet — the caller treats nil cursors
// as "sweep from the beginning".
func (d *DB) GetBookSyncState(ctx context.Context, owner, source string) (BookSyncState, error) {
	st := BookSyncState{Owner: owner, Source: source}
	err := d.Pool.QueryRow(ctx,
		`SELECT last_library_cursor, last_finished_cursor, last_activity_cursor,
		        last_backfill_at, last_forward_at
		   FROM book_sync_state WHERE owner=$1 AND source=$2`,
		owner, source).
		Scan(&st.LastLibraryCursor, &st.LastFinishedCursor, &st.LastActivityCursor,
			&st.LastBackfillAt, &st.LastForwardAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return st, nil
		}
		return st, err
	}
	return st, nil
}

// SetBookSyncState upserts the cursors for owner+source (keyed by the composite
// PK). Nil cursor fields are written as NULL — pass the full state you want
// stored.
func (d *DB) SetBookSyncState(ctx context.Context, st BookSyncState) error {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO book_sync_state
		   (owner, source, last_library_cursor, last_finished_cursor,
		    last_activity_cursor, last_backfill_at, last_forward_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7, now())
		 ON CONFLICT (owner, source) DO UPDATE SET
		    last_library_cursor  = EXCLUDED.last_library_cursor,
		    last_finished_cursor = EXCLUDED.last_finished_cursor,
		    last_activity_cursor = EXCLUDED.last_activity_cursor,
		    last_backfill_at     = COALESCE(EXCLUDED.last_backfill_at, book_sync_state.last_backfill_at),
		    last_forward_at      = COALESCE(EXCLUDED.last_forward_at, book_sync_state.last_forward_at),
		    updated_at           = now()`,
		st.Owner, st.Source, st.LastLibraryCursor, st.LastFinishedCursor,
		st.LastActivityCursor, st.LastBackfillAt, st.LastForwardAt)
	return err
}
