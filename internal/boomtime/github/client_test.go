package github

// Unit coverage for the GitHub fetcher (gaka-anh Phase 2). No DB — a mock
// httptest server stands in for api.github.com (REST) + the GraphQL endpoint.
// Pins the parse shapes, the pure helpers (TopReposByStars / SumStars), and the
// typed rate-limit / unauthorized error mapping the sync + handler branch on.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockGithub serves the REST + GraphQL endpoints the client calls.
const mockUserJSON = `{"login":"octocat","followers":10,"following":3,"public_repos":5,"public_gists":2,"created_at":"2015-01-01T00:00:00Z"}`
const mockReposJSON = `[
  {"name":"repo-a","full_name":"octocat/repo-a","stargazers_count":100,"language":"Go","html_url":"https://github.com/octocat/repo-a","fork":false},
  {"name":"repo-b","full_name":"octocat/repo-b","stargazers_count":250,"language":"TypeScript","html_url":"https://github.com/octocat/repo-b","fork":false}
]`
const mockGraphQLJSON = `{"data":{"user":{"contributionsCollection":{
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

func newMockGithub() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(mockUserJSON))
	})
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(mockReposJSON))
	})
	mux.HandleFunc("/repos/octocat/repo-a/languages", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Go":12000,"Shell":500}`))
	})
	mux.HandleFunc("/repos/octocat/repo-b/languages", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"TypeScript":8000,"Go":1000}`))
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(mockGraphQLJSON))
	})
	return httptest.NewServer(mux)
}

func testClient(base string) *Client {
	return newClient("gho_test_token", base, base+"/graphql", http.DefaultClient)
}

func TestFetchUser(t *testing.T) {
	srv := newMockGithub()
	defer srv.Close()
	u, err := testClient(srv.URL).FetchUser(context.Background())
	if err != nil {
		t.Fatalf("FetchUser: %v", err)
	}
	if u.Login != "octocat" || u.Followers != 10 || u.Following != 3 || u.PublicRepos != 5 || u.PublicGists != 2 {
		t.Fatalf("unexpected user: %+v", u)
	}
	if u.CreatedAt.Year() != 2015 {
		t.Fatalf("created_at not parsed: %v", u.CreatedAt)
	}
}

func TestFetchReposAndPureHelpers(t *testing.T) {
	srv := newMockGithub()
	defer srv.Close()
	repos, err := testClient(srv.URL).FetchRepos(context.Background())
	if err != nil {
		t.Fatalf("FetchRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("repos len = %d, want 2", len(repos))
	}
	// TopReposByStars sorts by stars desc: repo-b (250) before repo-a (100).
	top := TopReposByStars(repos, 6)
	if top[0].Name != "repo-b" || top[0].Stars != 250 || top[1].Name != "repo-a" {
		t.Fatalf("TopReposByStars order wrong: %+v", top)
	}
	// SumStars totals owned stargazers.
	if s := SumStars(repos); s != 350 {
		t.Fatalf("SumStars = %d, want 350", s)
	}
	// n cap is respected.
	if got := TopReposByStars(repos, 1); len(got) != 1 || got[0].Name != "repo-b" {
		t.Fatalf("TopReposByStars cap wrong: %+v", got)
	}
}

func TestFetchLanguagesAggregates(t *testing.T) {
	srv := newMockGithub()
	defer srv.Close()
	c := testClient(srv.URL)
	repos, _ := c.FetchRepos(context.Background())
	langs, err := c.FetchLanguages(context.Background(), repos)
	if err != nil {
		t.Fatalf("FetchLanguages: %v", err)
	}
	// Go: 12000 + 1000 = 13000 ; TypeScript: 8000 ; Shell: 500. Sorted desc.
	byName := map[string]int64{}
	for _, l := range langs {
		byName[l.Name] = l.Bytes
	}
	if byName["Go"] != 13000 || byName["TypeScript"] != 8000 || byName["Shell"] != 500 {
		t.Fatalf("language aggregate wrong: %+v", langs)
	}
	if langs[0].Name != "Go" {
		t.Fatalf("languages not sorted by bytes desc: %+v", langs)
	}
}

func TestFetchContributionsFlattensFullBreakdown(t *testing.T) {
	srv := newMockGithub()
	defer srv.Close()
	contrib, err := testClient(srv.URL).FetchContributions(context.Background(), "octocat")
	if err != nil {
		t.Fatalf("FetchContributions: %v", err)
	}
	if len(contrib.Grid) != 3 {
		t.Fatalf("grid flattened len = %d, want 3", len(contrib.Grid))
	}
	if contrib.Commits != 42 || contrib.PullRequests != 7 || contrib.PullRequestReviews != 5 ||
		contrib.Issues != 3 || contrib.Repositories != 9 || contrib.RestrictedPrivate != 11 ||
		contrib.TotalContributions != 100 {
		t.Fatalf("contribution totals wrong: %+v", contrib)
	}
}

func TestRateLimitAndUnauthorizedMapping(t *testing.T) {
	// 403 + X-RateLimit-Remaining: 0 -> ErrRateLimited.
	rl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer rl.Close()
	if _, err := testClient(rl.URL).FetchUser(context.Background()); err != ErrRateLimited {
		t.Fatalf("403+remaining0 -> %v, want ErrRateLimited", err)
	}

	// 401 -> ErrUnauthorized.
	un := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer un.Close()
	if _, err := testClient(un.URL).FetchUser(context.Background()); err != ErrUnauthorized {
		t.Fatalf("401 -> %v, want ErrUnauthorized", err)
	}
}

func TestGraphQLRateLimitedError(t *testing.T) {
	// GraphQL reports rate limiting as a 200 with a RATE_LIMITED error entry.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"user":null},"errors":[{"type":"RATE_LIMITED","message":"quota"}]}`))
	}))
	defer srv.Close()
	if _, err := testClient(srv.URL).FetchContributions(context.Background(), "octocat"); err != ErrRateLimited {
		t.Fatalf("graphql RATE_LIMITED -> %v, want ErrRateLimited", err)
	}
}

// The token must be sent as a Bearer Authorization header and never appear in a
// query string or anywhere loggable — assert it reaches GitHub as a header.
func TestTokenSentAsBearerHeader(t *testing.T) {
	var gotAuth, gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotURL = r.URL.String()
		_, _ = w.Write([]byte(mockUserJSON))
	}))
	defer srv.Close()
	if _, err := testClient(srv.URL).FetchUser(context.Background()); err != nil {
		t.Fatalf("FetchUser: %v", err)
	}
	if gotAuth != "Bearer gho_test_token" {
		t.Fatalf("auth header = %q, want Bearer token", gotAuth)
	}
	if strings.Contains(gotURL, "gho_test_token") {
		t.Fatalf("token leaked into URL: %q", gotURL)
	}
}

// Guard the mock JSON stays valid (a broken fixture would mask real regressions).
func TestMockFixturesAreValidJSON(t *testing.T) {
	for name, blob := range map[string]string{"user": mockUserJSON, "repos": mockReposJSON, "graphql": mockGraphQLJSON} {
		var v any
		if err := json.Unmarshal([]byte(blob), &v); err != nil {
			t.Fatalf("mock %s invalid JSON: %v", name, err)
		}
	}
}
