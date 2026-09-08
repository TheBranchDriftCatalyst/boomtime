// notebook.go — the KINDLE HIGHLIGHT wire surface (boom-siwi.5).
//
// boom-siwi.1 established that Kindle annotations are NOT in the Fiona CDE
// sidecar (type=EBOK carries only kindle.lpr) and NOT in whispersync (whose
// non-shelf namespaces are device settings and Vocabulary Builder decks). They
// live at read.amazon.com/notebook, behind the same website-cookie transport
// readamazon.go already uses for the Cloud Reader library:
//
//	library of annotated books  host read.amazon.com
//	  GET /notebook
//	  -> HTML; one row per book that HAS annotations. This is the index — the
//	     set of books to fetch, and it is far smaller than the library (14 of
//	     2512 on the probe account).
//	per-book annotations        host read.amazon.com
//	  GET /notebook?asin=<ASIN>&contentLimitState=&
//	  -> HTML; the highlights + notes for one book.
//
// It is HTML, not JSON, and that is the whole design problem in this file.
//
// THE FAILURE MODE THIS FILE EXISTS TO PREVENT. A scraper that stops
// understanding the page returns zero results — which is byte-for-byte
// indistinguishable from a book the user never highlighted. Report the second as
// the first and the ingest silently reports a healthy sync of nothing, the
// reconcile retires the whole corpus, and no error is ever logged. So the
// parsers here distinguish FOUR outcomes, not two:
//
//	book genuinely has no annotations   -> (nil, nil)          success, zero rows
//	sign-in bounce (HTTP 200 + login)   -> ErrNotebookShapeUnknown
//	container absent / DOM moved        -> ErrNotebookShapeUnknown
//	markers present, extracted zero     -> ErrNotebookShapeUnknown
//
// That last case is the load-bearing one, and it is checked with a signal
// COMPLETELY INDEPENDENT of the structural walk: a raw byte count of the class
// name. "The marker appears 14 times and the walker understood 0 of them" cannot
// be anything but parser breakage. Five lines, and it is what stands between a
// DOM rename and a silently-emptied corpus.
//
// The parse functions are pure (bytes -> typed) so they unit-test against
// hand-written fixtures with no network. Fixtures are INLINE consts with lorem
// text, never captured pages: notebook HTML contains the user's own highlight
// prose and this repository is public.

package amazon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// ErrNotebookShapeUnknown means the notebook HTML did not carry the structure
// this parser was built against — a sign-in bounce, a DOM rename, or an
// extraction that found the markers but understood none of them.
//
// It is NEVER returned for a book that simply has no annotations: that case is
// (nil, nil). The distinction is the entire point — see the file header.
var ErrNotebookShapeUnknown = errors.New("amazon notebook: unrecognised page shape")

// Notebook DOM markers. These are matched as CLASS SUBSTRINGS (Amazon composes
// long class lists) and counted as RAW BYTES for the independent cross-check.
const (
	markerLibraryBook = "kp-notebook-library-each-book"
	markerHighlight   = "kp-notebook-highlight"
	markerNote        = "kp-notebook-note"
	markerMetadata    = "kp-notebook-metadata"

	// idAnnotationLocation is the hidden input carrying the annotation's numeric
	// position. Preferred over scraping the metadata prose, which is localised.
	idAnnotationLocation = "kp-annotation-location"
	// idNextPageStart is the pagination token for a book with more annotations
	// than one page holds.
	idNextPageStart = "kp-notebook-annotations-next-page-start"
	// idContentLimitState rides along with the pagination token.
	idContentLimitState = "kp-notebook-content-limit-state"
)

// notebookMaxPages bounds per-book pagination so a malformed or looping token
// can never spin forever — the same posture as cloudLibraryMaxPages. A book with
// more than this many pages of highlights is not a case we need to serve
// perfectly; it is a case we need to not hang on.
const notebookMaxPages = 50

// NotebookBook is one row of the notebook library index: a book that HAS
// annotations. Title/Authors are best-effort — the ASIN is what matters, since
// reading_items already carries the metadata.
type NotebookBook struct {
	ASIN     string
	Title    string
	Authors  string
	CoverURL string
}

