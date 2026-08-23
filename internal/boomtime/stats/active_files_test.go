// active_files_test.go — boom-d6x.handler: cover ActiveFiles.
//
// Named invariants:
//
//	"unauth → 4xx" — the endpoint requires a token.
//
//	"empty user → 200 + {files:[], truncated:false} SHAPE" — the FE
//	relies on `files` being an array (never null) so a decode into a
//	typed struct works. A brand-new user must not 5xx and must not
//	render `files: null`.
//
//	"cross-user isolation" — bob's file entities never appear in alice's
//	/files response. Owner scoping is applied inside GetActiveFiles.
//
//	"limit=oversize is clamped to activeFilesMaxLimit (100)" — 150 file
//	entities seeded, limit=99999 requested. Response len(files) MUST be
//	exactly 100 (or reports truncated=true), NEVER 150. A handler that
//	passed the raw ?limit= verbatim to the DB would return 150 rows and
//	this assertion would fail.
package stats_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("ActiveFiles (boom-d6x.handler)", func() {
	It("rejects unauth'd GET with 4xx", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/files", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})

	It("empty user returns 200 + {files:[], truncated:bool} (files is [], never null)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("files_empty")

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/files", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var env struct {
			Files     []map[string]any `json:"files"`
			Truncated bool             `json:"truncated"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed(),
			"decode: %s", rec.Body.String())
		Expect(env.Files).NotTo(BeNil(),
			"files must render as [] not null (FE decode contract): %s", rec.Body.String())
		Expect(env.Files).To(BeEmpty(),
			"fresh user must have zero active files; got %+v", env.Files)
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

	It("limit=99999 is clamped to activeFilesMaxLimit (100) — response size proves the cap fired", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("files_cap")

		// Seed 150 distinct file entities so the natural row count exceeds
		// activeFilesMaxLimit (100). If the handler passed the raw
		// ?limit=99999 through, we'd see 150 files back and fail this test.
		base := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("alpha")
		for i := 0; i < 150; i++ {
			sd.Seed(testutil.HB{
				Project: "alpha", TS: base.Add(time.Duration(i) * time.Minute),
				Entity: fmt.Sprintf("clamp-%03d.go", i), Ty: "file",
				Language: "Go", Editor: "vim", Machine: "m", Platform: "linux",
				Gap: 60,
			})
		}

		start := base.AddDate(0, 0, -1).Format(time.RFC3339)
		end := base.AddDate(0, 0, 1).Format(time.RFC3339)
		q := "?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end) + "&limit=99999"

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/files"+q, token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var env struct {
			Files     []map[string]any `json:"files"`
			Truncated bool             `json:"truncated"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())
		Expect(len(env.Files)).To(BeNumerically("<=", 100),
			"limit clamp regression: expected <=100 files, got %d — activeFilesMaxLimit was bypassed",
			len(env.Files))
		Expect(env.Truncated).To(BeTrue(),
			"with 150 seeded files and cap=100, truncated MUST be true; got %+v", env)
	})

	It("limit=0 and limit=abc are normalized (no 5xx)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("files_lim_norm")

		for _, lim := range []string{"limit=0", "limit=abc"} {
			rec := doJSONReqG(e, http.MethodGet,
				"/api/v1/users/current/files?"+lim, token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK),
				"limit param %q: body=%s", lim, rec.Body.String())
		}
	})
})
