package hardcover

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// BulkUserBookInput is one book in a batched status/rating push. Carries the
// owner-scoped key (source/externalID) + the enum status so the caller can stamp
// the local mirror after a successful write, plus the resolved Hardcover ids.
type BulkUserBookInput struct {
	Source, ExternalID string
	Status             string // enum (want|reading|read|paused|dnf) — for the mirror stamp
	BookID, EditionID  int64
	StatusID           int64
	Rating             *float64
}

// BulkPushResult pairs one input with its per-item Hardcover outcome. UserBookID
// > 0 with an empty Err means the write landed (0 under dry-run / on error).
type BulkPushResult struct {
	Input      BulkUserBookInput
	UserBookID int64
	Err        string
}

// BulkUpsertUserBooks pushes N books' status/rating in ONE GraphQL request via
// aliased insert_user_book mutations (Hardcover has no native array mutation across
// books). One HTTP request == one rate-limiter token, so a batch of 50 costs the
// same 1s budget as a single push — the whole point of bulk sync under the 1 req/s
// cap. reading_format_id is NOT sent (edition_id carries format; see UpsertUserBook).
// Under dry-run the graphql gate blocks the whole batch and every result stays
// zero (UserBookID 0), so the caller skips the mirror stamp.
func (c *Client) BulkUpsertUserBooks(ctx context.Context, items []BulkUserBookInput) ([]BulkPushResult, error) {
	if len(items) == 0 {
		return nil, nil
	}
	var q strings.Builder
	varDefs := make([]string, len(items))
	vars := make(map[string]any, len(items))
	for i, it := range items {
		varDefs[i] = fmt.Sprintf("$o%d: UserBookCreateInput!", i)
		obj := map[string]any{"book_id": it.BookID, "status_id": it.StatusID}
		if it.EditionID > 0 {
			obj["edition_id"] = it.EditionID
		}
		if it.Rating != nil {
			obj["rating"] = *it.Rating
		}
		vars[fmt.Sprintf("o%d", i)] = obj
	}
	fmt.Fprintf(&q, "mutation BulkUpsertUserBooks(%s) {\n", strings.Join(varDefs, ", "))
	for i := range items {
		fmt.Fprintf(&q, "  b%d: insert_user_book(object: $o%d) { id error user_book { id } }\n", i, i)
	}
	q.WriteString("}")

	// Aliased fields → a per-alias result map (b0, b1, …).
	var data map[string]struct {
		ID       int64  `json:"id"`
		Error    string `json:"error"`
		UserBook struct {
			ID int64 `json:"id"`
		} `json:"user_book"`
	}
	if err := c.graphql(ctx, q.String(), vars, &data); err != nil {
		return nil, err
	}
	results := make([]BulkPushResult, len(items))
	for i := range items {
		r := BulkPushResult{Input: items[i]}
		if d, ok := data[fmt.Sprintf("b%d", i)]; ok {
			r.Err = d.Error
			if r.UserBookID = d.UserBook.ID; r.UserBookID == 0 {
				r.UserBookID = d.ID
			}
		}
		results[i] = r
	}
	return results, nil
}

// Hardcover status_id values (reading_items.status maps onto these).
const (
	StatusWant    int64 = 1
	StatusReading int64 = 2
	StatusRead    int64 = 3
	StatusPaused  int64 = 4
	StatusDNF     int64 = 5
)

// Hardcover reading_format_id values (reading_items.source maps onto these).
const (
	FormatPhysical int64 = 1
	FormatAudio    int64 = 2
	FormatEbook    int64 = 4
)

// StatusID maps a canonical boomtime status string onto its Hardcover status_id
// (the inverse of pull.StatusString). Unknown/empty → 0 so a caller can detect an
// unmappable status and skip the push rather than send a bogus id. This is the
// single status→id table the curation push uses instead of a hardcoded
// Reading/Read.
func StatusID(status string) int64 {
	switch status {
	case "want":
		return StatusWant
	case "reading":
		return StatusReading
	case "read":
		return StatusRead
	case "paused":
		return StatusPaused
	case "dnf":
		return StatusDNF
	default:
		return 0
	}
}

// FormatForSource maps a reading_items.source onto the Hardcover reading_format_id
// (kindle→ebook, audible→audio). 0 (unknown) is left off the user_book so
// Hardcover keeps whatever format it has.
func FormatForSource(source string) int64 {
	switch source {
	case "kindle":
		return FormatEbook
	case "audible":
		return FormatAudio
	default:
		return 0
	}
}

