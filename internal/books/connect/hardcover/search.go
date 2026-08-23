package hardcover

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// search.go — the interactive Hardcover SEARCH surface behind the manual
// match-fixer UI. Distinct from match.go's automated ladder: here a human types
// a query, we run Hardcover's Typesense search, and return DESCRIPTIVE candidate
// cards (cover, author, year) for the user to pick from. Read-only — the `search`
// query passes the dry-run gate cleanly. The chosen candidate is then written as
// a manual reading_items linkage (SetReadingItemHardcoverLink, confidence
// "manual") by the identity handler.

// Candidate is one Hardcover search hit rendered as a pick-able card. All fields
// beyond BookID/Title are best-effort (a sparse Typesense document leaves them
// empty) — the card degrades gracefully.
type Candidate struct {
	BookID   int64    `json:"bookId"`
	Title    string   `json:"title"`
	Authors  []string `json:"authors"`
	CoverURL string   `json:"coverUrl"`
	Year     int      `json:"year"`
	Slug     string   `json:"slug"`
}

// SearchCandidates runs Hardcover's Typesense `search` for a free-text query and
// returns up to `limit` descriptive candidates, newest-relevance-first (Typesense
// order preserved — no local re-scoring, since the human is the judge here). It is
// READ-ONLY. A blank query returns nil without a call.
func (c *Client) SearchCandidates(ctx context.Context, query string, limit int) ([]Candidate, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	// per_page MUST be an inline literal, NOT a GraphQL variable: Hardcover's
	// server-side Typesense call silently returns `results: null` (no GraphQL error,
	// HTTP 200) when per_page arrives as a bound $variable — so the proven automated
	// query (match.go searchMatch) uses a literal, and so must this. limit is a
	// bounded int (1..20, clamped above), so inlining it is injection-safe. Only the
	// free-text query stays a $variable (it must be parameterized).
	q := fmt.Sprintf(`query Search($query: String!) {
  search(query: $query, query_type: "Book", per_page: %d, page: 1) {
    results
  }
}`, limit)
	var data struct {
		Search struct {
			Results json.RawMessage `json:"results"`
		} `json:"search"`
	}
	if err := c.graphql(ctx, q, map[string]any{"query": query}, &data); err != nil {
		return nil, err
	}
	return parseSearchCandidates(data.Search.Results, limit), nil
}

// parseSearchCandidates extracts descriptive candidates from a Typesense response.
// Defensive: every field beyond id is optional and parsed only if present, so a
// beta/changed payload never errors — it just yields sparser cards. Cover + year
// have several plausible field names on Hardcover's document; we try each.
func parseSearchCandidates(raw json.RawMessage, limit int) []Candidate {
	if len(raw) == 0 {
		return nil
	}
	var ts struct {
		Hits []struct {
			Document struct {
				ID          json.Number `json:"id"`
				Title       string      `json:"title"`
				AuthorNames []string    `json:"author_names"`
				Slug        string      `json:"slug"`
				// image is an OBJECT ({url,color,...}), NOT a string — declaring it
				// `string` made json.Unmarshal fail the WHOLE hits array → 0 results
				// (boom-nq2m). Verified shape 2026-08. Empty object → URL "".
				Image struct {
					URL string `json:"url"`
				} `json:"image"`
				ReleaseYear json.Number `json:"release_year"`
				ReleaseDate string      `json:"release_date"`
			} `json:"document"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &ts); err != nil {
		return nil
	}
	out := make([]Candidate, 0, len(ts.Hits))
	for _, h := range ts.Hits {
		d := h.Document
		id, _ := d.ID.Int64()
		if id == 0 {
			continue
		}
		cand := Candidate{
			BookID:   id,
			Title:    d.Title,
			Authors:  d.AuthorNames,
			CoverURL: d.Image.URL,
			Slug:     d.Slug,
		}
		if y, err := d.ReleaseYear.Int64(); err == nil && y > 0 {
			cand.Year = int(y)
		} else if len(d.ReleaseDate) >= 4 {
			if y, err := strconv.Atoi(d.ReleaseDate[:4]); err == nil {
				cand.Year = y
			}
		}
		out = append(out, cand)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// SearchForOwner runs an interactive Hardcover search on behalf of a connected
// user. Returns (nil, false, nil) when the user has not connected Hardcover — the
// caller renders that as "connect Hardcover first", not an error.
func (s *SyncService) SearchForOwner(ctx context.Context, owner, query string, limit int) ([]Candidate, bool, error) {
	if s.Store == nil {
		return nil, false, nil
	}
	client, ok, err := s.Store.ClientForUser(ctx, owner)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	cands, err := client.SearchCandidates(ctx, query, limit)
	if err != nil {
		return nil, true, err
	}
	return cands, true, nil
}

// ResolveEditionForBook returns the first edition id + slug for a Hardcover book,
// used when applying a MANUAL match so the stored linkage carries an edition +
// the deep-link slug (mirrors the automated path's pickEdition). Best-effort: a
// book with no listed editions yields (0, "", nil) — a status push still works
// off book_id alone.
func (s *SyncService) ResolveEditionForBook(ctx context.Context, owner string, bookID int64) (int64, string, bool, error) {
	if s.Store == nil {
		return 0, "", false, nil
	}
	client, ok, err := s.Store.ClientForUser(ctx, owner)
	if err != nil {
		return 0, "", false, err
	}
	if !ok {
		return 0, "", false, nil
	}
	editionID, slug, err := client.pickEdition(ctx, bookID)
	return editionID, slug, true, err
}
