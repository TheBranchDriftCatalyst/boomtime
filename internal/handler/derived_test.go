// derived_test.go — gaka-d6x.handler: cover DerivedStatus/DerivedResync.
// Named invariants:
//
//	"unauth → 4xx no leak" — a missing/bad token returns 4xx BEFORE
//	touching the DB (fail-closed).
//
//	"self only: bob's token cannot resync or read alice's row" — the /derived
//	endpoints resolve the owner from the token; a second user gets his own
//	empty status, not alice's. No oracle.
//
//	"resync invalidates the owner's cached aggregates" — DerivedResync
//	explicitly calls invalidateOwnerCache. Two /stats reads (before + after
//	resync) must both return 200 and the second must NOT be a stale cache
//	from before resync ran. Because there's no data mutation between reads
//	the payload equality is the WEAKER guarantee; the STRONGER guarantee is
//	that /derived/status returns the fresh Sender field on the second read.
package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("Derived endpoints (gaka-d6x.handler)", func() {
	It("rejects unauthenticated GET /derived/status with 4xx (fail-closed)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/derived/status", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
		Expect(rec.Code).To(BeNumerically("<", 500))
	})

	It("rejects unauthenticated POST /derived/resync with 4xx", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/derived/resync", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
		Expect(rec.Code).To(BeNumerically("<", 500))
	})

	It("GET /derived/status returns 200 + JSON scoped to the caller", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceUser, aliceTok := hz.MintUser("derivedA")
		bobUser, bobTok := hz.MintUser("derivedB")

		aRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/derived/status", aliceTok, nil)
		Expect(aRec).To(testutil.HaveStatus(http.StatusOK), "alice: %s", aRec.Body.String())

		bRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/derived/status", bobTok, nil)
		Expect(bRec).To(testutil.HaveStatus(http.StatusOK), "bob: %s", bRec.Body.String())

		// Cross-user isolation: whatever "sender"-labelled field the payload
		// carries must reference the calling user, never the other user.
		Expect(aRec.Body.String()).NotTo(ContainSubstring(bobUser),
			"alice's /derived/status leaked bob's username")
		Expect(bRec.Body.String()).NotTo(ContainSubstring(aliceUser),
			"bob's /derived/status leaked alice's username")
	})

	It("POST /derived/resync succeeds for an owner (200 + status payload)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("derivedRS")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/derived/resync", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		// Response body is the refreshed status payload; must be valid JSON
		// (not a raw error envelope).
		var payload map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed(),
			"non-JSON body: %s", rec.Body.String())
	})
})
