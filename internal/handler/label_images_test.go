// label_images_test.go — HTTP-level coverage of GET /api/v1/labels/:id/image
// (gaka-myv). Non-tautological:
//
//   - a live DB row is saved via db.SaveLabelImage, the endpoint is hit
//     with an httptest Recorder, and the response body must match the
//     saved bytes byte-for-byte (a codec-in-the-middle regression would
//     surface here).
//   - the Cache-Control header is asserted verbatim so a future refactor
//     that changes max-age (breaking browser caching semantics we depend
//     on for the ?v= cache-bust contract) is caught.
package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// routerWithLabelImages wires just the public endpoint. Matches the
// production registration in server.go: no auth, no scope.
func TestLabelImage_Served(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()

	// Seed a row.
	id := "test-served-late-night-coder"
	want := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 'x', 'y', 'z'}
	if err := hz.DB.SaveLabelImage(context.Background(), id, want, "image/png", "flux_schnell_fast", "test prompt", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _ = hz.DB.DeleteLabelImage(context.Background(), id) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/labels/"+id+"/image", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type=%q want image/png", ct)
	}
	// Cache-Control is load-bearing: the FE relies on `immutable` to skip
	// revalidation, and busts via ?v=<epoch> on regenerate.
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control=%q — regenerate cache-bust contract expects verbatim `public, max-age=31536000, immutable`", cc)
	}
	got, _ := io.ReadAll(rec.Body)
	if string(got) != string(want) {
		t.Errorf("bytes mismatch: got %d bytes want %d", len(got), len(want))
	}
}

// TestLabelImage_NotFound: an unknown id returns 404. Public endpoint =>
// no auth leakage; the response is the standard error envelope.
func TestLabelImage_NotFound(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/labels/no-such-label-xyz/image", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d want 404 body=%s", rec.Code, rec.Body.String())
	}
}

// TestLabelImage_IgnoresCacheBustParam: the FE appends ?v=<epoch> so the
// browser fetches a fresh URL after a regeneration. The endpoint MUST
// ignore that parameter — same bytes served regardless.
func TestLabelImage_IgnoresCacheBustParam(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()

	id := "test-bust-param"
	body := []byte("fake-png-bytes")
	if err := hz.DB.SaveLabelImage(context.Background(), id, body, "image/png", "", "", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _ = hz.DB.DeleteLabelImage(context.Background(), id) })

	for _, v := range []string{"", "?v=1", "?v=999999999"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/labels/"+id+"/image"+v, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("v=%q status %d body=%s", v, rec.Code, rec.Body.String())
			continue
		}
		got, _ := io.ReadAll(rec.Body)
		if string(got) != string(body) {
			t.Errorf("v=%q bytes changed: got %q want %q", v, got, body)
		}
	}
}
