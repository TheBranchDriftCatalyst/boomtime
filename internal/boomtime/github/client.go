// Package github is the GitHub data fetcher for the per-user stats feature
// (boom-anh Phase 2). It talks REST (GET /user, /user/repos, per-repo
// /languages) + GraphQL (contributionsCollection) using a per-user OAuth token
// held in memory ONLY for the duration of a call — never logged, never
// persisted here (the encrypted token lives on users.encrypted_github_token;
// decryption happens in sync.go).
//
// The assembled results become a single db.GithubStatsCacheRow (see sync.go),
// which is upserted one-row-per-user, so a re-sync is a no-op on data. The
// GraphQL contribution grid is the FULL trailing year in one response and is
// stored wholesale — this client never returns partial/incremental deltas that
// a caller might be tempted to append.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// Default GitHub endpoints. Overridable per-Client so the mock-GitHub httptest
// server in tests can stand in for api.github.com.
const (
	defaultRESTBaseURL = "https://api.github.com"
	defaultGraphQLURL  = "https://api.github.com/graphql"
)

// githubUserAgent is a benign, self-identifying User-Agent. GitHub requires a
// UA header, and a Cloudflare-proxied edge 403s the stock "Go-http-client" UA
// (boom-93f.23) — this mirrors the OIDC/OAuth resolvers' benign-UA approach.
const githubUserAgent = "boomtime-github/1.0 (+https://boomtime.knowledgedump.space)"

// Tunables that keep each sync small — we never page through thousands of
// repos. topReposLimit caps the by-stars list we surface; languageRepoCap
// bounds how many repos we hit for /languages (one HTTP call each), and
// reposPerPage caps the single /user/repos page we pull.
const (
	topReposLimit    = 6
	languageRepoCap  = 25
	reposPerPage     = 100
	contributionYear = 365 * 24 * time.Hour
)

// Typed errors the sync + handler layers branch on.
var (
	// ErrRateLimited means GitHub returned 403/429 with X-RateLimit-Remaining: 0
	// (REST) or a RATE_LIMITED GraphQL error. The handler serves the last-good
	// cache + sets X-Boom-Stats-Stale on this.
	ErrRateLimited = errors.New("github: rate limited")
	// ErrUnauthorized means GitHub rejected the token (401). sync.go flips
	// github_token_status to 'invalid' on this.
	ErrUnauthorized = errors.New("github: unauthorized (token invalid)")
)

// Client performs the REST + GraphQL fetches for one user's token.
type Client struct {
	token      string
	http       *http.Client
	restBase   string
	graphqlURL string
}

// newClient builds a Client bound to token + endpoints, using the shared
// http.Client (which carries the benign-UA transport in production).
func newClient(token, restBase, graphqlURL string, httpClient *http.Client) *Client {
	if restBase == "" {
		restBase = defaultRESTBaseURL
	}
	if graphqlURL == "" {
		graphqlURL = defaultGraphQLURL
	}
	return &Client{
		token:      token,
		http:       httpClient,
		restBase:   strings.TrimRight(restBase, "/"),
		graphqlURL: graphqlURL,
	}
}

// User is the subset of GET /user we read: the login + live profile aggregates
// (followers/following/public repos+gists/account age). The contribution
// breakdown comes from GraphQL (FetchContributions), so we take each datum from
// exactly one source.
type User struct {
	Login       string    `json:"login"`
	Followers   int       `json:"followers"`
	Following   int       `json:"following"`
	PublicRepos int       `json:"public_repos"`
	PublicGists int       `json:"public_gists"`
	CreatedAt   time.Time `json:"created_at"`
}

// Repo is the subset of GET /user/repos we read. FullName ("owner/name") is
// used to address the per-repo /languages endpoint.
type Repo struct {
	Name       string `json:"name"`
	FullName   string `json:"full_name"`
	Stargazers int    `json:"stargazers_count"`
	Language   string `json:"language"`
	HTMLURL    string `json:"html_url"`
	Fork       bool   `json:"fork"`
}

// FetchUser reads the authenticated user's login, followers, and public-repo
// count from GET /user.
func (c *Client) FetchUser(ctx context.Context) (User, error) {
	var u User
	if err := c.doREST(ctx, "/user", &u); err != nil {
		return User{}, err
	}
	return u, nil
}

