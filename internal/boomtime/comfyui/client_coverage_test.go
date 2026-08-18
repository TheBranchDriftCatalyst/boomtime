// client_coverage_test.go — additional ginkgo specs covering the branches
// that client_test.go doesn't exercise, taking the package coverage from
// ~69% to >=90% (gaka-d6x).
//
// Cases pin NAMED INVARIANTS on the wire contract, security-relevant
// framing (raw-bytes MIME sniffing on adversarial payloads), and the
// state-machine boundaries between retryable vs. non-retryable failures.
package comfyui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// jpegHeader / webpHeader / gifHeader — precise magic-number prefixes used
// to prove sniffMime picks the right MIME on raw bytes without relying on
// filename or Content-Type hints (a caller-provided oracle would be a
// security smell for user-uploaded media).
func jpegHeader() []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
}

func webpHeader() []byte {
	// RIFF....WEBPVP8...
	return []byte{'R', 'I', 'F', 'F', 0x24, 0x00, 0x00, 0x00, 'W', 'E', 'B', 'P', 'V', 'P', '8', ' '}
}

// gifHeader is deliberately UNSUPPORTED — sniffMime must fall back to the
// safe default (image/png) rather than "guess GIF" without a positive
// match. Pinning this invariant guards against a future contributor
// silently expanding the sniff table without updating the caller
// (openai-shaped shim only claims png/jpeg/webp today).
func gifHeader() []byte {
	return []byte{'G', 'I', 'F', '8', '9', 'a', 0x00, 0x00}
}

// ---- NewClient (edge cases beyond the two in client_test.go) --------

var _ = Describe("NewClient (extended)", func() {
	It("trims trailing slash so URL concat produces a single-slash path (invariant: no // in outbound URL)", func() {
		c, err := NewClient("http://example.com:8012/")
		Expect(err).NotTo(HaveOccurred())
		Expect(c.URL).To(Equal("http://example.com:8012"))
	})

	It("trims surrounding whitespace before scheme check (operator ergonomics)", func() {
		c, err := NewClient("   http://localhost:8012   ")
		Expect(err).NotTo(HaveOccurred())
		Expect(c.URL).To(Equal("http://localhost:8012"))
	})

	It("whitespace-only URL is treated identically to empty (feature disabled, no error)", func() {
		c, err := NewClient("   \t \n  ")
		Expect(err).NotTo(HaveOccurred())
		Expect(c).To(BeNil())
	})

	It("rejects ftp:// (not just missing-scheme) — invariant: only http/https", func() {
		_, err := NewClient("ftp://example.com/")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("scheme"))
	})

	It("https:// URL is accepted (invariant: TLS transport supported)", func() {
		c, err := NewClient("https://shim.example.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(c.URL).To(Equal("https://shim.example.com"))
	})
})

// ---- Healthz (state-machine: 200 vs. non-200 vs. transport error) ---

var _ = Describe("Healthz (extended)", func() {
	It("returns nil-client error when called on a nil *Client (feature-disabled invariant)", func() {
		var c *Client
		err := c.Healthz(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("feature disabled"))
	})

	It("surfaces body snippet on non-200 healthz (operator debugging invariant)", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "shim is loading models")
		}))
		defer srv.Close()

		c, _ := NewClient(srv.URL)
		err := c.Healthz(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("503"))
		Expect(err.Error()).To(ContainSubstring("shim is loading models"))
	})

	It("caps error body to 512 bytes so a wedged shim can't OOM the operator log", func() {
		big := strings.Repeat("A", 4096)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, big)
		}))
		defer srv.Close()

		c, _ := NewClient(srv.URL)
		err := c.Healthz(context.Background())
		Expect(err).To(HaveOccurred())
		// The error message must contain some 'A's, but not the entire
		// 4096-char payload — the LimitReader is 512.
		count := strings.Count(err.Error(), "A")
		Expect(count).To(BeNumerically(">", 0))
		Expect(count).To(BeNumerically("<=", 512))
	})

	It("errors on NewRequest failure via an invalid URL character (control-char in path) — pins the URL-parse branch, not just 'any error'", func() {
		// Force http.NewRequestWithContext to fail: the URL is checked at
		// req construction time. A control char in the URL trips the URL
		// parser inside http.NewRequestWithContext with a specific sentinel
		// string ("invalid control character in URL"). Asserting on that
		// substring ties the test to the actual failure mode — a regression
		// that moved URL validation elsewhere (or converted this into a
		// transport-phase error) would surface a different message and fail
		// this spec loudly.
		c := &Client{URL: "http://example.com/\x7f", HTTP: &http.Client{}}
		err := c.Healthz(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid control character"))
	})
})

