// handler_ginkgo_test.go — ginkgo mirror of handler_test.go (gaka-bi2).
// 1:1 case map (4 stdlib TestXxx):
//
//	TestBindJSONWithLimit_HappyPath        → BindJSONWithLimit > "under-cap body binds cleanly"
//	TestBindJSONWithLimit_OverLimit        → BindJSONWithLimit > "over-cap body → 413, dst untouched"
//	TestBindJSONWithLimit_MalformedJSON    → BindJSONWithLimit > "malformed body under cap → 400"
//	TestBindJSONWithLimit_NeverReadsPastCap→ BindJSONWithLimit > "reader is capped at limit+ε (panicReader anchor)"
//	TestBindJSONWithLimit_ExactlyAtLimit   → BindJSONWithLimit > "body at exactly cap → binds"
package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
	"github.com/labstack/echo/v5"
)

// tinyPayloadGinkgo mirrors the stdlib file's tinyPayload — distinct name
// avoids duplicate-symbol collision (both files compile into the same test
// binary under package handler_test).
type tinyPayloadGinkgo struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func runBindLimitGinkgo(body []byte, dst any, limit int64) (*httptest.ResponseRecorder, bool) {
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

var _ = Describe("BindJSONWithLimit (gaka-bi2)", func() {
	It("binds cleanly on an under-cap body and 204s the downstream handler", func() {
		body, err := json.Marshal(tinyPayloadGinkgo{
			CurrentPassword: "test1234",
			NewPassword:     "test5678",
		})
		Expect(err).NotTo(HaveOccurred())

		var dst tinyPayloadGinkgo
		rec, ok := runBindLimitGinkgo(body, &dst, handler.BodyLimitSmall)
		Expect(ok).To(BeTrue(), "bind should succeed for under-cap body")
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent))
		Expect(dst.CurrentPassword).To(Equal("test1234"))
		Expect(dst.NewPassword).To(Equal("test5678"))
	})

	It("rejects an over-cap body with 413 AND leaves dst zero (proves reader tripped before decode)", func() {
		big := strings.Repeat("a", 5000)
		body := []byte(`{"currentPassword":"` + big + `","newPassword":"test5678"}`)
		Expect(int64(len(body))).To(BeNumerically(">", handler.BodyLimitSmall),
			"body (%d) must exceed cap (%d) for this test to mean anything", len(body), handler.BodyLimitSmall)

		var dst tinyPayloadGinkgo
		rec, ok := runBindLimitGinkgo(body, &dst, handler.BodyLimitSmall)
		Expect(ok).To(BeFalse(), "bind unexpectedly succeeded for over-cap body")
		Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge))

		var envelope struct {
			Error   string  `json:"error"`
			Message *string `json:"message"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
		Expect(envelope.Error).To(Equal("payload too large"))
		Expect(envelope.Message).NotTo(BeNil())
		Expect(*envelope.Message).To(ContainSubstring("limit="))
		// LOAD-BEARING: dst must be zero.
		Expect(dst.CurrentPassword).To(BeEmpty(), "MaxBytesReader leaked: dst populated")
		Expect(dst.NewPassword).To(BeEmpty(), "MaxBytesReader leaked: dst populated")
	})

	It("returns 400 (not 413) on syntactically-broken JSON under the cap", func() {
		body := []byte(`{"currentPassword": "test1234",`) // trailing comma, no closing brace
		var dst tinyPayloadGinkgo
		rec, ok := runBindLimitGinkgo(body, &dst, handler.BodyLimitSmall)
		Expect(ok).To(BeFalse(), "bind unexpectedly succeeded for malformed JSON")
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("MaxBytesReader trips at cap+ε — the panicReader anchor never fires", func() {
		big := strings.Repeat("a", 5000)
		body := []byte(`{"currentPassword":"` + big + `","newPassword":"x"}`)

		e := echo.New()
		var status int
		e.POST("/test", func(c *echo.Context) error {
			c.Request().Body = &panicReaderCloserGinkgo{r: &panicReaderGinkgo{
				src: bytes.NewReader(body),
				cap: handler.BodyLimitSmall + 16,
			}}
			var dst tinyPayloadGinkgo
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
		Expect(status).To(Equal(http.StatusRequestEntityTooLarge),
			"expected 413 status flag inside handler; got %d (rec.Code=%d)", status, rec.Code)
	})

	It("body sized to EXACTLY the cap binds (boundary N vs N+1)", func() {
		prefix := `{"currentPassword":"`
		suffix := `","newPassword":"x"}`
		pad := int(handler.BodyLimitSmall) - len(prefix) - len(suffix)
		Expect(pad).To(BeNumerically(">", 0), "scaffold too big for cap")
		body := []byte(prefix + strings.Repeat("a", pad) + suffix)
		Expect(int64(len(body))).To(Equal(handler.BodyLimitSmall))

		var dst tinyPayloadGinkgo
		rec, ok := runBindLimitGinkgo(body, &dst, handler.BodyLimitSmall)
		Expect(ok).To(BeTrue(), "bind should succeed at exactly cap; got status %d body=%s", rec.Code, rec.Body.String())
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent))
		Expect(dst.CurrentPassword).To(HaveLen(pad))
	})
})

// panicReaderGinkgo mirrors the stdlib file's panicReader — but reports via
// Ginkgo Fail instead of *testing.T Fatalf so failures surface as spec
// failures rather than crashing the process.
type panicReaderGinkgo struct {
	src  *bytes.Reader
	read int64
	cap  int64
}

func (p *panicReaderGinkgo) Read(b []byte) (int, error) {
	n, err := p.src.Read(b)
	p.read += int64(n)
	if p.read > p.cap {
		Fail("panicReaderGinkgo: read past cap — MaxBytesReader failed to trip")
	}
	return n, err
}

type panicReaderCloserGinkgo struct {
	r *panicReaderGinkgo
}

func (p *panicReaderCloserGinkgo) Read(b []byte) (int, error) { return p.r.Read(b) }
func (p *panicReaderCloserGinkgo) Close() error               { return nil }

// -- helpers restored from stdlib partner (gaka-0vp.17) --
type tinyPayload struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}
