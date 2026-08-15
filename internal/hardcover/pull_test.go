package hardcover

import (
	"encoding/json"
	"testing"
	"time"
)

// pull_test.go — pins the INBOUND pull parser against the VERIFIED user_books
// shape (see pull.go's header). Non-tautological: it unmarshals the exact wire
// JSON Hardcover returns and asserts every mapped field, the status_id→string
// mapping, the nullable read dates, and the empty-reads (unread) case. A schema
// drift or a broken date parser fails here rather than silently mis-linking.

// verifiedUserBooksJSON is the user_books array value exactly as verified live:
//   - a READ book (status_id 3) with one finished read (dates + pages)
//   - a CURRENTLY-READING book (status_id 2) with an in-progress read (no finish)
//   - a WANT book (status_id 1) with an empty reads array (unread)
const verifiedUserBooksJSON = `[
  {
    "id": 1001,
    "book_id": 555,
    "edition_id": 8801,
    "status_id": 3,
    "rating": 4.5,
    "updated_at": "2026-07-15T09:30:00Z",
    "book": { "title": "Dune", "slug": "dune" },
    "user_book_reads": [
      { "id": 71, "started_at": "2026-06-01", "finished_at": "2026-06-20", "progress_pages": 412 }
    ]
  },
  {
    "id": 1002,
    "book_id": 556,
    "edition_id": 8802,
    "status_id": 2,
    "rating": null,
    "updated_at": "2026-07-16T12:00:00Z",
    "book": { "title": "Anathem", "slug": "anathem" },
    "user_book_reads": [
      { "id": 72, "started_at": "2026-07-10", "finished_at": null, "progress_pages": 88 }
    ]
  },
  {
    "id": 1003,
    "book_id": 557,
    "edition_id": 8803,
    "status_id": 1,
    "rating": null,
    "updated_at": "2026-07-01T00:00:00Z",
    "book": { "title": "Piranesi", "slug": "piranesi" },
    "user_book_reads": []
  }
]`

func TestUnmarshalUserBooks_VerifiedShape(t *testing.T) {
	books, err := unmarshalUserBooks(json.RawMessage(verifiedUserBooksJSON))
	if err != nil {
		t.Fatalf("unmarshalUserBooks: %v", err)
	}
	if len(books) != 3 {
		t.Fatalf("want 3 books, got %d", len(books))
	}

	// --- Book 1: a fully-read book with a finished read. ---
	read := books[0]
	if read.ID != 1001 || read.BookID != 555 || read.EditionID != 8801 || read.StatusID != 3 {
		t.Fatalf("read book ids wrong: %+v", read)
	}
	if read.Title != "Dune" || read.Slug != "dune" {
		t.Fatalf("read book title/slug wrong: %q / %q", read.Title, read.Slug)
	}
	if read.Rating == nil || *read.Rating != 4.5 {
		t.Fatalf("read book rating = %v, want 4.5", read.Rating)
	}
	wantUpdated := time.Date(2026, 7, 15, 9, 30, 0, 0, time.UTC)
	if !read.UpdatedAt.Equal(wantUpdated) {
		t.Fatalf("read book updated_at = %v, want %v", read.UpdatedAt, wantUpdated)
	}
	if len(read.Reads) != 1 {
		t.Fatalf("read book: want 1 read, got %d", len(read.Reads))
	}
	r := read.Reads[0]
	if r.ID != 71 {
		t.Fatalf("read id = %d, want 71", r.ID)
	}
	if r.StartedAt == nil || !r.StartedAt.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("read started_at = %v", r.StartedAt)
	}
	if r.FinishedAt == nil || !r.FinishedAt.Equal(time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("read finished_at = %v", r.FinishedAt)
	}
	if r.ProgressPages == nil || *r.ProgressPages != 412 {
		t.Fatalf("read progress_pages = %v, want 412", r.ProgressPages)
	}

	// --- Book 2: currently-reading, null rating + null finished_at. ---
	reading := books[1]
	if reading.StatusID != 2 {
		t.Fatalf("reading status_id = %d, want 2", reading.StatusID)
	}
	if reading.Rating != nil {
		t.Fatalf("reading rating should be nil, got %v", *reading.Rating)
	}
	if len(reading.Reads) != 1 {
		t.Fatalf("reading: want 1 read, got %d", len(reading.Reads))
	}
	if reading.Reads[0].FinishedAt != nil {
		t.Fatalf("in-progress read finished_at should be nil, got %v", reading.Reads[0].FinishedAt)
	}
	if reading.Reads[0].StartedAt == nil {
		t.Fatal("in-progress read started_at should be set")
	}

	// --- Book 3: want, with an EMPTY reads array (unread). ---
	want := books[2]
	if want.StatusID != 1 {
		t.Fatalf("want status_id = %d, want 1", want.StatusID)
	}
	if len(want.Reads) != 0 {
		t.Fatalf("want book should have 0 reads, got %d", len(want.Reads))
	}
}