// ---- Generate — input validation & nil receiver -----

var _ = Describe("Generate (input validation)", func() {
	It("nil receiver errors with feature-disabled sentinel — invariant: no panic on the disabled path", func() {
		var c *Client
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("feature disabled"))
	})

	It("empty prompt rejected before any HTTP call (invariant: no wasted shim time on bad inputs)", func() {
		var called atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			called.Add(1)
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "   ", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("empty prompt"))
		Expect(called.Load()).To(BeEquivalentTo(0))
	})

	It("empty model rejected before any HTTP call", func() {
		var called atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			called.Add(1)
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "\t", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("empty model"))
		Expect(called.Load()).To(BeEquivalentTo(0))
	})

	It("empty size defaults to 1024x1024 on the wire (invariant: preserves pre-override behavior)", func() {
		var gotSize string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotSize, _ = body["size"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngBytesGinkgo())}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "m", "   ", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(gotSize).To(Equal("1024x1024"))
	})

	It("seed is threaded to the wire when non-nil (invariant: deterministic seed pass-through)", func() {
		var gotSeed *float64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if v, ok := body["seed"].(float64); ok {
				gotSeed = &v
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngBytesGinkgo())}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		s := int64(424242)
		_, _, err := c.Generate(context.Background(), "p", "m", "512x512", &s)
		Expect(err).NotTo(HaveOccurred())
		Expect(gotSeed).NotTo(BeNil())
		Expect(int64(*gotSeed)).To(Equal(int64(424242)))
	})

	It("nil seed is OMITTED from the JSON body (invariant: omitempty for random-per-call)", func() {
		var raw []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ = io.ReadAll(r.Body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngBytesGinkgo())}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "m", "512x512", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("seed"))
	})
})

// ---- Generate — response-decoding branches -----

var _ = Describe("Generate (response decoding)", func() {
	It("errors when response has zero data items (invariant: empty data is not a silent success)", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"created": time.Now().Unix(),
				"data":    []map[string]string{},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no data"))
	})

	It("errors when a data item has neither b64_json nor url (invariant: no oracle-driven ambiguity)", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"revised_prompt": "cats"}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("neither b64_json nor url"))
	})

	It("errors on undecodable JSON body (invariant: garbage in => loud failure, not a panic)", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "{this is not: json")
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("decode"))
	})

	It("errors when b64_json field is not valid base64 (invariant: shim malfunction surfaces, not silently empty bytes)", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"b64_json": "!!!not@@base64###"}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("b64 decode"))
	})

	It("errors when url payload is not a data: URL (invariant: no http(s) fetch on a foreign origin)", func() {
		// Security-critical: if the shim ever emits a full external URL, we
		// MUST NOT silently fetch it (SSRF vector). Reject and surface.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"url": "https://evil.example.com/image.png"}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsupported URL scheme"))
	})

	It("errors on a data: URL missing the comma separator (invariant: strict framing)", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"url": "data:image/png;base64<no-comma>payload"}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("malformed data URL"))
	})

	It("errors when a data: URL has invalid base64 after the comma (invariant: no partial-decode)", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"url": "data:image/png;base64,@@@not-base64@@@"}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("data URL b64 decode"))
	})

	It("data URL with NO metadata segment defaults to image/png (invariant: safe fallback mime)", func() {
		payload := base64.StdEncoding.EncodeToString(pngBytesGinkgo())
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"url": "data:," + payload}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, mime, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(mime).To(Equal("image/png"))
	})

	It("data URL with metadata but NO semicolon uses the whole meta as mime (invariant: 'data:image/webp,<b64>' works)", func() {
		payload := base64.StdEncoding.EncodeToString(webpHeader())
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"url": "data:image/webp," + payload}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, mime, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(mime).To(Equal("image/webp"))
	})
})

