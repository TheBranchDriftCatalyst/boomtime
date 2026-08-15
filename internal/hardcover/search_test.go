package hardcover

import (
	"encoding/json"
	"testing"
)

func TestParseSearchCandidates(t *testing.T) {
	// A representative Typesense payload: a rich hit, a hit with only the alt cover
	// field + a release_date (year derived from its prefix), and a zero-id hit
	// (dropped). Defensive parsing means missing fields just yield empty values.
	raw := json.RawMessage(`{
      "hits": [
        {"document": {"id": "101", "title": "Project Hail Mary",
          "author_names": ["Andy Weir"], "slug": "project-hail-mary",
          "image": "https://cdn/phm.jpg", "release_year": "2021"}},
        {"document": {"id": "202", "title": "Warlock",
          "author_names": ["Daniel Kensington"], "cached_image": "https://cdn/wl.jpg",
          "release_date": "2019-04-01"}},
        {"document": {"id": "0", "title": "junk"}}
      ]
    }`)

	got := parseSearchCandidates(raw, 8)
	if len(got) != 2 {
		t.Fatalf("want 2 candidates (zero-id dropped), got %d: %+v", len(got), got)
	}
	if got[0].BookID != 101 || got[0].Title != "Project Hail Mary" ||
		got[0].CoverURL != "https://cdn/phm.jpg" || got[0].Year != 2021 ||
		got[0].Slug != "project-hail-mary" || len(got[0].Authors) != 1 {
		t.Errorf("candidate 0 mismatch: %+v", got[0])
	}
	// Second uses the cached_image cover fallback + derives the year from release_date.
	if got[1].CoverURL != "https://cdn/wl.jpg" || got[1].Year != 2019 {
		t.Errorf("candidate 1 cover/year mismatch: %+v", got[1])
	}
}

func TestParseSearchCandidates_EmptyAndLimit(t *testing.T) {
	if got := parseSearchCandidates(nil, 8); got != nil {
		t.Errorf("nil raw → want nil, got %+v", got)
	}
	// limit caps the returned set.
	raw := json.RawMessage(`{"hits":[
      {"document":{"id":"1","title":"a"}},
      {"document":{"id":"2","title":"b"}},
      {"document":{"id":"3","title":"c"}}]}`)
	if got := parseSearchCandidates(raw, 2); len(got) != 2 {
		t.Errorf("limit 2 → want 2, got %d", len(got))
	}
}
