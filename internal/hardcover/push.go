package hardcover

import (
	"context"
	"fmt"
	"math"
	"time"
)

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
	if readingFormatID > 0 {
		object["reading_format_id"] = readingFormatID
	}

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
	if r.Progress != nil {
		obj["progress"] = *r.Progress
	}
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
	if r.ReadingFormatID > 0 {
		obj["reading_format_id"] = r.ReadingFormatID
	}
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
