// notebook_test.go — pins the Kindle notebook parsers (boom-siwi.5).
//
// Fixtures are INLINE and hand-reduced, with lorem where the prose goes. Never a
// captured page: notebook HTML carries the user's own highlight text and this
// repository is public. A hand-written skeleton also documents exactly what the
// parser depends on, which a 400 KB capture does not.
//
// Four of these are the guards that make a DOM change LOUD instead of silent,
// and each was mutation-verified against the defect it describes.
package amazon

import (
	"errors"
	"testing"
)

// notebookLibraryHTML — the index of books that have annotations, reduced to the
// structure the parser actually reads.
const notebookLibraryHTML = `<html><body>
<div id="kp-notebook-library">
  <div id="B08X4WWQCN" class="a-row kp-notebook-library-each-book">
    <img src="https://images.example/cover1.jpg">
    <h2 class="kp-notebook-searchable">Lorem Ipsum Volume One</h2>
    <p class="kp-notebook-searchable">By Dolor, Sit A.</p>
  </div>
  <div id="B0076Q1J60" class="a-row kp-notebook-library-each-book">
    <img src="https://images.example/cover2.jpg">
    <h2 class="kp-notebook-searchable">Consectetur Adipiscing</h2>
    <p class="kp-notebook-searchable">By Elit, N.</p>
  </div>
</div></body></html>`

// notebookPageHTML — one book's annotations: a highlight WITH a margin note, a
// bare highlight, and a standalone note. Note the two things that bite: the
// marker span carries its own id, and the note is a SIBLING of the highlight
// rather than a child.
const notebookPageHTML = `<html><body>
<div id="kp-notebook-annotations">
  <div id="ANNOT-0001" class="a-row a-spacing-base">
    <span id="annotationHighlightHeader" class="a-size-small a-color-secondary kp-notebook-metadata">Yellow highlight | Location: 1,234</span>
    <input type="hidden" value="1234" id="kp-annotation-location">
    <span id="highlight" class="a-size-base-plus a-color-base kp-notebook-highlight">Lorem ipsum dolor sit amet</span>
    <span id="note" class="a-size-base-plus a-color-base kp-notebook-note">consectetur adipiscing</span>
  </div>
  <div id="ANNOT-0002" class="a-row a-spacing-base">
    <span id="annotationHighlightHeader" class="a-size-small a-color-secondary kp-notebook-metadata">Blue highlight | Page: 42</span>
    <input type="hidden" value="42" id="kp-annotation-location">
    <span id="highlight" class="a-size-base-plus a-color-base kp-notebook-highlight">sed do eiusmod tempor</span>
  </div>
  <div id="ANNOT-0003" class="a-row a-spacing-base">
    <span id="annotationHighlightHeader" class="a-size-small a-color-secondary kp-notebook-metadata">Note | Location: 2,000</span>
    <input type="hidden" value="2000" id="kp-annotation-location">
    <span id="note" class="a-size-base-plus a-color-base kp-notebook-note">a thought with no highlight</span>
  </div>
</div></body></html>`

