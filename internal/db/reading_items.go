// reading_items.go: siloed storage for catalyst-books/audiobooks synced reading
// state (gaka-books). This is the ONLY place book/audiobook data lives — it does
// not write into heartbeats/stats/any core model. A user can view it
// (ListReadingItems) and wipe it on request (DeleteReadingItems); it also
// cascade-deletes with the user. See migrations/00058.
package db

import (
	"context"
	"fmt"
	"strings"
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

	// HardcoverSlug is the book's Hardcover slug (migration 00070) — the
	// /books/<slug> path segment the deep-link needs (a numeric id 404s on
	// Hardcover's book pages). NULL until a match/pull resolves it.
	HardcoverSlug *string

	// HardcoverEditionID is the resolved edition cached alongside the book id
	// (migration 00063). NULL until matched. The continuous-progress push uses it
	// to pin the read to a specific edition WITHOUT re-running the match ladder.
	HardcoverEditionID *int64
	// HardcoverPushedProgress is the percent WE last actually pushed to Hardcover
	// for this in-progress row (migration 00065). NULL = never pushed. The forward
	// sync skips a re-push when the current percent equals this value, so a book's
	// progress mirrors at most once per real change instead of every sync.
	HardcoverPushedProgress *int

	// HardcoverLists is the book's Hardcover LIST memberships as a JSON array of
	// list names (migration 00077), or nil. A property of the book — many-to-many,
	// written by the Hardcover pull (SetReadingItemListsForBook) keyed on
	// hardcover_book_id so all editions of a Work share it. Never written by ingest.
	HardcoverLists []byte

	// Curation override layer (migration 00069). The derived layer above
	// (Status/Finished/FinishedAt/Rating) is Amazon-owned and recomputed every
	// sync; these override columns are STICKY and written ONLY by the user (the
	// curation endpoint) or the Hardcover pull's last-writer-wins branch — NEVER by
	// ingest. effective = COALESCE(override, derived), computed in the query DSL.
	//   StatusOverride     — chosen status (want|reading|read|paused|dnf)
	//   RatingOverride     — chosen rating
	//   FinishedAtOverride — chosen finish date
	//   CurationUpdatedAt  — the row-level LWW stamp for the override layer
	StatusOverride     *string
	RatingOverride     *float64
	FinishedAtOverride *time.Time
	CurationUpdatedAt  *time.Time

	// HardcoverPushedStatus is the status string WE last pushed to Hardcover
	// (migration 00069), paired with HardcoverPushedAt. The pull's LWW branch uses
	// it to suppress our own echo — a remote status equal to our last push is not a
	// genuine Hardcover edit and must not be adopted back into the override layer.
	HardcoverPushedStatus *string
}

