package hardcover

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// trailingBracketRe matches a trailing "(...)" or "[...]" group — the series /
// edition markers Amazon appends to titles ("(Rho Agenda Assimilation Book 2)",
// "(The Kurtherian Endgame Book 7)", "[Unabridged]") that Hardcover's canonical
// title omits. Left in, they dilute the token-overlap score below the match floor
// (e.g. "The Altreian Enigma (Rho Agenda Assimilation Book 2)" vs "The Altreian
// Enigma" = 3/8 = 0.375 < 0.6), so a real book reads as "not matched".
var trailingBracketRe = regexp.MustCompile(`\s*[\(\[][^\)\]]*[\)\]]\s*$`)

// cleanTitleForMatch strips trailing bracket/paren groups (repeatedly — some
// titles carry two) for the FUZZY match path. It never empties the title: if a
// strip would leave nothing (the title was all-parenthetical), the last non-empty
// form is kept. Used for both the search query and the local similarity score.
func cleanTitleForMatch(title string) string {
	t := strings.TrimSpace(title)
	for {
		stripped := strings.TrimSpace(trailingBracketRe.ReplaceAllString(t, ""))
		if stripped == t || stripped == "" {
			break
		}
		t = stripped
	}
	return t
}

// MatchMethod records HOW a reading item was resolved to a Hardcover book, for
// the match-cache column + the admin diagnostics review list. Order of the
// ladder: asin → isbn13 → search → nomatch.
type MatchMethod string

const (
	MatchByASIN   MatchMethod = "asin"
	MatchByISBN13 MatchMethod = "isbn13"
	MatchBySearch MatchMethod = "search"
	// MatchByShelf resolves a row by scoring it against the user's OWN mirrored
	// Hardcover shelf (migration 00074) — a LOCAL, zero-API rung that catches books
	// the user shelved on Hardcover but that don't share an ASIN/ISBN with our
	// edition. Higher-precision than MatchBySearch (a small curated pool + a strict
	// runner-up margin), so unlike fuzzy it IS promoted to the global cache.
	MatchByShelf MatchMethod = "shelf"
	MatchNone    MatchMethod = "nomatch"
)

// MatchInput is the identity a reading item arrives with. Any subset may be
// empty; the ladder skips empty rungs. ASIN covers BOTH an Audible ASIN and a
// Kindle/print amazon_asin — the caller passes whichever it has.
type MatchInput struct {
	ASIN   string
	ISBN13 string
	Title  string
	Author string
}

// MatchResult is the resolved Hardcover identity to cache on the row. On a miss,
// Method is MatchNone and the IDs are zero — the caller records that and NEVER
// guess-pushes.
type MatchResult struct {
	BookID     int64
	EditionID  int64
	Slug       string // the book's Hardcover slug (the /books/<slug> path segment); "" when unknown
	Method     MatchMethod
	Confidence float64 // 1.0 for exact-id hits; a 0..1 score for fuzzy search
}

// hcEdition is the edition shape the ladder selects on. Book.Slug carries the
// book's Hardcover slug — the /books/<slug> path segment the deep-link needs
// (the numeric book id 404s on Hardcover's book pages, only the slug resolves).
// Asin/Isbn13 are the identity fields the BATCH rung (editionsByField) reads
// back so a single `_in` response can be mapped to the input row that asked for
// it (the single-lookup path filters on a known value, so it doesn't need them).
type hcEdition struct {
	ID              int64  `json:"id"`
	BookID          int64  `json:"book_id"`
	ReadingFormatID int64  `json:"reading_format_id"`
	Asin            string `json:"asin"`
	Isbn13          string `json:"isbn_13"`
	Book            struct {
		Slug string `json:"slug"`
	} `json:"book"`
}

// keyFor returns the edition's value for the batch field (asin | isbn_13) — the
// key a batch result is indexed under so the sweep can map it back to the input.
func (e hcEdition) keyFor(field string) string {
	switch field {
	case "asin":
		return e.Asin
	case "isbn_13":
		return e.Isbn13
	}
	return ""
}

