// oidc_resolver.go — the OIDCResolver (Authentik) implementation of
// IdentityResolver (gaka-0oe.11). Web sessions become boomtime opaque cookies
// backed by oidc_sessions; editor plugins keep using local API tokens
// (ResolveBearer delegates to local). The login START + callback live in the
// identity handlers, which call AuthCodeURL / HandleCallback on this concrete
// type (the callback must create the session, which the generic interface
// CompleteLogin can't return).
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/oauth2"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
)

// OIDCProviderName is the value stored in user_external_identities.provider.
const OIDCProviderName = "authentik"

// OIDCResolver authenticates via an OIDC provider.
type OIDCResolver struct {
	provider      *oidc.Provider
	verifier      *oidc.IDTokenVerifier
	oauth2        oauth2.Config
	groupToRole   map[string]string
	autoprovision bool
	httpClient    *http.Client
}

// oidcUserAgent replaces the default "Go-http-client" User-Agent on every
// server-side OIDC HTTP call (gaka-93f.23). A Cloudflare-proxied issuer
// (auth.knowledgedump.space) has "Go-http-client" on its managed-bot blocklist
// and 403s discovery/token/jwks; a benign UA passes (verified: default wget UA
// succeeds, Go-http-client UA 403s). Not a browser-spoof — just off the bot list.
const oidcUserAgent = "boomtime-oidc/1.0 (+https://boomtime.knowledgedump.space)"

// uaRoundTripper sets User-Agent on outbound requests without mutating the
// shared request (RoundTripper must not modify its argument).
type uaRoundTripper struct {
	ua   string
	base http.RoundTripper
}

func (t *uaRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.Header.Set("User-Agent", t.ua)
	return t.base.RoundTrip(r2)
}

// NewOIDCResolver runs OIDC discovery against issuer and builds the verifier +
// oauth2 config. Returns an error if the issuer is unreachable (boot fails
// loudly rather than silently falling back).
func NewOIDCResolver(ctx context.Context, issuer, authorizeURLOverride, clientID, clientSecret, redirectURL string, groupToRole map[string]string, autoprovision bool) (*OIDCResolver, error) {
	// gaka-93f.23: route ALL server-side OIDC HTTP (discovery here, JWKS refresh
	// via the keyset built below, and token exchange in exchangeAndVerify)
	// through a client with a non-"Go-http-client" User-Agent so a Cloudflare-
	// proxied issuer doesn't 403 it as a bot.
	httpClient := &http.Client{Transport: &uaRoundTripper{ua: oidcUserAgent, base: metrics.InstrumentTransport(http.DefaultTransport)}}
	ctx = oidc.ClientContext(ctx, httpClient)
	provider, err := oidc.NewProvider(ctx, strings.TrimRight(issuer, "/")+"/")
	if err != nil {
		return nil, fmt.Errorf("oidc discovery (%s): %w", issuer, err)
	}
	endpoint := provider.Endpoint()
	// Split-horizon dev: discovery/token/jwks stay on the cluster-internal
	// issuer (pod-reachable), but the browser must be redirected to a
	// host-reachable authorize URL. The id_token issuer is minted at the token
	// endpoint (still the cluster URL), so verification is unaffected.
	if authorizeURLOverride != "" {
		endpoint.AuthURL = authorizeURLOverride
	}
	return &OIDCResolver{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
		oauth2: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     endpoint,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile", "groups"},
		},
		groupToRole:   groupToRole,
		autoprovision: autoprovision,
		httpClient:    httpClient,
	}, nil
}

func (r *OIDCResolver) ProviderName() string { return "oidc" }

// AuthCodeURL builds the authorize redirect for the login-start handler.
func (r *OIDCResolver) AuthCodeURL(state, nonce string) string {
	return r.oauth2.AuthCodeURL(state, oidc.Nonce(nonce))
}

// ResolveBearer: editor plugins keep local API tokens even under OIDC — the
// OIDC path is web-session only.
func (r *OIDCResolver) ResolveBearer(ctx context.Context, database *db.DB, token string) (*Identity, *apierr.Error) {
	return LocalPasswordResolver{}.ResolveBearer(ctx, database, token)
}

