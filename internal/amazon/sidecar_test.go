// sidecar_test.go — pins the finalized forward reading-time parser against the
// live-captured kindle.lpr shape (scratchpad/kindle_sidecar_probe.py). The signed
// FETCH is not unit-tested here (it hits real Amazon); the pure parse + creationTime
// handling + interface conformance are.
package amazon

import (
	"testing"
	"time"
)

// KindleSidecarClient must satisfy the KindleSidecar interface the books domain
// depends on (compile-time; also asserted in the package via var _).
func TestKindleSidecarClient_ImplementsInterface(t *testing.T) {
	var _ KindleSidecar = NewKindleSidecarClient()
}

// The live 200 body: one kindle.lpr record with a STRING location + a
// space-separated creationTime with a trailing fractional second.
func TestParseLastPagePosition_LiveShape(t *testing.T) {
	body := []byte(`{"md5":"abc123","payload":{"records":[
	   {"type":"kindle.lpr","location":"9283","annotationId":"dev-B0X-EBOK-furthest-page-read","creationTime":"2026-08-07 03:03:02.0"}
	]}}`)
	pos, at, ok, err := parseLastPagePosition("B0X", body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !ok {
		t.Fatalf("ok=false, want a parsed record")
	}
	if pos != 9283 {
		t.Errorf("position = %d, want 9283 (string location parsed to int)", pos)
	}
	want := time.Date(2026, 8, 7, 3, 3, 2, 0, time.UTC)
	if !at.Equal(want) {
		t.Errorf("creationTime = %v, want %v (UTC, fractional second)", at, want)
	}
}

// creationTime with NO fractional second must still parse (the ".9" layout makes
// the fraction optional).
func TestParseLastPagePosition_NoFractionalSecond(t *testing.T) {
	body := []byte(`{"payload":{"records":[{"type":"kindle.lpr","location":"42","creationTime":"2026-08-07 03:03:02"}]}}`)
	pos, at, ok, err := parseLastPagePosition("B0X", body)
	if err != nil || !ok || pos != 42 {
		t.Fatalf("got pos=%d ok=%v err=%v, want 42/true/nil", pos, ok, err)
	}
	if !at.Equal(time.Date(2026, 8, 7, 3, 3, 2, 0, time.UTC)) {
		t.Errorf("creationTime = %v, want 2026-08-07 03:03:02 UTC", at)
	}
}

// It selects the kindle.lpr record even when other record types are present.
func TestParseLastPagePosition_SkipsNonLprRecords(t *testing.T) {
	body := []byte(`{"payload":{"records":[
	   {"type":"kindle.otherstuff","location":"111"},
	   {"type":"kindle.lpr","location":"777","creationTime":"2026-08-07 10:00:00.0"}
	]}}`)
	pos, _, ok, err := parseLastPagePosition("B0X", body)
	if err != nil || !ok || pos != 777 {
		t.Fatalf("got pos=%d ok=%v err=%v, want 777/true/nil", pos, ok, err)
	}
}

// A 200 with no kindle.lpr record is a clean miss (ok=false), not an error.
func TestParseLastPagePosition_NoLprRecordIsCleanMiss(t *testing.T) {
	pos, at, ok, err := parseLastPagePosition("B0X", []byte(`{"payload":{"records":[]}}`))
	if err != nil {
		t.Fatalf("err = %v, want nil (no record → clean miss)", err)
	}
	if ok || pos != 0 || !at.IsZero() {
		t.Errorf("got pos=%d ok=%v at=%v, want 0/false/zero", pos, ok, at)
	}
}

// A non-integer location surfaces as an error (a genuine wire-shape violation the
// job logs + skips), not a silent zero.
func TestParseLastPagePosition_NonIntegerLocationErrors(t *testing.T) {
	_, _, ok, err := parseLastPagePosition("B0X", []byte(`{"payload":{"records":[{"type":"kindle.lpr","location":"NaN"}]}}`))
	if err == nil {
		t.Fatalf("err = nil, want a parse error for a non-integer location")
	}
	if ok {
		t.Errorf("ok = true on a bad location, want false")
	}
}

// An empty creationTime → zero time (the caller falls back to poll time).
func TestParseSidecarCreationTime_EmptyAndBadYieldZero(t *testing.T) {
	for _, s := range []string{"", "   ", "not-a-date", "2026/08/07"} {
		if got := parseSidecarCreationTime(s); !got.IsZero() {
			t.Errorf("parseSidecarCreationTime(%q) = %v, want zero", s, got)
		}
	}
}
