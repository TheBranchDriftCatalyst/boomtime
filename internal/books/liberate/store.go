// store.go — liberation's data access.
//
// DELIBERATE DEVIATION, worth reading before you copy it. Every other books
// package reaches the database through methods hung on the shared *db.DB
// (internal/shared/db/reading_items.go and friends). This one does not: it holds
// the shared *pgxpool.Pool and owns its own SQL, so all liberation code —
// protocol, pipeline, and persistence — lives inside internal/books/.
//
// The reason is the domain-framework direction (boom-zp2s): a domain that owns
// its own storage can be lifted into the standalone catalyst-books image, or out
// of boomtime entirely, without unpicking methods from a shared god-object. The
// tradeoff is that liberation's queries are not discoverable alongside the other
// reading_items queries, which is why every query here names its migration
// (00082 host / 00004 standalone).
package liberate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Liberation statuses, matching the text enum in migration 00082. Text rather
// than a PG enum because the additive-only migration rule means we can never
// ALTER TYPE — a new state must not require a migration to an existing column.
const (
	StatusPending           = "pending"
	StatusLicensing         = "licensing"
	StatusDownloading       = "downloading"
	StatusConverting        = "converting"
	StatusLiberated         = "liberated"
	StatusFailed            = "failed"
	StatusDenied            = "denied"
	StatusUnsupportedCodec  = "unsupported_codec"
	StatusUnsupportedFormat = "unsupported_format"
	StatusSkipped           = "skipped"
)

// ErrItemNotFound means no reading_items row matched owner+asin.
var ErrItemNotFound = errors.New("liberate: no library item for that owner and asin")

// Store is liberation's persistence surface.
type Store struct{ Pool *pgxpool.Pool }

// NewStore wires the store to the shared pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

// Item is the liberation-relevant view of a reading_items row.
type Item struct {
	ID               int64
	Owner            string
	ASIN             string
	Title            string
	Authors          string
	RawMeta          []byte
	LiberationStatus string
	AudioPath        string
	AudioBytes       int64
}

// LoadItem fetches one owned Audible title. Scoped to source='audible' because
// liberation is meaningless for a Kindle ebook row — asking for one is a bug in
// the caller, not an empty result.
func (s *Store) LoadItem(ctx context.Context, owner, asin string) (Item, error) {
	const q = `
		SELECT id, owner, external_id, title, authors,
		       COALESCE(raw_meta::text, ''), COALESCE(liberation_status, ''),
		       COALESCE(audio_path, ''), COALESCE(audio_bytes, 0)
		FROM public.reading_items
		WHERE owner = $1 AND external_id = $2 AND source = 'audible'`
	var it Item
	var rawMeta string
	err := s.Pool.QueryRow(ctx, q, owner, asin).Scan(
		&it.ID, &it.Owner, &it.ASIN, &it.Title, &it.Authors,
		&rawMeta, &it.LiberationStatus, &it.AudioPath, &it.AudioBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, fmt.Errorf("%w: %s/%s", ErrItemNotFound, owner, asin)
	}
	if err != nil {
		return Item{}, fmt.Errorf("liberate: load item: %w", err)
	}
	it.RawMeta = []byte(rawMeta)
	return it, nil
}

// ListUnliberated returns the ASINs for one owner that still need work, oldest
// purchases first. Backs the sweep. limit <= 0 means no limit.
func (s *Store) ListUnliberated(ctx context.Context, owner string, limit int) ([]string, error) {
	q := `
		SELECT external_id
		FROM public.reading_items
		WHERE owner = $1 AND source = 'audible'
		  AND (liberation_status IS NULL OR liberation_status NOT IN ('liberated', 'denied', 'unsupported_format', 'skipped'))
		ORDER BY synced_at ASC`
	args := []any{owner}
	if limit > 0 {
		q += ` LIMIT $2`
		args = append(args, limit)
	}
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("liberate: list unliberated: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var asin string
		if err := rows.Scan(&asin); err != nil {
			return nil, err
		}
		out = append(out, asin)
	}
	return out, rows.Err()
}

// SetStatus records an in-flight stage transition. Clears the previous error so
// a retry does not display a stale failure while it is running.
func (s *Store) SetStatus(ctx context.Context, owner, asin, status string) error {
	const q = `
		UPDATE public.reading_items
		SET liberation_status = $3, liberation_error = NULL
		WHERE owner = $1 AND external_id = $2 AND source = 'audible'`
	_, err := s.Pool.Exec(ctx, q, owner, asin, status)
	if err != nil {
		return fmt.Errorf("liberate: set status: %w", err)
	}
	return nil
}