// UpsertReadingItem inserts or updates one item (keyed by owner+source+ASIN).
// Dates/rating/metadata use COALESCE so a source that omits them doesn't clobber
// a value backfilled from Goodreads/Amazon-export (or a higher-fidelity source)
// later. Text metadata columns are NOT NULL DEFAULT ” so they always overwrite.
//
// CURATION INVARIANT (migration 00069): the override columns (status_override,
// rating_override, finished_at_override, curation_updated_at) are DELIBERATELY
// absent from both the INSERT column list and the ON CONFLICT SET — ingest writes
// only the derived layer, so a sync can NEVER clobber a user/Hardcover curation.
// The override layer is touched only by SetReadingItemCuration, the finish
// promotion, and UpdateHardcoverLinkFromPull. Do not add override columns here.
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
// _edition_id (the resolved ids), hardcover_match_confidence (e.g. "asin"),
// hardcover_slug (the /books/<slug> deep-link segment), and hardcover_matched_at
// = now(). This is the "resolve-once, cache-forever" linkage the catalyst-books
// Kindle ingest writes so a bare ASIN is pre-linked to Hardcover without a later
// re-fuzz. No-op when bookID <= 0 (no match). It touches ONLY the linkage columns
// — never the row's own source metadata. The slug is COALESCE-guarded so an empty
// slug (a match path that didn't carry one) never clobbers a good one written
// earlier.
func (d *DB) SetReadingItemHardcoverLink(ctx context.Context, owner, source, externalID string, bookID, editionID int64, confidence, slug string) error {
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
	var slugPtr *string
	if slug != "" {
		slugPtr = &slug
	}
	_, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET
		    hardcover_book_id          = $4,
		    hardcover_edition_id       = COALESCE($5, hardcover_edition_id),
		    hardcover_match_confidence = COALESCE($6, hardcover_match_confidence),
		    hardcover_slug             = COALESCE($7, hardcover_slug),
		    hardcover_matched_at       = now()
		  WHERE owner = $1 AND source = $2 AND external_id = $3`,
		owner, source, externalID, bookID, edition, conf, slugPtr)
	return err
}

// SetReadingItemPushedProgress records the percent WE last actually pushed to
// Hardcover for an in-progress reading_item (keyed by owner+source+external_id),
// so the next forward sync skips a re-push when the local percent is unchanged.
// Called ONLY after a real (non-dry-run) push succeeded — a dry-run no-op must
// leave this NULL/unchanged so flipping dry-run off still flushes the backlog.
func (d *DB) SetReadingItemPushedProgress(ctx context.Context, owner, source, externalID string, pct int) error {
	_, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET hardcover_pushed_progress = $4
		  WHERE owner = $1 AND source = $2 AND external_id = $3`,
		owner, source, externalID, pct)
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
		    SELECT finished, status, status_override FROM reading_items
		     WHERE owner=$1 AND source=$2 AND external_id=$3
		 )
		 UPDATE reading_items ri SET
		    finished    = true,
		    status      = 'read',
		    finished_at = COALESCE(ri.finished_at, $4),
		    -- Amazon-finish promotion into the override layer (migration 00069): the
		    -- ONE place ingest may write an override. On the finish TRANSITION
		    -- (prev.finished=false) a real finish supersedes a stale non-read override
		    -- ('dnf'/'paused'/'reading') and stamps the LWW clock so Hardcover adopts
		    -- 'read'. Idempotent: guarded on the PRE-update effective status, and only
		    -- on the transition, so a re-run — or a deliberate later user override on an
		    -- already-finished row — is never clobbered.
		    status_override     = CASE WHEN prev.finished = false
		                                AND COALESCE(prev.status_override, prev.status) <> 'read'
		                               THEN 'read' ELSE ri.status_override END,
		    curation_updated_at = CASE WHEN prev.finished = false
		                                AND COALESCE(prev.status_override, prev.status) <> 'read'
		                               THEN now() ELSE ri.curation_updated_at END,
		    synced_at   = now()
		  FROM prev
		  WHERE ri.owner=$1 AND ri.source=$2 AND ri.external_id=$3
		 RETURNING prev.finished, ri.title, ri.authors, ri.isbn, ri.amazon_asin`,
		owner, source, asin, finishedAt).
		Scan(&wasFinished, &meta.Title, &meta.Authors, &meta.ISBN, &meta.AmazonASIN)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, meta, false, nil
		}
		return false, meta, false, err
	}
	// Record this finish as a discrete read (migration 00078) so the book's read
	// history includes Amazon finishes, not only Hardcover reads. Idempotent: the
	// key is asin+finish-date, so re-ingesting the same finish is a no-op; a re-read
	// with a DIFFERENT finish date is a new event. Best-effort (never fails the finish).
	fa := finishedAt.UTC()
	_ = d.UpsertReadingEvent(ctx, ReadingEvent{
		Owner: owner, Source: source, ExternalID: asin,
		Origin: ReadingEventOriginAudible, ExternalReadID: asin + "@" + fa.Format("2006-01-02"),
		FinishedAt: &fa,
	})
	return !wasFinished, meta, true, nil
}

// SetReadingItemFinishedFromInsights backfills a Kindle finish DATE from the
// Kindle Reading-Insights history onto an existing kindle reading_item (keyed by
// owner+source('kindle')+asin). Insights is the ONLY per-book finish-DATE source
// Kindle has — the Cloud Reader library carries no timestamps — so this is how a
// kindle row's finished_at gets populated. It sets finished=true, status='read',
// and finished_at = COALESCE(existing, insightsDate) so a richer finish date
// written earlier (e.g. by a positioned source or the user) is NEVER clobbered.
//
// Returns (newlyDated, found):
//   - found=false when no kindle row for that ASIN exists yet (its library row
//     hasn't synced); nothing is written and the caller skips it.
//   - newlyDated=true only when finished_at was previously NULL and is now set
//     from insights — the accurate "backfilled a date" signal for the summary
//     log (a re-run over an already-dated row reports newlyDated=false).
func (d *DB) SetReadingItemFinishedFromInsights(ctx context.Context, owner, asin string, finishedAt time.Time) (newlyDated bool, found bool, err error) {
	var prevWasNull bool
	err = d.Pool.QueryRow(ctx,
		`WITH prev AS (
		    SELECT finished_at AS pfa, finished, status, status_override FROM reading_items
		     WHERE owner=$1 AND source='kindle' AND external_id=$2
		 )
		 UPDATE reading_items ri SET
		    finished    = true,
		    status      = 'read',
		    finished_at = COALESCE(ri.finished_at, $3),
		    -- Amazon-finish promotion into the override layer (migration 00069) — same
		    -- rule as MarkReadingItemFinished: on the finish TRANSITION a real finish
		    -- supersedes a stale non-read override and stamps the LWW clock. Idempotent
		    -- and transition-only, so a re-run never clobbers a later user override.
		    status_override     = CASE WHEN prev.finished = false
		                                AND COALESCE(prev.status_override, prev.status) <> 'read'
		                               THEN 'read' ELSE ri.status_override END,
		    curation_updated_at = CASE WHEN prev.finished = false
		                                AND COALESCE(prev.status_override, prev.status) <> 'read'
		                               THEN now() ELSE ri.curation_updated_at END,
		    synced_at   = now()
		  FROM prev
		  WHERE ri.owner=$1 AND ri.source='kindle' AND ri.external_id=$2
		 RETURNING prev.pfa IS NULL`,
		owner, asin, finishedAt).Scan(&prevWasNull)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, false, nil
		}
		return false, false, err
	}
	// Record the Kindle-insights finish as a discrete read (migration 00078), keyed
	// idempotently by asin+finish-date. Best-effort.
	fa := finishedAt.UTC()
	_ = d.UpsertReadingEvent(ctx, ReadingEvent{
		Owner: owner, Source: "kindle", ExternalID: asin,
		Origin: ReadingEventOriginKindleInsights, ExternalReadID: asin + "@" + fa.Format("2006-01-02"),
		FinishedAt: &fa,
	})
	return prevWasNull, true, nil
}

// SetReadingItemReading flips a reading_item to status='reading' — the honest
// "this book is actually in progress" signal the Kindle status-reconcile
// (books-kindle-status-reconcile) writes when the CDE sidecar reports a
// last-page-read record for a non-read book. The Cloud Reader library feed
// reports percentageRead=0 for every book, so ingest defaults everything to
// 'want'; this promotes the ones with an lpr to 'reading'.
//
// The WHERE guard (finished=false AND status<>'read' AND status<>'reading')
// means it NEVER clobbers a read/finished book — a finished book's lpr is just
// its end position, not evidence it is still being read — and reports an honest
// "row changed" bool: a book insights already marked 'read' is refused, and a
// book already 'reading' is a no-op (RowsAffected 0), so a re-run of the sweep
// changes nothing. Returns whether a row was actually flipped to 'reading'.
func (d *DB) SetReadingItemReading(ctx context.Context, owner, source, externalID string) (bool, error) {
	tag, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET
		    status    = 'reading',
		    synced_at = now()
		  WHERE owner=$1 AND source=$2 AND external_id=$3
		    AND finished=false AND status<>'read' AND status<>'reading'`,
		owner, source, externalID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// CurationStatuses is the canonical 1:1-with-Hardcover status vocabulary. A
