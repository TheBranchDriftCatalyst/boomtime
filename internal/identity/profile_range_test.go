// profile_range_test.go — the ?days stats-window override on the public
// profile payload (gaka-174.7). Asserts parsing + clamping against the real
// handler; the awards endpoint is unaffected (separate route, canonical
// window) so re-scoping never desyncs labels.
package identity_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("public profile ?days window (gaka-174.7)", func() {
	// setup mints a user, enables their public profile, and returns the router
	// + slug for hitting the public route.
	setup := func() (http.Handler, string) {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("range_g")
		slug := "rangeg-" + strings.ToLower(strings.ReplaceAll(user[len(user)-8:], ".", ""))
		rec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true,
			"slug":    slug,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "PUT profile: body=%s", rec.Body.String())
		return e, slug
	}

	// windowDays GETs the payload (optionally with ?days=q) and returns the
	// span between startDate and endDate in days. The handler truncates the
	// start to a day boundary, so the span can run up to ~1 day over the
	// requested window — assertions use tolerances / equivalence accordingly.
	windowDays := func(e http.Handler, slug, q string) float64 {
		url := "/api/public/profile/" + slug
		if q != "" {
			url += "?days=" + q
		}
		req := httptest.NewRequest(http.MethodGet, url, nil)
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		Expect(rr.Code).To(Equal(http.StatusOK), "GET %s: body=%s", url, rr.Body.String())
		var body struct {
			StartDate time.Time `json:"startDate"`
			EndDate   time.Time `json:"endDate"`
		}
		Expect(json.Unmarshal(rr.Body.Bytes(), &body)).To(Succeed())
		return body.EndDate.Sub(body.StartDate).Hours() / 24
	}

	It("defaults to ~60 days", func() {
		e, slug := setup()
		Expect(windowDays(e, slug, "")).To(BeNumerically("~", 60, 1.2))
	})
	It("?days=7 narrows the window to ~7 days", func() {
		e, slug := setup()
		Expect(windowDays(e, slug, "7")).To(BeNumerically("~", 7, 1.2))
	})
	It("?days above the cap clamps to 365 (== ?days=365)", func() {
		e, slug := setup()
		Expect(windowDays(e, slug, "9999")).To(BeNumerically("~", windowDays(e, slug, "365"), 0.1))
	})
	It("?days below 1 clamps to 1 (== ?days=1)", func() {
		e, slug := setup()
		Expect(windowDays(e, slug, "0")).To(BeNumerically("~", windowDays(e, slug, "1"), 0.1))
	})
	It("non-numeric ?days falls back to the default (== no param)", func() {
		e, slug := setup()
		Expect(windowDays(e, slug, "abc")).To(BeNumerically("~", windowDays(e, slug, ""), 0.1))
	})
})
