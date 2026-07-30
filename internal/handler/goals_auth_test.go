// goals_auth_test.go — gaka-d6x.handler: hit every goals-endpoint
// auth-failure branch. All goals routes gate on resolveUser first; each
// endpoint's `if aerr != nil { return respondErr(c, aerr) }` block is
// covered here in one go by iterating the endpoint list.
//
// Named invariant:
//
//	"every goals endpoint fails-closed without a token" — 4xx before any
//	DB touch, no oracle. If a future refactor drops auth from any of
//	these, this table catches it.
package handler_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("goals endpoints all fail-closed on unauth (gaka-d6x.handler)", func() {
	It("every route returns 4xx without a token", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		cases := []struct {
			method, path string
		}{
			{http.MethodGet, "/api/v1/users/current/goals"},
			{http.MethodPost, "/api/v1/users/current/goals"},
			{http.MethodGet, "/api/v1/users/current/goals/progress"},
			{http.MethodGet, "/api/v1/users/current/goals/00000000-0000-0000-0000-000000000000"},
			{http.MethodPatch, "/api/v1/users/current/goals/00000000-0000-0000-0000-000000000000"},
			{http.MethodDelete, "/api/v1/users/current/goals/00000000-0000-0000-0000-000000000000"},
			{http.MethodPost, "/api/v1/users/current/goals/00000000-0000-0000-0000-000000000000/toggle"},
			{http.MethodGet, "/api/v1/users/current/goals/00000000-0000-0000-0000-000000000000/progress"},
		}
		for _, c := range cases {
			rec := doJSONReqG(e, c.method, c.path, "", nil)
			Expect(rec.Code).To(BeNumerically(">=", 400),
				"%s %s: got %d body=%s — want 4xx (fail-closed)",
				c.method, c.path, rec.Code, rec.Body.String())
			Expect(rec.Code).To(BeNumerically("<", 500),
				"%s %s: got 5xx — auth error should be 4xx",
				c.method, c.path)
		}
	})
})
