// dashboard_layout_ginkgo_test.go — ginkgo mirror of dashboard_layout_test.go (gaka-keb).
// 1:1 case map (6 stdlib TestXxx):
//   TestDashboardLayoutPersistence_Gaka6jmXRegression → dashboard layout > "PUT/GET semantic round-trip; overwrite replaces"
//   TestDashboardLayoutUnknownScope                   → dashboard layout > "unknown scope → 400 (PUT + GET)"
//   TestDashboardLayoutMissWhenUnset                  → dashboard layout > "GET before any PUT → 404"
//   TestPutDashboardLayout_BodySizeCap_413            → dashboard layout > "5 KiB body → 413 before write"
//   TestPublicProfileIncludesLayoutWhenSet            → public profile layout > "included verbatim when set"
//   TestPublicProfileLayoutOmittedWhenUnset           → public profile layout > "omitted (omitempty) when unset"
package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// routerWithDashboardLayoutG — mirror of the stdlib helper.
func routerWithDashboardLayoutG(hz *testutil.Harness) http.Handler {
	e := hz.Router()
	e.GET("/api/v1/users/current/profile", hz.H.GetPublicProfile)
	e.PUT("/api/v1/users/current/profile", hz.H.PutPublicProfile)
	e.GET("/api/public/profile/:slug", hz.H.PublicProfile)
	e.GET("/api/v1/users/current/dashboard/:scope", hz.H.GetDashboardLayout)
	e.PUT("/api/v1/users/current/dashboard/:scope", hz.H.PutDashboardLayout)
	e.DELETE("/api/v1/users/current/dashboard/:scope", hz.H.DeleteDashboardLayout)
	return e
}

// semanticJSONDiffG — mirror of semanticJSONDiff.
func semanticJSONDiffG(a, b string) string {
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		return "left is not valid JSON: " + err.Error()
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		return "right is not valid JSON: " + err.Error()
	}
	an, _ := json.Marshal(av)
	bn, _ := json.Marshal(bv)
	if string(an) != string(bn) {
		return "normalized forms differ"
	}
	return ""
}

var _ = Describe("dashboard layout (gaka-keb)", func() {
	It("PUT / GET semantic round-trip; overwrite replaces", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithDashboardLayoutG(hz)
		_, token := hz.MintUser("dash_rt_g")

		inner := `{"cols":12,"widgets":[{"i":"grade-badge","x":0,"y":0,"w":3,"h":3,"view":null},{"i":"top-langs","x":6,"y":3,"w":6,"h":4,"view":"bar"}]}`
		body := []byte(`{"layout":` + inner + `}`)

		req := httptest.NewRequest(http.MethodPut, "/api/v1/users/current/dashboard/public_profile", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK), "PUT: body=%s", rec.Body.String())

		req2 := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/dashboard/public_profile", nil)
		req2.Header.Set("Authorization", "Basic "+token)
		rec2 := httptest.NewRecorder()
		e.ServeHTTP(rec2, req2)
		Expect(rec2.Code).To(Equal(http.StatusOK), "GET: body=%s", rec2.Body.String())

		var getEnv struct {
			Layout json.RawMessage `json:"layout"`
		}
		Expect(json.Unmarshal(rec2.Body.Bytes(), &getEnv)).To(Succeed())
		Expect(semanticJSONDiffG(inner, string(getEnv.Layout))).To(BeEmpty(),
			"layout round-trip differs semantically\n  sent: %s\n   got: %s", inner, string(getEnv.Layout))

		// Overwrite semantics.
		inner2 := `{"cols":6,"widgets":[{"i":"punchcard","x":0,"y":0,"w":6,"h":4,"view":"heatmap"}]}`
		body2 := []byte(`{"layout":` + inner2 + `}`)
		req3 := httptest.NewRequest(http.MethodPut, "/api/v1/users/current/dashboard/public_profile", bytes.NewReader(body2))
		req3.Header.Set("Content-Type", "application/json")
		req3.Header.Set("Authorization", "Basic "+token)
		rec3 := httptest.NewRecorder()
		e.ServeHTTP(rec3, req3)
		Expect(rec3.Code).To(Equal(http.StatusOK), "PUT overwrite: body=%s", rec3.Body.String())

		req4 := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/dashboard/public_profile", nil)
		req4.Header.Set("Authorization", "Basic "+token)
		rec4 := httptest.NewRecorder()
		e.ServeHTTP(rec4, req4)
		var getEnv2 struct {
			Layout json.RawMessage `json:"layout"`
		}
		Expect(json.Unmarshal(rec4.Body.Bytes(), &getEnv2)).To(Succeed())
		Expect(semanticJSONDiffG(inner2, string(getEnv2.Layout))).To(BeEmpty(),
			"overwrite lost semantics\n  sent: %s\n   got: %s", inner2, string(getEnv2.Layout))
	})

	It("returns 400 (not 404/500) for an unknown scope on both PUT and GET", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithDashboardLayoutG(hz)
		_, token := hz.MintUser("dash_scope_g")

		body := []byte(`{"layout":{"widgets":[]}}`)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/users/current/dashboard/overview", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusBadRequest), "PUT unknown scope: got %d", rec.Code)

		req2 := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/dashboard/overview", nil)
		req2.Header.Set("Authorization", "Basic "+token)
		rec2 := httptest.NewRecorder()
		e.ServeHTTP(rec2, req2)
		Expect(rec2.Code).To(Equal(http.StatusBadRequest), "GET unknown scope: got %d", rec2.Code)
	})

	It("returns 404 on GET before any PUT (FE fall-back path)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithDashboardLayoutG(hz)
		_, token := hz.MintUser("dash_miss_g")

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/dashboard/public_profile", nil)
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusNotFound),
			"FE default-layout path relies on 404 for unset layouts; got %d", rec.Code)
	})

	It("rejects a > 4 KiB body with 413 before writing the layout row", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithDashboardLayoutG(hz)
		_, token := hz.MintUser("dash_413_g")

		pad := strings.Repeat("a", 5000)
		body := []byte(`{"layout":{"widgets":[],"_pad":"` + pad + `"}}`)

		req := httptest.NewRequest(http.MethodPut, "/api/v1/users/current/dashboard/public_profile", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusRequestEntityTooLarge),
			"200 would prove the cap didn't fire and the row was written")
	})
})

