package identity

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/labstack/echo/v5"
)

// mkTokenData mirrors Database.mkTokenData: base64(uuid) for both tokens.
func mkTokenData(user string) db.TokenData {
	return db.TokenData{
		Owner:        user,
		Token:        auth.ToBase64(auth.NewRawToken()),
		RefreshToken: auth.ToBase64(auth.NewRawToken()),
	}
}

// setRefreshCookie writes the refresh_token cookie: HttpOnly, SameSite=Strict,
// and Secure iff h.Cfg.CookieSecure is true (boom-b5x.1). Path is scoped to
// the app root (prefix + "/") rather than "/auth" so the cookie is also sent
// on the cookie-authenticated import WebSocket handshake
// (/import/jobs/:id/ws), which the browser can't send an Authorization header on.
func (h *Handler) setRefreshCookie(c *echo.Context, td db.TokenData) {
	prefix := strings.TrimSuffix(h.Cfg.APIPrefix, "/")
	c.SetCookie(&http.Cookie{
		Name:     "refresh_token",
		Value:    td.RefreshToken,
		Path:     prefix + "/",
		HttpOnly: true,
		Secure:   h.Cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearRefreshCookie emits a Set-Cookie header that expires the refresh_token
// cookie. The clearing cookie MUST carry the same attributes (Path, Domain,
// Secure, SameSite) as the original — browsers key their cookie store on that
// tuple. Without Secure matching the original in prod, a Set-Cookie without
// Secure won't clear the Secure-flagged cookie and Logout would leave a live
// cookie behind.
func (h *Handler) clearRefreshCookie(c *echo.Context) {
	prefix := strings.TrimSuffix(h.Cfg.APIPrefix, "/")
	c.SetCookie(&http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     prefix + "/",
		HttpOnly: true,
		Secure:   h.Cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1, // "delete now" per RFC 6265 §5.2.2
	})
}

// loginResponse builds {token, tokenExpiry(now+30min), tokenUsername}.
func loginResponse(td db.TokenData, now time.Time) model.LoginResponse {
	return model.LoginResponse{
		Token:         td.Token,
		TokenExpiry:   now.Add(30 * time.Minute),
		TokenUsername: td.Owner,
	}
}

// Login: POST /auth/login.
//
// boom-imm: constant-time user enumeration defence. Prior to this fix, the
// "no such user" branch short-circuited BEFORE Argon2id ran, so the response
// took ~1ms (network + SELECT) whereas the "user exists / wrong password"
// branch took ~10ms (argon2 dominates). Attackers observed that ~10x delta
// over a single unauth'd TCP connection to enumerate valid usernames without
// tripping any log signal (both paths already returned the same JSON body).
//
// Fix: whenever GetUserByName returns nil, run auth.BurnSentinelVerify —
// a wrapper around argon2.IDKey against a per-process sentinel hash+salt
// whose result is discarded. Both branches now burn the same ~10ms of CPU
// and return the identical InvalidCredentials envelope.
func (h *Handler) Login(c *echo.Context) (model.LoginResponse, error) {
	var out model.LoginResponse
	// boom-93f.11.4: under BOOM_AUTH_PROVIDER=oidc, password login is disabled —
	// sign-in goes through Authentik (/auth/login/oidc). Reject early with a
	// clear message so a stale password form can't mint a half-working local
	// session (bearer resolves locally, but the cookie-authed paths expect an
	// oidc_session). The FE hides the password form under oidc; this is the
	// server-side backstop.
	if auth.CurrentResolver().ProviderName() == "oidc" {
		return out, apierr.Forbidden("password login is disabled on this server — sign in with Authentik")
	}
	var creds model.AuthRequest
	// boom-bi2: 4 KiB cap. Credentials are two short strings; a fat body here
	// would just amplify the argon2 verify below into a memory DoS.
	//
	// NOTE (typed-seam migration): the bind stays INSIDE the handler
	// (registered via apiroute.POSTNoBody) so the provider guard above keeps
	// running BEFORE any request body is read — the same ordering invariant
	// Register asserts explicitly in auth_cluster_coverage_test.go.
	if aerr := apihelpers.BindJSONWithLimit(c, &creds, apihelpers.BodyLimitSmall); aerr != nil {
		return out, aerr
	}
	ctx := c.Request().Context()

	user, err := h.DB.GetUserByName(ctx, creds.Username)
	if err != nil {
		return out, fmt.Errorf("user lookup failed: %w", err)
	}
	if user == nil {
		// boom-imm: burn the same ~10ms of argon2 the found-user branch
		// spends in VerifyPassword. Result discarded — the point is to
		// eliminate the timing gap, not to actually authenticate.
		auth.BurnSentinelVerify(creds.Password)
		return out, apierr.InvalidCredentials()
	}
	if !auth.VerifyPasswordWithVersion(creds.Password, user.HashedPassword, user.SaltUsed, user.ArgonVersion) {
		return out, apierr.InvalidCredentials()
	}

	// boom-93f.15: a disabled account fails closed on the password path
	// UNCONDITIONALLY — the disable kill-switch must work regardless of
	// BOOM_FEATURE_USER_MODEL (previously this was gated behind the flag, so a
	// disabled user could still log in under the default flag-off posture).
	// One extra read on the (infrequent) login path only; the hot request path
	// stays query-free because disabling also revokes existing sessions
	// (db.SetUserDisabled). Return the same InvalidCredentials so we don't leak
	// that the account exists-but-disabled.
	full, ferr := h.DB.GetUserFullByName(ctx, creds.Username)
	if ferr != nil {
		return out, fmt.Errorf("user lookup failed: %w", ferr)
	}
	if full == nil || full.DisabledAt != nil {
		return out, apierr.InvalidCredentials()
	}

	// boom-awh.6 (Bravo MEDIUM): transparent rehash-on-login. If the row is
	// still at a legacy argon generation (< current), we just verified the
	// plaintext against the stored legacy hash — the ONLY moment we're
	// allowed to derive a fresh current-generation hash without prompting
	// the user again. Do it synchronously (a goroutine risks ordering issues
	// on rapid re-login) but bound the work: HashPasswordWithVersion +
	// UpgradeArgonVersion at v2 params is ~50-100ms, well under the ~500ms
	// budget. A failure here does NOT block login — we log and continue so
	// a transient DB blip doesn't lock a user out of a session they just
	// authenticated for. The row stays at v1 and will retry on next login.
	if user.ArgonVersion < auth.ArgonVersionCurrent {
		newHash, newSalt, herr := auth.HashPasswordWithVersion(creds.Password, auth.ArgonVersionCurrent)
		if herr != nil {
			h.Logger.Warn("argon rehash-on-login failed to hash",
				"user", creds.Username, "old_version", user.ArgonVersion, "err", herr)
		} else if uerr := h.DB.UpgradeArgonVersion(ctx, creds.Username, newHash, newSalt,
			user.ArgonVersion, auth.ArgonVersionCurrent); uerr != nil {
			h.Logger.Warn("argon rehash-on-login failed to update",
				"user", creds.Username, "old_version", user.ArgonVersion, "err", uerr)
		} else {
			h.Logger.Info("argon rehash-on-login succeeded",
				"user", creds.Username, "old_version", user.ArgonVersion, "new_version", auth.ArgonVersionCurrent)
		}
	}

	td := mkTokenData(creds.Username)
	if err := h.DB.CreateAccessTokens(ctx, td, h.Cfg.SessionExpiry); err != nil {
		return out, fmt.Errorf("access token creation failed: %w", err)
	}
	h.setRefreshCookie(c, td)
	h.Logger.Info("login", "user", creds.Username)
	return loginResponse(td, time.Now().UTC()), nil
}

// Register: POST /auth/register.
func (h *Handler) Register(c *echo.Context) (model.LoginResponse, error) {
	var out model.LoginResponse
	if !h.Cfg.EnableRegistration {
		return out, apierr.DisabledRegistration()
	}
	var creds model.AuthRequest
	// boom-bi2: 4 KiB cap. Same rationale as Login — credentials are short.
	//
	// NAMED INVARIANT (auth_cluster_coverage_test.go): the
	// EnableRegistration guard above MUST short-circuit BEFORE
	// BindJSONWithLimit — an over-cap body on a disabled-registration server
	// answers 403, never 413. That is why this route is on
	// apiroute.POSTNoBody (bind stays here) rather than apiroute.POST.
	if aerr := apihelpers.BindJSONWithLimit(c, &creds, apihelpers.BodyLimitSmall); aerr != nil {
		return out, aerr
	}
	// boom-e5e: enforce the shared password policy BEFORE hashing +
	// inserting. Prior to this check, POST /auth/register accepted empty
	// and toy passwords ("", "a", "1234") and minted a working session.
	// auth.ValidatePassword's sentinel errors are user-safe by design —
	// surface .Error() directly (no internal state leaked).
	if err := auth.ValidatePassword(creds.Password); err != nil {
		return out, apierr.BadRequest(err.Error())
	}
	// boom-93f.18: validate the username before hashing/inserting. Prior to
	// this, register accepted arbitrary usernames (control chars, whitespace,
	// the cache-key delimiter '|', unicode homoglyphs). ValidateUsername's
	// message is user-safe.
	if err := auth.ValidateUsername(creds.Username); err != nil {
		return out, apierr.BadRequest(err.Error())
	}
	ctx := c.Request().Context()

	if err := auth.CreateUser(ctx, h.DB, creds.Username, creds.Password); err != nil {
		if errors.Is(err, auth.ErrUserExists) {
			return out, apierr.UsernameExists(creds.Username)
		}
		if errors.Is(err, auth.ErrInvalidCredentials) {
			// unreachable via CreateUser; kept for symmetry with Login flow.
			return out, apierr.InvalidCredentials()
		}
		return out, fmt.Errorf("user creation failed: %w", err)
	}

	td := mkTokenData(creds.Username)
	if err := h.DB.CreateAccessTokens(ctx, td, h.Cfg.SessionExpiry); err != nil {
		return out, fmt.Errorf("access token creation failed: %w", err)
	}
	h.setRefreshCookie(c, td)
	h.Logger.Info("user registered", "user", creds.Username)
	return loginResponse(td, time.Now().UTC()), nil
}

// RefreshToken: POST /auth/refresh_token (reads refresh_token cookie).
func (h *Handler) RefreshToken(c *echo.Context) (model.LoginResponse, error) {
	var out model.LoginResponse
	owner, aerr := apihelpers.IdentifyOwnerFromCookie(h.DB, h.Logger, c, apierr.MissingRefreshTokenCookie())
	if aerr != nil {
		return out, aerr
	}
	ctx := c.Request().Context()

	// boom-93f.14: under OIDC the browser session IS the oidc_sessions cookie
	// (already validated by IdentifyOwnerFromCookie above). Mint ONLY a short
	// access bearer for the Authorization-header API surface — do NOT create a
	// local refresh token and do NOT overwrite the session cookie. Doing either
	// (as the local path below does) clobbered the OIDC session cookie and handed
	// out a standalone local credential that escaped the session's lifetime and
	// server-side revocation. The FE must re-present the (expiring, revocable)
	// OIDC session cookie to obtain each subsequent bearer.
	if auth.CurrentResolver().ProviderName() == "oidc" {
		// boom-93f.11.6: silently ROTATE the OIDC session so a short-lived
		// id_token can back a long-lived, revocable web session. Decrypt the
		// stored provider refresh, refresh-grant against the IdP, and extend
		// this session's id_token_expiry in place (same cookie). Best-effort:
		// on any miss (no stored refresh, no BOOM_ENCRYPTION_KEY, IdP rejects)
		// we fall through and just mint a bearer against the still-valid
		// session — the pre-11.6 behavior — so a refresh hiccup never logs the
		// user out early.
		h.tryRotateOIDCSession(c)

		td := mkTokenData(owner)
		if err := h.DB.CreateOIDCAccessToken(ctx, owner, td.Token); err != nil {
			return out, fmt.Errorf("access token creation failed: %w", err)
		}
		return loginResponse(td, time.Now().UTC()), nil
	}

	td := mkTokenData(owner)
	if err := h.DB.CreateAccessTokens(ctx, td, h.Cfg.SessionExpiry); err != nil {
		return out, fmt.Errorf("access token creation failed: %w", err)
	}
	h.setRefreshCookie(c, td)
	return loginResponse(td, time.Now().UTC()), nil
}

// tryRotateOIDCSession best-effort extends the caller's OIDC web session
// (boom-93f.11.6): decrypt the stored provider refresh, refresh-grant against
// the IdP, then rotate id_token_expiry + the (possibly new) refresh in place —
// same cookie, no re-login. Every failure is swallowed (the session stays valid
// until its current expiry), so a transient IdP hiccup never logs the user out.
func (h *Handler) tryRotateOIDCSession(c *echo.Context) {
	ctx := c.Request().Context()
	refresh, ok := auth.ParseRefreshCookie(c.Request().Header.Get("Cookie"))
	if !ok {
		return
	}
	oidcR, ok := auth.CurrentResolver().(*auth.OIDCResolver)
	if !ok {
		return
	}
	enc, _, found, err := h.DB.GetOIDCSessionRefresh(ctx, refresh)
	if err != nil || !found || len(enc) == 0 {
		return // no stored refresh (e.g. no BOOM_ENCRYPTION_KEY at login) → skip
	}
	raw, err := auth.Decrypt(enc)
	if err != nil {
		h.Logger.Warn("oidc refresh decrypt failed (key rotated?)", "err", err)
		return
	}
	newExpiry, newRefresh, aerr := oidcR.RefreshSession(ctx, string(raw))
	if aerr != nil {
		h.Logger.Debug("oidc session refresh skipped", "status", aerr.Status)
		return
	}
	var newEnc []byte
	if ct, e := auth.Encrypt([]byte(newRefresh)); e == nil {
		newEnc = ct
	}
	if err := h.DB.RotateOIDCSession(ctx, refresh, newExpiry, newEnc); err != nil {
		h.Logger.Warn("oidc session rotate failed", "err", err)
	}
}

// Logout: POST /auth/logout.
func (h *Handler) Logout(c *echo.Context) error {
	// boom-93f.13: OIDC-aware logout. Under provider=oidc the web session is a
	// server-side oidc_sessions row keyed by the opaque `refresh_token` cookie
	// (no local bearer exists), so the local DeleteTokens path below can never
	// revoke it — Logout was a silent no-op. Delete the OIDC session so the
	// cookie can't be replayed for the rest of the id_token window, and clear
	// the client cookie. The local path stays byte-identical behind this branch
	// (CurrentResolver() is "local" by default).
	if auth.CurrentResolver().ProviderName() == "oidc" {
		refresh, ok := auth.ParseRefreshCookie(c.Request().Header.Get("Cookie"))
		if !ok {
			return apierr.MissingRefreshTokenCookie()
		}
		ctx := c.Request().Context()
		// boom-93f.14: revoke any bearers this user minted via /auth/refresh_token
		// so they die WITH the session, not up to 30 min later. Resolve the owner
		// from the session before deleting it.
		var owner string
		if o, found, _ := h.DB.GetOIDCSessionUser(ctx, refresh); found && o != "" {
			owner = o
			_ = h.DB.DeleteUserAccessTokens(ctx, owner)
		}
		if err := h.DB.DeleteOIDCSession(ctx, refresh); err != nil {
			return fmt.Errorf("oidc session deletion failed: %w", err)
		}
		h.clearRefreshCookie(c)
		h.clearOIDCFlowCookies(c)
		h.Logger.Info("logout", "user", owner)
		return nil
	}

	tkn, aerr := apihelpers.TokenFromHeader(c)
	if aerr != nil {
		return aerr
	}
	refresh, ok := auth.ParseRefreshCookie(c.Request().Header.Get("Cookie"))
	if !ok {
		return apierr.MissingRefreshTokenCookie()
	}
	n, err := h.DB.DeleteTokens(c.Request().Context(), tkn, refresh)
	if err != nil {
		return fmt.Errorf("token deletion failed: %w", err)
	}
	if n < 2 {
		return apierr.InvalidCredentials()
	}
	// boom-b5x.1: clear the client-side cookie with matching attributes
	// (Path + Secure + SameSite) so browsers actually evict the entry.
	h.clearRefreshCookie(c)
	h.clearOIDCFlowCookies(c)
	// The local path revokes by token, not username — the owner isn't resolved
	// here (an extra lookup would be a behavior change), so log the fact only.
	h.Logger.Info("logout")
	return nil
}

// CreateAPIToken: POST /auth/create_api_token. Body is optional; when present
// it may carry a `name` field (<= 42 chars, trimmed) which is stored as the
// human-readable label for the minted token. Empty/missing name is fine —
// the tokens list will just show an em-dash until renamed.
func (h *Handler) CreateAPIToken(c *echo.Context) (model.TokenResponse, error) {
	var out model.TokenResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	var body struct {
		Name string `json:"name"`
	}
	// Ignore decode errors — the endpoint has always been callable without a
	// body, and the shape is documented as optional. That tolerance is why the
	// route is on apiroute.POSTNoBody rather than apiroute.POST: the seam's
	// bind would turn a malformed/absent-content-type body into a hard 400.
	_ = c.Bind(&body)
	name := strings.TrimSpace(body.Name)
	if len(name) > 42 {
		name = name[:42]
	}
	raw, err := auth.CreateAPIToken(c.Request().Context(), h.DB, owner, name)
	if err != nil {
		return out, fmt.Errorf("api token insert failed: %w", err)
	}
	// Log the fact + safe identifiers only — never the token value in `raw`.
	h.Logger.Info("api token created", "user", owner, "name", name)
	return model.TokenResponse{APIToken: raw}, nil
}

// ListAPITokens: GET /auth/tokens.
func (h *Handler) ListAPITokens(c *echo.Context) ([]model.StoredApiToken, error) {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return nil, aerr
	}
	tokens, err := h.DB.ListApiTokens(c.Request().Context(), owner)
	if err != nil {
		return nil, fmt.Errorf("api token list failed: %w", err)
	}
	return tokens, nil
}

// DeleteToken: DELETE /auth/token/:id. Deletion is scoped to the requesting
// owner; the response is the same whether or not a row matched (no oracle for
// probing other users' token values).
func (h *Handler) DeleteToken(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return aerr
	}
	id := c.Param("id")
	if err := h.DB.DeleteAuthToken(c.Request().Context(), id, owner); err != nil {
		return fmt.Errorf("api token deletion failed: %w", err)
	}
	h.Logger.Info("api token deleted", "user", owner, "id", id)
	return nil
}