// status override MUST be one of these five (Amazon ingest only ever produces
// want/reading/read; paused/dnf come only from a user or Hardcover override).
var CurationStatuses = map[string]bool{
	"want": true, "reading": true, "read": true, "paused": true, "dnf": true,
}

// ReadingItemCurationPatch is the override-layer write for SetReadingItemCuration.
// Each field has a Set* presence flag so PATCH semantics are exact: Set*=false
// leaves the column untouched; Set*=true writes the paired value — and a nil value
// with Set*=true CLEARS the override back to NULL (reverting to the Amazon-derived
// layer). curation_updated_at is always stamped to now() on any curation write.
type ReadingItemCurationPatch struct {
	Status        *string
	SetStatus     bool
	Rating        *float64
	SetRating     bool
	FinishedAt    *time.Time
	SetFinishedAt bool
}

// SetReadingItemCuration writes the curation OVERRIDE layer for one row (keyed by
// owner+source+external_id) and stamps curation_updated_at=now — the row-level LWW
// clock. This is the user-driven writer (the PATCH endpoint); the Hardcover pull's
// LWW branch is the other. It touches ONLY the override columns + the stamp, never
// the Amazon-derived status/finished/rating, so effective = COALESCE(override,
// derived) flips without losing what the source computed. A Set-with-nil-value
// clears that override. Returns the updated row (derived + override, so the caller
// can render the effective DTO) or (zero, pgx.ErrNoRows) when no such row exists.
// A non-nil Status override must be one of CurationStatuses.
func (d *DB) SetReadingItemCuration(ctx context.Context, owner, source, externalID string, patch ReadingItemCurationPatch) (ReadingItem, error) {
	if patch.SetStatus && patch.Status != nil && !CurationStatuses[*patch.Status] {
		return ReadingItem{}, fmt.Errorf("db: invalid curation status %q", *patch.Status)
	}
	it := ReadingItem{Owner: owner}
	err := d.Pool.QueryRow(ctx,
		`UPDATE reading_items ri SET
		    status_override      = CASE WHEN $4 THEN $5 ELSE ri.status_override END,
		    rating_override      = CASE WHEN $6 THEN $7 ELSE ri.rating_override END,
		    finished_at_override = CASE WHEN $8 THEN $9 ELSE ri.finished_at_override END,
		    curation_updated_at  = now(),
		    synced_at            = now()
		  WHERE ri.owner=$1 AND ri.source=$2 AND ri.external_id=$3
		 RETURNING source, external_id, title, authors, cover_url, status,
		           progress_percent, finished, started_at, finished_at, rating,
		           subtitle, narrators, series, runtime_min, goodreads_rating,
		           isbn, amazon_asin, hardcover_book_id, hardcover_status,
		           status_override, rating_override, finished_at_override,
		           curation_updated_at, hardcover_lists, synced_at`,
		owner, source, externalID,
		patch.SetStatus, patch.Status,
		patch.SetRating, patch.Rating,
		patch.SetFinishedAt, patch.FinishedAt).
		Scan(&it.Source, &it.ExternalID, &it.Title, &it.Authors, &it.CoverURL, &it.Status,
			&it.ProgressPercent, &it.Finished, &it.StartedAt, &it.FinishedAt, &it.Rating,
			&it.Subtitle, &it.Narrators, &it.Series, &it.RuntimeMin, &it.GoodreadsRating,
			&it.ISBN, &it.AmazonASIN, &it.HardcoverBookID, &it.HardcoverStatus,
			&it.StatusOverride, &it.RatingOverride, &it.FinishedAtOverride,
			&it.CurationUpdatedAt, &it.HardcoverLists, &it.SyncedAt)
	if err != nil {
		return ReadingItem{}, err
	}
	return it, nil
}

