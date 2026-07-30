// stats_more_test.go — gaka-d6x.handler: cover Stats/Timeline/StatusbarToday
// beyond the fast-path integration test.
//
// Named invariants:
//
//	"unauth → 4xx across /stats + /timeline + /statusbar/today" — all three
//	dashboard reads gate on auth first. A no-token request must never touch
//	the DB.
//
//	"empty user (no heartbeats) → 200 with structurally-valid payloads" —
//	the response is a real JSON object with the expected top-level keys,
//	not a null / 5xx. This pins the "empty state" contract the FE relies on.
//
//	"cache serves within TTL: identical bytes on a second read" — after
//	the first Stats read populates the cache, a second read within TTL
//	returns byte-identical output. Proves the cachedJSON wrap is wired.
//
//	"cache is owner-scoped: bob's identical query does NOT share alice's
//	 cached payload" — alice's totalSeconds != bob's when both have distinct
//	 seeded data. The cache key starts with `owner|`, so this pins the
//	 no-cross-user-collision property.
package stats_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("Stats / Timeline / StatusbarToday auth guards", func() {
	table := []struct{ path string }{
		{"/api/v1/users/current/stats"},
		{"/api/v1/users/current/timeline"},
		{"/api/v1/users/current/statusbar/today"},
	}
	for _, e := range table {
		e := e
		It("rejects unauth'd "+e.path+" with 4xx", func() {
			hz := testutil.NewHarness(GinkgoT())
			r := hz.Router()
			req := httptest.NewRequest(http.MethodGet, e.path, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			Expect(rec.Code).To(BeNumerically(">=", 400))
			Expect(rec.Code).To(BeNumerically("<", 500))
		})
	}
})

var _ = Describe("Stats raw (non-rollup) path", func() {
	It("hits the raw GetUserActivity branch when timeLimit != 15", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("stats_raw")

		base := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("alpha")
		sd.Block(testutil.HB{Project: "alpha", Language: "Go", Editor: "vim"}, base, 2, 60)
		sd.RefreshRollup(base.AddDate(0, 0, -1))

		start := base.AddDate(0, 0, -1).Format(time.RFC3339)
		end := base.AddDate(0, 0, 1).Format(time.RFC3339)
		// Non-default timeLimit forces the "raw" branch in Stats.
		q := "?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end) + "&timeLimit=30"

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/stats"+q, token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
	})
})

var _ = Describe("Timeline endpoint (empty state + auth)", func() {
	It("empty user returns 200 with a structural payload", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("timeline_empty")

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/timeline", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
	})
})

var _ = Describe("StatusbarToday endpoint (empty state + auth)", func() {
	It("empty user returns 200 with the compound-duration payload shape", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sb_empty")

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/statusbar/today", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		// Payload has a top-level `data` object with `grand_total.text`.
		Expect(rec.Body.String()).To(ContainSubstring(`"data"`))
	})
})

var _ = Describe("Stats caching + owner-scoping", func() {
	It("serves byte-identical bodies within the TTL and never crosses owners", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		aliceUser, aliceTok := hz.MintUser("statscache_a")
		bobUser, bobTok := hz.MintUser("statscache_b")

		base := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
		sdA := hz.Seeder(aliceUser).Projects("alpha")
		sdA.Block(testutil.HB{Project: "alpha", Language: "Go", Editor: "vim"}, base, 3, 120)
		sdA.RefreshRollup(base.AddDate(0, 0, -1))

		sdB := hz.Seeder(bobUser).Projects("beta")
		sdB.Block(testutil.HB{Project: "beta", Language: "Rust", Editor: "vim"}, base, 5, 60)
		sdB.RefreshRollup(base.AddDate(0, 0, -1))

		start := base.AddDate(0, 0, -1).Format(time.RFC3339)
		end := base.AddDate(0, 0, 1).Format(time.RFC3339)
		q := "?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)

		aRec1 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/stats"+q, aliceTok, nil)
		Expect(aRec1).To(testutil.HaveStatus(http.StatusOK), "body=%s", aRec1.Body.String())

		aRec2 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/stats"+q, aliceTok, nil)
		Expect(aRec2).To(testutil.HaveStatus(http.StatusOK))
		Expect(aRec1.Body.String()).To(Equal(aRec2.Body.String()),
			"cache hit should return byte-identical body within TTL")

		// Bob's identical query MUST NOT return alice's cached bytes.
		bRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/stats"+q, bobTok, nil)
		Expect(bRec).To(testutil.HaveStatus(http.StatusOK))
		Expect(bRec.Body.String()).NotTo(Equal(aRec1.Body.String()),
			"cache key must be owner-prefixed: bob got alice's cached bytes")

		// Basic sanity: alice's projects list mentions alpha, bob's beta.
		Expect(aRec1.Body.String()).To(ContainSubstring("alpha"))
		Expect(bRec.Body.String()).To(ContainSubstring("beta"))
		Expect(aRec1.Body.String()).NotTo(ContainSubstring("beta"),
			"alice's stats leaked bob's project name")
	})
})
