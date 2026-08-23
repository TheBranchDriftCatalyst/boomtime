package hardcover

import (
	"encoding/json"
	"testing"
)

func TestParseSearchCandidates(t *testing.T) {
	// A representative Typesense payload: a rich hit, a hit with only the alt cover
	// field + a release_date (year derived from its prefix), and a zero-id hit
	// (dropped). Defensive parsing means missing fields just yield empty values.
	// The REAL Hardcover shape (verified 2026-08): document.image is an OBJECT
	// ({url}), not a string — declaring it string once failed the whole unmarshal →
	// 0 results (boom-nq2m). An EMPTY image object must yield CoverURL "".
	raw := json.RawMessage(`{
      "hits": [
        {"document": {"id": "101", "title": "Project Hail Mary",
          "author_names": ["Andy Weir"], "slug": "project-hail-mary",
          "image": {"url": "https://cdn/phm.jpg", "color": "Blue"}, "release_year": 2021}},
        {"document": {"id": "202", "title": "Warlock",
          "author_names": ["Oakley Hall"], "image": {},
          "release_date": "1958-01-01"}},
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
	// Second: empty image object → CoverURL ""; year derived from release_date.
	if got[1].CoverURL != "" || got[1].Year != 1958 {
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
