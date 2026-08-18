package hardcover

import (
	"context"
	"net/http"
	"testing"
)

// curation_push_test.go — pins the curation push's two load-bearing facts:
//   - StatusID maps the canonical 1:1 vocabulary onto Hardcover status_ids, so the
//     push mirrors the user's CHOSEN status (not a hardcoded Reading/Read).
//   - UpsertUserBookCuration sends that status_id + the rating, and is blocked by
//     the dry-run gate (no write reaches the transport).

func TestStatusID_MapsCanonicalVocabulary(t *testing.T) {
	cases := map[string]int64{
		"want": StatusWant, "reading": StatusReading, "read": StatusRead,
		"paused": StatusPaused, "dnf": StatusDNF,
		"": 0, "bogus": 0,
	}
	for status, want := range cases {
		if got := StatusID(status); got != want {
			t.Errorf("StatusID(%q) = %d, want %d", status, got, want)
		}
	}
	// FormatForSource round-trips the two real sources; unknown → 0 (left off).
	if FormatForSource("kindle") != FormatEbook || FormatForSource("audible") != FormatAudio || FormatForSource("x") != 0 {
		t.Fatalf("FormatForSource mapping wrong")
	}
}

func TestUpsertUserBookCuration_PassesChosenStatusAndRating(t *testing.T) {
	rt := &fakeRoundTripper{}
	client := newFakeClient(rt) // dry-run OFF → writes reach the fake transport

	rating := 4.5
	id, err := client.UpsertUserBookCuration(context.Background(), 556, 8802, StatusDNF, FormatEbook, &rating)
	if err != nil {
		t.Fatalf("UpsertUserBookCuration: %v", err)
	}
	if id != 9001 {
		t.Fatalf("user_book id = %d, want 9001", id)
	}

	var ub *recordedMutation
	for i := range rt.mutations {
		if rt.mutations[i].op == "insert_user_book" {
			ub = &rt.mutations[i]
		}
	}
	if ub == nil {
		t.Fatalf("expected an insert_user_book mutation, got %+v", rt.mutations)
	}
	obj, _ := ub.vars["object"].(map[string]any)
	if got := jsonNum(obj["status_id"]); got != float64(StatusDNF) {
		t.Errorf("status_id = %v, want %d (dnf) — the CHOSEN status must be pushed", obj["status_id"], StatusDNF)
	}
	if got := jsonNum(obj["rating"]); got != 4.5 {
		t.Errorf("rating = %v, want 4.5", obj["rating"])
	}
	// reading_format_id must NOT be on the user_book object — Hardcover's
	// UserBookCreateInput has no such field (live 'field not found' error); format
	// is carried by edition_id. Pin the regression so it can never be re-added here.
	if _, present := obj["reading_format_id"]; present {
		t.Errorf("reading_format_id must NOT be sent on UserBookCreateInput (Hardcover rejects it); edition_id carries the format")
	}
	if got := jsonNum(obj["edition_id"]); got != float64(8802) {
		t.Errorf("edition_id = %v, want 8802 (the format-bearing edition)", obj["edition_id"])
	}
}

func TestUpsertUserBookCuration_OmitsNilRating(t *testing.T) {
	rt := &fakeRoundTripper{}
	client := newFakeClient(rt)
	if _, err := client.UpsertUserBookCuration(context.Background(), 556, 8802, StatusReading, FormatEbook, nil); err != nil {
		t.Fatalf("UpsertUserBookCuration: %v", err)
	}
	obj, _ := rt.mutations[0].vars["object"].(map[string]any)
	if _, present := obj["rating"]; present {
		t.Errorf("nil rating must be omitted (never null out Hardcover's rating), got %v", obj["rating"])
	}
}

func TestUpsertUserBookCuration_DryRunBlocks(t *testing.T) {
	rt := &fakeRoundTripper{}
	c := NewClient("tok") // dry-run ON (fail-safe default)
	c.http = &http.Client{Transport: rt}

	rating := 5.0
	id, err := c.UpsertUserBookCuration(context.Background(), 556, 8802, StatusDNF, FormatEbook, &rating)
	if err != nil {
		t.Fatalf("dry-run curation push must not error, got %v", err)
	}
	if id != 0 {
		t.Errorf("dry-run must return id 0 (write blocked), got %d", id)
	}
	if len(rt.mutations) != 0 {
		t.Errorf("dry-run must block the mutation (nothing reaches the transport), got %+v", rt.mutations)
	}
}