// UpsertUserBookCuration is UpsertUserBook plus an optional rating write — the
// curation push needs to mirror the user's chosen status AND rating in one
// user_book mutation (the plain UpsertUserBook never wrote rating). rating nil is
// omitted (leaves Hardcover's rating untouched); a non-nil rating is written onto
// the user_book. Same dry-run gating + error contract as UpsertUserBook (the write
// flows through client.graphql, so under dry-run it is blocked+logged, returns 0).
func (c *Client) UpsertUserBookCuration(ctx context.Context, bookID, editionID, statusID, readingFormatID int64, rating *float64) (int64, error) {
	if bookID <= 0 {
		return 0, fmt.Errorf("hardcover: UpsertUserBookCuration needs a book_id")
	}
	object := map[string]any{
		"book_id":   bookID,
		"status_id": statusID,
	}
	if editionID > 0 {
		object["edition_id"] = editionID
	}
	// NOTE: reading_format_id is deliberately NOT set here. Hardcover's
	// UserBookCreateInput has no such field (live error: "field 'reading_format_id'
	// not found in type: 'UserBookCreateInput'") — format lives on the EDITION, so
	// edition_id already pins it. readingFormatID stays in the signature for the
	// read-push (ReadInput) but must never be written onto the user_book object.
	_ = readingFormatID
	if rating != nil {
		object["rating"] = *rating
	}

	const q = `mutation UpsertUserBookCuration($object: UserBookCreateInput!) {
  insert_user_book(object: $object) {
    id
    error
    user_book { id }
  }
}`
	var data struct {
		InsertUserBook struct {
			ID       int64  `json:"id"`
			Error    string `json:"error"`
			UserBook struct {
				ID int64 `json:"id"`
			} `json:"user_book"`
		} `json:"insert_user_book"`
	}
	if err := c.graphql(ctx, q, map[string]any{"object": object}, &data); err != nil {
		return 0, err
	}
	if data.InsertUserBook.Error != "" {
		return 0, fmt.Errorf("hardcover: insert_user_book (curation): %s", data.InsertUserBook.Error)
	}
	if id := data.InsertUserBook.UserBook.ID; id > 0 {
		return id, nil
	}
	return data.InsertUserBook.ID, nil
}

// UpsertUserBook creates-or-updates the user_book row (status + edition) for a
// matched book and returns Hardcover's user_book id — cache it as
// hardcover_user_book_id so the read-progress push can update in place. Every
// GraphQL response is HTTP 200, so the mutation-level `error` field is checked
// explicitly. editionID / readingFormatID <= 0 are omitted (status-only push).
func (c *Client) UpsertUserBook(ctx context.Context, bookID, editionID, statusID, readingFormatID int64) (int64, error) {
	if bookID <= 0 {
		return 0, fmt.Errorf("hardcover: UpsertUserBook needs a book_id")
	}
	object := map[string]any{
		"book_id":   bookID,
		"status_id": statusID,
	}
	if editionID > 0 {
		object["edition_id"] = editionID
	}
	// reading_format_id is NOT a UserBookCreateInput field (Hardcover rejects it) —
	// edition_id pins the format. See UpsertUserBookCuration for the full note.
	_ = readingFormatID

	const q = `mutation UpsertUserBook($object: UserBookCreateInput!) {
  insert_user_book(object: $object) {
    id
    error
    user_book { id }
  }
}`
	var data struct {
		InsertUserBook struct {
			ID       int64  `json:"id"`
			Error    string `json:"error"`
			UserBook struct {
				ID int64 `json:"id"`
			} `json:"user_book"`
		} `json:"insert_user_book"`
	}
	if err := c.graphql(ctx, q, map[string]any{"object": object}, &data); err != nil {
		return 0, err
	}
	if data.InsertUserBook.Error != "" {
		return 0, fmt.Errorf("hardcover: insert_user_book: %s", data.InsertUserBook.Error)
	}
	if id := data.InsertUserBook.UserBook.ID; id > 0 {
		return id, nil
	}
	return data.InsertUserBook.ID, nil
}

// ReadInput is the progress/dates payload for a user_book_read row. All fields
// are optional; a nil pointer is omitted so a partial update doesn't clobber
// existing Hardcover data. Dates are pushed as YYYY-MM-DD (Hardcover's date
// granularity for reads).
type ReadInput struct {
	// Progress is the read percent (0-100). It maps onto user_book_reads.progress
	// (float8) — the canonical "how far through" signal for an in-progress book.
	Progress *float64
	// ProgressPages / ProgressSeconds are the derived absolute positions (Int).
	// The continuous-progress push fills whichever the matched edition's length
	// supports (pages for print/ebook, seconds for audio); either may be nil.
	ProgressPages   *int
	ProgressSeconds *int
	StartedAt       *time.Time
	FinishedAt      *time.Time
	EditionID       int64 // optional: pin the read to a specific edition
	ReadingFormatID int64 // optional: 1 physical · 2 audio · 4 ebook
	// UserBookReadID, when > 0, switches UpsertRead from insert to
	// update_user_book_read against that existing read row.
	UserBookReadID int64
}

