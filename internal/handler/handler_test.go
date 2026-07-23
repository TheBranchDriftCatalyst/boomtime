package handler_test

// gaka-bi2: exercises BindJSONWithLimit, the per-handler body-size cap wrapper
// used to shut down argon2/DoS amplification on authed writes (change-password
// was the motivating case — a 10 MiB body pinned ~256 MiB per verify).
//
// Two scenarios matter:
//
//   1. Happy path — an under-cap body binds normally and the handler runs.
//   2. Over-limit — the request Body read fails before json.Decode has to
//      allocate the tail, we respond 413 Payload Too Large, and NO Bind side
//      effect landed on the destination struct. That "never populated" check
//      is the load-bearing bit: it proves the cap fired BEFORE the full body
//      was materialized (otherwise the struct would carry the parsed prefix).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/labstack/echo/v5"
)

// tinyPayload mirrors the change-password body shape (two short strings). It
// stays tiny so the happy-path body sails well under BodyLimitSmall.
type tinyPayload struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// runBindLimit routes a single request through Echo, invoking BindJSONWithLimit
// against dst with the given limit. Returns the recorder + whether the bind
// succeeded (nil apierr).
func runBindLimit(t *testing.T, body []byte, dst any, limit int64) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	e := echo.New()
	var bound bool
	e.POST("/test", func(c *echo.Context) error {
		if aerr := handler.BindJSONWithLimit(c, dst, limit); aerr != nil {
			return aerr.Write(c)
		}
		bound = true
		return c.NoContent(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec, bound
}

// TestBindJSONWithLimit_HappyPath: an under-cap body binds cleanly, the handler
// runs, and the response is a 204 (proving BindJSONWithLimit didn't shadow the
// downstream code).
func TestBindJSONWithLimit_HappyPath(t *testing.T) {
	body, err := json.Marshal(tinyPayload{
		CurrentPassword: "test1234",
		NewPassword:     "test5678",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var dst tinyPayload
	rec, ok := runBindLimit(t, body, &dst, handler.BodyLimitSmall)
	if !ok {
		t.Fatalf("bind should succeed for under-cap body; got 413/400")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("status %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if dst.CurrentPassword != "test1234" || dst.NewPassword != "test5678" {
		t.Errorf("dst not bound: %+v", dst)
	}
}

// TestBindJSONWithLimit_OverLimit: a body larger than the cap yields 413 with
// the exact envelope {"error":"payload too large","message":"limit=<N>"} and
// the destination struct is left ZERO — proving the reader failed BEFORE the
// json decode could populate any prefix of the body.
func TestBindJSONWithLimit_OverLimit(t *testing.T) {
	// 5 KiB of 'a' inside a JSON string, well over BodyLimitSmall (4 KiB).
	// The prefix is valid JSON up to the `"currentPassword":"aaa...` chunk,
	// so if MaxBytesReader ever leaked, dst.CurrentPassword would carry the
	// truncated prefix. We assert it stays empty.
	big := strings.Repeat("a", 5000)
	body := []byte(`{"currentPassword":"` + big + `","newPassword":"test5678"}`)
	if int64(len(body)) <= handler.BodyLimitSmall {
		t.Fatalf("body (%d) must exceed cap (%d) for this test to mean anything", len(body), handler.BodyLimitSmall)
	}
	var dst tinyPayload
	rec, ok := runBindLimit(t, body, &dst, handler.BodyLimitSmall)
	if ok {
		t.Fatalf("bind unexpectedly succeeded for over-cap body")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	// Verify the response envelope carries the sentinel error text so the FE
	// can distinguish this from a generic 400.
	var envelope struct {
		Error   string  `json:"error"`
		Message *string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, rec.Body.String())
	}
	if envelope.Error != "payload too large" {
		t.Errorf("envelope.error = %q, want %q", envelope.Error, "payload too large")
	}
	if envelope.Message == nil || !strings.Contains(*envelope.Message, "limit=") {
		t.Errorf("envelope.message = %v, want limit=... hint", envelope.Message)
	}
	// LOAD-BEARING: dst must be zero. If MaxBytesReader had let the decode
	// eat the whole body, dst.CurrentPassword would carry the giant 'a'
	// prefix.
	if dst.CurrentPassword != "" || dst.NewPassword != "" {
		t.Errorf("dst populated on over-limit body — MaxBytesReader leaked: %+v", dst)
	}
}

// TestBindJSONWithLimit_MalformedJSON: a syntactically-broken body under the
// cap returns 400 (not 413) — the wrapper must preserve the existing
// "Invalid request body" contract for non-size errors.
func TestBindJSONWithLimit_MalformedJSON(t *testing.T) {
	body := []byte(`{"currentPassword": "test1234",`) // trailing comma, no closing brace
	var dst tinyPayload
	rec, ok := runBindLimit(t, body, &dst, handler.BodyLimitSmall)
	if ok {
		t.Fatalf("bind unexpectedly succeeded for malformed JSON")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// panicReader wraps a bytes.Reader and PANICS when total bytes read exceed
// `cap`. Used to prove — non-tautologically — that BindJSONWithLimit never lets
// json.Decoder read past the configured cap. If MaxBytesReader silently allowed
// the full body through, this panic would fire and the test would crash.
// With the cap in place, MaxBytesReader returns an error at exactly cap+1
// bytes, the Decoder stops, and no panic ever fires.
type panicReader struct {
	src  *bytes.Reader
	read int64
	cap  int64
	t    *testing.T
}

func (p *panicReader) Read(b []byte) (int, error) {
	n, err := p.src.Read(b)
	p.read += int64(n)
	if p.read > p.cap {
		p.t.Fatalf("panicReader: read %d bytes; cap is %d — MaxBytesReader failed to trip", p.read, p.cap)
	}
	return n, err
}

// TestBindJSONWithLimit_NeverReadsPastCap: mount a panicReader that fatals the
// test the instant any consumer reads past the cap. If we delete the
// http.MaxBytesReader line, json.Decoder would happily consume the whole
// 5 KiB body and this test would fail (fatal) — proving the cap is what's
// stopping the read, not an accident of Decode buffering.
//
// This is the anti-tautology anchor: the test cannot pass with the cap
// removed. It exercises the actual over-limit boundary via observable reads,
// not just the response envelope.
func TestBindJSONWithLimit_NeverReadsPastCap(t *testing.T) {
	big := strings.Repeat("a", 5000)
	body := []byte(`{"currentPassword":"` + big + `","newPassword":"x"}`)

	e := echo.New()
	var status int
	e.POST("/test", func(c *echo.Context) error {
		// Replace the request body with our panicking reader, capped 8 bytes
		// past BodyLimitSmall — MaxBytesReader inside BindJSONWithLimit will
		// cut the read at exactly cap+1, so the panicReader must never see
		// more than BodyLimitSmall + a few slack bytes.
		c.Request().Body = &panicReaderCloser{r: &panicReader{
			src: bytes.NewReader(body),
			cap: handler.BodyLimitSmall + 16, // small slack for framing bytes
			t:   t,
		}}
		var dst tinyPayload
		if aerr := handler.BindJSONWithLimit(c, &dst, handler.BodyLimitSmall); aerr != nil {
			status = aerr.Status
			return aerr.Write(c)
		}
		return c.NoContent(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if status != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 status flag inside handler; got %d (recorded rec.Code=%d)", status, rec.Code)
	}
}

// panicReaderCloser wraps panicReader with a nop Close so it satisfies the
// io.ReadCloser interface Echo expects for Request.Body.
type panicReaderCloser struct {
	r *panicReader
}

func (p *panicReaderCloser) Read(b []byte) (int, error) { return p.r.Read(b) }
func (p *panicReaderCloser) Close() error                { return nil }

// TestBindJSONWithLimit_ExactlyAtLimit: a body sized to EXACTLY the cap must
// bind. This nails down the boundary — MaxBytesReader is documented to allow
// up to and including N bytes and error on N+1. If we ever regress by using
// `< limit` (off-by-one), this test catches it.
func TestBindJSONWithLimit_ExactlyAtLimit(t *testing.T) {
	// Construct a JSON body of exactly BodyLimitSmall bytes. The scaffold is
	// `{"currentPassword":"...","newPassword":"x"}`; pad the currentPassword
	// value with 'a' until len(body) == BodyLimitSmall.
	prefix := `{"currentPassword":"`
	suffix := `","newPassword":"x"}`
	pad := int(handler.BodyLimitSmall) - len(prefix) - len(suffix)
	if pad <= 0 {
		t.Fatalf("scaffold too big for cap: prefix=%d suffix=%d cap=%d", len(prefix), len(suffix), handler.BodyLimitSmall)
	}
	body := []byte(prefix + strings.Repeat("a", pad) + suffix)
	if int64(len(body)) != handler.BodyLimitSmall {
		t.Fatalf("body size %d != cap %d", len(body), handler.BodyLimitSmall)
	}
	var dst tinyPayload
	rec, ok := runBindLimit(t, body, &dst, handler.BodyLimitSmall)
	if !ok {
		t.Fatalf("bind should succeed at exactly cap; got status %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("status %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if len(dst.CurrentPassword) != pad {
		t.Errorf("dst.CurrentPassword len %d, want %d", len(dst.CurrentPassword), pad)
	}
}
