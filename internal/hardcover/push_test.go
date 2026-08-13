package hardcover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// push_test.go — pins the continuous-progress push (gaka-books A):
//   - ReadInput.object() includes the progress fields when set and OMITS them
//     when nil (a partial update must never null out Hardcover data).
//   - PushProgress builds status=reading + the right progress payload, deriving
//     progress_seconds from the edition length. Uses a fake RoundTripper so no
//     live Hardcover call is made.

func ptrF(f float64) *float64 { return &f }
func ptrI(i int) *int         { return &i }

func TestReadInput_ObjectIncludesProgressFields(t *testing.T) {
	started := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	in := ReadInput{
		Progress:        ptrF(42.5),
		ProgressPages:   ptrI(88),
		ProgressSeconds: ptrI(3600),
		StartedAt:       &started,
	}
	obj := in.object()
	if obj["progress"] != 42.5 {
		t.Errorf("progress = %v, want 42.5", obj["progress"])
	}
	if obj["progress_pages"] != 88 {
		t.Errorf("progress_pages = %v, want 88", obj["progress_pages"])
	}
	if obj["progress_seconds"] != 3600 {
		t.Errorf("progress_seconds = %v, want 3600", obj["progress_seconds"])
	}
	if obj["started_at"] != "2026-07-10" {
		t.Errorf("started_at = %v, want 2026-07-10", obj["started_at"])
	}
}

func TestReadInput_ObjectOmitsNilProgressFields(t *testing.T) {
	// A read that only carries a finish date must NOT emit progress keys — else a
	// partial update would clobber Hardcover's existing progress.
	fin := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	obj := ReadInput{FinishedAt: &fin}.object()
	for _, k := range []string{"progress", "progress_pages", "progress_seconds", "started_at"} {
		if _, present := obj[k]; present {
			t.Errorf("object() should omit %q when nil, got %v", k, obj[k])
		}
	}
	if obj["finished_at"] != "2026-06-20" {
		t.Errorf("finished_at = %v, want 2026-06-20", obj["finished_at"])
	}
}

// fakeRoundTripper answers Hardcover GraphQL POSTs from canned data keyed off the
// query text, and records every mutation's variables for assertion.
type fakeRoundTripper struct {
	editionByASIN *hcEdition // nil => asin miss
	mutations     []recordedMutation
	// matchQueries counts every match-ladder read (edition lookups + Typesense
	// search) so a test can assert the MATCHED push issues none of them.
	matchQueries int
}

type recordedMutation struct {
	op   string
	vars map[string]any
}

func (f *fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
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

	switch {
	case strings.Contains(env.Query, "EditionByField"):
		f.matchQueries++
		if f.editionByASIN == nil {
			return respond(`{"editions":[]}`), nil
		}
		ed, _ := json.Marshal([]hcEdition{*f.editionByASIN})
		return respond(`{"editions":` + string(ed) + `}`), nil
	case strings.Contains(env.Query, "insert_user_book("):
		f.mutations = append(f.mutations, recordedMutation{op: "insert_user_book", vars: env.Variables})
		return respond(`{"insert_user_book":{"id":9001,"error":"","user_book":{"id":9001}}}`), nil
	case strings.Contains(env.Query, "insert_user_book_read"):
		f.mutations = append(f.mutations, recordedMutation{op: "insert_user_book_read", vars: env.Variables})
		return respond(`{"insert_user_book_read":{"id":7001,"error":"","user_book_read":{"id":7001}}}`), nil
	case strings.Contains(env.Query, "search("):
		f.matchQueries++
		return respond(`{"search":{"results":{"hits":[]}}}`), nil
	default:
		return respond(`{}`), nil
	}
}

func newFakeClient(rt *fakeRoundTripper) *Client {
	c := NewClient("tok").SetDryRun(false) // writes must reach the fake transport
	c.http = &http.Client{Transport: rt}
	return c
}

