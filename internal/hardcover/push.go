package hardcover

import (
	"context"
	"fmt"
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
	ProgressPages   *int
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
	if r.ProgressPages != nil {
		obj["progress_pages"] = *r.ProgressPages
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
