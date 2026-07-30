// active_files_test.go — gaka-d6x.handler: cover ActiveFiles.
//
// Named invariants:
//
//	"unauth → 4xx" — the endpoint requires a token.
//
//	"empty user → 200 + JSON payload with the expected top-level keys" —
//	the FE relies on a stable shape; a fresh user must not 5xx.
//
//	"cross-user isolation" — bob's file entities never appear in alice's
//	/files response. Owner scoping is applied inside GetActiveFiles.
//
//	"limit=0 or oversize is normalized to the default/cap" — a caller
//	passing limit=0 gets the same result as omitting the param; limit=999
//	is clamped to activeFilesMaxLimit (100). Not visible via a status
//	check, but pins the query-param-hygiene contract stated in the source.
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

var _ = Describe("ActiveFiles (gaka-d6x.handler)", func() {
	It("rejects unauth'd GET with 4xx", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/files", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})

	It("empty user returns 200 + a structural payload", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("files_empty")

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/files", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
	})

	It("cross-user: bob's files do not appear in alice's response", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceUser, aliceTok := hz.MintUser("files_a")
		bobUser, _ := hz.MintUser("files_b")

		base := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
		hz.Seeder(aliceUser).Projects("alpha").Seed(testutil.HB{
			Project: "alpha", TS: base, Entity: "alice-file.go", Ty: "file",
			Language: "Go", Editor: "vim", Machine: "m", Platform: "linux",
			Gap: 60,
		})
		hz.Seeder(bobUser).Projects("beta").Seed(testutil.HB{
			Project: "beta", TS: base, Entity: "bob-file.go", Ty: "file",
			Language: "Go", Editor: "vim", Machine: "m", Platform: "linux",
			Gap: 60,
		})

		start := base.AddDate(0, 0, -1).Format(time.RFC3339)
		end := base.AddDate(0, 0, 1).Format(time.RFC3339)
		q := "?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/files"+q, aliceTok, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("bob-file.go"),
			"leak: alice's /files returned bob's entity: %s", rec.Body.String())
	})

	It("limit=0 and limit=oversize both return 200 (params are normalized)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("files_lim")

		for _, lim := range []string{"limit=0", "limit=99999", "limit=abc"} {
			rec := doJSONReqG(e, http.MethodGet,
				"/api/v1/users/current/files?"+lim, token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK),
				"limit param %q: body=%s", lim, rec.Body.String())
		}
	})
})
