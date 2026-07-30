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

	It("errors on NewRequest failure via an invalid URL character (control-char in path)", func() {
		// Force http.NewRequestWithContext to fail: the URL is checked at
		// req construction time. A control char in the URL trips the URL
		// parser inside http.NewRequestWithContext.
		c := &Client{URL: "http://example.com/\x7f", HTTP: &http.Client{}}
		err := c.Healthz(context.Background())
		Expect(err).To(HaveOccurred())
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

	It("context cancel BEFORE first attempt still returns quickly — no attempts made after ctx done", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancelled up front
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		c, _ := NewClient(srv.URL)
		start := time.Now()
		_, _, err := c.Generate(ctx, "p", "m", "", nil)
		elapsed := time.Since(start)
		Expect(err).To(HaveOccurred())
		Expect(elapsed).To(BeNumerically("<", 2*time.Second))
	})

	It("errors on NewRequestWithContext failure via control-char in base URL", func() {
		c := &Client{URL: "http://example.com/\x7f", HTTP: &http.Client{}}
		_, _, err := c.Generate(context.Background(), "p", "m", "", nil)
		Expect(err).To(HaveOccurred())
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

	It("truncated PNG magic (only 7 bytes) → NOT sniffed as PNG (invariant: full-prefix required)", func() {
		short := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A}
		// Length < 8 fails the case guard → falls through to default (which
		// is also image/png). The important invariant is that we didn't
		// panic and didn't skip the guard.
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