func TestPushProgress_BuildsReadingWithProgress(t *testing.T) {
	rt := &fakeRoundTripper{editionByASIN: &hcEdition{ID: 8802, BookID: 556, ReadingFormatID: FormatAudio}}
	client := newFakeClient(rt)

	// 50% of a 10-hour (36000s) audiobook → progress_seconds ≈ 18000.
	match, err := PushProgress(context.Background(), client, MatchInput{ASIN: "B01"}, 50, 0, 36000, FormatAudio)
	if err != nil {
		t.Fatalf("PushProgress: %v", err)
	}
	if match.BookID != 556 || match.EditionID != 8802 {
		t.Fatalf("match = %+v, want book 556 / edition 8802", match)
	}

	var ub, read *recordedMutation
	for i := range rt.mutations {
		switch rt.mutations[i].op {
		case "insert_user_book":
			ub = &rt.mutations[i]
		case "insert_user_book_read":
			read = &rt.mutations[i]
		}
	}
	if ub == nil || read == nil {
		t.Fatalf("expected both a user_book and a read mutation, got %+v", rt.mutations)
	}

	// user_book must be pushed as currently-reading (status_id 2).
	ubObj, _ := ub.vars["object"].(map[string]any)
	if got := jsonNum(ubObj["status_id"]); got != float64(StatusReading) {
		t.Errorf("user_book status_id = %v, want %d (reading)", ubObj["status_id"], StatusReading)
	}

	// read must carry progress=50 and the derived progress_seconds=18000.
	readObj, _ := read.vars["object"].(map[string]any)
	if got := jsonNum(readObj["progress"]); got != 50 {
		t.Errorf("read progress = %v, want 50", readObj["progress"])
	}
	if got := jsonNum(readObj["progress_seconds"]); got != 18000 {
		t.Errorf("read progress_seconds = %v, want 18000", readObj["progress_seconds"])
	}
	if _, present := readObj["progress_pages"]; present {
		t.Errorf("audio push should not set progress_pages, got %v", readObj["progress_pages"])
	}
	if _, present := readObj["finished_at"]; present {
		t.Errorf("in-progress push must not set finished_at, got %v", readObj["finished_at"])
	}
}

func TestPushProgress_NoMatchNeverPushes(t *testing.T) {
	rt := &fakeRoundTripper{editionByASIN: nil} // asin miss + empty search → no match
	client := newFakeClient(rt)

	match, err := PushProgress(context.Background(), client, MatchInput{ASIN: "NOPE", Title: "Unfindable"}, 30, 0, 3600, FormatAudio)
	if err != nil {
		t.Fatalf("PushProgress: %v", err)
	}
	if match.Method != MatchNone || match.BookID != 0 {
		t.Errorf("want no-match, got %+v", match)
	}
	if len(rt.mutations) != 0 {
		t.Errorf("a no-match must issue ZERO mutations, got %+v", rt.mutations)
	}
}

func TestPushProgress_DryRunNoOps(t *testing.T) {
	rt := &fakeRoundTripper{editionByASIN: &hcEdition{ID: 8802, BookID: 556, ReadingFormatID: FormatAudio}}
	c := NewClient("tok") // dry-run ON (fail-safe default)
	c.http = &http.Client{Transport: rt}

	if _, err := PushProgress(context.Background(), c, MatchInput{ASIN: "B01"}, 50, 0, 36000, FormatAudio); err != nil {
		t.Fatalf("PushProgress (dry-run): %v", err)
	}
	// The match query runs (a read), but the gate blocks both mutations, so none
	// reach the transport — and the id=0 short-circuit avoids a spurious error.
	if len(rt.mutations) != 0 {
		t.Errorf("dry-run must block all mutations, got %+v", rt.mutations)
	}
}