// KindleAnnotation is one highlight or note on one book.
type KindleAnnotation struct {
	ASIN         string
	AnnotationID string // the DOM id, kept for raw_meta — NOT used as the storage key
	Kind         string // "highlight" | "note"
	Body         string // the highlighted passage (empty for a standalone note)
	Note         string // the user's own margin note
	Color        string // highlight colour when the metadata prose names one
	MetadataRaw  string // e.g. "Yellow highlight | Location: 1,234" — kept for raw_meta
	Position     int64
	PositionUnit string // "location" | "page"
}

// ---------------------------------------------------------------------------
// Transport (cookie, per readamazon.go)
// ---------------------------------------------------------------------------

// ListAnnotatedBooks GETs the notebook library index and returns the books that
// carry annotations. This is the ingest's work-list: fetching it is one request,
// and it is far smaller than the library.
func ListAnnotatedBooks(ctx context.Context, cookies map[string]string) ([]NotebookBook, error) {
	body, status, err := cookieGet(ctx, cookies, CloudReaderHost, "/notebook")
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("amazon notebook library: HTTP %d: %s", status, kindleSnippet(body))
	}
	return parseNotebookLibrary(body)
}

// FetchBookAnnotations GETs one book's annotations, following the pagination
// token so a heavily-highlighted book is not silently truncated.
func FetchBookAnnotations(ctx context.Context, cookies map[string]string, asin string) ([]KindleAnnotation, error) {
	asin = strings.TrimSpace(asin)
	if asin == "" {
		return nil, nil
	}
	var (
		out       []KindleAnnotation
		token     string
		limitStat string
	)
	for page := 0; page < notebookMaxPages; page++ {
		path := "/notebook?asin=" + asin + "&contentLimitState=" + limitStat + "&"
		if token != "" {
			path += "token=" + token
		}
		body, status, err := cookieGet(ctx, cookies, CloudReaderHost, path)
		if err != nil {
			return nil, err
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("amazon notebook %q: HTTP %d: %s", asin, status, kindleSnippet(body))
		}
		anns, nextToken, nextLimit, perr := parseNotebookPage(body)
		if perr != nil {
			return nil, perr
		}
		for i := range anns {
			anns[i].ASIN = asin
		}
		out = append(out, anns...)
		if nextToken == "" || nextToken == token {
			return out, nil
		}
		token, limitStat = nextToken, nextLimit
	}
	// Mirrors cloudLibraryMaxPages: a token that never terminates is a bug on
	// one side or the other, and silently returning a partial corpus is worse
	// than saying so.
	return out, fmt.Errorf("amazon notebook %q: pagination exceeded %d pages", asin, notebookMaxPages)
}

// ---------------------------------------------------------------------------
// Pure parsers (unit-tested against inline fixtures)
// ---------------------------------------------------------------------------

// parseNotebookLibrary extracts the annotated-book index. An empty index is a
// legitimate answer (an account that has never highlighted anything), so it
// returns (nil, nil) — but a page whose book markers are present and
// unparseable, or which is a sign-in bounce, is an error.
func parseNotebookLibrary(body []byte) ([]NotebookBook, error) {
	if isSignInBounce(body) {
		return nil, fmt.Errorf("%w: the response is a sign-in page, so the website cookies do not authenticate the notebook", ErrNotebookShapeUnknown)
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotebookShapeUnknown, err)
	}

	var out []NotebookBook
	seen := map[string]struct{}{}
	forEachElement(doc, func(n *html.Node) {
		if !hasClassSubstring(n, markerLibraryBook) {
			return
		}
		asin := strings.TrimSpace(attr(n, "id"))
		if asin == "" {
			return
		}
		if _, dup := seen[asin]; dup {
			return
		}
		seen[asin] = struct{}{}
		out = append(out, NotebookBook{
			ASIN:     asin,
			Title:    firstElementText(n, "h2"),
			Authors:  strings.TrimPrefix(firstElementText(n, "p"), "By "),
			CoverURL: firstImgSrc(n),
		})
	})

	// The independent cross-check. A raw byte count cannot be fooled by the same
	// structural assumption the walk just made.
	if raw := bytes.Count(body, []byte(markerLibraryBook)); raw > 0 && len(out) == 0 {
		return nil, fmt.Errorf("%w: the library marker appears %d times but no book row parsed — the DOM moved", ErrNotebookShapeUnknown, raw)
	}
	return out, nil
}

