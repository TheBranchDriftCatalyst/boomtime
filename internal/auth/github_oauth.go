// github_oauth.go — the GitHub OAuth-App code-exchange client (gaka-2ip Phase 1).
//
// Mirrors the OIDCResolver split: this concrete type lives in internal/auth and
// is constructed at boot (stored as a package instance for the handler to reach
// via GithubOAuthResolverInstance); the HTTP handlers that build the redirect
// and consume the callback live in internal/identity/github_oauth.go.
//
// SECURITY POSTURE
//
//   - The access token is NEVER logged and NEVER returned to any HTTP client.
//     Exchange returns it once, in-memory, to the handler which immediately
//     auth.Encrypts it before it touches the DB.
//   - The client_secret + the OAuth `code` are likewise never logged. Errors
//     from the exchange are deliberately generic ("github token exchange
//     failed") so a code/secret can't slip into a log line.
//   - Outbound calls go through the benign-UA client (gaka-93f.23) so a
//     Cloudflare-proxied edge doesn't 403 the default "Go-http-client" UA.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitHub OAuth default endpoints. Overridable per-resolver so the mock-GitHub
// httptest test can point them at a local server (see github_oauth_test.go).
const (
	githubDefaultAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubDefaultTokenURL     = "https://github.com/login/oauth/access_token"
	githubDefaultUserAPIURL   = "https://api.github.com/user"
)

// githubConnectScope is the minimal scope Phase 1 needs: read the authenticated
// user (login capture + token validation). Public stats reads don't need more;
// widening scope is a deliberate later change.
const githubConnectScope = "read:user"

// githubExchangeTimeout bounds the token exchange + /user validation so a hung
// GitHub never wedges the callback handler.
const githubExchangeTimeout = 10 * time.Second

// GithubOAuthResolver performs the authorize-URL build + code→token exchange +
// token validation for the per-user GitHub connect flow.
type GithubOAuthResolver struct {
	clientID     string
	clientSecret string
	redirectURL  string

	authorizeURL string
	tokenURL     string
	userAPIURL   string

	httpClient *http.Client
}

// NewGithubOAuthResolver builds a resolver from the OAuth-App credentials. The
// endpoints default to github.com / api.github.com; tests override them via the
// unexported fields (white-box, same package). Returns nil if clientID or
// clientSecret is empty — the caller (boot) only constructs when both are set.
func NewGithubOAuthResolver(clientID, clientSecret, redirectURL string) *GithubOAuthResolver {
	if clientID == "" || clientSecret == "" {
		return nil
	}
	return &GithubOAuthResolver{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
		authorizeURL: githubDefaultAuthorizeURL,
		tokenURL:     githubDefaultTokenURL,
		userAPIURL:   githubDefaultUserAPIURL,
		// gaka-93f.23: benign UA so a Cloudflare edge doesn't 403 the exchange.
		httpClient: &http.Client{
			Timeout:   githubExchangeTimeout,
			Transport: &uaRoundTripper{ua: oidcUserAgent, base: http.DefaultTransport},
		},
	}
}

// AuthCodeURL builds the GitHub authorize redirect for the connect-start
// handler. state is the signed CSRF/owner-binding token (internal/oauth).
func (r *GithubOAuthResolver) AuthCodeURL(state string) string {
	q := url.Values{}
	q.Set("client_id", r.clientID)
	q.Set("redirect_uri", r.redirectURL)
	q.Set("scope", githubConnectScope)
	q.Set("state", state)
	// allow_signup=false: this is a connect flow for an existing GitHub user,
	// not a signup funnel.
	q.Set("allow_signup", "false")
	return r.authorizeURL + "?" + q.Encode()
}

// githubTokenResponse is the subset of the /access_token JSON we read. GitHub
// returns HTTP 200 even for errors (with an `error` field), so we must inspect
// it rather than trusting the status code alone.
type githubTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// githubUserResponse is the subset of GET /user we read — the login handle.
type githubUserResponse struct {
	Login string `json:"login"`
}

