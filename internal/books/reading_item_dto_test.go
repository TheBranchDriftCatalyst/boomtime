// reading_item_dto_test.go — the reading_items view payload (gaka-qic0). Pins
// the Hardcover match-state + identifier fields onto the JSON contract the Books
// table depends on: a MATCHED row serializes hardcoverBookId/status/matchedAt;
// an UNMATCHED row omits every hardcover_* key (the honest "not matched" state);
// and the identifiers (isbn/amazonAsin) ride along for ASIN-precise linking.
package books

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

func toJSONMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestReadingItemDTO_MatchedRowSerializesHardcoverFields(t *testing.T) {
	bookID := int64(123456)
	status := "read"
	slug := "dune"
	matchedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	it := db.ReadingItem{
		Source: "audible", ExternalID: "B0ASIN123", Title: "Dune", Authors: "Frank Herbert",
		Status: "read", Finished: true, SyncedAt: time.Now(),
		ISBN: "9780441172719", AmazonASIN: "B0PRINT456",
		HardcoverBookID: &bookID, HardcoverStatus: &status, HardcoverMatchedAt: &matchedAt,
		HardcoverSlug: &slug,
	}
	m := toJSONMap(t, toReadingItemDTO(it))

	if got, ok := m["hardcoverBookId"]; !ok {
		t.Fatalf("hardcoverBookId missing for a matched row")
	} else if int64(got.(float64)) != bookID {
		t.Fatalf("hardcoverBookId = %v, want %d", got, bookID)
	}
	// hardcoverSlug is the deep-link segment the FE prefers (gaka-qic0).
	if m["hardcoverSlug"] != "dune" {
		t.Fatalf("hardcoverSlug = %v, want dune", m["hardcoverSlug"])
	}
	if m["hardcoverStatus"] != "read" {
		t.Fatalf("hardcoverStatus = %v, want read", m["hardcoverStatus"])
	}
	if got, ok := m["hardcoverMatchedAt"].(string); !ok || got != matchedAt.Format(time.RFC3339) {
		t.Fatalf("hardcoverMatchedAt = %v, want %s", m["hardcoverMatchedAt"], matchedAt.Format(time.RFC3339))
	}
	if m["isbn"] != "9780441172719" {
		t.Fatalf("isbn = %v", m["isbn"])
	}
	if m["amazonAsin"] != "B0PRINT456" {
		t.Fatalf("amazonAsin = %v", m["amazonAsin"])
	}
	if m["externalId"] != "B0ASIN123" {
		t.Fatalf("externalId = %v", m["externalId"])
	}
}

func TestReadingItemDTO_UnmatchedRowOmitsHardcoverFields(t *testing.T) {
	it := db.ReadingItem{
		Source: "audible", ExternalID: "B0ASIN999", Title: "Untitled", Authors: "",
		Status: "reading", SyncedAt: time.Now(),
		// hardcover_* all nil (NULL) — the pre-match reality. isbn empty too.
	}
	m := toJSONMap(t, toReadingItemDTO(it))

	for _, k := range []string{"hardcoverBookId", "hardcoverStatus", "hardcoverMatchedAt", "hardcoverSlug", "isbn"} {
		if _, ok := m[k]; ok {
			t.Fatalf("%s should be omitted for an unmatched/empty row, got %v", k, m[k])
		}
	}
	// externalId is always present (it is the ASIN, the linking fallback).
	if m["externalId"] != "B0ASIN999" {
		t.Fatalf("externalId = %v", m["externalId"])
	}
}
