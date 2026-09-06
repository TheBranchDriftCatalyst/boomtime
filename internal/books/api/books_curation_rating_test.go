// books_curation_rating_test.go — the curation rating range check (audit
// boom-l827). PATCH /books/items/:externalId/curation validated the STATUS enum
// but took any float for rating: {"rating": 100} answered 200, became the row's
// EFFECTIVE rating (which the Books table renders and toReadingItemDTO ships),
// and enqueued a CurationPushKind job whose UserBookCreateInput Hardcover
// rejects — three failed attempts per edit and a permanently diverged mirror.
// rating_override is a bare `numeric` column with no CHECK, so this handler is
// the only place the bound can be enforced.
package api

import (
	"encoding/json"
	"testing"
)

// ratingBody builds a curationBody carrying exactly the given raw JSON rating,
// the way the seam binds it (RawMessage so absent, null and a value stay
// distinguishable).
func ratingBody(rawRating string) curationBody {
	raw := json.RawMessage(rawRating)
	return curationBody{Rating: &raw}
}

func TestCurationRating_OutOfRangeIsRejected(t *testing.T) {
	// Every one of these used to be persisted verbatim and pushed to Hardcover.
	for _, raw := range []string{`100`, `6`, `5.0001`, `-3`, `-0.5`} {
		if _, err := ratingBody(raw).toPatch(); err == nil {
			t.Fatalf("rating %s accepted — it becomes the row's effective rating and is "+
				"pushed to Hardcover, which rejects it", raw)
		}
	}
}

func TestCurationRating_InRangeStillAccepted(t *testing.T) {
	// The star editor sends integers 1..5; halves and 0 are inside Hardcover's
	// own scale and must not become 400s.
	for _, raw := range []string{`0`, `0.5`, `1`, `3.5`, `4`, `5`} {
		p, err := ratingBody(raw).toPatch()
		if err != nil {
			t.Fatalf("rating %s rejected: %v", raw, err)
		}
		if !p.SetRating || p.Rating == nil {
			t.Fatalf("rating %s: patch did not carry the value: %+v", raw, p)
		}
	}
}

// TestCurationRating_NullStillClearsTheOverride guards the clear path the star
// editor uses (clicking the current star sends {"rating": null}): it must stay
// SetRating=true with a nil value, not fall into the range check.
func TestCurationRating_NullStillClearsTheOverride(t *testing.T) {
	p, err := ratingBody(`null`).toPatch()
	if err != nil {
		t.Fatalf("explicit null rejected — this is how a rating is cleared: %v", err)
	}
	if !p.SetRating {
		t.Fatal("explicit null must still SET the rating column (to NULL)")
	}
	if p.Rating != nil {
		t.Fatalf("explicit null must clear the override, got %v", *p.Rating)
	}
}

// TestCurationRating_AbsentLeavesTheOverrideAlone is the third state: a body
// that never mentions rating must not touch the column at all.
func TestCurationRating_AbsentLeavesTheOverrideAlone(t *testing.T) {
	status := json.RawMessage(`"dnf"`)
	p, err := curationBody{Status: &status}.toPatch()
	if err != nil {
		t.Fatalf("status-only patch rejected: %v", err)
	}
	if p.SetRating {
		t.Fatal("an absent rating must leave the override untouched")
	}
}
