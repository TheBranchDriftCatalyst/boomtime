// client_ginkgo_test.go — ginkgo mirror of client_test.go (gaka-0vp).
// 1:1 case map (7 stdlib TestXxx):
//   TestNewClient_EmptyURL           → NewClient > "empty URL is a no-op"
//   TestNewClient_MissingScheme      → NewClient > "missing scheme fails loudly"
//   TestGenerate_Success_B64JSON     → Generate > "success (b64_json shape)"
//   TestGenerate_Success_DataURL     → Generate > "success (data URL shape)"
//   TestGenerate_Retries_On5xx       → Generate > "retries with backoff on 5xx"
//   TestGenerate_NoRetry_On4xx       → Generate > "no retry on 4xx"
//   TestGenerate_ContextCancelled    → Generate > "context cancel short-circuits backoff"
//   TestGenerate_Healthz             → Healthz > 2 Its
package comfyui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// pngBytesGinkgo returns 12 bytes with the PNG magic-number prefix.
func pngBytesGinkgo() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 't', 'i', 'n', 'y'}
}

var _ = Describe("NewClient", func() {
	It("returns (nil, nil) on empty URL (feature disabled)", func() {
		c, err := NewClient("")
		Expect(err).NotTo(HaveOccurred())
		Expect(c).To(BeNil())
	})

	It("errors on missing scheme (operator typo)", func() {
		_, err := NewClient("localhost:8012")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Generate", func() {
	It("succeeds with a b64_json response (happy path)", func() {
		want := pngBytesGinkgo()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.URL.Path).To(Equal("/v1/images/generations"))
			var body map[string]any
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			Expect(body["model"]).To(Equal("flux_schnell_fast"))
			Expect(body["prompt"]).To(Equal("a distinctive emblem"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"created": time.Now().Unix(),
				"data":    []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(want)}},
			})
		}))
		defer srv.Close()

		c, err := NewClient(srv.URL)
		Expect(err).NotTo(HaveOccurred())
		got, mime, err := c.Generate(context.Background(), "a distinctive emblem", "flux_schnell_fast", "", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(want))
		Expect(mime).To(Equal("image/png"))
	})

	It("succeeds with a data-URL response shape", func() {
		want := pngBytesGinkgo()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(want)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"url": url}},
			})
		}))
		defer srv.Close()

		c, _ := NewClient(srv.URL)
		got, mime, err := c.Generate(context.Background(), "prompt", "model", "", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(want))
		Expect(mime).To(Equal("image/png"))
	})

	It("retries with 1s+2s backoff on 5xx (attempts=3, elapsed≥3s)", func() {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n := attempts.Add(1)
			if n < 3 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, "temporarily unavailable")
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngBytesGinkgo())}},
			})
		}))
		defer srv.Close()

		c, _ := NewClient(srv.URL)
		c.HTTP.Timeout = 5 * time.Second

		start := time.Now()
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		elapsed := time.Since(start)
		Expect(err).NotTo(HaveOccurred())
		Expect(attempts.Load()).To(BeEquivalentTo(3))
		Expect(elapsed).To(BeNumerically(">=", 3*time.Second))
	})

	It("does NOT retry on 4xx", func() {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "bad prompt")
		}))
		defer srv.Close()

		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("400"))
		Expect(attempts.Load()).To(BeEquivalentTo(1))
	})

	It("context cancel short-circuits backoff", func() {
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
		Expect(err).To(HaveOccurred())
		// The retry loop may return the wrapped 5xx before ctx does. Either
		// way, elapsed must be well under the full 1s+2s+4s backoff.
		Expect(elapsed).To(BeNumerically("<", 3*time.Second))
		_ = errors.Is
	})
})

var _ = Describe("Healthz", func() {
	It("succeeds against a live /healthz", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}))
		defer srv.Close()

		c, _ := NewClient(srv.URL)
		Expect(c.Healthz(context.Background())).To(Succeed())
	})

	It("fails against an unreachable URL", func() {
		c, _ := NewClient("http://127.0.0.1:1")
		c.HTTP.Timeout = 500 * time.Millisecond
		Expect(c.Healthz(context.Background())).To(HaveOccurred())
		_ = strings.Contains // preserve import symmetry with stdlib file
	})
})
