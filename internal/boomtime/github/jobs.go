package github

// GithubStatsRefreshKind is the catalyst-go-jobs kind for the periodic per-user
// GitHub stats refresh (gaka-hney.1). The handler — wired in cmd/boomtime where
// the Service + DB are in scope — fans over db.ListUsersWithGithubToken calling
// Service.SyncUser, so the jobs package stays free of GitHub-specific imports.
const GithubStatsRefreshKind = "github-stats-refresh"
