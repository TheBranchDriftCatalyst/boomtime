// github_stats.go — wire + storage value types for the per-user GitHub stats
// feature (gaka-anh Phase 2). These are the SHARED leaf types: the fetcher
// (internal/github) produces them, the cache layer (internal/db
// GithubStatsCacheRow) stores them as JSONB, and the HTTP payload
// (GithubStatsPayload below) serializes them to the FE. Keeping them in the
// leaf `model` package (which imports nothing internal) lets db + github both
// depend on them without an import cycle.
//
// NONE of these carry the GitHub access token or any secret — they are public
// GitHub aggregates only. The token is decrypted in memory for the API call
// and never travels into any of these structs.
package model

import "time"

// GithubContributionDay is one cell of the trailing-year contribution grid,
// exactly as GitHub's GraphQL contributionsCollection returns it. The whole
// grid is stored/replaced wholesale on every sync — never appended per-day —
// which is what keeps a re-sync a no-op on data (idempotency).
type GithubContributionDay struct {
	Date  string `json:"date"` // YYYY-MM-DD (GitHub's contributionDays.date)
	Count int    `json:"count"`
}

// GithubTopRepo is one entry of the top-repos-by-stars list.
type GithubTopRepo struct {
	Name     string `json:"name"`
	Stars    int    `json:"stars"`
	Language string `json:"language,omitempty"` // primary language ("" when GitHub reports none)
	URL      string `json:"url"`
}

// GithubLanguage is one aggregated language byte-count across the user's repos.
type GithubLanguage struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// GithubTotals is the full aggregate breakdown captured in one sync — stored as
// a SINGLE JSONB blob (github_stats_cache.totals_json) so adding another metric
// later needs no migration, only a new field here. Everything is a trailing-
// year contribution count or a live profile aggregate; none of it is a secret.
//
// The contribution* fields come from the GraphQL contributionsCollection (one
// call); the profile aggregates from REST GET /user; Stars is summed across the
// user's owned repos at sync time.
type GithubTotals struct {
	// Contribution breakdown (trailing year, from contributionsCollection).
	Commits            int `json:"commits"`            // totalCommitContributions
	PullRequests       int `json:"pullRequests"`       // totalPullRequestContributions
	PullRequestReviews int `json:"pullRequestReviews"` // totalPullRequestReviewContributions
	Issues             int `json:"issues"`             // totalIssueContributions
	Repositories       int `json:"repositories"`       // totalRepositoryContributions
	RestrictedPrivate  int `json:"restrictedPrivate"`  // restrictedContributionsCount
	TotalContributions int `json:"totalContributions"` // contributionCalendar.totalContributions

	// Profile aggregates (live, from GET /user + owned-repo scan).
	Followers      int `json:"followers"`
	Following      int `json:"following"`
	Stars          int `json:"stars"` // sum of stargazers across owned repos
	PublicRepos    int `json:"publicRepos"`
	PublicGists    int `json:"publicGists"`
	AccountAgeDays int `json:"accountAgeDays"` // days since account createdAt
}

// GithubStatsPayload is the authed + public wire shape
// (GET /api/v1/users/current/github/stats and
// GET /api/public/profile/:slug/github/stats). It NEVER carries the token.
//
// Totals holds the full aggregate breakdown so P3–P5 can surface any metric as
// a tile/chart. Stale is set true ONLY when the authed endpoint could not
// refresh a stale cache because GitHub rate-limited the sync and it fell back to
// the last-good cached row (the handler also sets the X-Boom-Stats-Stale header).
type GithubStatsPayload struct {
	Login            string                  `json:"login"`
	Totals           GithubTotals            `json:"totals"`
	ContributionGrid []GithubContributionDay `json:"contributionGrid"`
	TopRepos         []GithubTopRepo         `json:"topRepos"`
	Languages        []GithubLanguage        `json:"languages"`
	FetchedAt        time.Time               `json:"fetchedAt"`
	Stale            bool                    `json:"stale,omitempty"`
}
