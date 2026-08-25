// tags.go — step 5b of liberation: the M4B metadata tags and the cover image.
// See docs/design/catalyst-books-liberation-architecture.md §2.5.
//
// THE POINT OF THIS FILE: no extra Amazon call is needed. Every tag an M4B wants
// — title, subtitle, authors, narrators, series, release date, publisher, genre,
// description — is ALREADY in reading_items.raw_meta from the library sweep
// (internal/books/ingest/audible). Libation re-fetches product metadata at
// liberation time; we do not have to, because the ingest domain already did it
// and persisted the raw item. That is a real simplification, and it is why
// Metadata is built from a stored JSON blob rather than from a network call.
package liberate

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Metadata is everything needed to tag and name one liberated book.
type Metadata struct {
	Title       string
	Subtitle    string
	Authors     []string
	Narrators   []string
	Series      string
	SeriesIndex string
	Publisher   string
	Genre       string
	Description string
	ReleaseDate string // ISO-ish as Audible gives it; Year() extracts the year
	ASIN        string
	RuntimeMin  int
	CoverURL    string
}

// rawItem mirrors the subset of an Audible library item we read back out of
// reading_items.raw_meta. It is intentionally a separate type from
// audible.LibraryItem: this reads a STORED blob that may be months old and
// written by an older version of the ingest mapper, so it tolerates absence
// everywhere and shares no invariants with the live ingest path.
type rawItem struct {
	ASIN      string `json:"asin"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle"`
	Publisher string `json:"publisher_name"`
	Authors   []struct {
		Name string `json:"name"`
	} `json:"authors"`
	Narrators []struct {
		Name string `json:"name"`
	} `json:"narrators"`
	Series []struct {
		Title    string `json:"title"`
		Sequence string `json:"sequence"`
	} `json:"series"`
	RuntimeLengthMin int             `json:"runtime_length_min"`
	ReleaseDate      string          `json:"release_date"`
	IssueDate        string          `json:"issue_date"`
	PublisherSummary string          `json:"publisher_summary"`
	MerchandisingS   string          `json:"merchandising_summary"`
	ProductImages    json.RawMessage `json:"product_images"`
	CategoryLadders  []struct {
		Ladder []struct {
			Name string `json:"name"`
		} `json:"ladder"`
	} `json:"category_ladders"`
}

// MetadataFromRaw builds Metadata from a stored reading_items.raw_meta blob.
// Unparseable or empty input yields a zero Metadata and false — the caller then
// falls back to the row's denormalised title/authors columns rather than
// failing the liberation over a metadata blob.
func MetadataFromRaw(raw []byte) (Metadata, bool) {
	if len(raw) == 0 {
		return Metadata{}, false
	}
	var it rawItem
	if err := json.Unmarshal(raw, &it); err != nil {
		return Metadata{}, false
	}
	m := Metadata{
		Title:       strings.TrimSpace(it.Title),
		Subtitle:    strings.TrimSpace(it.Subtitle),
		Publisher:   strings.TrimSpace(it.Publisher),
		ASIN:        strings.TrimSpace(it.ASIN),
		RuntimeMin:  it.RuntimeLengthMin,
		Description: firstNonBlank(it.PublisherSummary, it.MerchandisingS),
		ReleaseDate: firstNonBlank(it.ReleaseDate, it.IssueDate),
	}
	for _, a := range it.Authors {
		if n := strings.TrimSpace(a.Name); n != "" {
			m.Authors = append(m.Authors, n)
		}
	}
	for _, n := range it.Narrators {
		if v := strings.TrimSpace(n.Name); v != "" {
			m.Narrators = append(m.Narrators, v)
		}
	}
	if len(it.Series) > 0 {
		m.Series = strings.TrimSpace(it.Series[0].Title)
		m.SeriesIndex = normalizeSeriesIndex(it.Series[0].Sequence)
	}
	// Genre: the LAST rung of the first category ladder is the most specific
	// ("Science Fiction & Fantasy > Science Fiction > Space Opera" → Space Opera).
	if len(it.CategoryLadders) > 0 {
		if l := it.CategoryLadders[0].Ladder; len(l) > 0 {
			m.Genre = strings.TrimSpace(l[len(l)-1].Name)
		}
	}
	m.CoverURL = largestCoverURL(it.ProductImages)
	return m, m.Title != ""
}

// Year extracts a 4-digit year from ReleaseDate, or "".
func (m Metadata) Year() string {
	if len(m.ReleaseDate) >= 4 {
		if _, err := strconv.Atoi(m.ReleaseDate[:4]); err == nil {
			return m.ReleaseDate[:4]
		}
	}
	return ""
}

// AuthorString joins authors for tagging and for the path template.
func (m Metadata) AuthorString() string { return strings.Join(m.Authors, ", ") }

// NarratorString joins narrators for tagging.
func (m Metadata) NarratorString() string { return strings.Join(m.Narrators, ", ") }

// BookMeta projects the tag metadata onto the naming-template inputs, so
// template.go and the tagger cannot disagree about what "the author" is.
func (m Metadata) BookMeta() BookMeta {
	return BookMeta{
		Title:       m.Title,
		Subtitle:    m.Subtitle,
		Author:      m.AuthorString(),
		Narrator:    m.NarratorString(),
		Series:      m.Series,
		SeriesIndex: m.SeriesIndex,
		Year:        m.Year(),
		ASIN:        m.ASIN,
	}
}

// FFmpegTagArgs renders the -metadata flags for the remux. Empty values are
// omitted rather than written as empty tags, which some players display as a
// blank field instead of falling back to the filename.
func (m Metadata) FFmpegTagArgs() []string {
	var args []string
	add := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			args = append(args, "-metadata", k+"="+v)
		}
	}
	add("title", m.Title)
	add("subtitle", m.Subtitle)
	add("artist", m.AuthorString())
	add("album_artist", m.AuthorString())
	// album = the series when there is one, else the title. Audiobook players
	// group by album, so a series shelves together this way.
	if m.Series != "" {
		add("album", m.Series)
	} else {
		add("album", m.Title)
	}
	add("composer", m.NarratorString()) // the m4b convention for narrator
	add("genre", m.Genre)
	add("date", m.Year())
	add("publisher", m.Publisher)
	add("comment", m.Description)
	add("description", m.Description)
	if m.SeriesIndex != "" {
		add("track", m.SeriesIndex)
	}
	// Audible's own identifier, so a liberated file can always be traced back.
	add("ASIN", m.ASIN)
	add("media_type", "2") // 2 = audiobook, per the iTunes stik atom
	return args
}

// normalizeSeriesIndex renders a sequence as a zero-padded, sortable string.
// "3" → "03" so that lexical directory ordering matches reading order; a
// non-numeric sequence ("3.5", "Prequel") is passed through untouched.
func normalizeSeriesIndex(seq string) string {
	seq = strings.TrimSpace(seq)
	if seq == "" {
		return ""
	}
	if n, err := strconv.Atoi(seq); err == nil && n >= 0 && n < 100 {
		return strconv.Itoa(n/10) + strconv.Itoa(n%10)
	}
	return seq
}

// largestCoverURL picks the biggest image from Audible's product_images, which
// is a map of pixel-width → URL ({"500": "...", "1024": "..."}).
func largestCoverURL(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var byWidth map[string]string
	if err := json.Unmarshal(raw, &byWidth); err != nil {
		return ""
	}
	best, bestW := "", -1
	for w, u := range byWidth {
		n, err := strconv.Atoi(w)
		if err != nil || u == "" {
			continue
		}
		if n > bestW {
			best, bestW = u, n
		}
	}
	return best
}

func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