func (r ReadInput) object() map[string]any {
	obj := map[string]any{}
	// NOTE: the percent `progress` is NOT a DatesReadInput field (schema
	// introspection: only progress_pages / progress_seconds exist) — Hardcover
	// derives the percent from the absolute position against the edition length.
	// Emitting `progress` gets the whole mutation rejected. Kept on the struct so
	// callers can pass it (and we still derive pages/seconds from it upstream), but
	// never sent on the read object.
	_ = r.Progress
	if r.ProgressPages != nil {
		obj["progress_pages"] = *r.ProgressPages
	}
	if r.ProgressSeconds != nil {
		obj["progress_seconds"] = *r.ProgressSeconds
	}
	if r.StartedAt != nil {
		obj["started_at"] = r.StartedAt.UTC().Format("2006-01-02")
	}
	if r.FinishedAt != nil {
		obj["finished_at"] = r.FinishedAt.UTC().Format("2006-01-02")
	}
	if r.EditionID > 0 {
		obj["edition_id"] = r.EditionID
	}
	// reading_format_id is NOT a DatesReadInput field either (live error: "field
	// 'reading_format_id' not found in type: 'DatesReadInput'"), despite the
	// user_book_reads TABLE carrying the column — the mutation input doesn't accept
	// it. edition_id pins the format on the read. Kept on the struct for callers but
	// never emitted onto the read object.
	_ = r.ReadingFormatID
	return obj
}

// UpsertRead writes reading progress + dates for a user_book. First time
// (in.UserBookReadID == 0) it calls insert_user_book_read against userBookID;
// subsequently it calls update_user_book_read against the cached read id. It
// returns the user_book_read id — cache it so the next push updates in place.
// The mutation-level `error` field is checked explicitly (HTTP is always 200).
func (c *Client) UpsertRead(ctx context.Context, userBookID int64, in ReadInput) (int64, error) {
	if in.UserBookReadID > 0 {
		return c.updateRead(ctx, in)
	}
	if userBookID <= 0 {
		return 0, fmt.Errorf("hardcover: UpsertRead needs a user_book_id for the initial insert")
	}
	const q = `mutation InsertRead($id: Int!, $object: DatesReadInput!) {
  insert_user_book_read(user_book_id: $id, user_book_read: $object) {
    id
    error
    user_book_read { id }
  }
}`
	var data struct {
		Insert struct {
			ID           int64  `json:"id"`
			Error        string `json:"error"`
			UserBookRead struct {
				ID int64 `json:"id"`
			} `json:"user_book_read"`
		} `json:"insert_user_book_read"`
	}
	if err := c.graphql(ctx, q, map[string]any{"id": userBookID, "object": in.object()}, &data); err != nil {
		return 0, err
	}
	if data.Insert.Error != "" {
		return 0, fmt.Errorf("hardcover: insert_user_book_read: %s", data.Insert.Error)
	}
	if id := data.Insert.UserBookRead.ID; id > 0 {
		return id, nil
	}
	return data.Insert.ID, nil
}

// updateRead is the update_user_book_read path (in.UserBookReadID > 0).
func (c *Client) updateRead(ctx context.Context, in ReadInput) (int64, error) {
	const q = `mutation UpdateRead($id: Int!, $object: DatesReadInput!) {
  update_user_book_read(id: $id, object: $object) {
    id
    error
    user_book_read { id }
  }
}`
	var data struct {
		Update struct {
			ID           int64  `json:"id"`
			Error        string `json:"error"`
			UserBookRead struct {
				ID int64 `json:"id"`
			} `json:"user_book_read"`
		} `json:"update_user_book_read"`
	}
	if err := c.graphql(ctx, q, map[string]any{"id": in.UserBookReadID, "object": in.object()}, &data); err != nil {
		return 0, err
	}
	if data.Update.Error != "" {
		return 0, fmt.Errorf("hardcover: update_user_book_read: %s", data.Update.Error)
	}
	if id := data.Update.UserBookRead.ID; id > 0 {
		return id, nil
	}
	return in.UserBookReadID, nil
}

