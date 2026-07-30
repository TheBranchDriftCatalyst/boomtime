// projects_test.go — gaka-d6x.handler: cover ProjectStats + ProjectList.
//
// Named invariants:
//
//	"unauth → 4xx" — both endpoints require a token.
//
//	"cross-user ownership check on ProjectStats" — bob's token asking for
//	alice's project name returns 404 (InvalidRelation), NEVER 200 with a
//	silently-empty payload. Prevents a leak-via-oracle.
//
//	"unknown project → 404" — alice asking for a project she doesn't own
//	is 404; the DB check runs before the aggregation query.
//
//	"ProjectList returns her own projects" — after seeding an alpha
//	project, ProjectList response mentions "alpha".
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("ProjectStats (gaka-d6x.handler)", func() {
	It("unauth'd → 4xx", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/users/current/projects/alpha", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})

	It("unknown project name → 404 (InvalidRelation)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("proj_unknown")

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/projects/does-not-exist", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
			"body=%s", rec.Body.String())
	})

	It("bob asking for alice's project → 404 (no oracle)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceUser, _ := hz.MintUser("proj_alice")
		_, bobTok := hz.MintUser("proj_bob")

		base := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
		sd := hz.Seeder(aliceUser).Projects("alpha-secret")
		sd.Block(testutil.HB{Project: "alpha-secret", Language: "Go"}, base, 3, 60)

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/projects/alpha-secret", bobTok, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
			"cross-owner project read must 404; body=%s", rec.Body.String())
	})

	It("owner asking for her own project → 200 + payload", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceUser, aliceTok := hz.MintUser("proj_ok")

		base := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
		sd := hz.Seeder(aliceUser).Projects("alpha")
		sd.Block(testutil.HB{Project: "alpha", Language: "Go", Editor: "vim"}, base, 3, 60)
		sd.RefreshRollup(base.AddDate(0, 0, -1))

		start := base.AddDate(0, 0, -1).Format(time.RFC3339)
		end := base.AddDate(0, 0, 1).Format(time.RFC3339)
		q := "?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/projects/alpha"+q, aliceTok, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK),
			"own project: body=%s", rec.Body.String())
	})
})

var _ = Describe("ProjectList (gaka-d6x.handler)", func() {
	It("unauth'd → 4xx", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})

	It("owner sees her own project; bob's projects never leak in alice's list", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceUser, aliceTok := hz.MintUser("plist_a")
		bobUser, _ := hz.MintUser("plist_b")

		base := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
		hz.Seeder(aliceUser).Projects("alpha-only").
			Block(testutil.HB{Project: "alpha-only", Language: "Go"}, base, 2, 60)
		hz.Seeder(bobUser).Projects("bob-only").
			Block(testutil.HB{Project: "bob-only", Language: "Rust"}, base, 2, 60)

		start := base.AddDate(0, 0, -1).Format(time.RFC3339)
		end := base.AddDate(0, 0, 1).Format(time.RFC3339)
		q := "?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/projects"+q, aliceTok, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("alpha-only"),
			"alice's own project missing: %s", rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("bob-only"),
			"alice's list leaked bob's project: %s", rec.Body.String())
	})
})
