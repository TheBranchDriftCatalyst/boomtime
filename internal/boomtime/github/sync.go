// sync.go — the SyncUser refresh service (boom-anh Phase 2). Both the
// on-demand-if-stale HTTP path and the `boomtime backfill github-stats` cobra
// command call SyncUser; it is IDEMPOTENT by construction (the assembled row is
// upserted one-row-per-user, replacing not accumulating).
//
// TOKEN HANDLING: the encrypted token is read from users.encrypted_github_token
// and auth.Decrypt'd into an in-memory string that lives ONLY for the fetch. It
// is never logged and never returned. On a 401 from GitHub the token status is
// flipped to 'invalid' (so the Settings card reflects it) and the sync errors.
package github

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// ErrNoToken is returned by SyncUser when the user has no linked GitHub token.
// The handler surfaces it as 404 (nothing to sync); the backfill command skips
// the user.
var ErrNoToken = errors.New("github: no linked token for user")

// syncHTTPTimeout bounds a single sync's total HTTP work so a hung GitHub can't
// wedge a request handler or the backfill loop.
const syncHTTPTimeout = 30 * time.Second

// Service performs a per-user GitHub sync end to end. Constructed once at boot
// (real endpoints) or per-test (mock endpoints).
type Service struct {
	DB         *db.DB
	Logger     *slog.Logger
	restBase   string
	graphqlURL string
	http       *http.Client
}

// uaRoundTripper sets a benign User-Agent on every outbound request without
// mutating the shared request (RoundTripper must not modify its argument).
// Mirrors internal/auth/oidc_resolver.go's transport (boom-93f.23) — GitHub
// requires a UA and a Cloudflare edge 403s the stock Go one.
type uaRoundTripper struct {
	ua   string
	base http.RoundTripper
}

func (t *uaRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.Header.Set("User-Agent", t.ua)
	return t.base.RoundTrip(r2)
}

// NewService builds the production Service pinned to the real GitHub endpoints,
// with the benign-UA http client.
func NewService(database *db.DB, logger *slog.Logger) *Service {
	return &Service{
		DB:         database,
		Logger:     logger,
		restBase:   defaultRESTBaseURL,
		graphqlURL: defaultGraphQLURL,
		http: &http.Client{
			Timeout: syncHTTPTimeout,
			// Instrumented transport UNDER the UA setter: the UA header is still
			// applied, and the wire call is recorded in the outbound RED metrics.
			Transport: &uaRoundTripper{ua: githubUserAgent, base: metrics.InstrumentTransport(http.DefaultTransport)},
		},
	}
}

// NewServiceForTest builds a Service pointed at caller-supplied endpoints (a
// mock-GitHub httptest server). TEST-ONLY seam — production uses NewService.
func NewServiceForTest(database *db.DB, restBase, graphqlURL string) *Service {
	return &Service{
		DB:         database,
		Logger:     slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError})),
		restBase:   restBase,
		graphqlURL: graphqlURL,
		http:       &http.Client{Timeout: syncHTTPTimeout},
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// SyncUser decrypts the user's token, fetches all GitHub stats, assembles ONE
// cache row, and upserts it (replace-on-conflict). Returns the freshly-written
// row so the on-demand handler can serve it without a re-read.
//
// Error contract:
//   - ErrNoToken   — no linked token (handler → 404).
//   - ErrUnauthorized — token rejected; github_token_status set to 'invalid'.
//   - ErrRateLimited  — GitHub rate limited; the handler serves last-good cache.
func (s *Service) SyncUser(ctx context.Context, username string) (db.GithubStatsCacheRow, error) {
	blob, ok, err := s.DB.GetEncryptedGithubToken(ctx, username)
	if err != nil {
		return db.GithubStatsCacheRow{}, err
	}
	if !ok {
		return db.GithubStatsCacheRow{}, ErrNoToken
	}
	tokenBytes, err := auth.Decrypt(blob)
	if err != nil {
		return db.GithubStatsCacheRow{}, err
	}
	// token lives only in this scope; never logged.
	client := newClient(string(tokenBytes), s.restBase, s.graphqlURL, s.http)

	row, err := s.fetchAll(ctx, username, client)
	if err != nil {
		// A 401 means the stored token is no longer valid — record that so the
		// Settings card reflects it, then bubble the error.
		if errors.Is(err, ErrUnauthorized) {
			if uerr := s.DB.UpdateGithubTokenStatus(ctx, username, db.GithubTokenStatusInvalid); uerr != nil {
				s.Logger.Warn("github sync: failed to mark token invalid", "user", username, "err", uerr)
			}
		}
		return db.GithubStatsCacheRow{}, err
	}
	if err := s.DB.UpsertGithubStatsCache(ctx, row); err != nil {
		return db.GithubStatsCacheRow{}, err
	}
	return row, nil
}

// fetchAll runs every GitHub call and assembles the cache row. Extracted so
// SyncUser's error-mapping stays in one place.
func (s *Service) fetchAll(ctx context.Context, username string, client *Client) (db.GithubStatsCacheRow, error) {
	user, err := client.FetchUser(ctx)
	if err != nil {
		return db.GithubStatsCacheRow{}, err
	}
	repos, err := client.FetchRepos(ctx)
	if err != nil {
		return db.GithubStatsCacheRow{}, err
	}
	languages, err := client.FetchLanguages(ctx, repos)
	if err != nil {
		return db.GithubStatsCacheRow{}, err
	}
	contrib, err := client.FetchContributions(ctx, user.Login)
	if err != nil {
		return db.GithubStatsCacheRow{}, err
	}
	accountAgeDays := 0
	if !user.CreatedAt.IsZero() {
		accountAgeDays = int(time.Since(user.CreatedAt).Hours() / 24)
	}
	return db.GithubStatsCacheRow{
		Username:         username,
		Login:            user.Login,
		ContributionGrid: contrib.Grid,
		TopRepos:         TopReposByStars(repos, topReposLimit),
		Languages:        languages,
		Totals: model.GithubTotals{
			Commits:            contrib.Commits,
			PullRequests:       contrib.PullRequests,
			PullRequestReviews: contrib.PullRequestReviews,
			Issues:             contrib.Issues,
			Repositories:       contrib.Repositories,
			RestrictedPrivate:  contrib.RestrictedPrivate,
			TotalContributions: contrib.TotalContributions,
			Followers:          user.Followers,
			Following:          user.Following,
			Stars:              SumStars(repos),
			PublicRepos:        user.PublicRepos,
			PublicGists:        user.PublicGists,
			AccountAgeDays:     accountAgeDays,
		},
		FetchedAt: time.Now().UTC(),
	}, nil
}
