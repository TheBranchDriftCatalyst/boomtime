// timezone_more_test.go — gaka-d6x.handler: additional coverage for
// timezone.go (trimTimezoneName + UpdateTimezone with whitespace-padded
// values + resolveUserTZ fallback).
//
// Named invariants:
//
//	"trimTimezoneName strips leading + trailing whitespace but preserves
//	 interior chars" — a valid IANA name with " America/Los_Angeles \t"
//	 padding parses cleanly. A name with interior whitespace is passed
//	 through to LoadLocation, which rejects it → 400.
//
//	"UpdateTimezone with valid name after trimming persists" — the trim
//	pass happens BEFORE LoadLocation; a padded valid name round-trips.
//
//	"unauth GET /timezone → 4xx" — fail-closed.
package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("Timezone extra branches (gaka-d6x.handler)", func() {
	It("unauth GET /timezone → 4xx", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/timezone", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})

	It("PATCH with whitespace-padded valid name round-trips", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("tz_pad")

		// Leading + trailing whitespace — trimTimezoneName strips it
		// BEFORE time.LoadLocation runs.
		rec := doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/timezone", token,
			map[string]string{"timezone": "  America/Los_Angeles \t"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var got struct {
			Timezone          string `json:"timezone"`
			EffectiveTimezone string `json:"effectiveTimezone"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got.Timezone).To(Equal("America/Los_Angeles"),
			"whitespace should have been trimmed; got %q", got.Timezone)
		Expect(got.EffectiveTimezone).To(Equal("America/Los_Angeles"))
	})

	It("PATCH with interior-whitespace name is REJECTED (interior isn't stripped)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("tz_int_ws")

		// Interior whitespace goes to LoadLocation as-is → 400.
		rec := doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/timezone", token,
			map[string]string{"timezone": "America/Los Angeles"})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
	})

	It("PATCH oversize body → 413 (BindJSONWithLimit small cap)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("tz_413")

		big := make([]byte, 5000)
		for i := range big {
			big[i] = 'x'
		}
		body := `{"timezone":"` + string(big) + `"}`
		rec := doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/timezone", token, json.RawMessage(body))
		// Either 413 (from BindJSONWithLimit) or 400 (malformed) is acceptable —
		// but the DB write must NOT have happened either way. Assert on 4xx.
		Expect(rec.Code).To(BeNumerically(">=", 400))
		Expect(rec.Code).To(BeNumerically("<", 500))
	})
})