// FetchRepos pulls a single page (up to reposPerPage) of the user's repos,
// owner-affiliated, sorted by most-recently-pushed. We deliberately do NOT
// page through everything — a user with thousands of repos would burn the rate
// budget; the top-by-stars slice + language aggregate over this page is a
// faithful summary. Sorting to the top-N by stars happens in TopReposByStars.
func (c *Client) FetchRepos(ctx context.Context) ([]Repo, error) {
	q := url.Values{}
	q.Set("per_page", fmt.Sprintf("%d", reposPerPage))
	q.Set("sort", "pushed")
	// boom-anh fix: GitHub /user/repos returns 422 when `type` is combined with
	// `affiliation` (or `visibility`). Use `affiliation=owner` ALONE to fetch
	// the user's own repos — don't also set `type`.
	q.Set("affiliation", "owner")
	var repos []Repo
	if err := c.doREST(ctx, "/user/repos?"+q.Encode(), &repos); err != nil {
		return nil, err
	}
	return repos, nil
}

// TopReposByStars returns the top-N repos by star count as the wire type. Pure
// (no HTTP) so the sync + tests can call it on an already-fetched slice.
func TopReposByStars(repos []Repo, n int) []model.GithubTopRepo {
	sorted := make([]Repo, len(repos))
	copy(sorted, repos)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Stargazers != sorted[j].Stargazers {
			return sorted[i].Stargazers > sorted[j].Stargazers
		}
		return sorted[i].Name < sorted[j].Name
	})
	if n > len(sorted) {
		n = len(sorted)
	}
	out := make([]model.GithubTopRepo, 0, n)
	for _, r := range sorted[:n] {
		out = append(out, model.GithubTopRepo{
			Name:     r.Name,
			Stars:    r.Stargazers,
			Language: r.Language,
			URL:      r.HTMLURL,
		})
	}
	return out
}

