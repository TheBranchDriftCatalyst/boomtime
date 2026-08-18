package hardcover

import (
	"context"
	"encoding/json"
	"strings"
)

// resolve.go — the KINDLE metadata resolver: an ASIN → title/author/cover
// lookup used to enrich a Kindle reading_item that arrives with little more than
// an Amazon ASIN. Where match.go's editionByField resolves ONLY the ids (for the
// push's book_id/edition_id cache), LookupByASIN pulls the human-readable
// metadata (title, authors, cover, reading format) in one round trip so the
// Books view can render a real row for an otherwise-bare Kindle ASIN.
//
// It is READ-ONLY (an editions query), so it passes the dry-run gate.

// BookMeta is the resolved, display-ready metadata for an ASIN. IDs are int64 to
// match match.go's MatchResult; ReadingFormatID lives on the EDITION (1 physical,
// 2 audio, 4 ebook — the FormatEbook const for Kindle). Any field may be zero if
// Hardcover doesn't have it.
type BookMeta struct {
	BookID          int64
	EditionID       int64
	Title           string
	Slug            string
	Authors         string // contributions[].author.name, comma-joined
	ReadingFormatID int
	CoverURL        string
}

// lookupByASINQuery resolves an edition by exact ASIN and pulls its book's
// display metadata. book.title/slug are VERIFIED; cached_image + contributions
// follow Hardcover's documented schema (cached_image is a JSON blob carrying a
// url; contributions link to author{name}). All are optional in the parse so a
// leaner-than-expected payload still yields a usable BookMeta.
const lookupByASINQuery = `query LookupByASIN($v: String!) {
  editions(where: {asin: {_eq: $v}}, limit: 1) {
    id
    book_id
    reading_format_id
    book {
      title
      slug
      cached_image
      contributions {
        author { name }
      }
    }
  }
}`

// LookupByASIN resolves an ASIN to display metadata, or (nil, nil) when no
// edition carries that ASIN (a clean miss — the caller falls back to its own
// source metadata). ErrBadToken / ErrRateLimited surface as errors.
func (c *Client) LookupByASIN(ctx context.Context, asin string) (*BookMeta, error) {
	asin = strings.TrimSpace(asin)
	if asin == "" {
		return nil, nil
	}
	var data struct {
		Editions json.RawMessage `json:"editions"`
	}
	if err := c.graphql(ctx, lookupByASINQuery, map[string]any{"v": asin}, &data); err != nil {
		return nil, err
	}
	return parseBookMeta(data.Editions), nil
}

// rawEdition mirrors the LookupByASIN wire shape. cached_image is a raw blob
// (string or object) decoded defensively by extractCoverURL.
type rawEdition struct {
	ID              int64 `json:"id"`
	BookID          int64 `json:"book_id"`
	ReadingFormatID int   `json:"reading_format_id"`
	Book            struct {
		Title         string          `json:"title"`
		Slug          string          `json:"slug"`
		CachedImage   json.RawMessage `json:"cached_image"`
		Contributions []struct {
			Author struct {
				Name string `json:"name"`
			} `json:"author"`
		} `json:"contributions"`
	} `json:"book"`
}

// parseBookMeta maps the first edition of an editions() response to a BookMeta,
// or nil when the array is empty. Split out of LookupByASIN so it is unit-testable
// against a representative fixture without a live client.
func parseBookMeta(raw json.RawMessage) *BookMeta {
	if len(raw) == 0 {
		return nil
	}
	var eds []rawEdition
	if err := json.Unmarshal(raw, &eds); err != nil {
		return nil
	}
	if len(eds) == 0 {
		return nil
	}
	e := eds[0]
	authors := make([]string, 0, len(e.Book.Contributions))
	for _, con := range e.Book.Contributions {
		if n := strings.TrimSpace(con.Author.Name); n != "" {
			authors = append(authors, n)
		}
	}
	return &BookMeta{
		BookID:          e.BookID,
		EditionID:       e.ID,
		Title:           e.Book.Title,
		Slug:            e.Book.Slug,
		Authors:         strings.Join(authors, ", "),
		ReadingFormatID: e.ReadingFormatID,
		CoverURL:        extractCoverURL(e.Book.CachedImage),
	}
}

// extractCoverURL pulls a usable image URL out of Hardcover's cached_image,
// which may be a bare URL string or an object with a "url" field. Anything else
// yields "".
func extractCoverURL(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return strings.TrimSpace(obj.URL)
	}
	return ""
}