// ResolveCookie: the web session cookie is an opaque id → oidc_sessions → user.
func (r *OIDCResolver) ResolveCookie(ctx context.Context, database *db.DB, sessionID string) (*Identity, *apierr.Error) {
	username, ok, err := database.GetOIDCSessionUser(ctx, sessionID)
	if err != nil {
		return nil, apierr.Generic()
	}
	if !ok {
		return nil, apierr.ExpiredRefreshToken()
	}
	return resolveIdentity(ctx, database, username)
}

// CompleteLogin (interface) is unused for OIDC — the callback goes through
// HandleCallback (concrete) because it must create the server-side session.
func (r *OIDCResolver) CompleteLogin(_ context.Context, _ *db.DB, _, _ string) (*Identity, *apierr.Error) {
	return nil, apierr.NotFound("OIDC callback is handled via HandleCallback")
}

// oidcClaims is the subset read from the verified id_token.
type oidcClaims struct {
	Sub               string   `json:"sub"`
	Email             string   `json:"email"`
	PreferredUsername string   `json:"preferred_username"`
	Groups            []string `json:"groups"`
}

// CallbackResult is what HandleCallback hands the handler to mint the cookie.
type CallbackResult struct {
	Identity  *Identity
	SessionID string    // raw opaque cookie value
	Expiry    time.Time // session validity (= id_token expiry)
}

// exchangeAndVerify does the code exchange + id_token JWKS verification and
// extracts the claims — the shared front half of both the login callback and
// the account-link callback. Returns the claims, their JSON, the id_token
// expiry, and the provider refresh_token.
func (r *OIDCResolver) exchangeAndVerify(ctx context.Context, code, expectedNonce string) (oidcClaims, []byte, time.Time, string, *apierr.Error) {
	var zero oidcClaims
	// gaka-93f.23: route the token exchange (oauth2) + any lazy JWKS refresh
	// (go-oidc) through the benign-UA client so Cloudflare doesn't 403 them.
	if r.httpClient != nil {
		ctx = oidc.ClientContext(ctx, r.httpClient)
		ctx = context.WithValue(ctx, oauth2.HTTPClient, r.httpClient)
	}
	tok, err := r.oauth2.Exchange(ctx, code)
	if err != nil {
		return zero, nil, time.Time{}, "", apierr.New(http.StatusBadGateway, "OIDC token exchange failed", nil)
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok {
		return zero, nil, time.Time{}, "", apierr.New(http.StatusBadGateway, "OIDC response missing id_token", nil)
	}
	idToken, err := r.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return zero, nil, time.Time{}, "", apierr.New(http.StatusUnauthorized, "OIDC id_token verification failed", nil)
	}
	// gaka-93f.16: verify the nonce we minted at authorize-time is echoed in the
	// id_token (OIDC core §3.1.3.7 step 11). go-oidc does NOT check this
	// automatically — without it, an id_token minted for a different auth request
	// at the same issuer could be injected. We ALWAYS send a nonce, so an empty
	// or mismatched idToken.Nonce (incl. a missing state/nonce cookie →
	// expectedNonce=="") fails closed.
	if idToken.Nonce == "" || idToken.Nonce != expectedNonce {
		return zero, nil, time.Time{}, "", apierr.New(http.StatusUnauthorized, "OIDC id_token nonce mismatch", nil)
	}
	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil {
		return zero, nil, time.Time{}, "", apierr.Generic()
	}
	if claims.Sub == "" {
		return zero, nil, time.Time{}, "", apierr.New(http.StatusUnauthorized, "OIDC id_token missing sub claim", nil)
	}
	claimsJSON, _ := json.Marshal(claims)
	return claims, claimsJSON, idToken.Expiry, tok.RefreshToken, nil
}

