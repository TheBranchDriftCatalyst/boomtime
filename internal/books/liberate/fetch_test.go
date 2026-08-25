package liberate

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
)

// fetchCred builds a credential with a real RSA key so Sign() succeeds — the
// download path signs every request, so an unsigned-capable cred would make
// every test here fail for the wrong reason.
func fetchCred(t *testing.T) *amazon.DeviceCredential {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &amazon.DeviceCredential{
		AdpToken:         "adp-tok",
		DevicePrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		DeviceSerial:     tstSerial,
		CustomerID:       tstCustomer,
		Marketplace:      amazon.MarketplaceUS,
	}
}

func TestFetchWritesFileAndSignsRequest(t *testing.T) {
	payload := strings.Repeat("AAXC", 300000) // ~1.2 MB, spans multiple chunks
	var gotToken, gotAlg, gotSig string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("x-adp-token")
		gotAlg = r.Header.Get("x-adp-alg")
		gotSig = r.Header.Get("x-adp-signature")
		w.Header().Set("Content-Type", "audio/vnd.audible.aax")
		// Set explicitly: Go falls back to chunked encoding for a body this
		// large, and the real CDN does send a length — which is what the
		// short-read check depends on, so the test must exercise that path.
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "book.aaxc")
	var lastWritten, lastTotal int64
	n, err := Fetch(context.Background(), fetchCred(t), srv.URL+"/x.aaxc?Policy=abc", dest,
		func(written, total int64) { lastWritten, lastTotal = written, total })
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if n != int64(len(payload)) {
		t.Errorf("wrote %d bytes, want %d", n, len(payload))
	}

	// The 403-without-headers finding is the reason this assertion exists: if
	// the signing headers stop being attached, the real CDN rejects every
	// download and nothing else in the pipeline runs.
	if gotToken != "adp-tok" || gotAlg != "SHA256withRSA:1.0" || gotSig == "" {
		t.Errorf("download was not ADP-signed: token=%q alg=%q sig-empty=%v", gotToken, gotAlg, gotSig == "")
	}

	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(body) != payload {
		t.Errorf("file content mismatch (%d bytes on disk)", len(body))
	}
	// Progress must actually fire — it is what keeps the job heartbeating.
	if lastWritten != int64(len(payload)) {
		t.Errorf("final progress written = %d, want %d", lastWritten, len(payload))
	}
	if lastTotal != int64(len(payload)) {
		t.Errorf("progress total = %d, want the Content-Length %d", lastTotal, len(payload))
	}
}

// A body shorter than the advertised Content-Length must be a RETRYABLE short
// read and must NOT leave a file behind — a truncated audiobook that looks
// complete is the worst outcome this package can produce.
func TestFetchShortReadIsRetryableAndLeavesNoFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("way too short"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "book.aaxc")
	_, err := Fetch(context.Background(), fetchCred(t), srv.URL+"/x.aaxc", dest, nil)
	if !errors.Is(err, ErrShortRead) {
		t.Fatalf("err = %v, want ErrShortRead", err)
	}
	if _, statErr := os.Stat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("a truncated file was left on disk; it must be removed")
	}
}

// The real CDN answers 403 to an unsigned request. Whatever the cause, a non-2xx
// must fail loudly rather than writing an HTML error page into the library as if
// it were an audiobook.
func TestFetchNon2xxFailsWithoutWritingFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>AccessDenied</Code></Error>"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "book.aaxc")
	_, err := Fetch(context.Background(), fetchCred(t), srv.URL+"/x.aaxc", dest, nil)
	if err == nil {
		t.Fatal("want an error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v, want it to name the status", err)
	}
	if _, statErr := os.Stat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("an error page was written to the destination")
	}
}

// A server that sends no Content-Length gives us nothing to verify against, so
// whatever arrived is accepted — but the file must still be written correctly.
func TestFetchAcceptsUnknownContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Transfer-Encoding", "chunked")
		_, _ = w.Write([]byte("streamed-without-length"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "book.aaxc")
	var sawTotal int64 = 999
	n, err := Fetch(context.Background(), fetchCred(t), srv.URL+"/x.aaxc", dest,
		func(_, total int64) { sawTotal = total })
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if n != int64(len("streamed-without-length")) {
		t.Errorf("wrote %d bytes", n)
	}
	if sawTotal != -1 {
		t.Errorf("progress total = %d, want -1 for an unknown length", sawTotal)
	}
}

// Cancelling the job must actually stop the transfer; a 600 MB download that
// ignores cancellation holds a worker slot long after the job is gone.
func TestFetchHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000000")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		chunk := make([]byte, fetchBufSize)
		for i := 0; i < 200; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			if i == 1 {
				cancel() // cancel mid-transfer
			}
		}
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "book.aaxc")
	_, err := Fetch(ctx, fetchCred(t), srv.URL+"/x.aaxc", dest, nil)
	if err == nil {
		t.Fatal("want an error after cancellation")
	}
	if _, statErr := os.Stat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("cancelled download left a partial file behind")
	}
}

func TestFetchInputValidation(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "book.aaxc")
	if _, err := Fetch(context.Background(), nil, "https://example.invalid/x", dest, nil); !errors.Is(err, amazon.ErrNotRegistered) {
		t.Errorf("nil cred: err = %v, want ErrNotRegistered", err)
	}
	if _, err := Fetch(context.Background(), fetchCred(t), "", dest, nil); err == nil {
		t.Error("empty url accepted")
	}
}
