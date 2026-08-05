// oidc.go — the OIDC (Authentik) web login handlers (gaka-0oe.11):
//
//	GET /auth/login/oidc     → 302 to the provider authorize endpoint
//	GET /auth/callback/oidc  → code exchange, provision/lookup, set session
//
// Both 404 when the active provider isn't OIDC (BOOM_AUTH_PROVIDER != oidc), so
// registering the routes unconditionally is safe. The session cookie reuses the
// `refresh_token` name so apihelpers.IdentifyFromCookie resolves it through the
// OIDCResolver exactly like a local session.
package identity

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
)

const oidcStateCookie = "oidc_state"

// oidcNonceCookie stores the OIDC nonce (gaka-93f.16) so the callback can assert
// the id_token echoes it. Same lifetime/attrs as the state cookie.
const oidcNonceCookie = "oidc_nonce"

// setOIDCFlowCookie writes one of the short-lived OIDC-flow CSRF cookies
// (state/nonce). SameSite=Lax so it survives the top-level redirect back from
// the provider; HttpOnly + optional Secure.
func (h *Handler) setOIDCFlowCookie(c *echo.Context, name, value string) {
	c.SetCookie(&http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.Cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
}

// clearOIDCFlowCookies expires the state+nonce cookies (gaka-93f.17). Called on
// logout so an abandoned in-flight link flow can't be completed by a DIFFERENT
// user on the same (shared/kiosk) browser: without the oidc_state cookie the
// callback's CSRF check fails, so the stale link intent can never be consumed.
func (h *Handler) clearOIDCFlowCookies(c *echo.Context) {
	for _, name := range []string{oidcStateCookie, oidcNonceCookie} {
		c.SetCookie(&http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1})
	}
}

// oidcResolver returns the constructed *auth.OIDCResolver (available whenever
// OIDC is CONFIGURED, so the link flow works under provider=local too), or nil.
func oidcResolver() *auth.OIDCResolver {
	return auth.OIDCResolverInstance()
}

// linkIntents maps an in-flight OAuth `state` → the boomtime user who initiated
// an account LINK (gaka-b5n.4). The state is the CSRF token (random +
// cookie-verified), so it's a safe, unguessable key — the callback can't be
// tricked into linking to an arbitrary account. In-memory + short-TTL (single
// dev instance; a shared store is a multi-instance follow-up).
var linkIntents = struct {
	sync.Mutex
	m map[string]linkIntent
}{m: map[string]linkIntent{}}

type linkIntent struct {
	username string
	expiry   time.Time
}

func putLinkIntent(state, username string) {
	linkIntents.Lock()
	defer linkIntents.Unlock()
	// gaka-93f.17/.19: reap expired intents on every insert so an authenticated
	// user who repeatedly STARTS (and abandons) link flows can't grow the map
	// without bound — abandoned intents are otherwise only removed when their
	// exact random state is later presented (never). The map is tiny in
	// practice, so the linear sweep is cheap.
	now := time.Now()
	for k, v := range linkIntents.m {
		if now.After(v.expiry) {
			delete(linkIntents.m, k)
		}
	}
	linkIntents.m[state] = linkIntent{username: username, expiry: now.Add(10 * time.Minute)}
}

// takeLinkIntent consumes (one-shot) the link intent for state, if present and
// unexpired.
func takeLinkIntent(state string) (string, bool) {
	linkIntents.Lock()
	defer linkIntents.Unlock()
	it, ok := linkIntents.m[state]
	if ok {
		delete(linkIntents.m, state)
	}
	if !ok || time.Now().After(it.expiry) {
		return "", false
	}
	return it.username, true
}