// ---- Generate — retry state machine (transport-layer failures) ------

var _ = Describe("Generate (retry state machine)", func() {
	It("retries on a dial error (connection refused) then succeeds via URL swap (invariant: dial-only retry)", func() {
		// Start a normal server, then simulate a shim restart: on attempts
		// 1 and 2 the URL points at a dead port, on attempt 3 the client
		// gets flipped to a working server. Emulates the launchd-restart
		// blip the docstring describes. Rather than swap URLs mid-flight,
		// we run against a fresh server that will refuse the FIRST N
		// connections via a listener that closes them.
		want := pngBytesGinkgo()

		// Simple approach: use a dedicated http.RoundTripper that fails
		// with a dial error for the first N calls, then succeeds.
		good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(want)}},
			})
		}))
		defer good.Close()

		c, _ := NewClient(good.URL)
		c.HTTP.Transport = &countingDialTransport{
			failN: 2,
			inner: c.HTTP.Transport,
		}
		c.HTTP.Timeout = 10 * time.Second

		got, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(want))
	})

	It("does NOT retry on a non-dial transport error (e.g. header timeout / body read) — matches doc invariant", func() {
		// Simulate a "shim accepted the request but never sends headers"
		// wedge: return a canned non-dial network error immediately.
		var attempts atomic.Int32
		c := &Client{URL: "http://example.com", HTTP: &http.Client{
			Transport: roundTripFn(func(_ *http.Request) (*http.Response, error) {
				attempts.Add(1)
				// Non-dial error: pretend we already got past dial (Op !=
				// "dial") — a body-phase problem.
				return nil, &net.OpError{Op: "read", Err: errors.New("simulated response-phase failure")}
			}),
		}}
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("simulated response-phase failure"))
		Expect(attempts.Load()).To(BeEquivalentTo(1))
	})

	It("gives up after exactly len(backoffs)+1 attempts on persistent 5xx (invariant: bounded retry)", func() {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "upstream dead")
		}))
		defer srv.Close()

		// Override backoffs indirectly by shortening the Timeout so the
		// full 1+2+4=7s doesn't stretch the suite. We still exercise the
		// full attempt count.
		c, _ := NewClient(srv.URL)
		c.HTTP.Timeout = 15 * time.Second

		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("gave up after 4 attempts"))
		Expect(attempts.Load()).To(BeEquivalentTo(4))
	})

	It("pre-cancelled ctx: first attempt runs and returns fast, no retries scheduled (invariant: attempt 0 not gated by ctx.Done())", func() {
		// The retry loop only checks ctx.Done() BETWEEN attempts (attempt>0),
		// so attempt 0 always runs — the cancelled ctx short-circuits via
		// http.Client.Do returning ctx.Err() immediately, and then the retry
		// loop's ctx.Done() branch prevents attempts 1..3. Assert on the
		// attempts counter to prove the retry-guard fired rather than relying
		// on a loose wall-clock bound (a fast httptest server could complete
		// 4 iterations inside 2s if the guard ever broke).
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancelled up front
		c, _ := NewClient(srv.URL)

		start := time.Now()
		_, _, err := c.Generate(ctx, "p", "m", "", nil)
		elapsed := time.Since(start)

		Expect(err).To(HaveOccurred())
		// Attempt 0 fires (transport returns ctx.Err()); retry loop's
		// ctx.Done() short-circuits before attempts 1-3. Depending on timing
		// of the httptest server startup, the server MAY or MAY NOT actually
		// observe attempt 0 (net/http bails early on cancelled ctx). Either
		// way, we must see at most 1 server-side hit (proving retries were
		// suppressed).
		Expect(attempts.Load()).To(BeNumerically("<=", 1))
		Expect(elapsed).To(BeNumerically("<", 500*time.Millisecond))
	})

	It("errors on NewRequestWithContext failure via control-char in base URL — pins the URL-parse branch specifically", func() {
		// Same reasoning as the Healthz spec above: assert on the URL parser's
		// sentinel string so we're pinning the URL-parse branch, not just
		// "any error happened somewhere downstream".
		c := &Client{URL: "http://example.com/\x7f", HTTP: &http.Client{}}
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid control character"))
	})
})