// MarkLiberated records a successful run.
func (s *Store) MarkLiberated(ctx context.Context, owner, asin, relPath string, bytes int64, contentFormat string) error {
	const q = `
		UPDATE public.reading_items
		SET liberation_status = 'liberated',
		    liberated_at      = now(),
		    audio_path        = $3,
		    audio_bytes       = $4,
		    audio_format      = 'm4b',
		    content_format    = $5,
		    liberation_error  = NULL
		WHERE owner = $1 AND external_id = $2 AND source = 'audible'`
	_, err := s.Pool.Exec(ctx, q, owner, asin, relPath, bytes, contentFormat)
	if err != nil {
		return fmt.Errorf("liberate: mark liberated: %w", err)
	}
	return nil
}

// MarkFailed records a terminal or retryable failure with its reason. The
// content format is carried even on failure — an unsupported_codec row is only
// useful if it says WHICH codec, since that count is what triggers the
// native-decoder epic.
func (s *Store) MarkFailed(ctx context.Context, owner, asin, status, reason, contentFormat string) error {
	const q = `
		UPDATE public.reading_items
		SET liberation_status = $3,
		    liberation_error  = $4,
		    content_format    = COALESCE(NULLIF($5, ''), content_format)
		WHERE owner = $1 AND external_id = $2 AND source = 'audible'`
	_, err := s.Pool.Exec(ctx, q, owner, asin, status, truncate(reason, 2000), contentFormat)
	if err != nil {
		return fmt.Errorf("liberate: mark failed: %w", err)
	}
	return nil
}

// ClearLiberation forgets a local file (the "forget" endpoint), returning the
// row to an unliberated state so a later sweep picks it up again.
func (s *Store) ClearLiberation(ctx context.Context, owner, asin string) (string, error) {
	const q = `
		UPDATE public.reading_items
		SET liberation_status = NULL, liberated_at = NULL, audio_path = NULL,
		    audio_bytes = NULL, audio_format = NULL, liberation_error = NULL
		WHERE owner = $1 AND external_id = $2 AND source = 'audible'
		RETURNING COALESCE($3, '')`
	var prev string
	err := s.Pool.QueryRow(ctx, q, owner, asin, "").Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w: %s/%s", ErrItemNotFound, owner, asin)
	}
	if err != nil {
		return "", fmt.Errorf("liberate: clear liberation: %w", err)
	}
	return prev, nil
}

// StatusCounts summarises an owner's library for the status endpoint. The empty
// string key holds rows never attempted.
func (s *Store) StatusCounts(ctx context.Context, owner string) (map[string]int, error) {
	const q = `
		SELECT COALESCE(liberation_status, ''), count(*)
		FROM public.reading_items
		WHERE owner = $1 AND source = 'audible'
		GROUP BY 1`
	rows, err := s.Pool.Query(ctx, q, owner)
	if err != nil {
		return nil, fmt.Errorf("liberate: status counts: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

// --- attempt history (book_liberation_attempts) ----------------------------

// StartAttempt opens an attempt row and returns its id.
func (s *Store) StartAttempt(ctx context.Context, owner, asin string) (int64, error) {
	const q = `
		INSERT INTO public.book_liberation_attempts (owner, asin, status)
		VALUES ($1, $2, 'pending') RETURNING id`
	var id int64
	if err := s.Pool.QueryRow(ctx, q, owner, asin).Scan(&id); err != nil {
		return 0, fmt.Errorf("liberate: start attempt: %w", err)
	}
	return id, nil
}

// FinishAttempt closes an attempt row. Failures here are logged by the caller
// and never fail the liberation itself — losing a history row is a diagnostics
// gap, not a reason to discard a book that is already on disk.
func (s *Store) FinishAttempt(ctx context.Context, id int64, status string, bytes int64, dur time.Duration, contentFormat, errMsg string) error {
	const q = `
		UPDATE public.book_liberation_attempts
		SET finished_at = now(), status = $2, bytes = $3, duration_ms = $4,
		    content_format = NULLIF($5, ''), error = NULLIF($6, '')
		WHERE id = $1`
	_, err := s.Pool.Exec(ctx, q, id, status, bytes, dur.Milliseconds(), contentFormat, truncate(errMsg, 2000))
	if err != nil {
		return fmt.Errorf("liberate: finish attempt: %w", err)
	}
	return nil
}

// rawMetaJSON is a tiny helper so callers can round-trip raw_meta safely.
func rawMetaJSON(b []byte) json.RawMessage {
	if !json.Valid(b) {
		return nil
	}
	return json.RawMessage(b)
}
