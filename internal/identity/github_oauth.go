// github_oauth.go — the per-user GitHub OAuth connect handlers (boom-2ip
// Phase 1) plus the status/disconnect API.
//
//	GET    /auth/github/connect          → 302 to GitHub authorize (authed via cookie)
//	GET    /auth/github/callback         → code exchange, encrypt+store, redirect back
//	GET    /api/v1/users/current/github  → {connected, login, status, checkedAt} (authed)
//	DELETE /api/v1/users/current/github  → clear the stored token (authed)
//
// GATING: the two /auth/github/* routes are registered ONLY when the feature is
// fully configured (Cfg.GithubConnectEnabled()). The status/disconnect API is
// always registered but reports connected=false when nothing is stored, so the
// FE card (which is itself hidden unless github_connect_enabled) degrades to
// inert. Every handler ALSO nil-checks the resolver, so even a mis-registration
// fails closed with a 404 rather than a panic.
//
// SECURITY: the GitHub access token and the OAuth `code` are NEVER logged and
// NEVER returned by any endpoint. The connect success log records only the
// captured login. The callback verifies a signed `state` (internal/oauth) that
// binds the round-trip to the initiating owner — the callback trusts the owner
// from the signature, never from a query param.
package identity

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity/oauth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// githubStateMaxAge bounds how long a minted connect `state` stays valid — the
// authorize round-trip should complete in seconds; 10 minutes is generous slack
// for a slow user while keeping the replay window small.
const githubStateMaxAge = 10 * time.Minute

// githubSettingsPath is where the callback returns the browser — the Settings
// Profile tab that hosts the GitHubConnectCard.
const githubSettingsPath = "/app/settings?tab=profile"

// githubOAuthResolver returns the constructed resolver (nil when the feature is
// off / unconfigured).
func githubOAuthResolver() *auth.GithubOAuthResolver {
	return auth.GithubOAuthResolverInstance()
}

// ConnectGithub: GET /auth/github/connect — start the OAuth-App flow for the
// currently-logged-in user. This is a top-level browser navigation (window
// .location), so the owner is resolved from the session COOKIE, not an
// Authorization header. Signs a state binding the flow to that owner and
// redirects to GitHub.
func (h *Handler) ConnectGithub(c *echo.Context) error {
	resolver := githubOAuthResolver()
	if resolver == nil {
		return apihelpers.RespondErr(c, apierr.NotFound("GitHub connect is not enabled"))
	}
	owner, aerr := apihelpers.IdentifyOwnerFromCookie(h.DB, h.Logger, c, apierr.MissingRefreshTokenCookie())
	if aerr != nil {
		// Not authenticated → bounce to login (you must be signed in to connect).
		return c.Redirect(http.StatusFound, "/login")
	}
	state, err := oauth.Sign([]byte(h.Cfg.OAuthStateSigningKey), owner, time.Now())
	if err != nil {
		// Signing key misconfigured — should be impossible when the routes are
		// registered (GithubConnectEnabled requires it), but fail closed.
		return apihelpers.InternalErr(h.Logger, c, "github connect: state sign failed", err)
	}
	return c.Redirect(http.StatusFound, resolver.AuthCodeURL(state))
}