// ---- sniffMime — raw-bytes matcher, no oracle -----

var _ = Describe("sniffMime (raw bytes)", func() {
	It("PNG magic → image/png (invariant: strict 8-byte prefix)", func() {
		Expect(sniffMime([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 'x'})).To(Equal("image/png"))
	})

	It("JPEG magic (0xFFD8FF) → image/jpeg", func() {
		Expect(sniffMime(jpegHeader())).To(Equal("image/jpeg"))
	})

	It("WEBP framing (RIFF....WEBP) → image/webp", func() {
		Expect(sniffMime(webpHeader())).To(Equal("image/webp"))
	})

	It("unknown/GIF payload → falls back to image/png (safe default)", func() {
		Expect(sniffMime(gifHeader())).To(Equal("image/png"))
	})

	It("empty payload → image/png (no panic on zero-length)", func() {
		Expect(sniffMime(nil)).To(Equal("image/png"))
		Expect(sniffMime([]byte{})).To(Equal("image/png"))
	})

	It("truncated JPEG magic (only 2 bytes: 0xFF 0xD8) → falls through to default, NOT image/jpeg (invariant: full 3-byte prefix required)", func() {
		// This is the definitive full-prefix-required test — it uses JPEG
		// magic (which requires 3 bytes) so the fallback (image/png) is
		// OBSERVABLY DIFFERENT from a mistaken match (image/jpeg). If a
		// regression relaxed the `len(b) >= 3` guard to `len(b) >= 2`, this
		// spec would flip to image/jpeg and fail. Compare with the truncated
		// PNG case below — that one can't distinguish "fell through" from
		// "matched" because default == PNG.
		short := []byte{0xFF, 0xD8}
		Expect(sniffMime(short)).To(Equal("image/png"))
	})

	It("truncated PNG magic (only 7 bytes) → does not panic; returns default (invariant: len>=8 guard holds; observable through NON-panic + default fallback)", func() {
		// The default fallback happens to also be image/png, so this test
		// pins ONLY "doesn't panic" and "returns default" — it cannot
		// distinguish "matched by prefix" from "fell through to default".
		// The truncated-JPEG spec above is the one that proves the
		// full-prefix invariant. This spec is kept for the no-panic
		// invariant on len=7 (regression that dereferenced b[7] without a
		// guard would crash).
		short := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A}
		Expect(func() { _ = sniffMime(short) }).NotTo(Panic())
		Expect(sniffMime(short)).To(Equal("image/png"))
	})

	It("truncated WEBP framing (only 11 bytes, one short of the check) → falls through to default, NOT image/webp (invariant: len>=12 guard)", func() {
		// Symmetric coverage: WEBP needs 12 bytes; give it 11 and prove we
		// don't accidentally return image/webp. Default is image/png, WEBP
		// mistake would return image/webp — observably distinct.
		short := []byte{'R', 'I', 'F', 'F', 0x00, 0x00, 0x00, 0x00, 'W', 'E', 'B'}
		Expect(sniffMime(short)).To(Equal("image/png"))
	})

	It("RIFF prefix but wrong body (not WEBP) does NOT report webp (invariant: both anchors required)", func() {
		wav := []byte{'R', 'I', 'F', 'F', 0x00, 0x00, 0x00, 0x00, 'W', 'A', 'V', 'E'}
		Expect(sniffMime(wav)).To(Equal("image/png"))
	})
})

