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
//
//	"PATCH oversize body → 413 STRICTLY (BindJSONWithLimit small cap)" —
//	the test used to accept either 413 or 400 which defeats the point of
//	a body-cap regression. We construct a payload that is:
//	  (a) syntactically valid JSON (so json.Decode wouldn't 400 on its own)
//	  (b) contains a semantically legal timezone name pad-suffixed with
//	      whitespace (so trimTimezoneName + LoadLocation would happily
//	      accept the trimmed value → 200)
//	  (c) exceeds BodyLimitSmall (4 KiB)
//	  → the ONLY failure mode left is the size cap. If a future refactor
//	  drops BindJSONWithLimit, this test flips to 200 and screams instead
//	  of silently passing.
package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

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

	It("PATCH oversize body → 413 STRICTLY (only the size cap can trip; JSON + tz name are both valid post-trim)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("tz_413")

		// Construction: `"America/Los_Angeles<lots of spaces>"` — valid IANA
		// name after trimTimezoneName strips the trailing whitespace. The
		// JSON itself is well-formed. Payload size > BodyLimitSmall (4 KiB),
		// so the ONLY error path left is the MaxBytesReader cap → 413.
		pad := strings.Repeat(" ", 5000)
		body := `{"timezone":"America/Los_Angeles` + pad + `"}`

		req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/current/timezone", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
			"body cap must fire before LoadLocation (both the JSON and the trimmed tz name would be legal); got %d body=%s",
			rec.Code, rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("payload too large"),
			"expected 'payload too large' hint from BindJSONWithLimit; got %s", rec.Body.String())
	})
})
