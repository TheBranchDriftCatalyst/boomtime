package hardcover

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// pull.go — the INBOUND half of the Hardcover bidirectional sync (the PULL). It
// reads the user's Hardcover shelf (user_books + their reads) so boomtime can
// reconcile its minimal per-row linkage (migration 00063) against the remote
// state. It is READ-ONLY: every query passes cleanly through client.graphql's
// dry-run gate (only mutations are blocked), so the pull works even while writes
// are fail-safe-disabled. Per the domain design there is NO local shelf mirror —
// callers reconcile in memory (see Shelf) and persist only the linkage columns.
//
// VERIFIED SHAPES (do not guess beyond these):
//   query { me { id username } }                → data.me is an ARRAY [{id,username}]
//   query { user_books(where:{user_id:{_eq}}, offset, limit){ ... } }
//     → [{ id, book_id, edition_id, status_id (1 want|2 reading|3 read),
//          rating (float|null), updated_at (rfc3339), book{title,slug},
//          user_book_reads:[{id,started_at,finished_at,progress_pages}] ([] unread) }]
//   Hasura pagination = offset/limit; page until a short page.

// userBooksPageSize is the offset/limit page size for the shelf sweep. 100 keeps
// each page's JSON small while staying well under Hardcover's rate budget (the
// throttle in client.graphql already caps us at ~1 req/s).
const userBooksPageSize = 100

// UserBookRead is one user_book_reads row: a single reading session's dates +
// page progress. All three are nullable on Hardcover (a shelved-but-unread book
// has an empty reads array; a want/reading row may have dates but no pages).
type UserBookRead struct {
	ID              int
	StartedAt       *time.Time
	FinishedAt      *time.Time
	ProgressPages   *int
	ProgressSeconds *int // audio listening position in seconds (nil for print/ebook)
}

// UserBook is one user_books row (the user's shelf entry for a book) plus its
// reads. StatusID maps onto the push.go Status* consts (1 want, 2 reading, 3
// read); use StatusString to render it. Reads is empty ([]) for an unread shelf
// entry. This is an in-memory reconcile shape — it is NEVER persisted wholesale;
// only the linkage columns (status + remote updated_at) land in reading_items.
type UserBook struct {
	ID        int
	BookID    int
	EditionID int
	StatusID  int
	Rating    *float64
	UpdatedAt time.Time
	Title     string
	Slug      string
	Reads     []UserBookRead
}

// StatusString maps a Hardcover status_id onto the boomtime status string the
// reading_items.hardcover_status column stores. Unknown ids map to "" so a new
// upstream status never silently masquerades as a known one.
func StatusString(statusID int) string {
	switch int64(statusID) {
	case StatusWant:
		return "want"
	case StatusReading:
		return "reading"
	case StatusRead:
		return "read"
	case StatusPaused:
		return "paused"
	case StatusDNF:
		return "dnf"
	default:
		return ""
	}
}

// Me returns the authenticated user's numeric Hardcover id (me[0].id). The
// user_books pull keys on this id. ErrBadToken on a rejected token; a 200 with an
// empty me{} is treated as an error here (unlike Validate) because the pull
// cannot proceed without a user id.
func (c *Client) Me(ctx context.Context) (int, error) {
	const q = `query { me { id } }`
	var data struct {
		Me []struct {
			ID int `json:"id"`
		} `json:"me"`
	}
	if err := c.graphql(ctx, q, nil, &data); err != nil {
		return 0, err
	}
	if len(data.Me) == 0 {
		return 0, ErrBadToken
	}
	return data.Me[0].ID, nil
}

// userBooksQuery is the paginated shelf read. order_by:{id:asc} makes the
// offset/limit paging stable (without a total order, offset pagination can skip
// or repeat rows). Every field here is in the VERIFIED shape.
const userBooksQuery = `query UserBooks($u: Int!, $o: Int!, $l: Int!) {
  user_books(where: {user_id: {_eq: $u}}, order_by: {id: asc}, offset: $o, limit: $l) {
    id
    book_id
    edition_id
    status_id
    rating
    updated_at
    book { title slug }
    user_book_reads { id started_at finished_at progress_pages progress_seconds }
  }
}`

// UserBooks fetches the user's entire Hardcover shelf, paging offset/limit to
// exhaustion (a short page ends the sweep). Returns the reconcile-ready UserBook
// slice; the caller persists only the minimal linkage (see the sync service).
func (c *Client) UserBooks(ctx context.Context, userID int) ([]UserBook, error) {
	var out []UserBook
	for offset := 0; ; offset += userBooksPageSize {
		var data struct {
			UserBooks json.RawMessage `json:"user_books"`
		}
		if err := c.graphql(ctx, userBooksQuery, map[string]any{
			"u": userID, "o": offset, "l": userBooksPageSize,
		}, &data); err != nil {
			return nil, err
		}
		page, err := unmarshalUserBooks(data.UserBooks)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < userBooksPageSize {
			break // short (or empty) page → shelf exhausted
		}
	}
	return out, nil
}