var _ = Describe("public profile layout inlining", func() {
	It("includes the layout verbatim when set (single-fetch contract)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithDashboardLayoutG(hz)
		user, token := hz.MintUser("pub_layout_g")

		slug := "publayoutg-" + strings.ToLower(strings.ReplaceAll(user[len(user)-8:], ".", ""))
		rec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true, "slug": slug,
		})
		Expect(rec.Code).To(Equal(http.StatusOK), "PUT profile: body=%s", rec.Body.String())

		inner := `{"cols":12,"widgets":[{"i":"grade-badge","x":0,"y":0,"w":3,"h":3,"view":null}]}`
		body := []byte(`{"layout":` + inner + `}`)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/users/current/dashboard/public_profile", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		Expect(rr.Code).To(Equal(http.StatusOK), "PUT layout: body=%s", rr.Body.String())

		req2 := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug, nil)
		rr2 := httptest.NewRecorder()
		e.ServeHTTP(rr2, req2)
		Expect(rr2.Code).To(Equal(http.StatusOK), "public profile GET: body=%s", rr2.Body.String())

		var resp struct {
			Layout json.RawMessage `json:"layout"`
		}
		Expect(json.Unmarshal(rr2.Body.Bytes(), &resp)).To(Succeed())
		Expect(semanticJSONDiffG(inner, string(resp.Layout))).To(BeEmpty(),
			"public profile layout mismatch\n  sent: %s\n   got: %s", inner, string(resp.Layout))
	})

	It("omits the layout key entirely when unset (omitempty contract)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithDashboardLayoutG(hz)
		user, token := hz.MintUser("pub_nolayout_g")

		slug := "nolayoutg-" + strings.ToLower(strings.ReplaceAll(user[len(user)-8:], ".", ""))
		rec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true, "slug": slug,
		})
		Expect(rec.Code).To(Equal(http.StatusOK), "PUT profile: body=%s", rec.Body.String())

		req := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug, nil)
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		Expect(rr.Code).To(Equal(http.StatusOK), "public profile GET: body=%s", rr.Body.String())
		Expect(rr.Body.String()).NotTo(ContainSubstring(`"layout"`),
			"expected no `layout` key when unset; body=%s", rr.Body.String())
	})
})