func TestParseNotebookLibraryExtractsAnnotatedASINsInOrder(t *testing.T) {
	books, err := parseNotebookLibrary([]byte(notebookLibraryHTML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(books) != 2 {
		t.Fatalf("got %d books, want 2", len(books))
	}
	if books[0].ASIN != "B08X4WWQCN" || books[1].ASIN != "B0076Q1J60" {
		t.Fatalf("ASINs out of document order: %q, %q", books[0].ASIN, books[1].ASIN)
	}
	if books[0].Title != "Lorem Ipsum Volume One" {
		t.Fatalf("title = %q", books[0].Title)
	}
	if books[0].Authors != "Dolor, Sit A." {
		t.Fatalf("authors should have the \"By \" prefix stripped, got %q", books[0].Authors)
	}
	if books[0].CoverURL == "" {
		t.Fatal("cover url not extracted")
	}
}

// An account that has never highlighted anything ships the container with no
// book rows. That is a legitimate empty answer, NOT breakage.
func TestParseNotebookLibraryEmptyIndexIsNotAnError(t *testing.T) {
	books, err := parseNotebookLibrary([]byte(`<html><body><div id="kp-notebook-library"></div></body></html>`))
	if err != nil {
		t.Fatalf("an empty index must not be an error: %v", err)
	}
	if len(books) != 0 {
		t.Fatalf("got %d books, want 0", len(books))
	}
}

func TestParseNotebookPageFoldsNoteIntoItsHighlight(t *testing.T) {
	anns, _, _, err := parseNotebookPage([]byte(notebookPageHTML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 2 highlights + 1 standalone note. If the note attached to ANNOT-0001 were
	// emitted separately this would be 4.
	if len(anns) != 3 {
		t.Fatalf("got %d annotations, want 3 (2 highlights + 1 standalone note): %+v", len(anns), anns)
	}

	first := anns[0]
	if first.Kind != AnnotationKindHighlightStr {
		t.Fatalf("first annotation kind = %q", first.Kind)
	}
	if first.Body != "Lorem ipsum dolor sit amet" {
		t.Fatalf("body = %q", first.Body)
	}
	if first.Note != "consectetur adipiscing" {
		t.Fatalf("the margin note was not folded into its highlight: note = %q", first.Note)
	}
	if first.AnnotationID != "ANNOT-0001" {
		t.Fatalf("annotation id = %q — the block walk returned the marker span, not the container", first.AnnotationID)
	}
	if first.Color != "yellow" {
		t.Fatalf("color = %q", first.Color)
	}

	var standalone *KindleAnnotation
	for i := range anns {
		if anns[i].Kind == AnnotationKindNoteStr {
			standalone = &anns[i]
		}
	}
	if standalone == nil {
		t.Fatal("the standalone note was dropped")
	}
	if standalone.Note != "a thought with no highlight" || standalone.Body != "" {
		t.Fatalf("standalone note = %+v", *standalone)
	}
}

// The unit-lying test. Kindle is not one unit, so the parser must report what
// the page said rather than assuming source='kindle' implies a location.
func TestParseNotebookPageReportsLocationVsPageHonestly(t *testing.T) {
	anns, _, _, err := parseNotebookPage([]byte(notebookPageHTML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	byID := map[string]KindleAnnotation{}
	for _, a := range anns {
		byID[a.AnnotationID] = a
	}
	if got := byID["ANNOT-0001"]; got.PositionUnit != PositionUnitLocationStr || got.Position != 1234 {
		t.Fatalf("reflowable highlight = unit %q pos %d, want location 1234", got.PositionUnit, got.Position)
	}
	if got := byID["ANNOT-0002"]; got.PositionUnit != PositionUnitPageStr || got.Position != 42 {
		t.Fatalf("print-replica highlight = unit %q pos %d, want page 42", got.PositionUnit, got.Position)
	}
}

// ⚠ GUARD. Amazon answers an unauthenticated notebook request with HTTP 200 and
// a login form, so status proves nothing.
// Mutation: make isSignInBounce return false → the ingest reports a successful
// sync of zero highlights for every book, forever.
func TestParseNotebookSignInBounceIsAnError(t *testing.T) {
	bounce := []byte(`<html><body><form name="signIn" action="/ap/signin"><input name="email"></form></body></html>`)

	if _, _, _, err := parseNotebookPage(bounce); !errors.Is(err, ErrNotebookShapeUnknown) {
		t.Fatalf("per-book: want ErrNotebookShapeUnknown, got %v", err)
	}
	if _, err := parseNotebookLibrary(bounce); !errors.Is(err, ErrNotebookShapeUnknown) {
		t.Fatalf("library: want ErrNotebookShapeUnknown, got %v", err)
	}
}

// ⚠ GUARD, and the load-bearing one. The class name survives in a script string
// while the DOM around it has been renamed — so the raw byte count sees markers
// the structural walk cannot use. That disagreement is unambiguous breakage.
// Mutation: delete the bytes.Count cross-check → a DOM rename becomes a clean,
// silent, entirely plausible zero.
func TestParseNotebookMarkersPresentButExtractionEmptyIsAnError(t *testing.T) {
	moved := []byte(`<html><body>
<div id="kp-notebook-annotations">
  <div id="ANNOT-0001" class="a-row">
    <span id="highlight" class="brand-new-classname-amazon-shipped">Lorem ipsum dolor</span>
  </div>
</div>
<script>var legacy = "kp-notebook-highlight";</script>
</body></html>`)

	anns, _, _, err := parseNotebookPage(moved)
	if !errors.Is(err, ErrNotebookShapeUnknown) {
		t.Fatalf("markers present + nothing parsed must be an error, got %d annotations and err=%v", len(anns), err)
	}

	movedLib := []byte(`<html><body><div id="kp-notebook-library">
  <div class="brand-new-book-row">Lorem</div>
</div><script>var legacy = "kp-notebook-library-each-book";</script></body></html>`)
	if _, err := parseNotebookLibrary(movedLib); !errors.Is(err, ErrNotebookShapeUnknown) {
		t.Fatalf("library: want ErrNotebookShapeUnknown, got %v", err)
	}
}

// ⚠ THE CONTROL. Without this, the two guards above would be satisfied by a
// parser that simply errors on everything — which would be just as broken, in
// the opposite direction. A book the user owns but never highlighted must be a
// clean, silent success.
func TestParseNotebookGenuinelyEmptyBookIsNotAnError(t *testing.T) {
	empty := []byte(`<html><body><div id="kp-notebook-annotations"></div></body></html>`)
	anns, next, _, err := parseNotebookPage(empty)
	if err != nil {
		t.Fatalf("a book with no annotations must not be an error: %v", err)
	}
	if len(anns) != 0 {
		t.Fatalf("got %d annotations, want 0", len(anns))
	}
	if next != "" {
		t.Fatalf("next token = %q, want empty", next)
	}
}

// A page that is neither a bounce nor the notebook — no container, no markers —
// is not an empty book. An empty book still ships the container.
func TestParseNotebookUnrecognisedPageIsAnError(t *testing.T) {
	if _, _, _, err := parseNotebookPage([]byte(`<html><body><h1>Something else entirely</h1></body></html>`)); !errors.Is(err, ErrNotebookShapeUnknown) {
		t.Fatalf("want ErrNotebookShapeUnknown, got %v", err)
	}
}

// Pagination: a heavily-highlighted book truncates silently if the token is
// ignored, and no count-based check can catch a PARTIAL result.
func TestParseNotebookPageExtractsPaginationToken(t *testing.T) {
	paged := []byte(`<html><body><div id="kp-notebook-annotations">
  <div id="ANNOT-0001" class="a-row">
    <span id="highlight" class="kp-notebook-highlight">Lorem ipsum</span>
  </div>
</div>
<input type="hidden" id="kp-notebook-annotations-next-page-start" value="NEXTTOKEN123">
<input type="hidden" id="kp-notebook-content-limit-state" value="LIMITSTATE">
</body></html>`)

	anns, next, limit, err := parseNotebookPage(paged)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(anns) != 1 {
		t.Fatalf("got %d annotations, want 1", len(anns))
	}
	if next != "NEXTTOKEN123" {
		t.Fatalf("next token = %q", next)
	}
	if limit != "LIMITSTATE" {
		t.Fatalf("content limit state = %q", limit)
	}
}