// GetReadingItem loads one row by owner+source+external_id — the async curation
// push handler uses it to read the freshly-written effective status/rating/finish
// + the cached Hardcover book/edition ids it needs to mirror the curation out.
// (zero, pgx.ErrNoRows) when the row does not exist for this owner.
func (d *DB) GetReadingItem(ctx context.Context, owner, source, externalID string) (ReadingItem, error) {
	it := ReadingItem{Owner: owner}
	err := d.Pool.QueryRow(ctx,
		`SELECT source, external_id, title, authors, cover_url, status,
		        progress_percent, finished, started_at, finished_at, rating,
		        subtitle, narrators, series, runtime_min, goodreads_rating,
		        isbn, amazon_asin, hardcover_book_id, hardcover_edition_id,
		        hardcover_status, status_override, rating_override,
		        finished_at_override, curation_updated_at, hardcover_lists, synced_at
		   FROM reading_items
		  WHERE owner=$1 AND source=$2 AND external_id=$3`,
		owner, source, externalID).
		Scan(&it.Source, &it.ExternalID, &it.Title, &it.Authors, &it.CoverURL, &it.Status,
			&it.ProgressPercent, &it.Finished, &it.StartedAt, &it.FinishedAt, &it.Rating,
			&it.Subtitle, &it.Narrators, &it.Series, &it.RuntimeMin, &it.GoodreadsRating,
			&it.ISBN, &it.AmazonASIN, &it.HardcoverBookID, &it.HardcoverEditionID,
			&it.HardcoverStatus, &it.StatusOverride, &it.RatingOverride,
			&it.FinishedAtOverride, &it.CurationUpdatedAt, &it.HardcoverLists, &it.SyncedAt)
	if err != nil {
		return ReadingItem{}, err
	}
	return it, nil
}

