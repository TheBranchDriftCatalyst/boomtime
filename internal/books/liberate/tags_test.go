package liberate

import (
	"strings"
	"testing"
)

// A realistic raw_meta blob as the Audible library sweep stores it. The point of
// this test is that liberation needs NO extra Amazon call — everything below
// comes out of a row that ingest already wrote.
const rawMetaFixture = `{
  "asin": "B09GCYRZRQ",
  "title": "The Gate of the Feral Gods",
  "subtitle": "Dungeon Crawler Carl, Book 4",
  "publisher_name": "Soundbooth Theater",
  "authors": [{"name": "Matt Dinniman"}],
  "narrators": [{"name": "Jeff Hays"}],
  "series": [{"title": "Dungeon Crawler Carl", "sequence": "4"}],
  "runtime_length_min": 1083,
  "release_date": "2021-09-14",
  "publisher_summary": "A summary of the book.",
  "product_images": {"500": "https://img/500.jpg", "1024": "https://img/1024.jpg", "252": "https://img/252.jpg"},
  "category_ladders": [{"ladder": [{"name": "Science Fiction & Fantasy"}, {"name": "Science Fiction"}, {"name": "LitRPG"}]}]
}`

func TestMetadataFromRaw(t *testing.T) {
	m, ok := MetadataFromRaw([]byte(rawMetaFixture))
	if !ok {
		t.Fatal("MetadataFromRaw returned not-ok for a valid blob")
	}
	checks := map[string]struct{ got, want string }{
		"title":        {m.Title, "The Gate of the Feral Gods"},
		"subtitle":     {m.Subtitle, "Dungeon Crawler Carl, Book 4"},
		"author":       {m.AuthorString(), "Matt Dinniman"},
		"narrator":     {m.NarratorString(), "Jeff Hays"},
		"series":       {m.Series, "Dungeon Crawler Carl"},
		"series index": {m.SeriesIndex, "04"},
		"publisher":    {m.Publisher, "Soundbooth Theater"},
		"year":         {m.Year(), "2021"},
		"description":  {m.Description, "A summary of the book."},
		"asin":         {m.ASIN, "B09GCYRZRQ"},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", name, c.got, c.want)
		}
	}
	// Genre is the MOST SPECIFIC ladder rung, not the first — "LitRPG" is more
	// useful for shelving than "Science Fiction & Fantasy".
	if m.Genre != "LitRPG" {
		t.Errorf("genre = %q, want the deepest ladder rung LitRPG", m.Genre)
	}
	// Largest cover, chosen numerically — a lexical max would pick "500" over "1024".
	if m.CoverURL != "https://img/1024.jpg" {
		t.Errorf("cover = %q, want the 1024px image", m.CoverURL)
	}
}

func TestMetadataFromRawTolerantOfJunk(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"empty", ""},
		{"not json", "<html>nope</html>"},
		{"empty object", "{}"},
		{"no title", `{"asin":"B0X"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A stale or malformed raw_meta must not fail the liberation — the
			// caller falls back to the row's denormalised columns.
			if _, ok := MetadataFromRaw([]byte(tc.raw)); ok {
				t.Error("reported ok for unusable metadata")
			}
		})
	}
}

// Series index is zero-padded so lexical directory ordering matches reading
// order — "10 - Title" must not sort before "04 - Title".
func TestNormalizeSeriesIndex(t *testing.T) {
	cases := map[string]string{
		"4": "04", "04": "04", "10": "10", "0": "00",
		"3.5": "3.5", "Prequel": "Prequel", "": "", "  7  ": "07",
	}
	for in, want := range cases {
		if got := normalizeSeriesIndex(in); got != want {
			t.Errorf("normalizeSeriesIndex(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFFmpegTagArgs(t *testing.T) {
	m, _ := MetadataFromRaw([]byte(rawMetaFixture))
	args := m.FFmpegTagArgs()
	joined := strings.Join(args, "\x00")

	// Every -metadata flag must be followed by exactly one key=value pair.
	for i := 0; i < len(args); i += 2 {
		if args[i] != "-metadata" {
			t.Fatalf("arg %d = %q, want -metadata (args must be flag/value pairs)", i, args[i])
		}
		if i+1 >= len(args) || !strings.Contains(args[i+1], "=") {
			t.Fatalf("arg %d has no key=value partner", i)
		}
	}
	want := []string{
		"title=The Gate of the Feral Gods",
		"artist=Matt Dinniman",
		"album_artist=Matt Dinniman",
		"composer=Jeff Hays", // the m4b convention for narrator
		"genre=LitRPG",
		"date=2021",
		"track=04",
		"ASIN=B09GCYRZRQ",
		"media_type=2", // audiobook
	}
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missing tag %q", w)
		}
	}
	// A series book albums under the SERIES so it shelves together.
	if !strings.Contains(joined, "album=Dungeon Crawler Carl") {
		t.Error("series book should album under the series name")
	}
}

// A standalone book has no series, so album falls back to the title — otherwise
// every standalone lands in one nameless album.
func TestFFmpegTagArgsStandaloneAlbumsUnderTitle(t *testing.T) {
	m := Metadata{Title: "Snow Crash", Authors: []string{"Neal Stephenson"}}
	joined := strings.Join(m.FFmpegTagArgs(), "\x00")

	if !strings.Contains(joined, "album=Snow Crash") {
		t.Error("standalone book should album under its title")
	}
	// Empty values must be OMITTED, not written as blank tags.
	for _, absent := range []string{"genre=", "date=", "publisher=", "track="} {
		if strings.Contains(joined, "\x00"+absent+"\x00") || strings.HasSuffix(joined, "\x00"+absent) {
			t.Errorf("empty tag %q was written; it should be omitted", absent)
		}
	}
}

// BookMeta must agree with the tags about who the author is, so the file's
// path and its metadata never disagree.
func TestMetadataBookMetaProjection(t *testing.T) {
	m, _ := MetadataFromRaw([]byte(rawMetaFixture))
	bm := m.BookMeta()

	if bm.Author != m.AuthorString() {
		t.Errorf("BookMeta author %q != tag author %q", bm.Author, m.AuthorString())
	}
	if bm.Series != "Dungeon Crawler Carl" || bm.SeriesIndex != "04" {
		t.Errorf("series projection wrong: %+v", bm)
	}
	// And it must render to a sane path.
	got, err := RenderPath("", bm)
	if err != nil {
		t.Fatalf("RenderPath: %v", err)
	}
	want := "Matt Dinniman/Dungeon Crawler Carl/The Gate of the Feral Gods/The Gate of the Feral Gods.m4b"
	if got != want {
		t.Errorf("RenderPath = %q, want %q", got, want)
	}
}
