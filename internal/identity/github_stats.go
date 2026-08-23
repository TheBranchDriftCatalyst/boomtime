// github_stats.go — the per-user GitHub stats endpoints (boom-anh Phase 2).
//
//	GET /api/v1/users/current/github/stats        (authed)  — cache-or-sync
//	GET /api/public/profile/:slug/github/stats     (public) — cache-only
//
// IDEMPOTENCY / RATE-LIMIT / SYNC POLICY:
//
//   - The authed endpoint serves the cache when it is fresh (< githubStatsTTL).
//     When stale (or absent) it calls Service.SyncUser, which upserts ONE row
//     per user — a re-sync REPLACES, never accumulates.
//   - On a GitHub rate-limit during that sync we serve the last-good cache and
//     set X-Boom-Stats-Stale: true (payload.Stale=true) rather than erroring.
//   - The PUBLIC endpoint is READ-ONLY: it NEVER triggers a sync. Public hits
//     must not burn the owner's rate budget — it serves the cache or 404s. It
//     resolves slug→user and respects public_profile_enabled, mirroring
//     PublicProfile.
//
// SECURITY: neither endpoint ever returns the GitHub token — the payload is
// public GitHub aggregates only (model.GithubStatsPayload). The sync decrypts
// the token in memory for the API calls (internal/github/sync.go); it never
// reaches this layer.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/github"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/jackc/pgx/v5"
)

// githubStatsTTL bounds how long a cached row is served before the authed
// endpoint re-syncs. ~1h keeps the dashboard reasonably live without hammering
// the GitHub API (each sync is several REST + one GraphQL call).
const githubStatsTTL = time.Hour

// staleHeader is set to "true" when the authed endpoint served a stale cache
// because a refresh was rate-limited.
const staleHeader = "X-Boom-Stats-Stale"

// GetGithubStats: GET /api/v1/users/current/github/stats (authed).
//
//   - No linked token → 404 (nothing to show).
//   - Fresh cache → return it.
//   - Stale/absent cache → SyncUser, then return the fresh row.
//   - Rate-limited sync with a prior cache → return the stale cache + the
//     X-Boom-Stats-Stale header. With no prior cache → 503.
func (h *Handler) GetGithubStats(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	ctx := c.Request().Context()

	cached, hasCache, err := h.DB.GetGithubStatsCache(ctx, owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "github stats cache lookup failed", err)
	}
	// Fresh cache short-circuits — no token touch, no GitHub call.
	if hasCache && time.Since(cached.FetchedAt) < githubStatsTTL {
		return c.JSON(http.StatusOK, toGithubStatsPayload(cached, false))
	}

	// Cache is stale or absent — we need to sync, which requires a linked token.
	info, err := h.DB.GetGithubTokenInfo(ctx, owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "github connection lookup failed", err)
	}
	if !info.Connected {
		// No token to sync with. Even if a stale cache lingers, report 404 —
		// disconnect clears the cache, so this is the "never connected" case.
		return apihelpers.RespondErr(c, apierr.NotFound("GitHub is not connected"))
	}

	if h.GithubStats == nil {
		return apihelpers.InternalErr(h.Logger, c, "github stats service not wired", errors.New("nil GithubStats service"))
	}
	fresh, serr := h.GithubStats.SyncUser(ctx, owner)
	if serr != nil {
		switch {
		case errors.Is(serr, github.ErrRateLimited):
			// Serve the last-good cache marked stale; 503 when we have none.
			if hasCache {
				c.Response().Header().Set(staleHeader, "true")
				return c.JSON(http.StatusOK, toGithubStatsPayload(cached, true))
			}
			return apihelpers.RespondErr(c, apierr.New(http.StatusServiceUnavailable, "GitHub rate limited and no cached stats yet", nil))
		case errors.Is(serr, github.ErrNoToken):
			return apihelpers.RespondErr(c, apierr.NotFound("GitHub is not connected"))
		case errors.Is(serr, github.ErrUnauthorized):
			// Token was rejected (SyncUser already flipped status to invalid).
			// Serve a stale cache if we have one; otherwise surface a 502.
			if hasCache {
				c.Response().Header().Set(staleHeader, "true")
				return c.JSON(http.StatusOK, toGithubStatsPayload(cached, true))
			}
			return apihelpers.RespondErr(c, apierr.New(http.StatusBadGateway, "GitHub rejected the stored token", nil))
		default:
			// Any other fetch/DB failure: serve stale if possible, else 502.
			if hasCache {
				c.Response().Header().Set(staleHeader, "true")
				return c.JSON(http.StatusOK, toGithubStatsPayload(cached, true))
			}
			return apihelpers.InternalErr(h.Logger, c, "github stats sync failed", serr)
		}
	}
	return c.JSON(http.StatusOK, toGithubStatsPayload(fresh, false))
}