// FetchLanguages aggregates language byte-counts across (up to languageRepoCap)
// of the passed repos via GET /repos/{full_name}/languages, returning a
// breakdown sorted by descending bytes. Capping the number of repos bounds the
// HTTP fan-out — one call per repo — so a sync stays small.
func (c *Client) FetchLanguages(ctx context.Context, repos []Repo) ([]model.GithubLanguage, error) {
	// Aggregate over the most-starred repos first (they best represent the
	// user's language mix) within the cap.
	ranked := make([]Repo, len(repos))
	copy(ranked, repos)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Stargazers > ranked[j].Stargazers })
	if len(ranked) > languageRepoCap {
		ranked = ranked[:languageRepoCap]
	}

	totals := map[string]int64{}
	for _, r := range ranked {
		if r.FullName == "" {
			continue
		}
		var langBytes map[string]int64
		if err := c.doREST(ctx, "/repos/"+r.FullName+"/languages", &langBytes); err != nil {
			// A rate-limit / auth failure is terminal — bubble it so the sync
			// aborts and the handler can serve stale. A single-repo 404 (repo
			// deleted mid-sync) should NOT be fatal, but doREST only returns
			// typed rate/auth errors + generic ones; treat rate/auth as fatal
			// and skip other per-repo errors.
			if errors.Is(err, ErrRateLimited) || errors.Is(err, ErrUnauthorized) {
				return nil, err
			}
			continue
		}
		for name, n := range langBytes {
			totals[name] += n
		}
	}

	out := make([]model.GithubLanguage, 0, len(totals))
	for name, n := range totals {
		out = append(out, model.GithubLanguage{Name: name, Bytes: n})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Contributions is the parsed contributionsCollection: the full trailing-year
// daily grid + the FULL contribution breakdown (commits, PRs, PR reviews,
// issues, repositories, restricted/private count, and the calendar total).
type Contributions struct {
	Grid               []model.GithubContributionDay
	TotalContributions int
	Commits            int
	PullRequests       int
	PullRequestReviews int
	Issues             int
	Repositories       int
	RestrictedPrivate  int
}

// contributionsQuery is the GraphQL query. `from` pins the window to the
// trailing year so the returned grid is deterministic; GitHub returns the whole
// calendar + every contribution total in ONE response, which we flatten and
// store wholesale. Pulls the FULL breadth (commits, PRs, PR reviews, issues,
// repositories, restricted/private) so the payload can surface any of them.
const contributionsQuery = `query($login:String!, $from:DateTime!) {
  user(login: $login) {
    contributionsCollection(from: $from) {
      totalCommitContributions
      totalPullRequestContributions
      totalPullRequestReviewContributions
      totalIssueContributions
      totalRepositoryContributions
      restrictedContributionsCount
      contributionCalendar {
        totalContributions
        weeks {
          contributionDays { date contributionCount }
        }
      }
    }
  }
}`

// graphQLResponse mirrors the query shape above.
type graphQLResponse struct {
	Data struct {
		User struct {
			ContributionsCollection struct {
				TotalCommitContributions            int `json:"totalCommitContributions"`
				TotalPullRequestContributions       int `json:"totalPullRequestContributions"`
				TotalPullRequestReviewContributions int `json:"totalPullRequestReviewContributions"`
				TotalIssueContributions             int `json:"totalIssueContributions"`
				TotalRepositoryContributions        int `json:"totalRepositoryContributions"`
				RestrictedContributionsCount        int `json:"restrictedContributionsCount"`
				ContributionCalendar                struct {
					TotalContributions int `json:"totalContributions"`
					Weeks              []struct {
						ContributionDays []struct {
							Date              string `json:"date"`
							ContributionCount int    `json:"contributionCount"`
						} `json:"contributionDays"`
					} `json:"weeks"`
				} `json:"contributionCalendar"`
			} `json:"contributionsCollection"`
		} `json:"user"`
	} `json:"data"`
	Errors []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"errors"`
}

// FetchContributions runs the GraphQL query for login and flattens the calendar
// into a flat day slice. The grid is the whole trailing year — stored wholesale
// on the cache row (replace, never append).
func (c *Client) FetchContributions(ctx context.Context, login string) (Contributions, error) {
	from := time.Now().UTC().Add(-contributionYear).Format(time.RFC3339)
	body, _ := json.Marshal(map[string]any{
		"query": contributionsQuery,
		"variables": map[string]any{
			"login": login,
			"from":  from,
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlURL, strings.NewReader(string(body)))
	if err != nil {
		return Contributions{}, fmt.Errorf("github graphql: build request failed")
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Contributions{}, fmt.Errorf("github graphql: request failed")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if rlErr := classifyStatus(resp); rlErr != nil {
		return Contributions{}, rlErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Contributions{}, fmt.Errorf("github graphql: status %d", resp.StatusCode)
	}
	var gr graphQLResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		return Contributions{}, fmt.Errorf("github graphql: malformed response")
	}
	// GraphQL reports rate limiting as a 200 with a RATE_LIMITED error entry.
	for _, e := range gr.Errors {
		if strings.EqualFold(e.Type, "RATE_LIMITED") {
			return Contributions{}, ErrRateLimited
		}
	}
	if len(gr.Errors) > 0 {
		return Contributions{}, fmt.Errorf("github graphql: %s", gr.Errors[0].Type)
	}

	cc := gr.Data.User.ContributionsCollection
	grid := make([]model.GithubContributionDay, 0, 371)
	for _, w := range cc.ContributionCalendar.Weeks {
		for _, d := range w.ContributionDays {
			grid = append(grid, model.GithubContributionDay{Date: d.Date, Count: d.ContributionCount})
		}
	}
	return Contributions{
		Grid:               grid,
		TotalContributions: cc.ContributionCalendar.TotalContributions,
		Commits:            cc.TotalCommitContributions,
		PullRequests:       cc.TotalPullRequestContributions,
		PullRequestReviews: cc.TotalPullRequestReviewContributions,
		Issues:             cc.TotalIssueContributions,
		Repositories:       cc.TotalRepositoryContributions,
		RestrictedPrivate:  cc.RestrictedContributionsCount,
	}, nil
}

// SumStars totals the stargazers across the passed owned repos — the "total
// stars" profile aggregate. Pure (no HTTP) so sync + tests share it.
func SumStars(repos []Repo) int {
	var total int
	for _, r := range repos {
		total += r.Stargazers
	}
	return total
}

// doREST issues a GET to restBase+path and decodes JSON into out. Maps 401 ->
// ErrUnauthorized and 403/429-with-remaining-0 -> ErrRateLimited so callers can
// branch; other non-2xx become a generic error.
func (c *Client) doREST(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.restBase+path, nil)
	if err != nil {
		return fmt.Errorf("github rest: build request failed")
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github rest: request failed")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if clErr := classifyStatus(resp); clErr != nil {
		return clErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github rest: status %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("github rest: malformed response")
	}
	return nil
}

// setHeaders applies the token + Accept + benign UA. The token is NEVER logged.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", githubUserAgent)
}

// classifyStatus returns ErrUnauthorized on 401, ErrRateLimited on a
// 403/429 that carries X-RateLimit-Remaining: 0, else nil (the caller then
// checks the 2xx range).
func classifyStatus(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden, http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return ErrRateLimited
		}
		// A 403 without the remaining-0 marker (e.g. secondary rate limit /
		// abuse detection) — treat as rate limiting too, conservatively, so we
		// serve stale rather than error the user.
		if resp.StatusCode == http.StatusTooManyRequests {
			return ErrRateLimited
		}
	}
	return nil
}
