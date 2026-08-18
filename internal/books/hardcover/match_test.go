package hardcover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// match_test.go — pins the deep-link slug threading through the match ladder
// (gaka-qic0). The Hardcover book PAGE resolves on the book's SLUG, not the
// numeric id, so an exact-id match MUST surface book.slug on the MatchResult or
// the "open on Hardcover" link 404s. Uses the shared fakeRoundTripper
// (push_test.go), which serializes the injected hcEdition — including its
// book.slug — back through the real editionByField JSON parse.

// TestMatch_ASINCarriesSlug: an exact-ASIN edition hit surfaces book.slug on the
// MatchResult (the segment the FE builds /books/<slug> from).
func TestMatch_ASINCarriesSlug(t *testing.T) {
	ed := &hcEdition{ID: 8802, BookID: 556, ReadingFormatID: FormatAudio}
	ed.Book.Slug = "the-way-of-kings"
	client := newFakeClient(&fakeRoundTripper{editionByASIN: ed})

	res, err := client.Match(context.Background(), MatchInput{ASIN: "B0ASINSLUG"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if res.Method != MatchByASIN {
		t.Fatalf("Method = %q, want %q", res.Method, MatchByASIN)
	}
	if res.BookID != 556 || res.EditionID != 8802 {
		t.Fatalf("ids = book %d / edition %d, want 556 / 8802", res.BookID, res.EditionID)
	}
	if res.Slug != "the-way-of-kings" {
		t.Fatalf("Slug = %q, want %q — a slugless match 404s the deep-link", res.Slug, "the-way-of-kings")
	}
}

// batchRoundTripper answers an EditionsByField (_in) query by echoing one edition
// per requested value, so a test can assert the batch maps each result back to the
// value that asked for it. It records how many requests it served (chunk count).
type batchRoundTripper struct {
	requests int
	lastVals []string
}

func (f *batchRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	var env struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	_ = json.Unmarshal(body, &env)

	respond := func(data string) *http.Response {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"data":` + data + `}`)),
			Header:     make(http.Header),
		}
	}
	if !strings.Contains(env.Query, "EditionsByField") {
		return respond(`{}`), nil
	}
	f.requests++
	isbnField := strings.Contains(env.Query, "isbn_13: {_in")
	rawVals, _ := env.Variables["vs"].([]any)
	f.lastVals = nil
	eds := make([]hcEdition, 0, len(rawVals))
	for i, rv := range rawVals {
		v, _ := rv.(string)
		f.lastVals = append(f.lastVals, v)
		ed := hcEdition{ID: int64(1000 + i), BookID: int64(500 + i)}
		ed.Book.Slug = "slug-" + v
		if isbnField {
			ed.Isbn13 = v
		} else {
			ed.Asin = v
		}
		eds = append(eds, ed)
	}
	blob, _ := json.Marshal(eds)
	return respond(`{"editions":` + string(blob) + `}`), nil
}

// TestEditionsByField_BatchesAndMapsBackByASIN pins the batch rung: many ASINs go
// out in one request and each returned edition is mapped back under the ASIN that
// asked for it (with its slug), so the sweep can link each row.
func TestEditionsByField_BatchesAndMapsBackByASIN(t *testing.T) {
	rt := &batchRoundTripper{}
	c := NewClient("tok").SetDryRun(false)
	c.http = &http.Client{Transport: rt}

	asins := []string{"A1", "A2", "A3"}
	m, err := c.editionsByField(context.Background(), "asin", asins)
	if err != nil {
		t.Fatalf("editionsByField: %v", err)
	}
	if rt.requests != 1 {
		t.Fatalf("requests = %d, want 1 (three asins in a single _in batch)", rt.requests)
	}
	if len(m) != 3 {
		t.Fatalf("got %d editions, want 3: %+v", len(m), m)
	}
	for _, a := range asins {
		ed, ok := m[a]
		if !ok {
			t.Fatalf("asin %q missing from batch result", a)
		}
		if ed.Asin != a {
			t.Errorf("edition for %q keyed wrong: asin=%q", a, ed.Asin)
		}
		if ed.Book.Slug != "slug-"+a {
			t.Errorf("slug for %q = %q, want %q", a, ed.Book.Slug, "slug-"+a)
		}
		if ed.BookID <= 0 || ed.ID <= 0 {
			t.Errorf("edition for %q has zero ids: %+v", a, ed)
		}
	}
}

// TestEditionsByField_ISBNMapsBackAndDedupes pins the isbn_13 field variant plus
// the de-dupe/trim: duplicate + blank inputs collapse to the distinct set.
func TestEditionsByField_ISBNMapsBackAndDedupes(t *testing.T) {
	rt := &batchRoundTripper{}
	c := NewClient("tok").SetDryRun(false)
	c.http = &http.Client{Transport: rt}

	// Two distinct isbns, one repeated, one blank → the query must carry only the
	// two distinct values.
	in := []string{"9780000000001", "9780000000002", "9780000000001", "  "}
	m, err := c.editionsByField(context.Background(), "isbn_13", in)
	if err != nil {
		t.Fatalf("editionsByField: %v", err)
	}
	if len(rt.lastVals) != 2 {
		t.Fatalf("query carried %d values, want 2 distinct (deduped, blanks dropped): %v", len(rt.lastVals), rt.lastVals)
	}
	if len(m) != 2 {
		t.Fatalf("got %d editions, want 2: %+v", len(m), m)
	}
	if ed, ok := m["9780000000002"]; !ok || ed.Isbn13 != "9780000000002" {
		t.Fatalf("isbn 9780000000002 not mapped back: ok=%v ed=%+v", ok, ed)
	}
}

// TestEditionsByField_EmptyNoRequest guards the no-values short-circuit: an all-
// blank input issues ZERO requests and returns an empty map.
func TestEditionsByField_EmptyNoRequest(t *testing.T) {
	rt := &batchRoundTripper{}
	c := NewClient("tok").SetDryRun(false)
	c.http = &http.Client{Transport: rt}

	m, err := c.editionsByField(context.Background(), "asin", []string{"", "  "})
	if err != nil {
		t.Fatalf("editionsByField: %v", err)
	}
	if rt.requests != 0 {
		t.Fatalf("requests = %d, want 0 (no non-blank values)", rt.requests)
	}
	if len(m) != 0 {
		t.Fatalf("got %d editions, want 0", len(m))
	}
}

// TestCleanTitleForMatch pins the series-suffix stripping that unblocks matches:
// Amazon appends "(Series … Book N)" cruft that Hardcover's canonical title omits,
// diluting the token-overlap score below the floor.
func TestCleanTitleForMatch(t *testing.T) {
	cases := map[string]string{
		"The Altreian Enigma (Rho Agenda Assimilation Book 2)": "The Altreian Enigma",
		"Enter Into Valhalla (The Kurtherian Endgame Book 6)":  "Enter Into Valhalla",
		"Rainbow Six (John Clark Novel, A Book 2)":             "Rainbow Six",
		"Waylander (Drenai Saga Book 3)":                       "Waylander",
		"Revenger (The Revenger Series Book 1)":                "Revenger",
		"Some Book [Unabridged] (Series Book 1)":               "Some Book", // two trailing groups
		"Plain Title":                                          "Plain Title",
		"(All Parenthetical)":                                  "(All Parenthetical)", // never empties
	}
	for in, want := range cases {
		if got := cleanTitleForMatch(in); got != want {
			t.Errorf("cleanTitleForMatch(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestScoreCandidate_SeriesSuffixClearsFloor proves the end goal: a candidate whose
// canonical title matches the CLEANED input clears the 0.6 floor (it did not with
// the raw "(… Book 2)" title — 3/8 = 0.375).
func TestScoreCandidate_SeriesSuffixClearsFloor(t *testing.T) {
	in := MatchInput{Title: cleanTitleForMatch("The Altreian Enigma (Rho Agenda Assimilation Book 2)"), Author: "Richard Phillips"}
	cand := searchCandidate{BookID: 1, Title: "The Altreian Enigma", Authors: []string{"Richard Phillips"}}
	if got := scoreCandidate(in, cand); got < 0.6 {
		t.Errorf("score = %.3f, want >= 0.6 (series suffix stripped → clean title matches)", got)
	}
}