// Match walks the ladder and STOPS at the first confident hit, returning the
// book+edition to cache. It is throttle-friendly by construction — each rung is
// at most one GraphQL call and later rungs run only when earlier ones miss.
func (c *Client) Match(ctx context.Context, in MatchInput) (MatchResult, error) {
	// 1 + 2) Exact ASIN (Audible audio OR Kindle/print ebook — same edition arg).
	if asin := strings.TrimSpace(in.ASIN); asin != "" {
		ed, ok, err := c.editionByField(ctx, "asin", asin)
		if err != nil {
			return MatchResult{}, err
		}
		if ok {
			return MatchResult{BookID: ed.BookID, EditionID: ed.ID, Slug: ed.Book.Slug, Method: MatchByASIN, Confidence: 1}, nil
		}
	}

	// 3) Exact ISBN-13.
	if isbn := normalizeISBN(in.ISBN13); isbn != "" {
		ed, ok, err := c.editionByField(ctx, "isbn_13", isbn)
		if err != nil {
			return MatchResult{}, err
		}
		if ok {
			return MatchResult{BookID: ed.BookID, EditionID: ed.ID, Slug: ed.Book.Slug, Method: MatchByISBN13, Confidence: 1}, nil
		}
	}

	// 4) Fuzzy fallback via Typesense search (server-side _like/regex disabled).
	if title := strings.TrimSpace(in.Title); title != "" {
		res, err := c.searchMatch(ctx, in)
		if err != nil {
			return MatchResult{}, err
		}
		if res.Method == MatchBySearch {
			return res, nil
		}
	}

	// 5) No confident match — leave it for the manual-review list.
	return MatchResult{Method: MatchNone}, nil
}

// editionByField resolves a single edition by an exact-equality field on the
// editions table (asin / isbn_13). Returns the first match. Field is a
// compile-time constant from the ladder, never user input.
func (c *Client) editionByField(ctx context.Context, field, value string) (hcEdition, bool, error) {
	q := `query EditionByField($v: String!) {
  editions(where: {` + field + `: {_eq: $v}}, limit: 1) {
    id
    book_id
    reading_format_id
    book { slug }
  }
}`
	var data struct {
		Editions []hcEdition `json:"editions"`
	}
	if err := c.graphql(ctx, q, map[string]any{"v": value}, &data); err != nil {
		return hcEdition{}, false, err
	}
	if len(data.Editions) == 0 {
		return hcEdition{}, false, nil
	}
	return data.Editions[0], true, nil
}

// editionsBatchChunk caps how many identity values ride in one `_in` query. It
// stays well under Hasura's argument limits while collapsing thousands of exact-id
// lookups into a handful of requests — the whole point of the batch rung. The
// per-request rate limit (client.go) is untouched: each chunk is one request.
const editionsBatchChunk = 100

// editionsByField resolves MANY editions in one shot by an exact-equality field
// (asin | isbn_13) using Hasura's `_in`. It de-dupes + trims the inputs, splits
// them into chunks of editionsBatchChunk, and returns a map keyed by the MATCHED
// field value (asin/isbn_13) so the caller maps each hit back to the row that
// asked for it — the batch counterpart of editionByField. First edition wins per
// key. field is a compile-time constant from the ladder (asin|isbn_13), NEVER
// user input, so string-splicing it into the query text is safe. Honors ctx
// cancellation between chunks and surfaces ErrBadToken/ErrRateLimited unchanged.
func (c *Client) editionsByField(ctx context.Context, field string, values []string) (map[string]hcEdition, error) {
	out := make(map[string]hcEdition)
	seen := make(map[string]struct{}, len(values))
	uniq := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		uniq = append(uniq, v)
	}
	if len(uniq) == 0 {
		return out, nil
	}

	q := `query EditionsByField($vs: [String!]!) {
  editions(where: {` + field + `: {_in: $vs}}) {
    id
    book_id
    reading_format_id
    asin
    isbn_13
    book { slug }
  }
}`
	for start := 0; start < len(uniq); start += editionsBatchChunk {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		end := start + editionsBatchChunk
		if end > len(uniq) {
			end = len(uniq)
		}
		var data struct {
			Editions []hcEdition `json:"editions"`
		}
		if err := c.graphql(ctx, q, map[string]any{"vs": uniq[start:end]}, &data); err != nil {
			return out, err
		}
		for _, ed := range data.Editions {
			key := ed.keyFor(field)
			if key == "" {
				continue
			}
			if _, exists := out[key]; exists {
				continue // first edition per key
			}
			out[key] = ed
		}
	}
	return out, nil
}

// editionsForBook lists a book's editions so the search path can pick one.
func (c *Client) editionsForBook(ctx context.Context, bookID int64) ([]hcEdition, error) {
	const q = `query EditionsForBook($id: Int!) {
  editions(where: {book_id: {_eq: $id}}, limit: 20) {
    id
    book_id
    reading_format_id
    book { slug }
  }
}`
	var data struct {
		Editions []hcEdition `json:"editions"`
	}
	if err := c.graphql(ctx, q, map[string]any{"id": bookID}, &data); err != nil {
		return nil, err
	}
	return data.Editions, nil
}

// searchCandidate is one scored Typesense hit.
type searchCandidate struct {
	BookID  int64
	Title   string
	Authors []string
	Score   float64
}