// ---- isDialError — classification of transport errors --------------

var _ = Describe("isDialError (transport classification)", func() {
	It("nil → false (invariant: no false-positive on success)", func() {
		Expect(isDialError(nil)).To(BeFalse())
	})

	It("net.OpError with Op=='dial' → true", func() {
		e := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
		Expect(isDialError(e)).To(BeTrue())
	})

	It("net.OpError with Op=='read' → false (response-phase, not dial)", func() {
		e := &net.OpError{Op: "read", Err: errors.New("something")}
		Expect(isDialError(e)).To(BeFalse())
	})

	It("wrapped string 'connection refused' → true (fallback sniff)", func() {
		Expect(isDialError(errors.New("Get http://x: dial tcp: connection refused"))).To(BeTrue())
	})

	It("wrapped string 'no route to host' → true", func() {
		Expect(isDialError(errors.New("no route to host"))).To(BeTrue())
	})

	It("wrapped string 'no such host' → true", func() {
		Expect(isDialError(errors.New("lookup shim: no such host"))).To(BeTrue())
	})

	It("wrapped 'dial ... i/o timeout' → true (dial-phase TCP timeout)", func() {
		Expect(isDialError(errors.New("dial tcp 10.0.0.1:8012: i/o timeout"))).To(BeTrue())
	})

	It("'i/o timeout' WITHOUT 'dial' → false (response-phase timeout, not dial)", func() {
		Expect(isDialError(errors.New("read tcp: i/o timeout"))).To(BeFalse())
	})

	It("unrelated error string → false (invariant: no false positives on random text)", func() {
		Expect(isDialError(errors.New("something else entirely"))).To(BeFalse())
	})
})

// ---- Sniff-through-Generate — invariant: real-image bytes propagate ---

var _ = Describe("Generate MIME propagation", func() {
	It("returns image/jpeg when the shim payload starts with JPEG magic (raw-bytes sniff, no header oracle)", func() {
		want := jpegHeader()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(want)}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		got, mime, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Equal(got, want)).To(BeTrue())
		Expect(mime).To(Equal("image/jpeg"))
	})

	It("returns image/webp when payload starts with WEBP framing", func() {
		want := webpHeader()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(want)}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		got, mime, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Equal(got, want)).To(BeTrue())
		Expect(mime).To(Equal("image/webp"))
	})
})

// ---- Missing invariants (added per code-review critique) ------------

var _ = Describe("Generate (wire-contract invariants)", func() {
	It("outbound POST carries Content-Type: application/json (invariant: proxies/shims may reject a JSON POST without it)", func() {
		var gotCT string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotCT = r.Header.Get("Content-Type")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngBytesGinkgo())}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(gotCT).To(Equal("application/json"))
	})

	It("outbound URL path is EXACTLY /v1/images/generations when base URL has trailing slash (invariant: no // path-doubling)", func() {
		// Security-relevant: path-doubling defeats CDN and reverse-proxy
		// routing rules. NewClient trims the trailing slash so concat is
		// always clean; if that trim regressed, `srv.URL+"/" + "/v1/..."`
		// would produce "//v1/images/generations" and this handler would
		// see r.URL.Path == "//v1/images/generations" instead.
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngBytesGinkgo())}},
			})
		}))
		defer srv.Close()

		// Deliberately pass srv.URL with a trailing slash to prove the trim.
		c, err := NewClient(srv.URL + "/")
		Expect(err).NotTo(HaveOccurred())
		_, _, err = c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(gotPath).To(Equal("/v1/images/generations"))
		Expect(gotPath).NotTo(HavePrefix("//"))
	})

	It("Healthz outbound URL path is EXACTLY /healthz when base URL has trailing slash (invariant: no // path-doubling on health probe)", func() {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}))
		defer srv.Close()

		c, err := NewClient(srv.URL + "/")
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Healthz(context.Background())).To(Succeed())
		Expect(gotPath).To(Equal("/healthz"))
		Expect(gotPath).NotTo(HavePrefix("//"))
	})
})