// TestUnmarshalUserBooks_CoverURL pins the book.image.url → UserBook.CoverURL
// mapping the inbound ingest carries onto source='hardcover' rows. A book with no
// image object yields an empty CoverURL (not an error), and the present url round-
// trips exactly.
func TestUnmarshalUserBooks_CoverURL(t *testing.T) {
	const j = `[
	  {"id":1,"book_id":800,"edition_id":10,"status_id":3,"updated_at":"2026-01-01T00:00:00Z",
	   "book":{"title":"With Cover","slug":"with-cover","image":{"url":"https://img/cover.jpg"}},
	   "user_book_reads":[]},
	  {"id":2,"book_id":801,"edition_id":20,"status_id":1,"updated_at":"2026-01-01T00:00:00Z",
	   "book":{"title":"No Cover","slug":"no-cover"},
	   "user_book_reads":[]}
	]`
	books, err := unmarshalUserBooks(json.RawMessage(j))
	if err != nil {
		t.Fatalf("unmarshalUserBooks: %v", err)
	}
	if books[0].CoverURL != "https://img/cover.jpg" {
		t.Fatalf("cover url = %q, want https://img/cover.jpg", books[0].CoverURL)
	}
	if books[1].CoverURL != "" {
		t.Fatalf("missing image should yield empty cover url, got %q", books[1].CoverURL)
	}
}

func TestStatusString(t *testing.T) {
	cases := map[int]string{
		1: "want", 2: "reading", 3: "read", 4: "paused", 5: "dnf", 99: "",
	}
	for id, want := range cases {
		if got := StatusString(id); got != want {
			t.Errorf("StatusString(%d) = %q, want %q", id, got, want)
		}
	}
}

func TestShelf_HasRead(t *testing.T) {
	books, err := unmarshalUserBooks(json.RawMessage(verifiedUserBooksJSON))
	if err != nil {
		t.Fatalf("unmarshalUserBooks: %v", err)
	}
	shelf := BuildShelf(books)

	// book 555 is status_id 3 (read) → finished on the shelf.
	if !shelf.HasRead(555) {
		t.Error("HasRead(555) = false, want true (status read)")
	}
	// book 556 is currently-reading with no finished_at → not read.
	if shelf.HasRead(556) {
		t.Error("HasRead(556) = true, want false (in progress)")
	}
	// book 557 is want, no reads → not read.
	if shelf.HasRead(557) {
		t.Error("HasRead(557) = true, want false (want)")
	}
	// a book not on the shelf → not read.
	if shelf.HasRead(999999) {
		t.Error("HasRead(unknown) = true, want false")
	}
	if _, ok := shelf.Get(555); !ok {
		t.Error("Get(555) not found")
	}
}

// TestShelf_HasRead_FinishViaRead proves a book whose STATUS isn't "read" but
// which carries a read with a finished_at still counts as read (a partial-shelf
// state the push must not re-push).
func TestShelf_HasRead_FinishViaRead(t *testing.T) {
	const j = `[{"id":1,"book_id":42,"edition_id":9,"status_id":2,"updated_at":"2026-01-01T00:00:00Z",
	  "book":{"title":"X","slug":"x"},
	  "user_book_reads":[{"id":5,"started_at":"2025-12-01","finished_at":"2025-12-15","progress_pages":300}]}]`
	books, err := unmarshalUserBooks(json.RawMessage(j))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !BuildShelf(books).HasRead(42) {
		t.Error("HasRead(42) = false, want true (has a finished read despite status=reading)")
	}
}
