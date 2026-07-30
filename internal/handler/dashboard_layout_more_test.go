// dashboard_layout_more_test.go — extra coverage for dashboard_layout.go
// (gaka-d6x.handler). Focus: the DELETE endpoint (0% coverage on baseline),
// the malformed-JSON reject branch on PUT, the missing-layout-field 400
// branch, and cross-user isolation on both PUT and DELETE (a layout row is
// keyed on (username, scope) — a leak would let user B stomp A's layout).
package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("dashboard layout extras (gaka-d6x.handler)", func() {
	Describe("DeleteDashboardLayout", func() {
		It("is IDEMPOTENT: DELETE with no row returns 204 (docstring guarantee)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("dash_del_idem")

			req := httptest.NewRequest(http.MethodDelete,
				"/api/v1/users/current/dashboard/public_profile", nil)
			req.Header.Set("Authorization", "Basic "+token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec).To(testutil.HaveStatus(http.StatusNoContent),
				"DELETE on unset scope must be 204 (idempotent), got %d body=%s", rec.Code, rec.Body.String())
		})

		It("actually REMOVES the row: PUT then DELETE then GET → 404 (proves the row is gone)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("dash_del_real")

			// PUT first.
			body := []byte(`{"layout":{"cols":12,"widgets":[]}}`)
			req := httptest.NewRequest(http.MethodPut,
				"/api/v1/users/current/dashboard/public_profile", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Basic "+token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK), "PUT: body=%s", rec.Body.String())

			// GET must see it.
			req2 := httptest.NewRequest(http.MethodGet,
				"/api/v1/users/current/dashboard/public_profile", nil)
			req2.Header.Set("Authorization", "Basic "+token)
			rec2 := httptest.NewRecorder()
			e.ServeHTTP(rec2, req2)
			Expect(rec2).To(testutil.HaveStatus(http.StatusOK), "GET after PUT: body=%s", rec2.Body.String())

			// DELETE.
			req3 := httptest.NewRequest(http.MethodDelete,
				"/api/v1/users/current/dashboard/public_profile", nil)
			req3.Header.Set("Authorization", "Basic "+token)
			rec3 := httptest.NewRecorder()
			e.ServeHTTP(rec3, req3)
			Expect(rec3).To(testutil.HaveStatus(http.StatusNoContent))

			// GET must now 404 — proves DELETE actually reached the DB.
			req4 := httptest.NewRequest(http.MethodGet,
				"/api/v1/users/current/dashboard/public_profile", nil)
			req4.Header.Set("Authorization", "Basic "+token)
			rec4 := httptest.NewRecorder()
			e.ServeHTTP(rec4, req4)
			Expect(rec4).To(testutil.HaveStatus(http.StatusNotFound),
				"post-DELETE GET must 404 (row is gone): body=%s", rec4.Body.String())
		})

		It("rejects an UNKNOWN scope with 400 (same allowlist as PUT/GET)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("dash_del_scope")

			req := httptest.NewRequest(http.MethodDelete,
				"/api/v1/users/current/dashboard/overview", nil)
			req.Header.Set("Authorization", "Basic "+token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"DELETE unknown scope must be 400: got %d body=%s", rec.Code, rec.Body.String())
		})

		It("CROSS-USER: B's DELETE does NOT drop A's row (owner-keyed)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, tokenA := hz.MintUser("dash_del_iso_a")
			_, tokenB := hz.MintUser("dash_del_iso_b")

			// A PUTs a distinctive layout.
			inner := `{"cols":12,"widgets":[{"i":"A-ONLY-MARKER","x":0,"y":0,"w":3,"h":3}]}`
			bodyA := []byte(`{"layout":` + inner + `}`)
			req := httptest.NewRequest(http.MethodPut,
				"/api/v1/users/current/dashboard/public_profile", bytes.NewReader(bodyA))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Basic "+tokenA)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK), "A PUT: body=%s", rec.Body.String())

			// B DELETEs public_profile — should hit B's row (which does not
			// exist), NOT A's. Endpoint returns 204 either way (idempotent).
			req = httptest.NewRequest(http.MethodDelete,
				"/api/v1/users/current/dashboard/public_profile", nil)
			req.Header.Set("Authorization", "Basic "+tokenB)
			rec = httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec).To(testutil.HaveStatus(http.StatusNoContent), "B DELETE: body=%s", rec.Body.String())

			// A's row must still be there — the load-bearing anti-tampering check.
			req = httptest.NewRequest(http.MethodGet,
				"/api/v1/users/current/dashboard/public_profile", nil)
			req.Header.Set("Authorization", "Basic "+tokenA)
			rec = httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK),
				"CROSS-USER LEAK: B's DELETE nuked A's layout row: body=%s", rec.Body.String())
			Expect(rec.Body.String()).To(ContainSubstring("A-ONLY-MARKER"),
				"A's layout content lost or overwritten: body=%s", rec.Body.String())
		})
	})

	Describe("PutDashboardLayout error branches", func() {
		It("rejects a syntactically-broken JSON body with 400 (not 500)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("dash_put_badjson")

			// truncated JSON — json.Decoder returns a syntax error, not MaxBytesError.
			req := httptest.NewRequest(http.MethodPut,
				"/api/v1/users/current/dashboard/public_profile",
				bytes.NewReader([]byte(`{"layout":`)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Basic "+token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"malformed JSON must return 400 (not 500): body=%s", rec.Body.String())
		})

		It("rejects a body missing the 'layout' key with 400 (contract check)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("dash_put_nolayout")

			req := httptest.NewRequest(http.MethodPut,
				"/api/v1/users/current/dashboard/public_profile",
				bytes.NewReader([]byte(`{"widgets":[]}`)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Basic "+token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"missing 'layout' key must be 400: body=%s", rec.Body.String())
			Expect(rec.Body.String()).To(ContainSubstring("layout"),
				"expected layout-related error, got %s", rec.Body.String())
		})

		It("rejects an EXPLICITLY-NULL layout with 400 (null defeat)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("dash_put_null")

			req := httptest.NewRequest(http.MethodPut,
				"/api/v1/users/current/dashboard/public_profile",
				bytes.NewReader([]byte(`{"layout":null}`)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Basic "+token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"layout:null must be 400 (would silently unset otherwise): body=%s", rec.Body.String())
		})

		It("CROSS-USER: B's PUT does NOT overwrite A's row (owner-keyed)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, tokenA := hz.MintUser("dash_put_iso_a")
			_, tokenB := hz.MintUser("dash_put_iso_b")

			// A puts a distinctive layout.
			innerA := `{"marker":"A-VALUE"}`
			put := func(tok string, inner string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPut,
					"/api/v1/users/current/dashboard/public_profile",
					bytes.NewReader([]byte(`{"layout":`+inner+`}`)))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Basic "+tok)
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				return rec
			}
			Expect(put(tokenA, innerA)).To(testutil.HaveStatus(http.StatusOK))

			// B puts a different layout under the same scope path.
			innerB := `{"marker":"B-VALUE"}`
			Expect(put(tokenB, innerB)).To(testutil.HaveStatus(http.StatusOK))

			// A's GET must STILL see A's marker.
			req := httptest.NewRequest(http.MethodGet,
				"/api/v1/users/current/dashboard/public_profile", nil)
			req.Header.Set("Authorization", "Basic "+tokenA)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))
			var envA struct {
				Layout json.RawMessage `json:"layout"`
			}
			Expect(json.Unmarshal(rec.Body.Bytes(), &envA)).To(Succeed())
			Expect(string(envA.Layout)).To(ContainSubstring("A-VALUE"),
				"CROSS-USER LEAK: B's PUT overwrote A's layout: got %s", string(envA.Layout))
			Expect(string(envA.Layout)).NotTo(ContainSubstring("B-VALUE"),
				"CROSS-USER LEAK: A sees B's marker: got %s", string(envA.Layout))
		})
	})
})