// HandleCallback (login mode): exchange + verify + provision/lookup (design
// §6.5) + create the server-side oidc_session.
func (r *OIDCResolver) HandleCallback(ctx context.Context, database *db.DB, code, expectedNonce string) (*CallbackResult, *apierr.Error) {
	claims, claimsJSON, expiry, refresh, aerr := r.exchangeAndVerify(ctx, code, expectedNonce)
	if aerr != nil {
		return nil, aerr
	}
	role := string(RoleFromGroups(claims.Groups, r.groupToRole))
	username, aerr := r.resolveOrProvision(ctx, database, claims, role, claimsJSON)
	if aerr != nil {
		return nil, aerr
	}
	if expiry.IsZero() {
		expiry = time.Now().Add(8 * time.Hour)
	}
	sessionID, err := randToken()
	if err != nil {
		return nil, apierr.Generic()
	}
	// gaka-93f.11.6: persist the provider refresh ENCRYPTED (recoverable) so
	// /auth/refresh_token can silently rotate the session. Best-effort: if
	// there's no refresh or BOOM_ENCRYPTION_KEY is unset, store nil — the
	// session still works, only silent refresh is unavailable (no re-login
	// beyond the id_token window).
	var encRefresh []byte
	if refresh != "" {
		if ct, encErr := Encrypt([]byte(refresh)); encErr == nil {
			encRefresh = ct
		}
	}
	if err := database.CreateOIDCSession(ctx, sessionID, username, expiry, encRefresh); err != nil {
		return nil, apierr.Generic()
	}
	ident, aerr := resolveIdentity(ctx, database, username)
	if aerr != nil {
		return nil, aerr
	}
	return &CallbackResult{Identity: ident, SessionID: sessionID, Expiry: expiry}, nil
}

// RefreshSession exchanges a stored provider refresh_token for a fresh id_token
// (OAuth2 refresh-grant) and returns the new session expiry + the possibly
// rotated refresh_token (gaka-93f.11.6). It re-verifies the refreshed id_token's
// signature/issuer/audience/expiry via the same JWKS verifier as login — but
// does NOT check a nonce (a refresh-grant id_token carries none; nonce binds the
// original authorization request only). Any failure (IdP rejects the refresh,
// missing/invalid id_token) returns an error so the caller can fall back to the
// still-valid session instead of extending it.
func (r *OIDCResolver) RefreshSession(ctx context.Context, rawRefresh string) (time.Time, string, *apierr.Error) {
	if r.httpClient != nil {
		ctx = oidc.ClientContext(ctx, r.httpClient)
		ctx = context.WithValue(ctx, oauth2.HTTPClient, r.httpClient)
	}
	tok, err := r.oauth2.TokenSource(ctx, &oauth2.Token{RefreshToken: rawRefresh}).Token()
	if err != nil {
		return time.Time{}, "", apierr.New(http.StatusUnauthorized, "OIDC session refresh failed", nil)
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok {
		return time.Time{}, "", apierr.New(http.StatusBadGateway, "OIDC refresh response missing id_token", nil)
	}
	idToken, err := r.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return time.Time{}, "", apierr.New(http.StatusUnauthorized, "OIDC refreshed id_token verification failed", nil)
	}
	// Providers MAY rotate the refresh_token; keep the old one if they don't.
	newRefresh := tok.RefreshToken
	if newRefresh == "" {
		newRefresh = rawRefresh
	}
	expiry := idToken.Expiry
	if expiry.IsZero() {
		expiry = time.Now().Add(8 * time.Hour)
	}
	return expiry, newRefresh, nil
}

// HandleLink (link mode, gaka-b5n.4): exchange + verify, then bind the resolved
// (authentik, sub) to currentUsername — the account the caller is ALREADY
// logged in as. Does NOT create a session (the caller keeps theirs). Idempotent
// if already linked to the same user; 409 if the identity belongs to a
// different account.
func (r *OIDCResolver) HandleLink(ctx context.Context, database *db.DB, code, currentUsername, expectedNonce string) *apierr.Error {
	claims, claimsJSON, _, _, aerr := r.exchangeAndVerify(ctx, code, expectedNonce)
	if aerr != nil {
		return aerr
	}
	existing, ok, err := database.GetUserByExternalIdentity(ctx, OIDCProviderName, claims.Sub)
	if err != nil {
		return apierr.Generic()
	}
	if ok {
		if existing == currentUsername {
			return nil // already linked — idempotent
		}
		return apierr.New(http.StatusConflict, "this Authentik identity is already linked to another account", nil)
	}
	if err := database.LinkExternalIdentity(ctx, currentUsername, OIDCProviderName, claims.Sub, claims.Email, claimsJSON); err != nil {
		// gaka-93f.19: a concurrent link of the SAME (provider, sub) can slip
		// between the GetUserByExternalIdentity check above and this INSERT,
		// racing the UNIQUE(provider, sub) constraint. That's the identity being
		// taken, not a server fault — surface the SAME 409 the pre-check returns
		// rather than a 500.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return apierr.New(http.StatusConflict, "this Authentik identity is already linked to another account", nil)
		}
		return apierr.Generic()
	}
	return nil
}

