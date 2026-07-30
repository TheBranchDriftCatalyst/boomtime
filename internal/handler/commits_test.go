// commits_test.go — gaka-d6x.handler: cover Commits guard paths.
//
// Named invariants:
//
//	"unauth → 4xx" — the endpoint requires a token.
//
//	"missing required query params → 400" — repoName / repoOwner / user
//	each guard with MissingQueryParam BEFORE fetchCommits touches the
//	network. This lets a client debug typos without hitting api.github.com.
//
//	"no GITHUB_TOKEN configured → 500 (MissingGithubToken)" — the
//	handler explicitly rejects when Cfg.GithubToken is empty, so the
//	operator sees the exact env var to set.
//
//	"upstream fetch failure → GenericHTTP (500)" — with a token
//	configured but the fetch failing (invalid token → 401 upstream), the
//	handler surfaces a generic 500 with a message referencing
//	api.github.com. This pins the "no leak of upstream body" contract.
package handler_test

import (
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("Commits endpoint (gaka-d6x.handler)", func() {
	It("rejects unauth'd GET with 4xx", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/commits/alpha/report?repoName=r&repoOwner=o&user=u", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})

	It("rejects a request missing repoName / repoOwner / user with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("commits_missing")

		cases := []string{
			"/api/v1/commits/alpha/report",                                 // all missing
			"/api/v1/commits/alpha/report?repoOwner=o&user=u",              // no repoName
			"/api/v1/commits/alpha/report?repoName=r&user=u",               // no repoOwner
			"/api/v1/commits/alpha/report?repoName=r&repoOwner=o",          // no user
			"/api/v1/commits/alpha/report?repoName=&repoOwner=&user=",      // all blank
		}
		for _, path := range cases {
			rec := doJSONReqG(e, http.MethodGet, path, token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"path %q: body=%s", path, rec.Body.String())
		}
	})

	It("token configured but upstream call fails → 500 with generic api.github.com message (no upstream body leak)", func() {
		hz := testutil.NewHarness(GinkgoT())
		// Nonsense token; api.github.com will 401 → fetchCommits returns
		// an error → handler surfaces a generic 500.
		hz.Cfg.GithubToken = "definitely-not-a-real-token-gaka-d6x"
		e := hz.Router()
		_, token := hz.MintUser("commits_upstream_fail")

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/commits/alpha/report?repoName=nonexistent-repo-gaka-d6x&repoOwner=TheBranchDriftCatalyst&user=someuser",
			token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError),
			"body=%s", rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("api.github.com"),
			"error should reference api.github.com; got %s", rec.Body.String())
	})

	It("no GithubToken → 500 with MissingGithubToken sentinel", func() {
		hz := testutil.NewHarness(GinkgoT())
		// Explicit reset so an env var in the shell doesn't accidentally
		// pass the "token configured" guard.
		hz.Cfg.GithubToken = ""
		e := hz.Router()
		_, token := hz.MintUser("commits_no_gh_token")

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/commits/alpha/report?repoName=r&repoOwner=o&user=u",
			token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError),
			"body=%s", rec.Body.String())
		// The apierr message spells out the env var.
		Expect(rec.Body.String()).To(ContainSubstring("GITHUB_TOKEN"),
			"MissingGithubToken message must reference GITHUB_TOKEN; got %s",
			rec.Body.String())
	})
})