// Exchange trades an authorization code for an access token and validates it by
// calling GET /user, returning the token + the captured GitHub login. Any
// failure returns a generic error (never carrying the code, secret, or token).
//
// The returned token is the plaintext GitHub access token — the caller MUST
// auth.Encrypt it immediately and MUST NOT log it.
func (r *GithubOAuthResolver) Exchange(ctx context.Context, code string) (token, login string, err error) {
	if code == "" {
		return "", "", errors.New("github exchange: empty code")
	}
	ctx, cancel := context.WithTimeout(ctx, githubExchangeTimeout)
	defer cancel()

	tok, err := r.exchangeCode(ctx, code)
	if err != nil {
		return "", "", err
	}
	lg, err := r.fetchLogin(ctx, tok)
	if err != nil {
		return "", "", err
	}
	return tok, lg, nil
}

// exchangeCode POSTs the code to the token endpoint with Accept:
// application/json and returns the access token. Never logs the code/secret.
func (r *GithubOAuthResolver) exchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("client_id", r.clientID)
	form.Set("client_secret", r.clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", r.redirectURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("github token exchange: build request failed")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github token exchange: request failed")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github token exchange: status %d", resp.StatusCode)
	}
	var tr githubTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("github token exchange: malformed response")
	}
	if tr.Error != "" {
		// GitHub reports bad_verification_code etc. with HTTP 200 + error field.
		// Surface the machine code (safe — not a secret) but not the raw body.
		return "", fmt.Errorf("github token exchange: %s", tr.Error)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("github token exchange: no access_token in response")
	}
	return tr.AccessToken, nil
}

// fetchLogin validates the token by calling GET /user and returns the login.
// A 401/403 means the token is bad; any non-2xx fails the connect.
func (r *GithubOAuthResolver) fetchLogin(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.userAPIURL, nil)
	if err != nil {
		return "", fmt.Errorf("github user lookup: build request failed")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github user lookup: request failed")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github user lookup: status %d", resp.StatusCode)
	}
	var ur githubUserResponse
	if err := json.Unmarshal(body, &ur); err != nil {
		return "", fmt.Errorf("github user lookup: malformed response")
	}
	if ur.Login == "" {
		return "", fmt.Errorf("github user lookup: no login in response")
	}
	return ur.Login, nil
}

// NewGithubOAuthResolverForTest builds a resolver with caller-supplied
// endpoints so a mock-GitHub httptest server can stand in for github.com /
// api.github.com. TEST-ONLY seam (mirrors the intent of NewAEADFromBase64 /
// ResetForTest) — production always uses NewGithubOAuthResolver, which pins the
// real endpoints. Uses the DefaultTransport (no benign-UA wrapper needed
// against a local httptest server).
func NewGithubOAuthResolverForTest(clientID, clientSecret, redirectURL, authorizeURL, tokenURL, userAPIURL string) *GithubOAuthResolver {
	return &GithubOAuthResolver{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
		authorizeURL: authorizeURL,
		tokenURL:     tokenURL,
		userAPIURL:   userAPIURL,
		httpClient:   &http.Client{Timeout: githubExchangeTimeout},
	}
}

// githubInstance is the constructed resolver, set at boot when the GitHub
// connect feature is fully configured (gate on + client id/secret + state
// signing key). nil otherwise — the handlers check for nil and 404.
var githubInstance *GithubOAuthResolver

// SetGithubOAuthResolver stores the constructed resolver for the handlers.
func SetGithubOAuthResolver(r *GithubOAuthResolver) { githubInstance = r }

// GithubOAuthResolverInstance returns the constructed resolver (nil when the
// feature is off / unconfigured). Used by the /auth/github/* handlers.
func GithubOAuthResolverInstance() *GithubOAuthResolver { return githubInstance }
