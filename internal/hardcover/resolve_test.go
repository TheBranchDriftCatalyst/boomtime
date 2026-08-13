package hardcover

import (
	"encoding/json"
	"testing"
)

// resolve_test.go — pins the ASIN→BookMeta parser (the Kindle metadata resolver)
// against a representative editions() payload: book.title/slug are VERIFIED;
// reading_format_id lives on the EDITION; authors come from contributions[];
// cached_image is decoded defensively (object-with-url here, string below).

const editionsByASINJSON = `[
  {
    "id": 8801,
    "book_id": 555,
    "reading_format_id": 4,
    "book": {
      "title": "Dune",
      "slug": "dune",
      "cached_image": { "url": "https://img.hardcover.app/dune.jpg", "width": 400 },
      "contributions": [
        { "author": { "name": "Frank Herbert" } },
        { "author": { "name": "" } }
      ]
    }
  }
]`

func TestParseBookMeta_Representative(t *testing.T) {
	bm := parseBookMeta(json.RawMessage(editionsByASINJSON))
	if bm == nil {
		t.Fatal("parseBookMeta returned nil for a non-empty editions array")
	}
	if bm.BookID != 555 {
		t.Errorf("BookID = %d, want 555", bm.BookID)
	}
	if bm.EditionID != 8801 {
		t.Errorf("EditionID = %d, want 8801", bm.EditionID)
	}
	if bm.ReadingFormatID != int(FormatEbook) { // 4 = ebook (Kindle)
		t.Errorf("ReadingFormatID = %d, want %d (ebook)", bm.ReadingFormatID, FormatEbook)
	}
	if bm.Title != "Dune" || bm.Slug != "dune" {
		t.Errorf("title/slug = %q / %q", bm.Title, bm.Slug)
	}
	// The empty-name contribution must be dropped, leaving a single clean author.
	if bm.Authors != "Frank Herbert" {
		t.Errorf("Authors = %q, want %q", bm.Authors, "Frank Herbert")
	}
	if bm.CoverURL != "https://img.hardcover.app/dune.jpg" {
		t.Errorf("CoverURL = %q", bm.CoverURL)
	}
}

func TestParseBookMeta_Empty(t *testing.T) {
	if bm := parseBookMeta(json.RawMessage(`[]`)); bm != nil {
		t.Errorf("empty editions array should yield nil, got %+v", bm)
	}
	if bm := parseBookMeta(nil); bm != nil {
		t.Errorf("nil raw should yield nil, got %+v", bm)
	}
}

func TestExtractCoverURL_Variants(t *testing.T) {
	cases := map[string]string{
		`"https://x/y.jpg"`:         "https://x/y.jpg", // bare string
		`{"url":"https://a/b.png"}`: "https://a/b.png", // object with url
		`{"width":10}`:              "",                // object without url
		`null`:                      "",
		``:                          "",
	}
	for raw, want := range cases {
		if got := extractCoverURL(json.RawMessage(raw)); got != want {
			t.Errorf("extractCoverURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestParseBookMeta_StringCachedImage covers the alternate cached_image shape (a
// bare URL string) and a multi-author join.
func TestParseBookMeta_StringCachedImage(t *testing.T) {
	const j = `[{"id":1,"book_id":2,"reading_format_id":1,"book":{
	  "title":"Good Omens","slug":"good-omens",
	  "cached_image":"https://img/go.jpg",
	  "contributions":[{"author":{"name":"Terry Pratchett"}},{"author":{"name":"Neil Gaiman"}}]}}]`
	bm := parseBookMeta(json.RawMessage(j))
	if bm == nil {
		t.Fatal("nil BookMeta")
	}
	if bm.CoverURL != "https://img/go.jpg" {
		t.Errorf("CoverURL = %q", bm.CoverURL)
	}
	if bm.Authors != "Terry Pratchett, Neil Gaiman" {
		t.Errorf("Authors = %q", bm.Authors)
	}
	if bm.ReadingFormatID != int(FormatPhysical) {
		t.Errorf("ReadingFormatID = %d, want %d", bm.ReadingFormatID, FormatPhysical)
	}
}
