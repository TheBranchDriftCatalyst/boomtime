// github.ts — GitHub stats wire types (boom-anh Phase 2). Mirrors
// internal/model/github_stats.go GithubStatsPayload. These are the shapes
// returned by GET /api/v1/users/current/github/stats (authed) and
// GET /api/public/profile/:slug/github/stats (public). NONE carries the token —
// only public GitHub aggregates. The visuals (calendar heatmap, tiles, charts)
// land in P3–P5; P2 is just the data client + types.

// One cell of the trailing-year contribution grid.
export interface GithubContributionDay {
  date: string; // YYYY-MM-DD
  count: number;
}

// One entry of the top-repos-by-stars list.
export interface GithubTopRepo {
  name: string;
  stars: number;
  language?: string;
  url: string;
}

// One aggregated language byte-count across the user's repos.
export interface GithubLanguage {
  name: string;
  bytes: number;
}

// The full aggregate breakdown — a single extensible object so P3–P5 can
// surface any metric (commits, PR reviews, issues, followers, stars, …) as a
// tile/chart without a wire-shape change per metric.
export interface GithubTotals {
  // Contribution breakdown (trailing year).
  commits: number;
  pullRequests: number;
  pullRequestReviews: number;
  issues: number;
  repositories: number;
  restrictedPrivate: number;
  totalContributions: number;
  // Profile aggregates.
  followers: number;
  following: number;
  stars: number;
  publicRepos: number;
  publicGists: number;
  accountAgeDays: number;
}

// GET /api/v1/users/current/github/stats + the public mirror.
// `stale` is true only when the authed endpoint served a last-good cache
// because a refresh was rate-limited (also signalled by the X-Boom-Stats-Stale
// response header).
export interface GithubStatsPayload {
  login: string;
  totals: GithubTotals;
  contributionGrid: GithubContributionDay[];
  topRepos: GithubTopRepo[];
  languages: GithubLanguage[];
  fetchedAt: string;
  stale?: boolean;
}