// parseNotebookPage extracts one book's annotations plus the pagination token.
//
// Returns (nil, "", "", nil) for a book that genuinely has none. Returns
// ErrNotebookShapeUnknown when the page is a sign-in bounce, when the
// annotations container is missing entirely, or when the highlight markers are
// present but nothing parsed out of them.
func parseNotebookPage(body []byte) ([]KindleAnnotation, string, string, error) {
	if isSignInBounce(body) {
		return nil, "", "", fmt.Errorf("%w: the response is a sign-in page, so the website cookies do not authenticate the notebook", ErrNotebookShapeUnknown)
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, "", "", fmt.Errorf("%w: %v", ErrNotebookShapeUnknown, err)
	}

	rawHighlights := bytes.Count(body, []byte(markerHighlight))
	rawNotes := bytes.Count(body, []byte(markerNote))

	// A page with neither the annotations container nor any marker is not an
	// empty book — it is a page we do not recognise. An empty book still ships
	// the container.
	if rawHighlights == 0 && rawNotes == 0 && !bytes.Contains(body, []byte("kp-notebook-annotations")) {
		return nil, "", "", fmt.Errorf("%w: no annotations container and no markers on the page", ErrNotebookShapeUnknown)
	}

	var out []KindleAnnotation
	forEachElement(doc, func(n *html.Node) {
		switch {
		case hasClassSubstring(n, markerHighlight):
			out = append(out, annotationFrom(n, AnnotationKindHighlightStr))
		case hasClassSubstring(n, markerNote) && noteIsStandalone(n):
			// A standalone note — a note the user attached without highlighting.
			// Notes nested inside a highlight block are folded into that
			// highlight by annotationFrom instead of becoming their own row.
			out = append(out, annotationFrom(n, AnnotationKindNoteStr))
		}
	})

	// The cross-check, again independent of the walk above.
	if rawHighlights > 0 && len(out) == 0 {
		return nil, "", "", fmt.Errorf("%w: the highlight marker appears %d times but no annotation parsed — the DOM moved", ErrNotebookShapeUnknown, rawHighlights)
	}
	return out, inputValueByID(doc, idNextPageStart), inputValueByID(doc, idContentLimitState), nil
}

// Kind strings, mirrored from the db package rather than imported: connect/amazon
// is a wire package and must not depend on storage.
const (
	AnnotationKindHighlightStr = "highlight"
	AnnotationKindNoteStr      = "note"
)

// annotationFrom builds one annotation from the node carrying the marker class,
// reading the rest out of its enclosing annotation block. Walking UP to the
// block and back down makes the parser independent of attribute order and of the
// exact nesting depth, both of which Amazon has changed before.
func annotationFrom(marker *html.Node, kind string) KindleAnnotation {
	block := annotationBlock(marker)
	a := KindleAnnotation{
		AnnotationID: strings.TrimSpace(attr(block, "id")),
		Kind:         kind,
		MetadataRaw:  firstTextByClass(block, markerMetadata),
	}
	if kind == AnnotationKindHighlightStr {
		a.Body = nodeText(marker)
		a.Note = firstTextByClass(block, markerNote)
	} else {
		a.Note = nodeText(marker)
	}
	// Prefer the hidden input: it is a bare integer, whereas the metadata prose
	// is localised and formats thousands separators.
	if v := inputValueByID(block, idAnnotationLocation); v != "" {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			a.Position = n
		}
	}
	a.Color, a.PositionUnit, a.Position = interpretMetadata(a.MetadataRaw, a.Position)
	return a
}

// metadataRe pulls the colour and the position out of the metadata prose, e.g.
// "Yellow highlight | Location: 1,234" or "Blue highlight | Page: 12".
var metadataRe = regexp.MustCompile(`(?i)^\s*(\w+)?\s*highlight\s*\|\s*(location|page)\s*:\s*([\d,]+)`)

