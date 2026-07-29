// timezone_ginkgo_test.go — ginkgo mirror of timezone_test.go (gaka-dg7).
// 1:1 case map (2 stdlib TestXxx):
//   TestUpdateTimezone_RejectsInvalidIANA → timezone endpoints > "PATCH invalid IANA → 400, no DB write"
//   TestUpdateTimezone_ValidRoundtrips    → timezone endpoints > "PATCH valid IANA round-trips through GET; empty clears"
package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
	"github.com/labstack/echo/v5"
)

// routerWithTimezoneGinkgo — mirror of the stdlib file's routerWithTimezone.
// Distinct name avoids duplicate-symbol collision in the same test binary.
func routerWithTimezoneGinkgo(hz *testutil.Harness) *echo.Echo {
	e := hz.Router()
	e.GET("/api/v1/users/current/timezone", hz.H.GetTimezone)
	e.PATCH("/api/v1/users/current/timezone", hz.H.UpdateTimezone)
	return e
}

// doJSONGinkgo — mirror of the stdlib file's doJSON but reports via Expect
// rather than testing.T. Distinct name avoids collision.
func doJSONGinkgo(e *echo.Echo, method, path, token string, body any) *httptest.ResponseRecorder {
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

var _ = Describe("timezone endpoints (gaka-dg7)", func() {
	It("rejects an invalid IANA name with 400 and does not touch the DB", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithTimezoneGinkgo(hz)
		_, token := hz.MintUser("tz_invalid")

		// Baseline: user has never picked a tz.
		rec := doJSONGinkgo(e, http.MethodGet, "/api/v1/users/current/timezone", token, nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		var baseline struct {
			Timezone          string `json:"timezone"`
			EffectiveTimezone string `json:"effectiveTimezone"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &baseline)).To(Succeed())
		Expect(baseline.Timezone).To(BeEmpty(), "never-picked user should have empty tz")
		Expect(baseline.EffectiveTimezone).To(Equal("UTC"), "no env default in test harness → UTC")

		// PATCH bogus name → 400.
		rec = doJSONGinkgo(e, http.MethodPatch, "/api/v1/users/current/timezone", token,
			map[string]string{"timezone": "Mars/Olympus"})
		Expect(rec.Code).To(Equal(http.StatusBadRequest),
			"PATCH invalid IANA: body=%s", rec.Body.String())

		// GET must still show empty — proves no DB write happened.
		rec = doJSONGinkgo(e, http.MethodGet, "/api/v1/users/current/timezone", token, nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		var after struct{ Timezone string }
		_ = json.Unmarshal(rec.Body.Bytes(), &after)
		Expect(after.Timezone).To(BeEmpty(),
			"invalid PATCH should have failed BEFORE any DB write")
	})

	It("PATCH valid IANA round-trips through GET; empty string clears the pick", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithTimezoneGinkgo(hz)
		_, token := hz.MintUser("tz_valid")

		// PATCH a valid IANA name.
		rec := doJSONGinkgo(e, http.MethodPatch, "/api/v1/users/current/timezone", token,
			map[string]string{"timezone": "America/Los_Angeles"})
		Expect(rec.Code).To(Equal(http.StatusOK), "PATCH valid: body=%s", rec.Body.String())
		var patched struct {
			Timezone          string `json:"timezone"`
			EffectiveTimezone string `json:"effectiveTimezone"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &patched)).To(Succeed())
		Expect(patched.Timezone).To(Equal("America/Los_Angeles"))
		Expect(patched.EffectiveTimezone).To(Equal("America/Los_Angeles"),
			"user pick MUST win the 3-level chain over any env default")

		// GET must show the same.
		rec = doJSONGinkgo(e, http.MethodGet, "/api/v1/users/current/timezone", token, nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		var got struct{ Timezone, EffectiveTimezone string }
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		Expect(got.Timezone).To(Equal("America/Los_Angeles"))
		Expect(got.EffectiveTimezone).To(Equal("America/Los_Angeles"))

		// PATCH with empty clears the pick.
		rec = doJSONGinkgo(e, http.MethodPatch, "/api/v1/users/current/timezone", token,
			map[string]string{"timezone": ""})
		Expect(rec.Code).To(Equal(http.StatusOK), "PATCH empty: body=%s", rec.Body.String())

		rec = doJSONGinkgo(e, http.MethodGet, "/api/v1/users/current/timezone", token, nil)
		var cleared struct{ Timezone, EffectiveTimezone string }
		_ = json.Unmarshal(rec.Body.Bytes(), &cleared)
		Expect(cleared.Timezone).To(BeEmpty())
		Expect(cleared.EffectiveTimezone).To(Equal("UTC"), "fallback after clear")
	})
})