// PushProgress is the reusable continuous-progress push: it matches an
// in-progress reading item to a Hardcover book+edition, marks it
// currently-reading, and upserts the read with progress=percent — plus
// progress_pages / progress_seconds derived from the edition length when known.
// Shared by the Audible forward sync and (via the parent's wiring) the Kindle
// sync so an in-progress % on either source mirrors to Hardcover.
//
// It never guess-pushes: a no-confident-match returns (MatchResult{Method:
// MatchNone}, nil) with no mutation. Every write flows through the client's
// dry-run gate — when dry-run is on, UpsertUserBook is blocked and returns id 0,
// so the read upsert is skipped and the whole call is a logged no-op (kept that
// way deliberately). editionLenPages / editionLenSeconds <= 0 mean "unknown",
// in which case only the percent is pushed. format is the Hardcover
// reading_format_id (FormatAudio / FormatEbook / FormatPhysical); <= 0 is left
// off the user_book so Hardcover keeps whatever it has.
func PushProgress(ctx context.Context, client *Client, in MatchInput, percent float64, editionLenPages, editionLenSeconds int, format int64) (MatchResult, error) {
	if client == nil {
		return MatchResult{Method: MatchNone}, fmt.Errorf("hardcover: PushProgress needs a client")
	}
	match, err := client.Match(ctx, in)
	if err != nil {
		return MatchResult{}, err
	}
	if match.BookID <= 0 {
		return match, nil // no confident match — leave it for review, never guess-push
	}

	userBookID, err := client.UpsertUserBook(ctx, match.BookID, match.EditionID, StatusReading, format)
	if err != nil {
		return match, err
	}
	if userBookID <= 0 {
		// Dry-run gate blocked the write (or Hardcover returned no id) — there is
		// no user_book to attach a read to. The intent was already logged by the
		// gate; treat as a successful no-op.
		return match, nil
	}
	if _, err := client.UpsertRead(ctx, userBookID, progressReadInput(percent, editionLenPages, editionLenSeconds, match.EditionID, format)); err != nil {
		return match, err
	}
	return match, nil
}

// PushProgressMatched is the continuous-progress push for an ALREADY-MATCHED
// reading item: it skips the rate-limited match ladder entirely and pushes
// straight against the caller-supplied bookID / editionID (the ids the match
// step already resolved + cached on the row). This is the fix for the
// in-progress push FLOOD — re-running client.Match for every in-progress book on
// every sync was the flood; the stored link makes it unnecessary.
//
// It marks the book currently-reading (UpsertUserBook) and, when that produced a
// real user_book, upserts the read with progress=percent (+ derived
// pages/seconds). applied is true ONLY when a real write happened: under the
// dry-run gate UpsertUserBook returns id 0, so this returns (false, nil) — a
// logged no-op, exactly like PushProgress. bookID <= 0 is a no-op (the caller
// should skip unmatched rows; this guards it) returning (false, nil).
func PushProgressMatched(ctx context.Context, client *Client, bookID, editionID int64, percent float64, editionLenPages, editionLenSeconds int, format int64) (bool, error) {
	if client == nil {
		return false, fmt.Errorf("hardcover: PushProgressMatched needs a client")
	}
	if bookID <= 0 {
		return false, nil // unmatched — nothing to push (matching is the match step's job)
	}
	userBookID, err := client.UpsertUserBook(ctx, bookID, editionID, StatusReading, format)
	if err != nil {
		return false, err
	}
	if userBookID <= 0 {
		// Dry-run gate blocked the write (or Hardcover returned no id) — no
		// user_book to attach a read to, and no real write happened. The intent was
		// already logged by the gate; treat as a no-op that did NOT apply.
		return false, nil
	}
	if _, err := client.UpsertRead(ctx, userBookID, progressReadInput(percent, editionLenPages, editionLenSeconds, editionID, format)); err != nil {
		return false, err
	}
	return true, nil
}

// progressReadInput builds the ReadInput for a continuous-progress push: the
// clamped percent, plus the absolute page/second positions when the edition
// length is known (round(percent/100 * length)).
func progressReadInput(percent float64, lenPages, lenSeconds int, editionID, format int64) ReadInput {
	p := clampPercent(percent)
	in := ReadInput{
		Progress:        &p,
		EditionID:       editionID,
		ReadingFormatID: format,
	}
	if lenPages > 0 {
		pages := int(math.Round(p / 100 * float64(lenPages)))
		in.ProgressPages = &pages
	}
	if lenSeconds > 0 {
		secs := int(math.Round(p / 100 * float64(lenSeconds)))
		in.ProgressSeconds = &secs
	}
	return in
}

// clampPercent bounds a read percent to [0, 100] so a slightly-over source value
// never pushes an out-of-range progress.
func clampPercent(p float64) float64 {
	switch {
	case p < 0:
		return 0
	case p > 100:
		return 100
	default:
		return p
	}
}
