// hardcover_match_cache.go: the GLOBAL, cross-user Hardcover match cache
// (gaka-wzgr). Unlike reading_items.hardcover_* (which caches a match PER USER
// per row), this table caches the resolved identity ONCE for all of boomtime,
// keyed by the objective book identifier (ASIN or ISBN-13). The match sweep
// (internal/hardcover/match_sweep.go) reads it before spending a Hardcover API
// call and writes it after a confident EXACT-ID hit, so a book that any user has
// already resolved never re-hits the Hardcover editions API. See migration 00066.
package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// HardcoverMatch is a cached exact-id resolution: the Hardcover book+edition a
// given ASIN/ISBN-13 maps to, plus HOW it was matched (asin | isbn13). EditionID
// is 0 when the cached row has no edition (stored NULL).
type HardcoverMatch struct {
	BookID    int64
	EditionID int64
	Method    string
}

// LookupHardcoverMatch reads the global cache for one identity. ok is false (with
// a nil error) when no row is cached for (idType, externalID) — the caller then
// falls through to the live Hardcover ladder. A cached edition of NULL surfaces
// as EditionID 0.
func (d *DB) LookupHardcoverMatch(ctx context.Context, idType, externalID string) (HardcoverMatch, bool, error) {
	var m HardcoverMatch
	err := d.Pool.QueryRow(ctx,
		`SELECT hardcover_book_id, COALESCE(hardcover_edition_id, 0), method
		   FROM hardcover_match_cache
		  WHERE id_type = $1 AND external_id = $2`,
		idType, externalID).
		Scan(&m.BookID, &m.EditionID, &m.Method)
	if err != nil {
		if err == pgx.ErrNoRows {
			return HardcoverMatch{}, false, nil
		}
		return HardcoverMatch{}, false, err
	}
	return m, true, nil
}

// PutHardcoverMatch upserts a resolved exact-id match into the global cache
// (idempotent on the (id_type, external_id) PK). A later resolution overwrites an
// earlier one — Hardcover's own book/edition ids are the source of truth. An
// editionID <= 0 is stored as NULL so a subsequent lookup round-trips back to 0.
func (d *DB) PutHardcoverMatch(ctx context.Context, idType, externalID string, bookID, editionID int64, method string) error {
	var edition *int64
	if editionID > 0 {
		edition = &editionID
	}
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO hardcover_match_cache
		     (id_type, external_id, hardcover_book_id, hardcover_edition_id, method)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (id_type, external_id) DO UPDATE SET
		     hardcover_book_id    = EXCLUDED.hardcover_book_id,
		     hardcover_edition_id = EXCLUDED.hardcover_edition_id,
		     method               = EXCLUDED.method,
		     matched_at           = now()`,
		idType, externalID, bookID, edition, method)
	return err
}