// LoginOIDC: GET /auth/login/oidc — start the auth-code flow.
func (h *Handler) LoginOIDC(c *echo.Context) error {
	resolver := oidcResolver()
	if resolver == nil {
		return apihelpers.RespondErr(c, apierr.NotFound("OIDC login is not enabled"))
	}
	state, err1 := auth.RandToken()
	nonce, err2 := auth.RandToken()
	if err1 != nil || err2 != nil {
		return apihelpers.RespondErr(c, apierr.Generic())
	}
	// Short-lived CSRF state + nonce cookies (gaka-93f.16).
	h.setOIDCFlowCookie(c, oidcStateCookie, state)
	h.setOIDCFlowCookie(c, oidcNonceCookie, nonce)
	return c.Redirect(http.StatusFound, resolver.AuthCodeURL(state, nonce))
}

// LinkOIDC: GET /auth/link/oidc — start an account-LINK flow for the currently
// logged-in user (gaka-b5n.4). Available whenever OIDC is configured (works
// under provider=local, so you can link your password account before flipping
// to oidc). The current user is resolved from the session COOKIE — this is a
// top-level browser navigation, which can't carry an Authorization header.
func (h *Handler) LinkOIDC(c *echo.Context) error {
	resolver := oidcResolver()
	if resolver == nil {
		return apihelpers.RespondErr(c, apierr.NotFound("OIDC is not configured"))
	}
	owner, aerr := apihelpers.IdentifyOwnerFromCookie(h.DB, h.Logger, c, apierr.MissingRefreshTokenCookie())
	if aerr != nil {
		// Not authenticated → bounce to login (you must be signed in to link).
		return c.Redirect(http.StatusFound, "/login")
	}
	state, err1 := auth.RandToken()
	nonce, err2 := auth.RandToken()
	if err1 != nil || err2 != nil {
		return apihelpers.RespondErr(c, apierr.Generic())
	}
	putLinkIntent(state, owner)
	h.setOIDCFlowCookie(c, oidcStateCookie, state)
	h.setOIDCFlowCookie(c, oidcNonceCookie, nonce)
	return c.Redirect(http.StatusFound, resolver.AuthCodeURL(state, nonce))
}

// CallbackOIDC: GET /auth/callback/oidc?code&state — complete the flow.
func (h *Handler) CallbackOIDC(c *echo.Context) error {
	resolver := oidcResolver()
	if resolver == nil {
		return apihelpers.RespondErr(c, apierr.NotFound("OIDC login is not enabled"))
	}

	// CSRF: the returned state must match the cookie we set at login start.
	stateCookie, cerr := c.Cookie(oidcStateCookie)
	if cerr != nil || stateCookie.Value == "" || stateCookie.Value != c.QueryParam("state") {
		return h.oidcErrorRedirect(c, "state_mismatch")
	}
	// Capture the nonce we set at authorize-time (gaka-93f.16); a missing cookie
	// leaves it "" so exchangeAndVerify fails closed.
	nonce := ""
	if nc, nerr := c.Cookie(oidcNonceCookie); nerr == nil {
		nonce = nc.Value
	}
	// One-shot: clear both flow cookies.
	c.SetCookie(&http.Cookie{Name: oidcStateCookie, Value: "", Path: "/", MaxAge: -1})
	c.SetCookie(&http.Cookie{Name: oidcNonceCookie, Value: "", Path: "/", MaxAge: -1})

	if c.QueryParam("error") != "" {
		return h.oidcErrorRedirect(c, "provider_error")
	}
	code := c.QueryParam("code")
	if code == "" {
		return h.oidcErrorRedirect(c, "missing_code")
	}

	// LINK MODE (gaka-b5n.4): if this state was started by /auth/link/oidc,
	// bind the resolved identity to the initiating user and keep their existing
	// session — do NOT mint a new one.
	if username, ok := takeLinkIntent(c.QueryParam("state")); ok {
		if aerr := resolver.HandleLink(c.Request().Context(), h.DB, code, username, nonce); aerr != nil {
			h.Logger.Warn("oidc link failed", "user", username, "status", aerr.Status, "msg", aerr.Message)
			reason := "error"
			if aerr.Status == http.StatusConflict {
				reason = "conflict"
			}
			return c.Redirect(http.StatusFound, "/app/settings?tab=profile&link="+reason)
		}
		h.Logger.Info("oidc identity linked", "user", username)
		return c.Redirect(http.StatusFound, "/app/settings?tab=profile&link=success")
	}

	result, aerr := resolver.HandleCallback(c.Request().Context(), h.DB, code, nonce)
	if aerr != nil {
		h.Logger.Warn("oidc callback failed", "status", aerr.Status, "msg", aerr.Message)
		return h.oidcErrorRedirect(c, "login_failed")
	}

	// Boomtime session cookie — reuses `refresh_token` so IdentifyFromCookie
	// resolves it via OIDCResolver.ResolveCookie. Value is the opaque session
	// id (server-side oidc_sessions holds the id_token expiry + refresh).
	c.SetCookie(&http.Cookie{
		Name:     "refresh_token",
		Value:    result.SessionID,
		Path:     strings.TrimSuffix(h.Cfg.APIPrefix, "/") + "/",
		HttpOnly: true,
		Secure:   h.Cfg.CookieSecure,
		// gaka-93f.19: Strict, matching the local session cookie (auth.go
		// setRefreshCookie). The `oidc_state` CSRF cookie must be Lax to
		// survive the provider's cross-site redirect back, but this SESSION
		// cookie is only ever set AFTER the callback completes, so Strict is
		// both safe and consistent — no reason for it to be weaker than local.
		SameSite: http.SameSiteStrictMode,
		Expires:  result.Expiry,
	})
	h.Logger.Info("oidc login", "user", result.Identity.Username, "role", result.Identity.Role)
	return c.Redirect(http.StatusFound, "/app")
}

