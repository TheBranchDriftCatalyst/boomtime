package hardcover

import (
	"context"
	"testing"
)

// match_test.go — pins the deep-link slug threading through the match ladder
// (gaka-qic0). The Hardcover book PAGE resolves on the book's SLUG, not the
// numeric id, so an exact-id match MUST surface book.slug on the MatchResult or
// the "open on Hardcover" link 404s. Uses the shared fakeRoundTripper
// (push_test.go), which serializes the injected hcEdition — including its
// book.slug — back through the real editionByField JSON parse.

// TestMatch_ASINCarriesSlug: an exact-ASIN edition hit surfaces book.slug on the
// MatchResult (the segment the FE builds /books/<slug> from).
func TestMatch_ASINCarriesSlug(t *testing.T) {
	ed := &hcEdition{ID: 8802, BookID: 556, ReadingFormatID: FormatAudio}
	ed.Book.Slug = "the-way-of-kings"
	client := newFakeClient(&fakeRoundTripper{editionByASIN: ed})

	res, err := client.Match(context.Background(), MatchInput{ASIN: "B0ASINSLUG"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if res.Method != MatchByASIN {
		t.Fatalf("Method = %q, want %q", res.Method, MatchByASIN)
	}
	if res.BookID != 556 || res.EditionID != 8802 {
		t.Fatalf("ids = book %d / edition %d, want 556 / 8802", res.BookID, res.EditionID)
	}
	if res.Slug != "the-way-of-kings" {
		t.Fatalf("Slug = %q, want %q — a slugless match 404s the deep-link", res.Slug, "the-way-of-kings")
	}
}