// UpdateToken: POST /auth/token (rename).
// boom-bi2: 4 KiB cap. Token metadata is a name string; no reason to accept a
// runaway body — apiroute.NoContentBody applies the same BodyLimitSmall cap the
// hand-rolled BindJSONWithLimit call used to.
func (h *Handler) UpdateToken(c *echo.Context, meta model.TokenMetadata) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return aerr
	}
	if err := h.DB.UpdateTokenMetadata(c.Request().Context(), owner, meta); err != nil {
		return fmt.Errorf("token metadata update failed: %w", err)
	}
	return nil
}

// CurrentUser: GET /auth/users/current (Users.hs).
//
// boom-dg7: also emits `timezone` (raw stored) and `effective_timezone`
// (post-3-level-resolution). Wakatime editor plugins ignore unknown fields,
// so this stays wire-safe with wakatime-compat callers.
func (h *Handler) CurrentUser(c *echo.Context) (model.UserStatusResponse, error) {
	var out model.UserStatusResponse
	owner, aerr := apihelpers.IdentifyOwnerFromCookie(h.DB, h.Logger, c, apierr.MissingRefreshTokenCookie())
	if aerr != nil {
		return out, aerr
	}
	ctx := c.Request().Context()
	// Best-effort read: on a lookup failure log and fall through to "", so
	// the resolver still yields a non-empty EffectiveTimezone (UTC unless
	// BOOM_DEFAULT_TIMEZONE is set). Never fail the whole /users/current
	// response — that would log every editor plugin out on a transient blip.
	rawTZ, err := h.DB.GetUserTimezone(ctx, owner)
	if err != nil {
		h.Logger.Warn("CurrentUser: users.timezone lookup failed; emitting empty",
			"user", owner, "err", err)
		rawTZ = ""
	}
	effective := db.ResolveTimezone(rawTZ, h.Cfg.DefaultTimezone)
	return model.UserStatusResponse{
		Data: model.UserStatus{
			FullName:          owner,
			Email:             owner + "@hakatime.dev",
			Photo:             "",
			IsAdmin:           h.Cfg.IsAdmin(owner),
			Timezone:          rawTZ,
			EffectiveTimezone: effective,
		},
	}, nil
}