// EffectiveStatus / EffectiveRating / EffectiveFinishedAt resolve the override ??
// derived layers in Go — the same COALESCE the query DSL applies in SQL, for
// callers holding a scanned ReadingItem (the push handler + the DTO builders).
func (it ReadingItem) EffectiveStatus() string {
	if it.StatusOverride != nil {
		return *it.StatusOverride
	}
	return it.Status
}

func (it ReadingItem) EffectiveRating() *float64 {
	if it.RatingOverride != nil {
		return it.RatingOverride
	}
	return it.Rating
}

func (it ReadingItem) EffectiveFinishedAt() *time.Time {
	if it.FinishedAtOverride != nil {
		return it.FinishedAtOverride
	}
	return it.FinishedAt
}

// SetReadingItemPushed records a successful (or dry-run-previewed) Hardcover push
// of a curation: hardcover_pushed_at=now + hardcover_pushed_status=status. This is
// the echo-suppression stamp — the pull's LWW branch skips adopting a remote status
// equal to hardcover_pushed_status so our own write doesn't look like a Hardcover
// edit. Keyed by owner+source+external_id; status "" leaves the status unchanged.
func (d *DB) SetReadingItemPushed(ctx context.Context, owner, source, externalID, status string) error {
	var st *string
	if status != "" {
		st = &status
	}
	// Advance BOTH the echo-suppression stamp (hardcover_pushed_status) AND the local
	// Hardcover mirror (hardcover_status). After a successful push, Hardcover's shelf
	// IS this status — so mirroring it here immediately clears the "diverged" /
	// "<remote> → <effective>" out-of-sync badge (which reads hardcover_status)
	// without waiting for the next pull. The pull would set the same value anyway
	// (and its LWW branch suppresses our echo via hardcover_pushed_status).
	_, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET
		    hardcover_pushed_at     = now(),
		    hardcover_pushed_status = COALESCE($4, hardcover_pushed_status),
		    hardcover_status        = COALESCE($4, hardcover_status)
		  WHERE owner=$1 AND source=$2 AND external_id=$3`,
		owner, source, externalID, st)
	return err
}

// ListReadingItems returns a user's synced items (source=="" → all sources),
// unfinished first then alphabetical. Never returns raw_meta (the view payload).
func (d *DB) ListReadingItems(ctx context.Context, owner, source string) ([]ReadingItem, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT owner, source, external_id, title, authors, cover_url, status,
		        progress_percent, finished, started_at, finished_at, rating,
		        subtitle, narrators, series, runtime_min, goodreads_rating,
		        isbn, amazon_asin, hardcover_book_id, hardcover_edition_id,
		        hardcover_status, hardcover_matched_at, hardcover_slug,
		        hardcover_pushed_progress,
		        status_override, rating_override, finished_at_override,
		        curation_updated_at, hardcover_lists, synced_at
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
			&it.HardcoverBookID, &it.HardcoverEditionID, &it.HardcoverStatus,
			&it.HardcoverMatchedAt, &it.HardcoverSlug, &it.HardcoverPushedProgress,
			&it.StatusOverride, &it.RatingOverride, &it.FinishedAtOverride,
			&it.CurationUpdatedAt, &it.HardcoverLists, &it.SyncedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// SetReadingItemListsForBook writes a book's Hardcover LIST memberships onto ALL
// the owner's reading_items linked to that Hardcover book id (every edition of the
// Work shares the same lists). lists is the JSON array of list names; passing an
// empty/nil array clears them (a book removed from every list). No-op when
// hardcoverBookID <= 0. Written by the Hardcover pull.
func (d *DB) SetReadingItemListsForBook(ctx context.Context, owner string, hardcoverBookID int64, lists []byte) error {
	if hardcoverBookID <= 0 {
		return nil
	}
	if lists == nil {
		lists = []byte("[]")
	}
	_, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET hardcover_lists = $3
		  WHERE owner = $1 AND hardcover_book_id = $2`,
		owner, hardcoverBookID, lists)
	return err
}