// rawUserBook mirrors the wire JSON so date parsing stays flexible: Hardcover
// reads use date-only strings ("2024-01-15") while updated_at is rfc3339, and
// Go's time.Time only unmarshals rfc3339 — so dates arrive as *string and are
// parsed by parseHardcoverTime.
type rawUserBook struct {
	ID        int      `json:"id"`
	BookID    int      `json:"book_id"`
	EditionID int      `json:"edition_id"`
	StatusID  int      `json:"status_id"`
	Rating    *float64 `json:"rating"`
	UpdatedAt string   `json:"updated_at"`
	Book      struct {
		Title string `json:"title"`
		Slug  string `json:"slug"`
	} `json:"book"`
	Reads []struct {
		ID              int     `json:"id"`
		StartedAt       *string `json:"started_at"`
		FinishedAt      *string `json:"finished_at"`
		ProgressPages   *int    `json:"progress_pages"`
		ProgressSeconds *int    `json:"progress_seconds"`
	} `json:"user_book_reads"`
}

func (r rawUserBook) toUserBook() UserBook {
	ub := UserBook{
		ID:        r.ID,
		BookID:    r.BookID,
		EditionID: r.EditionID,
		StatusID:  r.StatusID,
		Rating:    r.Rating,
		Title:     r.Book.Title,
		Slug:      r.Book.Slug,
	}
	if t := parseHardcoverTime(r.UpdatedAt); t != nil {
		ub.UpdatedAt = *t
	}
	ub.Reads = make([]UserBookRead, 0, len(r.Reads))
	for _, rd := range r.Reads {
		ub.Reads = append(ub.Reads, UserBookRead{
			ID:              rd.ID,
			StartedAt:       parseHardcoverTimePtr(rd.StartedAt),
			FinishedAt:      parseHardcoverTimePtr(rd.FinishedAt),
			ProgressPages:   rd.ProgressPages,
			ProgressSeconds: rd.ProgressSeconds,
		})
	}
	return ub
}

// unmarshalUserBooks decodes a user_books JSON array into the typed reconcile
// shape. Split out of UserBooks so the parser is unit-testable against a fixture
// of the VERIFIED shape without a live client.
func unmarshalUserBooks(raw json.RawMessage) ([]UserBook, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rows []rawUserBook
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	out := make([]UserBook, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toUserBook())
	}
	return out, nil
}

// parseHardcoverTime parses a Hardcover timestamp, tolerating rfc3339
// (updated_at), a naive datetime, or a bare date (read dates). Returns nil on an
// empty/unparseable value so a missing date stays a nil pointer, never epoch.
func parseHardcoverTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

// parseHardcoverTimePtr is the *string variant for nullable JSON date fields.
func parseHardcoverTimePtr(s *string) *time.Time {
	if s == nil {
		return nil
	}
	return parseHardcoverTime(*s)
}

// Shelf is an in-memory index of a pulled shelf, keyed by Hardcover book_id. It
// backs the outbound push's "already read on the shelf?" check (HasRead) so the
// push can SKIP a book the user already finished on Hardcover — reconcile in
// memory, per the no-mirror design.
type Shelf struct {
	byBook map[int]UserBook
}

// BuildShelf indexes pulled user_books by book_id (last write wins on a dup).
func BuildShelf(books []UserBook) *Shelf {
	m := make(map[int]UserBook, len(books))
	for _, b := range books {
		m[b.BookID] = b
	}
	return &Shelf{byBook: m}
}

// Get returns the shelf entry for a book_id, if present.
func (s *Shelf) Get(bookID int) (UserBook, bool) {
	if s == nil {
		return UserBook{}, false
	}
	ub, ok := s.byBook[bookID]
	return ub, ok
}

// HasRead reports whether the book is already finished on the shelf: either its
// status is "read" (status_id 3) or any of its reads carries a finished_at. The
// push can consult this to avoid re-pushing a finish the user already has.
func (s *Shelf) HasRead(bookID int) bool {
	ub, ok := s.Get(bookID)
	if !ok {
		return false
	}
	if int64(ub.StatusID) == StatusRead {
		return true
	}
	for _, r := range ub.Reads {
		if r.FinishedAt != nil {
			return true
		}
	}
	return false
}