// TestPushProgressMatched_SkipsMatch pins the anti-flood fix: given already-
// resolved ids, PushProgressMatched pushes straight to UpsertUserBook +
// UpsertRead and issues ZERO match-ladder queries (no edition lookup, no search).
func TestPushProgressMatched_SkipsMatch(t *testing.T) {
	// A live editionByASIN is available — but the matched path must NOT consult it.
	rt := &fakeRoundTripper{editionByASIN: &hcEdition{ID: 8802, BookID: 556, ReadingFormatID: FormatAudio}}
	client := newFakeClient(rt)

	applied, err := PushProgressMatched(context.Background(), client, 556, 8802, 50, 0, 36000, FormatAudio)
	if err != nil {
		t.Fatalf("PushProgressMatched: %v", err)
	}
	if !applied {
		t.Fatalf("applied = false, want true (a real write happened)")
	}
	if rt.matchQueries != 0 {
		t.Errorf("matched push must issue ZERO match/search queries, got %d", rt.matchQueries)
	}

	var ub, read *recordedMutation
	for i := range rt.mutations {
		switch rt.mutations[i].op {
		case "insert_user_book":
			ub = &rt.mutations[i]
		case "insert_user_book_read":
			read = &rt.mutations[i]
		}
	}
	if ub == nil || read == nil {
		t.Fatalf("expected both a user_book and a read mutation, got %+v", rt.mutations)
	}
	// user_book uses the SUPPLIED book_id/edition_id + status reading.
	ubObj, _ := ub.vars["object"].(map[string]any)
	if got := jsonNum(ubObj["book_id"]); got != 556 {
		t.Errorf("user_book book_id = %v, want 556 (the stored id)", ubObj["book_id"])
	}
	if got := jsonNum(ubObj["edition_id"]); got != 8802 {
		t.Errorf("user_book edition_id = %v, want 8802 (the stored id)", ubObj["edition_id"])
	}
	if got := jsonNum(ubObj["status_id"]); got != float64(StatusReading) {
		t.Errorf("user_book status_id = %v, want %d (reading)", ubObj["status_id"], StatusReading)
	}
	// read carries progress=50 + derived progress_seconds=18000.
	readObj, _ := read.vars["object"].(map[string]any)
	if got := jsonNum(readObj["progress"]); got != 50 {
		t.Errorf("read progress = %v, want 50", readObj["progress"])
	}
	if got := jsonNum(readObj["progress_seconds"]); got != 18000 {
		t.Errorf("read progress_seconds = %v, want 18000", readObj["progress_seconds"])
	}
}

// TestPushProgressMatched_DryRunNoOp pins that under the dry-run gate the matched
// push reports applied=false and issues NO UpsertRead (nothing reaches the
// transport) — so the caller never records a pushed_progress it didn't push.
func TestPushProgressMatched_DryRunNoOp(t *testing.T) {
	rt := &fakeRoundTripper{editionByASIN: &hcEdition{ID: 8802, BookID: 556, ReadingFormatID: FormatAudio}}
	c := NewClient("tok") // dry-run ON (fail-safe default)
	c.http = &http.Client{Transport: rt}

	applied, err := PushProgressMatched(context.Background(), c, 556, 8802, 50, 0, 36000, FormatAudio)
	if err != nil {
		t.Fatalf("PushProgressMatched (dry-run): %v", err)
	}
	if applied {
		t.Errorf("dry-run must report applied=false")
	}
	if rt.matchQueries != 0 {
		t.Errorf("matched push must never match, got %d match queries", rt.matchQueries)
	}
	if len(rt.mutations) != 0 {
		t.Errorf("dry-run must block all mutations (no UpsertRead), got %+v", rt.mutations)
	}
}

// TestPushProgressMatched_NoBookIDNoOp guards the bookID<=0 path (a caller that
// forgot to skip an unmatched row): no write, no error, no match query.
func TestPushProgressMatched_NoBookIDNoOp(t *testing.T) {
	rt := &fakeRoundTripper{editionByASIN: &hcEdition{ID: 8802, BookID: 556}}
	client := newFakeClient(rt)

	applied, err := PushProgressMatched(context.Background(), client, 0, 0, 50, 0, 36000, FormatAudio)
	if err != nil {
		t.Fatalf("PushProgressMatched(bookID=0): %v", err)
	}
	if applied {
		t.Errorf("bookID<=0 must report applied=false")
	}
	if rt.matchQueries != 0 || len(rt.mutations) != 0 {
		t.Errorf("bookID<=0 must be a pure no-op, got %d match queries + %+v", rt.matchQueries, rt.mutations)
	}
}

func TestProgressReadInput_DerivesPositions(t *testing.T) {
	in := progressReadInput(25, 400, 0, 8801, FormatEbook)
	if in.Progress == nil || *in.Progress != 25 {
		t.Fatalf("progress = %v, want 25", in.Progress)
	}
	if in.ProgressPages == nil || *in.ProgressPages != 100 { // 25% of 400
		t.Errorf("progress_pages = %v, want 100", in.ProgressPages)
	}
	if in.ProgressSeconds != nil {
		t.Errorf("no audio length → progress_seconds should stay nil, got %v", *in.ProgressSeconds)
	}
	// Over-range percent is clamped.
	if p := clampPercent(150); p != 100 {
		t.Errorf("clampPercent(150) = %v, want 100", p)
	}
}

// jsonNum coerces a decoded JSON number (which may be float64 or json.Number)
// into a float64 for comparison.
func jsonNum(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return -1
	}
}