// ListReadingItemsForWork returns every edition of ONE canonical Work for the
// owner — the backing for the Book detail side panel. A "Work" is identified by
// its Hardcover book id when matched (all editions of a book share it), falling
// back to amazon_asin for unmatched books (which cross-links an Audible edition to
// its Kindle sibling before either is matched). At least one of hardcoverBookID /
// amazonASIN must be set; both may be to widen the collapse. Same full projection
// as ListReadingItems so each row is a complete ReadingItemDTO.
func (d *DB) ListReadingItemsForWork(ctx context.Context, owner string, hardcoverBookID *int64, amazonASIN string) ([]ReadingItem, error) {
	amazonASIN = strings.TrimSpace(amazonASIN)
	if hardcoverBookID == nil && amazonASIN == "" {
		return nil, nil // no Work identity → nothing to collapse
	}
	rows, err := d.Pool.Query(ctx,
		`SELECT owner, source, external_id, title, authors, cover_url, status,
		        progress_percent, finished, started_at, finished_at, rating,
		        subtitle, narrators, series, runtime_min, goodreads_rating,
		        isbn, amazon_asin, hardcover_book_id, hardcover_edition_id,
		        hardcover_status, hardcover_matched_at, hardcover_slug,
		        hardcover_pushed_progress,
		        status_override, rating_override, finished_at_override,
		        curation_updated_at, hardcover_lists, synced_at
		   FROM reading_items
		  WHERE owner = $1
		    AND ( ($2::bigint IS NOT NULL AND hardcover_book_id = $2)
		       OR ($3 <> '' AND amazon_asin = $3) )
		  ORDER BY source, external_id`,
		owner, hardcoverBookID, amazonASIN)
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
			&it.HardcoverBookID, &it.HardcoverEditionID, &it.HardcoverStatus,
			&it.HardcoverMatchedAt, &it.HardcoverSlug, &it.HardcoverPushedProgress,
			&it.StatusOverride, &it.RatingOverride, &it.FinishedAtOverride,
			&it.CurationUpdatedAt, &it.HardcoverLists, &it.SyncedAt); err != nil {
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

// ListUnmatchedReadingItemsForMatch is ListUnmatchedReadingItems narrowed by the
// negative/attempt cache (migration 00071): it returns the same unmatched-with-an-
// identity worklist BUT drops rows whose ladder last returned no-match RECENTLY —
// match_attempted_at >= retryBefore. A no-match is therefore retried at most once
// per retry window (the caller passes now-window), so a repeat sweep skips the
// expensive fuzzy tail it already proved fruitless, while still re-checking each
// row eventually in case Hardcover adds the book later. retryBefore is the OLDEST
// attempt stamp still considered "fresh"; a row with match_attempted_at IS NULL
// (never attempted) always qualifies. This is the sweep's candidate loader; the
// plain ListUnmatchedReadingItems (no window) stays for other callers + a future
// force-rematch that deliberately ignores the window.
func (d *DB) ListUnmatchedReadingItemsForMatch(ctx context.Context, owner string, retryBefore time.Time) ([]ReadingItem, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT source, external_id, isbn, amazon_asin, title, authors
		   FROM reading_items
		  WHERE owner = $1
		    AND hardcover_book_id IS NULL
		    AND (external_id <> '' OR isbn <> '' OR title <> '')
		    AND (match_attempted_at IS NULL OR match_attempted_at < $2)
		  ORDER BY source, external_id`,
		owner, retryBefore.UTC())
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

// SetReadingItemMatchAttempted stamps match_attempted_at=now() on one row (keyed
// by owner+source+external_id) — the negative-cache mark the sweep writes when the
// ladder returns no confident match, so a repeat sweep within the retry window
// skips the row (see ListUnmatchedReadingItemsForMatch). It touches ONLY the
// attempt stamp — never the linkage or metadata — and is a no-op (0 rows) when the
// row does not exist. A subsequent successful match clears the row from the
// worklist via hardcover_book_id, so the stale stamp is harmless.
func (d *DB) SetReadingItemMatchAttempted(ctx context.Context, owner, source, externalID string) error {
	_, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET match_attempted_at = now()
		  WHERE owner = $1 AND source = $2 AND external_id = $3`,
		owner, source, externalID)
	return err
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
	Slug            string // the book's Hardcover slug; "" leaves hardcover_slug untouched
	RemoteUpdatedAt time.Time
	// Rating / FinishedAt are the remote curation values the LWW branch adopts into
	// the OVERRIDE layer when Hardcover is the newer writer. Nil = the remote didn't
	// carry that field, so the corresponding override is left untouched.
	Rating     *float64
	FinishedAt *time.Time
}

