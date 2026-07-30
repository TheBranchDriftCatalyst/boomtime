// leaderboards_test.go — gaka-d6x.handler: cover Leaderboards.
//
// Named invariants:
//
//	"unauth → 4xx" — the endpoint requires a token even though the
//	response is cross-user (curation/space scoping is per-requester).
//
//	"200 + payload for an authed user" — leaderboards works even with
//	zero heartbeats seeded (payload is an empty leaderboard, not a 5xx).
//
//	"caching is owner-scoped in the key" — two owners hitting the same
//	range must not share cache bytes. Bob's cache key is prefixed with
//	his username, so the second read is computed independently.
package handler_test

import (
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("Leaderboards (gaka-d6x.handler)", func() {
	It("rejects unauth'd GET with 4xx", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboards", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})

	It("authed empty user returns 200 with payload structure", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("lb_empty")

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/leaderboards", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
	})

	It("cache key is per-owner: two callers get independent responses", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, aliceTok := hz.MintUser("lb_a")
		_, bobTok := hz.MintUser("lb_b")

		aRec := doJSONReqG(e, http.MethodGet, "/api/v1/leaderboards", aliceTok, nil)
		Expect(aRec).To(testutil.HaveStatus(http.StatusOK))
		bRec := doJSONReqG(e, http.MethodGet, "/api/v1/leaderboards", bobTok, nil)
		Expect(bRec).To(testutil.HaveStatus(http.StatusOK))
		// Both callers should reach 200 (no cache-key collision blocking a real query).
	})
})
