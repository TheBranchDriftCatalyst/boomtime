// github_stats_test.go — end-to-end coverage of the GitHub stats endpoints
// (gaka-anh Phase 2). A mock-GitHub httptest server (REST /user, /user/repos,
// per-repo /languages, and /graphql) drives github.Service.SyncUser through the
// real Echo router + DB. Pins: IDEMPOTENCY (sync twice -> one row, no doubling),
// the authed cache-or-sync policy (fresh / stale-resync / rate-limited-stale),
// the public cache-only policy (serves cache, non-public 404s, never syncs), and
// that the token NEVER leaks into a response.
package identity_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/github"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

const ghStatsToken = "gho_STATS_TOKEN_should_never_leak"

const ghStatsGraphQL = `{"data":{"user":{"contributionsCollection":{
  "totalCommitContributions":42,
  "totalPullRequestContributions":7,
  "totalPullRequestReviewContributions":5,
  "totalIssueContributions":3,
  "totalRepositoryContributions":9,
  "restrictedContributionsCount":11,
  "contributionCalendar":{"totalContributions":100,"weeks":[
    {"contributionDays":[{"date":"2026-01-01","contributionCount":3},{"date":"2026-01-02","contributionCount":0}]},
    {"contributionDays":[{"date":"2026-01-03","contributionCount":5}]}
  ]}
}}}}`

// mockGithubStats serves the endpoints SyncUser calls, counting every hit so a
// test can assert "no sync happened". mode=="ratelimited" makes /user return
// 403 + X-RateLimit-Remaining: 0.
func mockGithubStats(hits *int32, mode string) *httptest.Server {
	mux := http.NewServeMux()
	guard := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(hits, 1)
			if mode == "ratelimited" {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("/user", guard(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"login":"octocat","followers":10,"following":3,"public_repos":5,"public_gists":2,"created_at":"2015-01-01T00:00:00Z"}`))
	}))
	mux.HandleFunc("/user/repos", guard(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"repo-a","full_name":"octocat/repo-a","stargazers_count":100,"language":"Go","html_url":"https://github.com/octocat/repo-a"}]`))
	}))
	mux.HandleFunc("/repos/octocat/repo-a/languages", guard(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Go":12000}`))
	}))
	mux.HandleFunc("/graphql", guard(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(ghStatsGraphQL))
	}))
	return httptest.NewServer(mux)
}

// seedGithubToken installs the encryption key and stores an encrypted token for
// user so SyncUser can decrypt + fetch.
func seedGithubToken(hz *testutil.Harness, user string) {
	ct, err := auth.Encrypt([]byte(ghStatsToken))
	Expect(err).NotTo(HaveOccurred())
	Expect(hz.DB.SetEncryptedGithubToken(context.Background(), user, ct, "octocat", db.GithubTokenStatusValid)).To(Succeed())
}

// wireGithubStats builds the router and points the identity handler's stats
// service at the mock server.
func wireGithubStats(hz *testutil.Harness, srv *httptest.Server) http.Handler {
	e := hz.Router()
	hz.H.Identity.GithubStats = github.NewServiceForTest(hz.DB, srv.URL, srv.URL+"/graphql")
	return e
}