// searchMatch runs the fuzzy fallback: Typesense `search`, scores candidates
// locally against the input title+author, and if the best clears the confidence
// floor picks an edition for it. Below the floor it returns MatchNone so the
// caller never guess-pushes.
func (c *Client) searchMatch(ctx context.Context, in MatchInput) (MatchResult, error) {
	// Strip the "(Series … Book N)" cruft for BOTH the query and the local score
	// (in is by value — this local edit doesn't leak to the caller). Hardcover's
	// fuzzy search tolerates the noise, but the score floor rejects the diluted
	// token overlap, so a real book stays unmatched without this.
	in.Title = cleanTitleForMatch(in.Title)
	query := strings.TrimSpace(in.Title)
	if a := strings.TrimSpace(in.Author); a != "" {
		query += " " + a
	}
	const q = `query Search($query: String!) {
  search(query: $query, query_type: "Book", per_page: 5, page: 1) {
    results
  }
}`
	var data struct {
		Search struct {
			Results json.RawMessage `json:"results"`
		} `json:"search"`
	}
	if err := c.graphql(ctx, q, map[string]any{"query": query}, &data); err != nil {
		return MatchResult{}, err
	}

	cands := parseSearchResults(data.Search.Results)
	if len(cands) == 0 {
		return MatchResult{Method: MatchNone}, nil
	}
	for i := range cands {
		cands[i].Score = scoreCandidate(in, cands[i])
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Score > cands[j].Score })

	best := cands[0]
	// Confidence floor: below this we treat it as no-match rather than risk a
	// wrong push. Title-token overlap of ~0.6 with an author corroboration is
	// the intended bar.
	const floor = 0.6
	if best.Score < floor || best.BookID == 0 {
		return MatchResult{Method: MatchNone}, nil
	}

	editionID, slug, err := c.pickEdition(ctx, best.BookID)
	if err != nil {
		return MatchResult{}, err
	}
	return MatchResult{
		BookID:     best.BookID,
		EditionID:  editionID,
		Slug:       slug,
		Method:     MatchBySearch,
		Confidence: best.Score,
	}, nil
}

// pickEdition chooses an edition for a book found via search. It prefers the
// book's default edition ordering (first returned) and returns 0 when the book
// has no listed editions (a status-only push is still possible off book_id). It
// also returns the book's slug (all editions of a book carry the same
// book.slug), so a search-path match still yields the deep-link slug.
func (c *Client) pickEdition(ctx context.Context, bookID int64) (int64, string, error) {
	eds, err := c.editionsForBook(ctx, bookID)
	if err != nil {
		return 0, "", err
	}
	if len(eds) == 0 {
		return 0, "", nil
	}
	return eds[0].ID, eds[0].Book.Slug, nil
}

// parseSearchResults extracts candidates from a Typesense response JSON. The
// shape is defensive: hits[].document with id + title + author_names (any of
// which may be absent on a beta payload).
func parseSearchResults(raw json.RawMessage) []searchCandidate {
	if len(raw) == 0 {
		return nil
	}
	var ts struct {
		Hits []struct {
			Document struct {
				ID          json.Number `json:"id"`
				Title       string      `json:"title"`
				AuthorNames []string    `json:"author_names"`
			} `json:"document"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &ts); err != nil {
		return nil
	}
	out := make([]searchCandidate, 0, len(ts.Hits))
	for _, h := range ts.Hits {
		id, _ := h.Document.ID.Int64()
		if id == 0 {
			continue
		}
		out = append(out, searchCandidate{
			BookID:  id,
			Title:   h.Document.Title,
			Authors: h.Document.AuthorNames,
		})
	}
	return out
}

// scoreCandidate is a cheap local relevance score in [0,1]: title-token Jaccard
// plus a small author-match bonus. It exists to keep a fuzzy Typesense hit from
// being pushed on rank alone — a wrong push is worse than a miss.
func scoreCandidate(in MatchInput, c searchCandidate) float64 {
	titleScore := tokenJaccard(in.Title, c.Title)
	authorBonus := 0.0
	if a := strings.TrimSpace(in.Author); a != "" {
		for _, cand := range c.Authors {
			if tokenJaccard(a, cand) >= 0.5 {
				authorBonus = 0.15
				break
			}
		}
	}
	score := titleScore + authorBonus
	if score > 1 {
		score = 1
	}
	return score
}

// tokenJaccard is the Jaccard similarity of the lowercased word sets of a and b.
func tokenJaccard(a, b string) float64 {
	as := tokenSet(a)
	bs := tokenSet(b)
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	inter := 0
	for t := range as {
		if _, ok := bs[t]; ok {
			inter++
		}
	}
	union := len(as) + len(bs) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func tokenSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if f != "" {
			out[f] = struct{}{}
		}
	}
	return out
}

// normalizeISBN strips hyphens/spaces from an ISBN-13 so the exact-equality
// lookup matches Hardcover's stored digits.
func normalizeISBN(isbn string) string {
	isbn = strings.TrimSpace(isbn)
	if isbn == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range isbn {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