// resolveOrProvision implements the design §6.5 lookup/link/provision cascade.
func (r *OIDCResolver) resolveOrProvision(ctx context.Context, database *db.DB, claims oidcClaims, role string, claimsJSON []byte) (string, *apierr.Error) {
	// Existing link → refresh role from current groups + touch claims.
	if username, ok, err := database.GetUserByExternalIdentity(ctx, OIDCProviderName, claims.Sub); err != nil {
		return "", apierr.Generic()
	} else if ok {
		// gaka-93f.19: only rewrite users.role when the group-derived role
		// actually DIFFERS from what's stored. An unconditional SetUserRole on
		// EVERY login silently clobbered an operator's manual role change (and
		// swallowed the write error). Read the current role, compare, log the
		// old→new transition, and surface a write failure instead of dropping it.
		full, ferr := database.GetUserFullByName(ctx, username)
		if ferr != nil {
			return "", apierr.Generic()
		}
		if full != nil && full.Role != role {
			if _, serr := database.SetUserRole(ctx, username, role); serr != nil {
				slog.Error("oidc: failed to update role from provider groups on login",
					"username", username, "old_role", full.Role, "new_role", role, "err", serr)
				return "", apierr.Generic()
			}
			slog.Info("oidc: role updated from provider groups on login",
				"username", username, "old_role", full.Role, "new_role", role)
		}
		_ = database.TouchExternalIdentity(ctx, OIDCProviderName, claims.Sub, claims.Email, claimsJSON)
		return username, nil
	}

	preferred := claims.PreferredUsername
	if preferred == "" {
		preferred = localpart(claims.Email)
	}
	if preferred == "" {
		return "", apierr.New(http.StatusBadRequest, "OIDC identity has neither preferred_username nor email", nil)
	}
	// gaka-93f.18: never insert an IdP-supplied username verbatim. Reject
	// control chars / whitespace / '|' (cache-key delimiter) / non-ASCII before
	// it can become a boomtime username. Fail closed with a clear 400 rather
	// than provisioning a hostile/namespace-colliding account.
	if err := ValidateUsername(preferred); err != nil {
		return "", apierr.New(http.StatusBadRequest, "OIDC preferred_username is not an acceptable boomtime username: "+err.Error(), nil)
	}

	// NOTE: username-based autolink was REMOVED (gaka-93f.12, red-team HIGH).
	// Matching an IdP-supplied preferred_username against an existing boomtime
	// account and binding to it — with no email_verified check — was an
	// unauthenticated account-takeover primitive (name your Authentik user
	// "admin" → own the admin account). Linking an existing account to OIDC now
	// goes ONLY through the authenticated link flow (HandleLink), which is
	// state + live-session bound. There is no auto-link on first login.

	// Autoprovision a NEW user (opt-in, default off). Never binds to an
	// existing account — ProvisionOIDCUser fails closed on a username collision.
	if !r.autoprovision {
		return "", apierr.New(http.StatusForbidden, "no boomtime account for this identity; ask an admin to create one", nil)
	}
	username, err := database.ProvisionOIDCUser(ctx, preferred, OIDCProviderName, claims.Sub, claims.Email, role, claimsJSON)
	if err != nil {
		return "", apierr.Generic()
	}
	return username, nil
}

// randToken returns a URL-safe 256-bit random string (state, nonce, session id).
func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// RandToken is the exported form for the login-start handler's state/nonce.
func RandToken() (string, error) { return randToken() }

func localpart(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return ""
}

// oidcInstance is the constructed OIDCResolver, kept available for the
// account-LINK flow even when the ACTIVE auth provider is local (you link your
// existing password account to Authentik BEFORE flipping to oidc). Set at boot
// whenever OIDC is configured (BOOM_OIDC_ISSUER present); independent of
// SetResolver (which sets the active login provider).
var oidcInstance *OIDCResolver

// SetOIDCResolver stores the constructed OIDC resolver for the link flow.
func SetOIDCResolver(r *OIDCResolver) { oidcInstance = r }

// OIDCResolverInstance returns the constructed OIDC resolver (nil if OIDC isn't
// configured). Used by the /auth/link/oidc + /auth/login/oidc handlers.
func OIDCResolverInstance() *OIDCResolver { return oidcInstance }
