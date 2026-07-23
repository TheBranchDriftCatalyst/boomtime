package handler_test

// gaka-bi2: body-size cap integration test for POST wakatime_key.
//
// The SaveWakatimeKey handler probes wakatime.com with the supplied key
// BEFORE persisting. If the body-size cap failed, a hostile client could send
// a 5 KiB key, and the handler would happily base64-encode it into an
// Authorization header and DNS-resolve wakatime.com — amplifying a single
// authed POST into an outbound HTTP round-trip.
//
// A 413 here proves probeWakatimeKey never ran, no encrypt happened, no DB
// write happened. Non-tautologically: without the cap the response would be
// 400 ("Wakatime rejected this key") after the outbound probe returns 401 —
// a distinct signal.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// routerWithWakatimeKey wires just the save endpoint since Harness.Router()
// doesn't include it by default.
func routerWithWakatimeKey(hz *testutil.Harness) http.Handler {
	e := hz.Router()
	e.POST("/api/v1/users/current/wakatime_key", hz.H.SaveWakatimeKey)
	return e
}

// TestSaveWakatimeKey_BodySizeCap_413: 5 KiB body → 413, no probe, no encrypt.
func TestSaveWakatimeKey_BodySizeCap_413(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithWakatimeKey(hz)
	_, token := hz.MintUser("wkkey_413")

	big := strings.Repeat("a", 5000)
	body := []byte(`{"key":"` + big + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/wakatime_key", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize wakatime_key POST: status %d (want 413). Any other status would prove the outbound wakatime.com probe ran on the payload — a per-request amplifier this fix closes. body=%s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "payload too large") {
		t.Errorf("body missing sentinel: %s", rec.Body.String())
	}
}