var _ = Describe("Generate (retry backoff TIMING invariant)", func() {
	// Named invariant: backoffs are 1s, 2s, 4s (exponential). If a regression
	// hardcoded {100ms, 100ms, 100ms}, no existing test would fail — this
	// spec pins the timing floor at 1+2+4=7s for the 4-attempt case.
	It("persistent 5xx elapses ≥7s across 4 attempts (invariant: 1s+2s+4s exponential backoff)", func() {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "upstream dead")
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		c.HTTP.Timeout = 15 * time.Second

		start := time.Now()
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		elapsed := time.Since(start)

		Expect(err).To(HaveOccurred())
		Expect(attempts.Load()).To(BeEquivalentTo(4))
		// 1s + 2s + 4s = 7s minimum wall-clock between-attempt sleep, PLUS
		// four fast HTTP round-trips. Allow a small tolerance (200ms) for
		// scheduling jitter — the invariant is "backoffs actually occurred",
		// which requires ≥ ~6.8s in practice.
		Expect(elapsed).To(BeNumerically(">=", 6800*time.Millisecond))
	})
})

var _ = Describe("Generate (header-timeout does NOT retry — real transport path)", func() {
	// Complements the synthetic OpError{Op:"read"} spec: exercises the REAL
	// http.Transport.ResponseHeaderTimeout code path. A shim that accepts
	// TCP but never flushes headers must fail once (not retry).
	It("wedged server (headers never flushed) fails after one attempt when ResponseHeaderTimeout fires — no retry", func() {
		var attempts atomic.Int32
		// Use an explicit unblock channel so srv.Close() below does NOT
		// deadlock waiting for the handler. httptest.Server.Close() waits
		// on all in-flight handlers; if we only block on r.Context().Done()
		// the handler is stuck until we tell it to unwind — the client
		// giving up on the response does NOT cancel the server-side request
		// context in the httptest transport.
		unblock := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			// Block until either the client hangs up (real ctx cancel) OR
			// the test unblocks us via close(unblock).
			select {
			case <-r.Context().Done():
			case <-unblock:
			}
		}))
		defer func() {
			close(unblock) // release the stuck handler so srv.Close() returns
			srv.Close()
		}()

		c, _ := NewClient(srv.URL)
		// Shorten header timeout so the spec is fast; retain the invariant.
		c.HTTP.Transport = &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   2 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: 250 * time.Millisecond,
		}
		c.HTTP.Timeout = 5 * time.Second

		start := time.Now()
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		elapsed := time.Since(start)

		Expect(err).To(HaveOccurred())
		// Invariant: exactly ONE attempt — header timeout is a response-phase
		// failure, isDialError returns false, retry loop bails.
		Expect(attempts.Load()).To(BeEquivalentTo(1))
		// Sanity: elapsed is bounded well below the full 1s+2s+4s backoff
		// (proves no retries happened, in addition to the attempts counter).
		Expect(elapsed).To(BeNumerically("<", 2*time.Second))
	})
})

// ---- Security gaps (added per code-review critique) -----------------