func (h *Handler) oidcErrorRedirect(c *echo.Context, reason string) error {
	return c.Redirect(http.StatusFound, "/login?error="+reason)
}

// identityResponse is one linked external identity for the Settings UI.
type identityResponse struct {
	Provider  string `json:"provider"`
	Email     string `json:"email"`
	SubPrefix string `json:"subPrefix"` // truncated opaque sub, for display only
	LinkedAt  string `json:"linkedAt"`
}

// ListIdentities: GET /api/v1/users/current/identities — the caller's linked
// identities + whether OIDC linking is available + whether they have a password
// (drives the Settings > Account "Linked identities" card, gaka-b5n.4/.7).
func (h *Handler) ListIdentities(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	ctx := c.Request().Context()
	rows, err := h.DB.ListExternalIdentities(ctx, owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "list identities failed", err)
	}
	out := make([]identityResponse, 0, len(rows))
	for _, r := range rows {
		prefix := r.Sub
		if len(prefix) > 12 {
			prefix = prefix[:12]
		}
		out = append(out, identityResponse{
			Provider:  r.Provider,
			Email:     r.Email,
			SubPrefix: prefix,
			LinkedAt:  r.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	hasPw, _ := h.DB.HasUsablePassword(ctx, owner)
	return c.JSON(http.StatusOK, map[string]any{
		"identities":    out,
		"oidcAvailable": oidcResolver() != nil,
		"hasPassword":   hasPw,
	})
}

// UnlinkIdentity: DELETE /api/v1/users/current/identities/:provider — remove a
// link, refusing when it's the caller's ONLY sign-in method (no password AND
// last remaining link), so a user can never lock themselves out.
func (h *Handler) UnlinkIdentity(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	ctx := c.Request().Context()
	hasPw, err := h.DB.HasUsablePassword(ctx, owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "unlink guard failed", err)
	}
	count, err := h.DB.CountExternalIdentities(ctx, owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "unlink guard failed", err)
	}
	if !hasPw && count <= 1 {
		return apihelpers.RespondErr(c, apierr.New(http.StatusConflict,
			"cannot unlink your only sign-in method — set a password first", nil))
	}
	ok, err := h.DB.DeleteExternalIdentity(ctx, owner, c.Param("provider"))
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "unlink failed", err)
	}
	if !ok {
		return apihelpers.RespondErr(c, apierr.NotFound("no linked identity for that provider"))
	}
	return apihelpers.NoContent(c)
}
