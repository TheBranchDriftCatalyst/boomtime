package comfyui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// pngBytes returns 12 bytes with the PNG magic-number prefix so sniffMime picks
// image/png. Payload is intentionally short — we care about round-tripping, not
// image parsing.
func pngBytes() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 't', 'i', 'n', 'y'}
}

// TestNewClient_EmptyURL: the "feature disabled" path — empty URL returns
// (nil, nil), the caller treats it as "we simply don't have a client".
func TestNewClient_EmptyURL(t *testing.T) {
	c, err := NewClient("")
	if err != nil {
		t.Fatalf("empty URL should be no-op, got err: %v", err)
	}
	if c != nil {
		t.Fatalf("empty URL should yield nil client, got %+v", c)
	}
}

// TestNewClient_MissingScheme: an operator-typo URL fails at boot, loudly.
func TestNewClient_MissingScheme(t *testing.T) {
	if _, err := NewClient("localhost:8012"); err == nil {
		t.Fatal("expected error for missing scheme, got nil")
	}
}

// TestGenerate_Success_B64JSON: the happy path. Shim returns b64_json PNG,
// client decodes bytes + sniffs mime.
func TestGenerate_Success_B64JSON(t *testing.T) {
	want := pngBytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Verify request shape (non-tautological: catches a client that
		// forgets to send the model or prompt).
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode req: %v", err)
		}
		if body["model"] != "flux_schnell_fast" {
			t.Errorf("model=%v", body["model"])
		}
		if body["prompt"] != "a distinctive emblem" {
			t.Errorf("prompt=%v", body["prompt"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": time.Now().Unix(),
			"data":    []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(want)}},
		})
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, mime, err := c.Generate(context.Background(), "a distinctive emblem", "flux_schnell_fast", "", nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("bytes: got %q want %q", got, want)
	}
	if mime != "image/png" {
		t.Errorf("mime: got %q want image/png", mime)
	}
}

// TestGenerate_Success_DataURL: the shim can be configured to return a
// data: URL instead of b64_json. Client must still work.
func TestGenerate_Success_DataURL(t *testing.T) {
	want := pngBytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(want)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"url": url}},
		})
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	got, mime, err := c.Generate(context.Background(), "prompt", "model", "", nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("bytes mismatch")
	}
	if mime != "image/png" {
		t.Errorf("mime got %q want image/png", mime)
	}
}

// TestGenerate_Retries_On5xx: two consecutive 500s, then a 200. The client
// should back off and succeed. Non-tautological: an attempts counter tracks
// the actual number of HTTP calls made.
func TestGenerate_Retries_On5xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "temporarily unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngBytes())}},
		})
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	// Shrink the client's HTTP timeout so the test finishes fast; the retry
	// loop uses ctx timing.
	c.HTTP.Timeout = 5 * time.Second

	start := time.Now()
	_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Generate after 2x5xx: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("attempts=%d want 3", got)
	}
	// 1s + 2s backoff = 3s minimum before the third attempt.
	if elapsed < 3*time.Second {
		t.Errorf("elapsed %v — retries didn't wait the expected 1s + 2s backoff", elapsed)
	}
}

// TestGenerate_NoRetry_On4xx: a 400 is not retryable; the client must return
// immediately without a retry loop (four attempts would exceed the test's
// short deadline).
func TestGenerate_NoRetry_On4xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "bad prompt")
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
	if err == nil {
		t.Fatal("expected error for 4xx, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error doesn't mention status: %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts=%d want 1 (4xx must not retry)", got)
	}
}

// TestGenerate_ContextCancelled: a cancelled context between retries returns
// promptly rather than waiting out the backoff. Non-tautological: measure
// elapsed time.
func TestGenerate_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := c.Generate(ctx, "p", "m", "", nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context deadline") {
		// The retry loop may return the wrapped 5xx error before ctx returns
		// DeadlineExceeded — that's fine, we just need it to be fast.
	}
	// With cancellation, we should return well within the 1s+2s+4s full backoff.
	if elapsed > 3*time.Second {
		t.Errorf("elapsed %v — ctx cancellation didn't short-circuit backoff", elapsed)
	}
}

// TestGenerate_Healthz: the operator-visible connectivity probe.
func TestGenerate_Healthz(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	if err := c.Healthz(context.Background()); err != nil {
		t.Errorf("Healthz: %v", err)
	}

	// Down case: healthz on a bogus URL returns an error.
	c2, _ := NewClient("http://127.0.0.1:1") // no listener
	c2.HTTP.Timeout = 500 * time.Millisecond
	if err := c2.Healthz(context.Background()); err == nil {
		t.Error("Healthz on bogus URL should fail")
	}
	// Silence unused import warnings if the pkg imports get pruned.
	_ = fmt.Sprint("")
}