// interpretMetadata derives colour + position unit from the metadata prose,
// keeping a position already read from the hidden input when the prose has none.
//
// The UNIT is the point. Kindle is not one unit: a reflowable book reports a
// location and a print replica reports a page, so it cannot be inferred from
// source='kindle'. An unrecognised shape falls back to "location", which is what
// the overwhelming majority of Kindle books report — and being explicit about it
// means a wrong guess is one UPDATE away from fixed.
func interpretMetadata(meta string, fallbackPos int64) (color, unit string, pos int64) {
	unit, pos = PositionUnitLocationStr, fallbackPos
	m := metadataRe.FindStringSubmatch(meta)
	if m == nil {
		return "", unit, pos
	}
	color = strings.ToLower(strings.TrimSpace(m[1]))
	if strings.EqualFold(m[2], "page") {
		unit = PositionUnitPageStr
	}
	if pos == 0 {
		if n, err := strconv.ParseInt(strings.ReplaceAll(m[3], ",", ""), 10, 64); err == nil {
			pos = n
		}
	}
	return color, unit, pos
}

// Position units, mirrored from the db package for the same reason the kind
// strings are.
const (
	PositionUnitLocationStr = "location"
	PositionUnitPageStr     = "page"
)

// ---------------------------------------------------------------------------
// Small DOM helpers
// ---------------------------------------------------------------------------

// isSignInBounce detects the HTTP-200 login page. Amazon answers an
// unauthenticated notebook request with a 200 and a sign-in form, so status
// alone proves nothing.
func isSignInBounce(body []byte) bool {
	if bytes.Contains(body, []byte(markerHighlight)) || bytes.Contains(body, []byte(markerLibraryBook)) {
		return false
	}
	return bytes.Contains(body, []byte("ap/signin")) || bytes.Contains(body, []byte("auth-signin-form"))
}

func forEachElement(n *html.Node, fn func(*html.Node)) {
	if n.Type == html.ElementNode {
		fn(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		forEachElement(c, fn)
	}
}

func attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// hasClassSubstring matches a class SUBSTRING because Amazon composes long
// class lists ("a-size-base-plus a-color-base kp-notebook-highlight ...") and
// exact-token matching would break on any reordering or suffixing.
func hasClassSubstring(n *html.Node, want string) bool {
	return strings.Contains(attr(n, "class"), want)
}

// annotationBlock walks up to the nearest ancestor carrying an id — the
// per-annotation container.
//
// The walk starts at the PARENT, not at the marker. Amazon puts an id on the
// marker span itself (id="highlight"), so starting at the node would return the
// span and every sibling lookup — the note, the metadata, the location input —
// would come back empty while looking perfectly healthy.
func annotationBlock(n *html.Node) *html.Node {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && attr(p, "id") != "" && !strings.EqualFold(p.Data, "body") {
			return p
		}
	}
	if n.Parent != nil {
		return n.Parent
	}
	return n
}

// noteIsStandalone reports whether a note is the annotation in its own right,
// rather than the margin note attached to a highlight.
//
// The test is whether the note's BLOCK also holds a highlight, not whether the
// note is nested inside one: in the real DOM the two are SIBLINGS within the
// annotation container. Checking for ancestry instead would emit every
// highlight's note a second time as its own row.
func noteIsStandalone(n *html.Node) bool {
	block := annotationBlock(n)
	found := false
	forEachElement(block, func(x *html.Node) {
		if hasClassSubstring(x, markerHighlight) {
			found = true
		}
	})
	return !found
}

func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			sb.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(strings.Join(strings.Fields(sb.String()), " "))
}

func firstTextByClass(root *html.Node, class string) string {
	var found string
	forEachElement(root, func(n *html.Node) {
		if found == "" && hasClassSubstring(n, class) {
			found = nodeText(n)
		}
	})
	return found
}

func firstElementText(root *html.Node, tag string) string {
	var found string
	forEachElement(root, func(n *html.Node) {
		if found == "" && strings.EqualFold(n.Data, tag) {
			found = nodeText(n)
		}
	})
	return found
}

func firstImgSrc(root *html.Node) string {
	var found string
	forEachElement(root, func(n *html.Node) {
		if found == "" && strings.EqualFold(n.Data, "img") {
			found = attr(n, "src")
		}
	})
	return found
}

func inputValueByID(root *html.Node, id string) string {
	var found string
	forEachElement(root, func(n *html.Node) {
		if found == "" && attr(n, "id") == id {
			found = attr(n, "value")
		}
	})
	return found
}
