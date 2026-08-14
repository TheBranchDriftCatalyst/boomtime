// hardcover_shelf.go: the local mirror of a user's FULL Hardcover shelf
// (migration 00074) — the candidate pool the match sweep scores unmatched
// reading_items against (catalyst-books shelf-match). The Hardcover pull upserts
// every shelf entry here (ALL statuses); the sweep's LOCAL shelf rung loads the
// whole shelf once (ListHardcoverShelf) and title/author-scores each still-
// unmatched row against it — a zero-API, high-precision rung for books the user
// shelved on Hardcover but that don't share an ASIN/ISBN with our edition.
//
// This is the INPUT to matching (the user's own curated shelf), distinct from
// reading_items.hardcover_* (the resolved link, per user per row) and
// hardcover_match_cache (the global exact-id resolution).
package db

import (
	"context"
	"time"
)

// ShelfEntry is one row of the local Hardcover shelf mirror the scorer reads.
// BookID is the Hardcover book id; Title/Author are the tokens the local scorer
// matches on; Slug is the /books/<slug> deep-link segment carried onto a match;
// Status is the shelf status string (want|reading|read|paused|dnf|"").
type ShelfEntry struct {
	BookID int64
	Title  string
	Author string
	Slug   string
	Status string
}

// UpsertHardcoverShelfEntry inserts or refreshes one shelf entry for owner
// (keyed by owner+hardcover_book_id) — the per-book write the Hardcover pull runs
// over the user's whole shelf. updatedAt is Hardcover's own updated_at for the
// entry (nil leaves the column NULL). synced_at is stamped now() on every write so
// a later reconcile can tell a fresh mirror from a stale one. It touches ONLY this
// mirror table — never reading_items or any core model.
func (d *DB) UpsertHardcoverShelfEntry(ctx context.Context, owner string, e ShelfEntry, updatedAt *time.Time) error {
	var updated *time.Time
	if updatedAt != nil && !updatedAt.IsZero() {
		u := updatedAt.UTC()
		updated = &u
	}
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO hardcover_user_shelf
		     (owner, hardcover_book_id, title, author, slug, status, updated_at, synced_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		 ON CONFLICT (owner, hardcover_book_id) DO UPDATE SET
		     title      = EXCLUDED.title,
		     author     = EXCLUDED.author,
		     slug       = EXCLUDED.slug,
		     status     = EXCLUDED.status,
		     updated_at = EXCLUDED.updated_at,
		     synced_at  = now()`,
		owner, e.BookID, e.Title, e.Author, e.Slug, e.Status, updated)
	return err
}

// ListHardcoverShelf returns the owner's whole locally-mirrored Hardcover shelf —
// the candidate pool the sweep's shelf rung scores against (loaded once per
// sweep). Order is stable (book id) but the scorer is order-independent.
func (d *DB) ListHardcoverShelf(ctx context.Context, owner string) ([]ShelfEntry, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT hardcover_book_id, title, author, slug, status
		   FROM hardcover_user_shelf
		  WHERE owner = $1
		  ORDER BY hardcover_book_id`,
		owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShelfEntry
	for rows.Next() {
		var e ShelfEntry
		if err := rows.Scan(&e.BookID, &e.Title, &e.Author, &e.Slug, &e.Status); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