// PublicGithubStats: GET /api/public/profile/:slug/github/stats (NO auth).
// Resolves slug→user, respects public_profile_enabled, and serves the cached
// payload READ-ONLY. It NEVER triggers a sync (a public hit must not burn the
// owner's rate budget) — 404 when no cache exists. Mirrors PublicProfile's
// slug resolution + terse "not public" 404 so slug existence isn't confirmed.
func (h *Handler) PublicGithubStats(c *echo.Context) error {
	slug := strings.ToLower(strings.TrimSpace(c.Param("slug")))
	if slug == "" || !publicProfileSlugRe.MatchString(slug) {
		return apihelpers.RespondErr(c, apierr.NotFound("This profile isn't public"))
	}
	ctx := c.Request().Context()

	username, err := h.DB.LookupUsernameBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apihelpers.RespondErr(c, apierr.NotFound("This profile isn't public"))
		}
		return apihelpers.InternalErr(h.Logger, c, "public github stats slug lookup failed", err)
	}
	enabled, _, err := h.DB.GetPublicProfile(ctx, username)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "public github stats enabled check failed", err)
	}
	if !enabled {
		return apihelpers.RespondErr(c, apierr.NotFound("This profile isn't public"))
	}

	cached, hasCache, err := h.DB.GetGithubStatsCache(ctx, username)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "public github stats cache lookup failed", err)
	}
	if !hasCache {
		// No sync from the public path — the owner (or the backfill command)
		// must have populated the cache first.
		return apihelpers.RespondErr(c, apierr.NotFound("No GitHub stats published for this profile"))
	}

	payload := toGithubStatsPayload(cached, false)
	body, err := json.Marshal(payload)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "public github stats marshal failed", err)
	}
	// Same short-cache + ETag posture as PublicProfile: brief browser cache so a
	// disabled/rotated profile propagates promptly, ETag for cheap 304s.
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	c.Response().Header().Set("Cache-Control", "public, max-age=60, must-revalidate")
	c.Response().Header().Set("ETag", etag)
	if match := c.Request().Header.Get("If-None-Match"); match != "" && match == etag {
		return c.NoContent(http.StatusNotModified)
	}
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c.Response().WriteHeader(http.StatusOK)
	_, werr := c.Response().Write(body)
	return werr
}

// toGithubStatsPayload maps a cache row to the wire payload. stale marks a
// last-good cache served because a refresh was rate-limited. Nil JSON slices
// become empty arrays on the wire (never null) for a stable FE contract.
func toGithubStatsPayload(row db.GithubStatsCacheRow, stale bool) model.GithubStatsPayload {
	grid := row.ContributionGrid
	if grid == nil {
		grid = []model.GithubContributionDay{}
	}
	repos := row.TopRepos
	if repos == nil {
		repos = []model.GithubTopRepo{}
	}
	langs := row.Languages
	if langs == nil {
		langs = []model.GithubLanguage{}
	}
	return model.GithubStatsPayload{
		Login:            row.Login,
		Totals:           row.Totals,
		ContributionGrid: grid,
		TopRepos:         repos,
		Languages:        langs,
		FetchedAt:        row.FetchedAt,
		Stale:            stale,
	}
}