// UpdateHardcoverLinkFromPull reconciles a pulled Hardcover shelf entry onto the
// local reading_item already linked to that Hardcover book (matched earlier by
// the push/match ladder, so hardcover_book_id is set). It always refreshes the
// provenance linkage — hardcover_status (last-seen remote) and
// hardcover_remote_updated_at (Hardcover's updated_at) — leaving the row's own
// Amazon-derived metadata untouched. Returns the number of rows updated: 0 means
// the user has this book on Hardcover but no matching local reading_item yet
// (inbound-origin creation is a documented follow-up — the caller logs it).
// Status=="" is skipped via COALESCE so an unknown upstream status_id never
// blanks a good value.
//
// LAST-WRITER-WINS (migration 00069): Hardcover is the SECOND curation writer
// (the user's PATCH is the first). When the remote change is newer than our
// override stamp AND is not our own echo, adopt it into the OVERRIDE layer:
//
//	adopt ⇔ remote status present
//	         AND remote_updated_at > curation_updated_at   (remote is newer)
//	         AND remote status ≠ hardcover_pushed_status    (not the echo of our push)
//
// On adopt: status_override←remote status, rating/finished_at overrides←remote
// (when carried), curation_updated_at←remote time. Else keep local (the next push
// reconciles Hardcover). Amazon's per-sync re-derivation is NOT a participant — it
// never touches these columns — so the LWW is strictly user-vs-Hardcover.
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
	var slug *string
	if link.Slug != "" {
		slug = &link.Slug
	}
	tag, err := d.Pool.Exec(ctx,
		`UPDATE reading_items ri SET
		    hardcover_status            = COALESCE($3, hardcover_status),
		    hardcover_remote_updated_at = COALESCE($4, hardcover_remote_updated_at),
		    hardcover_slug              = COALESCE($7, hardcover_slug),
		    -- LWW adopt-gate (see doc): remote present + strictly newer than our
		    -- override stamp + not the echo of our own last push.
		    status_override = CASE WHEN $3::text IS NOT NULL AND $4::timestamptz IS NOT NULL
		                             AND $4::timestamptz > COALESCE(ri.curation_updated_at, 'epoch'::timestamptz)
		                             AND $3::text IS DISTINCT FROM ri.hardcover_pushed_status
		                           THEN $3::text ELSE ri.status_override END,
		    rating_override = CASE WHEN $5::numeric IS NOT NULL AND $4::timestamptz IS NOT NULL
		                             AND $4::timestamptz > COALESCE(ri.curation_updated_at, 'epoch'::timestamptz)
		                             AND $3::text IS DISTINCT FROM ri.hardcover_pushed_status
		                           THEN $5::numeric ELSE ri.rating_override END,
		    finished_at_override = CASE WHEN $6::timestamptz IS NOT NULL AND $4::timestamptz IS NOT NULL
		                             AND $4::timestamptz > COALESCE(ri.curation_updated_at, 'epoch'::timestamptz)
		                             AND $3::text IS DISTINCT FROM ri.hardcover_pushed_status
		                           THEN $6::timestamptz ELSE ri.finished_at_override END,
		    curation_updated_at = CASE WHEN $3::text IS NOT NULL AND $4::timestamptz IS NOT NULL
		                             AND $4::timestamptz > COALESCE(ri.curation_updated_at, 'epoch'::timestamptz)
		                             AND $3::text IS DISTINCT FROM ri.hardcover_pushed_status
		                           THEN $4::timestamptz ELSE ri.curation_updated_at END
		  WHERE ri.owner = $1 AND ri.hardcover_book_id = $2`,
		owner, link.BookID, status, remote, link.Rating, link.FinishedAt, slug)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListOwnerHardcoverLinkedBookIDs returns the set of hardcover_book_ids already
// linked to a NON-hardcover reading_item (a matched Kindle/Audible row) for owner.
// The inbound Hardcover shelf-ingest consults it to SKIP creating a
// source='hardcover' library row for a shelf book a real Kindle/Audible purchase
// already represents — the fused library shows one row per book, not a physical-
// shelf duplicate of an owned ebook/audiobook. Source='hardcover' rows are
// deliberately EXCLUDED so the ingest keeps re-upserting (refreshing) its own rows
// idempotently rather than treating them as already-covered; the upsert key
// (owner+source+external_id) is what prevents a duplicate of the hardcover row
// itself. Returns an empty (non-nil) map when the owner has no external matches.
func (d *DB) ListOwnerHardcoverLinkedBookIDs(ctx context.Context, owner string) (map[int64]bool, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT DISTINCT hardcover_book_id
		   FROM reading_items
		  WHERE owner = $1
		    AND hardcover_book_id IS NOT NULL
		    AND source <> 'hardcover'`,
		owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
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
