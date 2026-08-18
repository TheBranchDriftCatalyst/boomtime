package audible

import (
	"encoding/json"
	"testing"
)

// Regression guards for two bugs found + fixed live against the real Audible API
// (2026-08-12). Both use SYNTHETIC minimal payloads (no user data) that reproduce
// the exact shapes that broke the parser — each fails on the pre-fix code.

// listening_status is an OBJECT, not a string. A prior `string` typing made
// json.Unmarshal fail the ENTIRE library page ("cannot unmarshal object into Go
// struct field ...listening_status of type string"). Guard: an item carrying the
// object shape parses cleanly and the fields we DO map survive.
func TestLibraryItem_ListeningStatusObject_DoesNotBreakParse(t *testing.T) {
	raw := []byte(`{
		"asin": "B0TEST0001",
		"title": "The Regression Guard",
		"authors": [{"name": "A. Tester"}],
		"is_finished": true,
		"percent_complete": 100.0,
		"listening_status": {
			"finished_at_timestamp": "2026-08-07T03:02:21.912Z",
			"is_finished": true,
			"percent_complete": 100.0,
			"time_remaining_seconds": 0
		},
		"runtime_length_min": 745,
		"product_images": {"500": "https://example/cover.jpg"}
	}`)
	var li LibraryItem
	if err := json.Unmarshal(raw, &li); err != nil {
		t.Fatalf("library item with object listening_status must parse, got: %v", err)
	}
	if li.ASIN != "B0TEST0001" || li.Title != "The Regression Guard" {
		t.Fatalf("mapped fields wrong: asin=%q title=%q", li.ASIN, li.Title)
	}
	if !li.IsFinished || li.PercentComplete != 100.0 {
		t.Fatalf("is_finished/percent_complete not read: finished=%v pct=%v", li.IsFinished, li.PercentComplete)
	}
	ri := li.toReadingItem("tester")
	if ri.Status != "read" || !ri.Finished {
		t.Fatalf("finished item should map to status=read finished=true, got %q/%v", ri.Status, ri.Finished)
	}
}

// aggregated_sum is a FLOAT in MILLISECONDS. Parsing it with json.Number.Int64()
// failed on the decimal -> 0 seconds -> every bucket dropped -> reading_activity
// empty. Guard: a float ms sum parses to the correct SECONDS magnitude.
func TestParseAggregates_FloatMillisecondsSum(t *testing.T) {
	body := []byte(`{
		"aggregated_monthly_listening_stats": [
			{"aggregated_sum": 49012010.0, "interval_identifier": "2025-08", "unit": "Milliseconds"},
			{"aggregated_sum": 74059000.0, "interval_identifier": "2025-09", "unit": "Milliseconds"}
		],
		"aggregated_total_listening_stats": {"aggregated_sum": 123071010.0, "unit": "Milliseconds"},
		"response_groups": ["total_listening_stats"]
	}`)
	buckets := parseAggregates(body)
	if len(buckets) != 2 {
		t.Fatalf("expected 2 monthly buckets, got %d", len(buckets))
	}
	// 49012010 ms -> 49012 s (normalizeSeconds divides the ms magnitude by 1000).
	if buckets[0].seconds != 49012 {
		t.Fatalf("Aug bucket: expected 49012 s, got %d (float/ms conversion regressed)", buckets[0].seconds)
	}
	if buckets[0].date.Year() != 2025 || buckets[0].date.Month() != 8 {
		t.Fatalf("Aug bucket date wrong: %s", buckets[0].date.Format("2006-01"))
	}
	// Sanity: never the raw un-converted ms magnitude.
	for _, b := range buckets {
		if b.seconds > 5_000_000 {
			t.Fatalf("%s seconds=%d looks like un-converted ms", b.date.Format("2006-01"), b.seconds)
		}
	}
}

// gaka-vvij: a near-100% title with is_finished=false must classify as completed.
func TestToReadingItem_NearComplete_CountsAsFinished(t *testing.T) {
	li := LibraryItem{ASIN: "B0N95", Title: "Almost Done", IsFinished: false, PercentComplete: 99}
	ri := li.toReadingItem("tester")
	if !ri.Finished || ri.Status != "read" {
		t.Fatalf("99%% unfinished-flagged should be finished/read, got finished=%v status=%q", ri.Finished, ri.Status)
	}
	// A genuinely in-progress title stays reading.
	mid := LibraryItem{ASIN: "B0M50", Title: "Halfway", IsFinished: false, PercentComplete: 50}
	if r := mid.toReadingItem("tester"); r.Finished || r.Status != "reading" {
		t.Fatalf("50%% should stay reading, got finished=%v status=%q", r.Finished, r.Status)
	}
}