var _ = Describe("Generate (SSRF hardening — table-driven bad schemes in shim response)", func() {
	// The url-field parser in doOne only accepts a "data:" prefix. Any other
	// scheme MUST be rejected, not fetched. Cover a range of dangerous
	// schemes so a future regression that changed the prefix check (e.g. to
	// case-insensitive, or that accepted `data:` case-variants like `DATA:`
	// via a strings.EqualFold refactor) is caught.
	badSchemes := []struct {
		name string
		url  string
	}{
		{"file:// (local filesystem)", "file:///etc/passwd"},
		{"gopher:// (legacy exfil vector)", "gopher://evil.example.com:70/_junk"},
		{"ftp:// (credential-in-URL exfil)", "ftp://user:pass@evil.example.com/x"},
		{"http:// external (SSRF classic)", "http://169.254.169.254/latest/meta-data/"},
		{"https:// external", "https://evil.example.com/image.png"},
		{"DATA: (case-variant — data: prefix is case-sensitive)", "DATA:image/png;base64,AAAA"},
		{"Data: (title-case)", "Data:image/png;base64,AAAA"},
		{" data: (leading whitespace)", " data:image/png;base64,AAAA"},
		{"javascript: (XSS vector if ever rendered)", "javascript:alert(1)"},
		{"empty string is neither b64_json nor url (falls through to different error)", ""},
	}
	for _, tc := range badSchemes {
		tc := tc
		It("rejects "+tc.name+" — invariant: only data: (lowercase, no leading ws) is accepted", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]string{{"url": tc.url}},
				})
			}))
			defer srv.Close()
			c, _ := NewClient(srv.URL)
			_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
			Expect(err).To(HaveOccurred())
			// The error must be EITHER "unsupported URL scheme" (non-empty,
			// non-data:) OR "neither b64_json nor url" (empty). It must NEVER
			// be nil (which would imply we tried to fetch or decoded garbage).
			msg := err.Error()
			okScheme := strings.Contains(msg, "unsupported URL scheme")
			okNeither := strings.Contains(msg, "neither b64_json nor url")
			Expect(okScheme || okNeither).To(BeTrue(),
				"expected 'unsupported URL scheme' or 'neither b64_json nor url', got: %s", msg)
		})
	}
})

var _ = Describe("Generate (log-injection surface on unsupported URL scheme)", func() {
	// The unsupported-scheme error reflects the first 30 chars of the
	// attacker-supplied URL via %.30s. That's already a bounded truncation,
	// so log-injection is limited — but we pin the bound so a future change
	// that widened it to %s (unbounded) is caught. We also assert control
	// chars are surfaced as-is (Go's fmt doesn't sanitize them, so the
	// downstream logger is responsible for escaping — this is documented
	// behavior we don't want to silently change).
	It("caps reflected URL at 30 chars in the error message (invariant: bounded log-injection surface)", func() {
		// 100-char attacker-controlled URL prefix with control chars mixed in.
		attacker := "notascheme://" + strings.Repeat("A", 87) + "\x1b[31mRED"
		Expect(len(attacker)).To(BeNumerically(">=", 100))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"url": attacker}},
			})
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("unsupported URL scheme"))
		// The full 100-char attacker string must NOT appear verbatim — %.30s
		// truncates. The ANSI escape sequence sits past the 30-char boundary,
		// so it must NOT appear in the message.
		Expect(msg).NotTo(ContainSubstring("\x1b[31mRED"))
		Expect(msg).NotTo(ContainSubstring(attacker))
		// The FIRST 30 chars of the attacker URL WILL appear (that's the
		// documented behavior — the operator needs enough context to
		// diagnose). Prove it: "notascheme://" is 13 chars, so the first
		// 30 chars = "notascheme://" + 17 A's.
		Expect(msg).To(ContainSubstring("notascheme://" + strings.Repeat("A", 17)))
	})
})

// ---- Test helpers (must not be top-level in a production build) -----

// roundTripFn adapts a function into a http.RoundTripper.
type roundTripFn func(*http.Request) (*http.Response, error)

func (f roundTripFn) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// countingDialTransport wraps another RoundTripper and returns a synthetic
// net.OpError{Op:"dial"} for the first `failN` calls, then delegates.
// Used to simulate a shim that's still coming up.
type countingDialTransport struct {
	failN int
	n     atomic.Int32
	inner http.RoundTripper
}

func (t *countingDialTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	n := t.n.Add(1)
	if int(n) <= t.failN {
		return nil, &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: fmt.Errorf("connection refused"),
		}
	}
	return t.inner.RoundTrip(r)
}
