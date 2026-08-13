package amazon

import (
	"testing"
)

// exchangeCookiesFixture is a captured /ap/exchangetoken/cookies response: the
// .amazon.com jar with the auth cookies read.amazon.com needs. Two values are
// wrapped in quotes to prove the quote-strip.
const exchangeCookiesFixture = `{
  "response": {
    "tokens": {
      "cookies": {
        ".amazon.com": [
          {"Name": "at-main",       "Value": "Atza|abc123"},
          {"Name": "session-token", "Value": "\"wrapped-in-quotes\""},
          {"Name": "ubid-main",     "Value": "131-0000000-0000000"},
          {"Name": "x-main",        "Value": "\"xmain-quoted\""},
          {"Name": "session-id",    "Value": "141-1111111-1111111"}
        ]
      }
    }
  }
}`

func TestParseExchangeCookies(t *testing.T) {
	jar, err := parseExchangeCookies([]byte(exchangeCookiesFixture))
	if err != nil {
		t.Fatalf("parseExchangeCookies: %v", err)
	}
	if len(jar) != 5 {
		t.Fatalf("want 5 cookies, got %d: %v", len(jar), jar)
	}
	if jar["at-main"] != "Atza|abc123" {
		t.Fatalf("at-main: got %q", jar["at-main"])
	}
	// wrapping quotes stripped
	if jar["session-token"] != "wrapped-in-quotes" {
		t.Fatalf("session-token quotes not stripped: %q", jar["session-token"])
	}
	if jar["x-main"] != "xmain-quoted" {
		t.Fatalf("x-main quotes not stripped: %q", jar["x-main"])
	}
	if jar["session-id"] != "141-1111111-1111111" {
		t.Fatalf("session-id: got %q", jar["session-id"])
	}
}

func TestParseExchangeCookiesEmpty(t *testing.T) {
	jar, err := parseExchangeCookies([]byte(`{"response":{"tokens":{"cookies":{}}}}`))
	if err != nil {
		t.Fatalf("parseExchangeCookies: %v", err)
	}
	if len(jar) != 0 {
		t.Fatalf("want empty jar, got %v", jar)
	}
}

func TestCookieHeaderDeterministic(t *testing.T) {
	got := cookieHeader(map[string]string{"b": "2", "a": "1", "c": "3"})
	if got != "a=1; b=2; c=3" {
		t.Fatalf("cookieHeader not sorted/deterministic: %q", got)
	}
}

// libraryPageFixture is a captured /kindle-library/search page: three items with
// authors ("Last, First:" strings, one multi-author), percentageRead spanning
// want/reading/read, plus a non-null paginationToken (more pages follow).
const libraryPageFixture = `{
  "itemsList": [
    {
      "asin": "B0READING01",
      "title": "Project Hail Mary",
      "authors": ["Weir, Andy:"],
      "percentageRead": 37,
      "productUrl": "https://m.media-amazon.com/images/I/phm.jpg",
      "webReaderUrl": "https://read.amazon.com/?asin=B0READING01",
      "resourceType": "EBOOK",
      "originType": "PURCHASE",
      "mangaOrComicAsin": false
    },
    {
      "asin": "B0FINISHED2",
      "title": "Good Omens",
      "authors": ["Gaiman, Neil:", "Pratchett, Terry:"],
      "percentageRead": 100,
      "productUrl": "https://m.media-amazon.com/images/I/go.jpg",
      "webReaderUrl": "https://read.amazon.com/?asin=B0FINISHED2",
      "resourceType": "EBOOK",
      "originType": "PURCHASE"
    },
    {
      "asin": "B0UNOPENED3",
      "title": "Dune",
      "authors": ["Herbert, Frank:"],
      "percentageRead": 0,
      "productUrl": "https://m.media-amazon.com/images/I/dune.jpg",
      "resourceType": "EBOOK",
      "originType": "PURCHASE"
    }
  ],
  "paginationToken": "PAGE2TOKEN"
}`

func TestParseCloudLibraryPage(t *testing.T) {
	items, next, err := parseCloudLibraryPage([]byte(libraryPageFixture))
	if err != nil {
		t.Fatalf("parseCloudLibraryPage: %v", err)
	}
	if next != "PAGE2TOKEN" {
		t.Fatalf("paginationToken: want PAGE2TOKEN, got %q", next)
	}
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}

	r := items[0]
	if r.ASIN != "B0READING01" || r.Title != "Project Hail Mary" {
		t.Fatalf("item 0 asin/title wrong: %+v", r)
	}
	if r.PercentageRead != 37 {
		t.Fatalf("percentageRead: want 37, got %d", r.PercentageRead)
	}
	if r.CoverURL != "https://m.media-amazon.com/images/I/phm.jpg" {
		t.Fatalf("cover (productUrl) not mapped: %q", r.CoverURL)
	}
	if r.WebReaderURL == "" || r.ResourceType != "EBOOK" {
		t.Fatalf("webReaderUrl/resourceType not mapped: %+v", r)
	}
	// AuthorsCSV strips the trailing ':'.
	if got := r.AuthorsCSV(); got != "Weir, Andy" {
		t.Fatalf("AuthorsCSV single: want %q, got %q", "Weir, Andy", got)
	}

	// multi-author join
	if got := items[1].AuthorsCSV(); got != "Gaiman, Neil, Pratchett, Terry" {
		t.Fatalf("AuthorsCSV multi: got %q", got)
	}
	if items[1].PercentageRead != 100 {
		t.Fatalf("finished item percentageRead: want 100, got %d", items[1].PercentageRead)
	}
	if items[2].PercentageRead != 0 {
		t.Fatalf("unopened item percentageRead: want 0, got %d", items[2].PercentageRead)
	}
}

// TestParseCloudLibraryPageLastPage: a null paginationToken ends pagination, and
// an item with no ASIN is dropped.
func TestParseCloudLibraryPageLastPage(t *testing.T) {
	body := `{
      "itemsList": [
        {"asin": "B0KEEP", "title": "Keep Me", "authors": [], "percentageRead": 5, "resourceType": "EBOOK"},
        {"asin": "", "title": "No ASIN", "percentageRead": 9}
      ],
      "paginationToken": null
    }`
	items, next, err := parseCloudLibraryPage([]byte(body))
	if err != nil {
		t.Fatalf("parseCloudLibraryPage: %v", err)
	}
	if next != "" {
		t.Fatalf("null paginationToken should yield empty next, got %q", next)
	}
	if len(items) != 1 || items[0].ASIN != "B0KEEP" {
		t.Fatalf("item with no ASIN should be dropped: %+v", items)
	}
	if items[0].AuthorsCSV() != "" {
		t.Fatalf("empty authors should render empty CSV, got %q", items[0].AuthorsCSV())
	}
}

func TestCloudLibraryItemIsSample(t *testing.T) {
	if !(CloudLibraryItem{ResourceType: "EBOOK_SAMPLE"}).IsSample() {
		t.Fatal("EBOOK_SAMPLE should be a sample")
	}
	if (CloudLibraryItem{ResourceType: "EBOOK"}).IsSample() {
		t.Fatal("EBOOK should not be a sample")
	}
}

func TestClampPercentInt(t *testing.T) {
	cases := map[float64]int{-5: 0, 0: 0, 36.4: 36, 36.6: 37, 99.9: 100, 100: 100, 150: 100}
	for in, want := range cases {
		if got := clampPercentInt(in); got != want {
			t.Fatalf("clampPercentInt(%v) = %d, want %d", in, got, want)
		}
	}
}