// CallbackGithub: GET /auth/github/callback?code&state — complete the flow.
// Verifies the signed state (owner + signature + freshness), exchanges the code
// for a token, validates it (captures the login), encrypts, and stores. Any
// failure redirects back to the settings card with ?github=error — never a raw
// error to the browser, never the code/token in a log.
func (h *Handler) CallbackGithub(c *echo.Context) error {
	resolver := githubOAuthResolver()
	if resolver == nil {
		return apihelpers.RespondErr(c, apierr.NotFound("GitHub connect is not enabled"))
	}
	if c.QueryParam("error") != "" {
		// User denied, or GitHub returned an error — no code to exchange.
		return h.githubCallbackRedirect(c, "denied")
	}

	// CSRF + owner binding: the state must verify under our signing key and be
	// fresh. The owner comes from the SIGNATURE, not any client-controlled field.
	owner, err := oauth.Verify([]byte(h.Cfg.OAuthStateSigningKey), c.QueryParam("state"), time.Now(), githubStateMaxAge)
	if err != nil {
		h.Logger.Warn("github callback: state verification failed", "err", err)
		return h.githubCallbackRedirect(c, "state")
	}

	code := c.QueryParam("code")
	if code == "" {
		return h.githubCallbackRedirect(c, "missing_code")
	}

	token, login, xerr := resolver.Exchange(c.Request().Context(), code)
	if xerr != nil {
		// Exchange errors are pre-sanitized (no code/secret/token); log the
		// generic reason tagged with the owner.
		h.Logger.Warn("github callback: exchange failed", "user", owner, "err", xerr)
		return h.githubCallbackRedirect(c, "exchange")
	}

	ct, eerr := auth.Encrypt([]byte(token))
	if eerr != nil {
		return h.githubCallbackRedirectInternal(c, owner, "github token encrypt failed", eerr)
	}
	if serr := h.DB.SetEncryptedGithubToken(c.Request().Context(), owner, ct, login, db.GithubTokenStatusValid); serr != nil {
		return h.githubCallbackRedirectInternal(c, owner, "github token persist failed", serr)
	}
	// Log the fact of a connect + the (non-secret) login. NEVER the token.
	h.Logger.Info("github connected", "user", owner, "login", login)
	return h.githubCallbackRedirect(c, "connected")
}

// githubCallbackRedirect sends the browser back to the settings card with a
// status param the FE surfaces as a banner.
func (h *Handler) githubCallbackRedirect(c *echo.Context, status string) error {
	return c.Redirect(http.StatusFound, githubSettingsPath+"&github="+status)
}

// githubCallbackRedirectInternal logs an internal failure (server-side detail
// stays in the log) and redirects with a generic error param.
func (h *Handler) githubCallbackRedirectInternal(c *echo.Context, owner, msg string, err error) error {
	h.Logger.Error(msg, "user", owner, "err", err)
	return h.githubCallbackRedirect(c, "error")
}

// githubConnectionResponse is the shape returned by GET /github. It NEVER
// includes the token — only presence + the (safe) login + last-known status.
type githubConnectionResponse struct {
	Connected bool    `json:"connected"`
	Login     *string `json:"login,omitempty"`
	Status    *string `json:"status,omitempty"`
	CheckedAt *string `json:"checkedAt,omitempty"`
}

// GetGithubConnection: GET /api/v1/users/current/github — reports whether the
// caller has connected GitHub, the captured login, and the last-known status.
// Authed via the normal access token (XHR from the FE). Never returns the token.
func (h *Handler) GetGithubConnection(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	info, err := h.DB.GetGithubTokenInfo(c.Request().Context(), owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "github connection lookup failed", err)
	}
	resp := githubConnectionResponse{Connected: info.Connected}
	if info.Connected {
		resp.Login = info.Login
		resp.Status = info.Status
		if info.CheckedAt != nil {
			ts := info.CheckedAt.UTC().Format(time.RFC3339)
			resp.CheckedAt = &ts
		}
	}
	return c.JSON(http.StatusOK, resp)
}

// DisconnectGithub: DELETE /api/v1/users/current/github — clear any stored
// token + its metadata for the caller. Idempotent (204 whether or not one
// existed).
func (h *Handler) DisconnectGithub(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if err := h.DB.ClearEncryptedGithubToken(c.Request().Context(), owner); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "github disconnect failed", err)
	}
	// boom-anh Phase 2: also drop the stats cache so a stale row can't outlive
	// the token that produced it (and so a re-connect starts clean).
	if err := h.DB.ClearGithubStatsCache(c.Request().Context(), owner); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "github stats cache clear failed", err)
	}
	h.Logger.Info("github disconnected", "user", owner)
	return apihelpers.NoContent(c)
}