var _ = Describe("GitHub stats (gaka-anh Phase 2)", func() {
	ctx := context.Background()

	Describe("idempotent sync", func() {
		It("syncing the SAME user twice yields exactly one row with un-doubled totals", func() {
			installEncryptionKeyAC()
			hz := testutil.NewHarness(GinkgoT())
			var hits int32
			srv := mockGithubStats(&hits, "ok")
			DeferCleanup(srv.Close)
			_ = wireGithubStats(hz, srv)

			user, _ := hz.MintUser("gh_idem")
			seedGithubToken(hz, user)

			r1, err := hz.H.Identity.GithubStats.SyncUser(ctx, user)
			Expect(err).NotTo(HaveOccurred())
			r2, err := hz.H.Identity.GithubStats.SyncUser(ctx, user)
			Expect(err).NotTo(HaveOccurred())

			// Exactly one row.
			var n int
			Expect(hz.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM github_stats_cache WHERE username=$1`, user).Scan(&n)).To(Succeed())
			Expect(n).To(Equal(1), "re-sync accrued a duplicate row")

			// Totals equal a single fetch — commits is 42 (not 84), grid length stable.
			Expect(r1.Totals.Commits).To(Equal(42))
			Expect(r2.Totals.Commits).To(Equal(42), "commits doubled on re-sync")
			Expect(r2.Totals.PullRequestReviews).To(Equal(5))
			Expect(len(r2.ContributionGrid)).To(Equal(len(r1.ContributionGrid)))

			// The stored row equals a single fetch, too.
			stored, ok, err := hz.DB.GetGithubStatsCache(ctx, user)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(stored.Totals.Commits).To(Equal(42))
			Expect(stored.Totals.Stars).To(Equal(100))
		})
	})

	Describe("authed GET /github/stats", func() {
		It("serves a FRESH cache without syncing (no GitHub hit)", func() {
			installEncryptionKeyAC()
			hz := testutil.NewHarness(GinkgoT())
			var hits int32
			srv := mockGithubStats(&hits, "ok")
			DeferCleanup(srv.Close)
			e := wireGithubStats(hz, srv)

			user, token := hz.MintUser("gh_fresh")
			// Fresh cache row (now) with a distinctive commit count.
			Expect(hz.DB.UpsertGithubStatsCache(ctx, db.GithubStatsCacheRow{
				Username: user, Login: "cached", Totals: model.GithubTotals{Commits: 7}, FetchedAt: time.Now().UTC(),
			})).To(Succeed())

			rec := doAuthedGet(e, "/api/v1/users/current/github/stats", token)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(atomic.LoadInt32(&hits)).To(Equal(int32(0)), "fresh cache should not trigger a GitHub sync")

			var p model.GithubStatsPayload
			Expect(json.Unmarshal(rec.Body.Bytes(), &p)).To(Succeed())
			Expect(p.Totals.Commits).To(Equal(7))
			Expect(p.Stale).To(BeFalse())
		})

		It("re-syncs a STALE cache and returns the fresh data", func() {
			installEncryptionKeyAC()
			hz := testutil.NewHarness(GinkgoT())
			var hits int32
			srv := mockGithubStats(&hits, "ok")
			DeferCleanup(srv.Close)
			e := wireGithubStats(hz, srv)

			user, token := hz.MintUser("gh_stale")
			seedGithubToken(hz, user)
			// Stale cache row (2h old) with an OLD value.
			Expect(hz.DB.UpsertGithubStatsCache(ctx, db.GithubStatsCacheRow{
				Username: user, Login: "old", Totals: model.GithubTotals{Commits: 1}, FetchedAt: time.Now().UTC().Add(-2 * time.Hour),
			})).To(Succeed())

			rec := doAuthedGet(e, "/api/v1/users/current/github/stats", token)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(atomic.LoadInt32(&hits)).To(BeNumerically(">", 0), "stale cache should trigger a sync")

			var p model.GithubStatsPayload
			Expect(json.Unmarshal(rec.Body.Bytes(), &p)).To(Succeed())
			Expect(p.Login).To(Equal("octocat"))
			Expect(p.Totals.Commits).To(Equal(42))
			Expect(p.Stale).To(BeFalse())
			Expect(rec.Body.String()).NotTo(ContainSubstring(ghStatsToken))
		})

		It("serves the last-good cache marked STALE when the refresh is rate-limited", func() {
			installEncryptionKeyAC()
			hz := testutil.NewHarness(GinkgoT())
			var hits int32
			srv := mockGithubStats(&hits, "ratelimited")
			DeferCleanup(srv.Close)
			e := wireGithubStats(hz, srv)

			user, token := hz.MintUser("gh_rl")
			seedGithubToken(hz, user)
			Expect(hz.DB.UpsertGithubStatsCache(ctx, db.GithubStatsCacheRow{
				Username: user, Login: "old", Totals: model.GithubTotals{Commits: 1}, FetchedAt: time.Now().UTC().Add(-2 * time.Hour),
			})).To(Succeed())

			rec := doAuthedGet(e, "/api/v1/users/current/github/stats", token)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Header().Get("X-Boom-Stats-Stale")).To(Equal("true"))

			var p model.GithubStatsPayload
			Expect(json.Unmarshal(rec.Body.Bytes(), &p)).To(Succeed())
			Expect(p.Stale).To(BeTrue())
			Expect(p.Totals.Commits).To(Equal(1), "should serve the OLD cached value on rate-limit")
		})

		It("404s when GitHub is not connected and no cache exists", func() {
			installEncryptionKeyAC()
			hz := testutil.NewHarness(GinkgoT())
			var hits int32
			srv := mockGithubStats(&hits, "ok")
			DeferCleanup(srv.Close)
			e := wireGithubStats(hz, srv)

			_, token := hz.MintUser("gh_none")
			rec := doAuthedGet(e, "/api/v1/users/current/github/stats", token)
			Expect(rec.Code).To(Equal(http.StatusNotFound))
			Expect(atomic.LoadInt32(&hits)).To(Equal(int32(0)))
		})
	})

	Describe("public GET /api/public/profile/:slug/github/stats", func() {
		It("serves the cached payload for a public profile WITHOUT syncing", func() {
			installEncryptionKeyAC()
			hz := testutil.NewHarness(GinkgoT())
			var hits int32
			srv := mockGithubStats(&hits, "ok")
			DeferCleanup(srv.Close)
			e := wireGithubStats(hz, srv)

			user, _ := hz.MintUser("gh_pub")
			slug := fmt.Sprintf("ghpub-%d", time.Now().UnixNano()%1000000)
			Expect(hz.DB.SetPublicProfile(ctx, user, true, slug)).To(Succeed())
			// Stale cache — the public path must serve it as-is (never re-sync).
			Expect(hz.DB.UpsertGithubStatsCache(ctx, db.GithubStatsCacheRow{
				Username: user, Login: "octocat", Totals: model.GithubTotals{Commits: 33}, FetchedAt: time.Now().UTC().Add(-5 * time.Hour),
			})).To(Succeed())

			rec := doAuthedGet(e, "/api/public/profile/"+slug+"/github/stats", "")
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(atomic.LoadInt32(&hits)).To(Equal(int32(0)), "public path must NOT burn the owner's rate budget")

			var p model.GithubStatsPayload
			Expect(json.Unmarshal(rec.Body.Bytes(), &p)).To(Succeed())
			Expect(p.Totals.Commits).To(Equal(33))
		})

		It("404s when the profile is not public", func() {
			installEncryptionKeyAC()
			hz := testutil.NewHarness(GinkgoT())
			var hits int32
			srv := mockGithubStats(&hits, "ok")
			DeferCleanup(srv.Close)
			e := wireGithubStats(hz, srv)

			user, _ := hz.MintUser("gh_priv")
			slug := fmt.Sprintf("ghpriv-%d", time.Now().UnixNano()%1000000)
			// Slug set but profile DISABLED.
			Expect(hz.DB.SetPublicProfile(ctx, user, false, slug)).To(Succeed())
			Expect(hz.DB.UpsertGithubStatsCache(ctx, db.GithubStatsCacheRow{
				Username: user, Login: "octocat", Totals: model.GithubTotals{Commits: 33}, FetchedAt: time.Now().UTC(),
			})).To(Succeed())

			rec := doAuthedGet(e, "/api/public/profile/"+slug+"/github/stats", "")
			Expect(rec.Code).To(Equal(http.StatusNotFound))
		})

		It("404s when a public profile has no cached stats", func() {
			installEncryptionKeyAC()
			hz := testutil.NewHarness(GinkgoT())
			var hits int32
			srv := mockGithubStats(&hits, "ok")
			DeferCleanup(srv.Close)
			e := wireGithubStats(hz, srv)

			user, _ := hz.MintUser("gh_pubempty")
			slug := fmt.Sprintf("ghpube-%d", time.Now().UnixNano()%1000000)
			Expect(hz.DB.SetPublicProfile(ctx, user, true, slug)).To(Succeed())

			rec := doAuthedGet(e, "/api/public/profile/"+slug+"/github/stats", "")
			Expect(rec.Code).To(Equal(http.StatusNotFound))
			Expect(atomic.LoadInt32(&hits)).To(Equal(int32(0)))
		})
	})

	Describe("token never leaks", func() {
		It("never returns the stored token from the authed sync path", func() {
			installEncryptionKeyAC()
			hz := testutil.NewHarness(GinkgoT())
			var hits int32
			srv := mockGithubStats(&hits, "ok")
			DeferCleanup(srv.Close)
			e := wireGithubStats(hz, srv)

			user, token := hz.MintUser("gh_leak")
			seedGithubToken(hz, user)
			// No cache -> full sync path.
			rec := doAuthedGet(e, "/api/v1/users/current/github/stats", token)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).NotTo(ContainSubstring(ghStatsToken))
			// And the stored cache row carries no token either.
			var raw string
			Expect(hz.DB.Pool.QueryRow(ctx, `SELECT row_to_json(github_stats_cache)::text FROM github_stats_cache WHERE username=$1`, user).Scan(&raw)).To(Succeed())
			Expect(raw).NotTo(ContainSubstring(ghStatsToken))
		})
	})
})

// doAuthedGet issues a GET with optional Basic auth against the harness router.
func doAuthedGet(e http.Handler, target, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}
