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
	"slices"
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

// MaxAutoAttempts is how many CONSECUTIVE failures the unattended sweep tolerates
// before it stops picking a title up on its own.
//
// 3, because the failures worth retrying are transient (a dropped CDN
// connection, a pod evicted mid-convert) and those clear well inside three
// tries; anything still failing on the fourth is a property of the title, not
// the weather. Before this existed a permanently-failing title was re-licensed
// on EVERY sweep forever — which is how three podcasts came to be re-requested
// from Amazon indefinitely.
//
// This bounds the SWEEP only. An explicit single-title liberate (the context
// menu, the CLI) ignores it completely: giving up is the unattended path being
// careful, never the server refusing a direct instruction.
const MaxAutoAttempts = 3

// terminalStatuses are the outcomes the sweep never retries on its own.
// Kept as one list because it is needed in three places (the sweep's exclusion,
// the excluded-set query, and the status rollup) and three hand-copied SQL
// literals is how they drift.
var terminalStatuses = []string{
	StatusDenied, StatusUnsupportedFormat, StatusUnsupportedCodec, StatusSkipped,
}

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
	// Two independent exclusions, and they are not the same thing:
	//   - a TERMINAL status is a verdict about the title (Amazon denied it, the
	//     asset is a podcast, the codec is one we cannot remux)
	//   - MaxAutoAttempts is a verdict about the ATTEMPTS (it keeps failing for
	//     reasons we never managed to classify)
	// Both mean "the sweep stops", but only the first is a statement about the
	// book, which is why the UI reports them separately.
	q := `
		SELECT external_id
		FROM public.reading_items
		WHERE owner = $1 AND source = 'audible'
		  AND liberation_status IS DISTINCT FROM $2
		  AND (liberation_status IS NULL OR NOT (liberation_status = ANY($3)))
		  AND liberation_attempts < $4
		ORDER BY synced_at ASC`
	args := []any{owner, StatusLiberated, terminalStatuses, MaxAutoAttempts}
	if limit > 0 {
		q += ` LIMIT $5`
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
		    liberation_error  = NULL,
		    liberation_attempts = 0
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
		    content_format    = COALESCE(NULLIF($5, ''), content_format),
		    -- Counts CONSECUTIVE failures: every success path resets it to 0, so
		    -- a title that fails twice, succeeds, then fails again starts over
		    -- rather than inching toward a give-up it never earned.
		    liberation_attempts = liberation_attempts + 1
		WHERE owner = $1 AND external_id = $2 AND source = 'audible'`
	_, err := s.Pool.Exec(ctx, q, owner, asin, status, truncate(reason, 2000), contentFormat)
	if err != nil {
		return fmt.Errorf("liberate: mark failed: %w", err)
	}
	return nil
}

// ClearLiberation forgets a local file (the "forget" endpoint), returning the
// row to an unliberated state so a later sweep picks it up again.
//
// This is ALSO the un-give-up path: it resets liberation_attempts, so a title
// the sweep abandoned after MaxAutoAttempts (or classified terminal) becomes
// eligible again. Without that reset "retry" would appear to work, run once,
// and then be silently dropped by the sweep forever after.
func (s *Store) ClearLiberation(ctx context.Context, owner, asin string) (string, error) {
	const q = `
		UPDATE public.reading_items
		SET liberation_status = NULL, liberated_at = NULL, audio_path = NULL,
		    audio_bytes = NULL, audio_format = NULL, liberation_error = NULL,
		    liberation_attempts = 0
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

// ExcludedItem is one title the sweep will not pick up on its own, with enough
// context to judge whether that is right.
type ExcludedItem struct {
	ASIN     string `json:"asin"`
	Title    string `json:"title"`
	Author   string `json:"author,omitempty"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	Attempts int    `json:"attempts"`
	// Retryable distinguishes "we gave up counting failures" (true — a retry is
	// plausible) from a verdict about the title itself (false — a retry will
	// reproduce the same refusal). The UI leads with this because it is the
	// difference between a button worth pressing and one that just burns a
	// request against Amazon.
	Retryable bool `json:"retryable"`
}

// ListExcluded returns every title the sweep skips, newest failure first.
//
// This is the answer to "what did liberation quietly give up on" — a question
// that had no answer before: the rows were correctly excluded from the sweep and
// then invisible, so a permanently-failing title looked identical to one that
// had simply never been queued.
func (s *Store) ListExcluded(ctx context.Context, owner string) ([]ExcludedItem, error) {
	const q = `
		SELECT external_id,
		       COALESCE(title, ''),
		       COALESCE(authors, ''),
		       COALESCE(liberation_status, ''),
		       COALESCE(liberation_error, ''),
		       liberation_attempts
		FROM public.reading_items
		WHERE owner = $1 AND source = 'audible'
		  AND (liberation_status = ANY($2) OR
		       (liberation_attempts >= $3 AND liberation_status IS DISTINCT FROM $4))
		ORDER BY liberation_attempts DESC, title ASC`
	rows, err := s.Pool.Query(ctx, q, owner, terminalStatuses, MaxAutoAttempts, StatusLiberated)
	if err != nil {
		return nil, fmt.Errorf("liberate: list excluded: %w", err)
	}
	defer rows.Close()
	out := []ExcludedItem{}
	for rows.Next() {
		var it ExcludedItem
		if err := rows.Scan(&it.ASIN, &it.Title, &it.Author, &it.Status, &it.Error, &it.Attempts); err != nil {
			return nil, err
		}
		// Terminal is a verdict about the title; exhausted attempts is not.
		it.Retryable = !slices.Contains(terminalStatuses, it.Status)
		out = append(out, it)
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
