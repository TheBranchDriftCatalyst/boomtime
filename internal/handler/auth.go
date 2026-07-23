package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
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
// and Secure iff h.Cfg.CookieSecure is true (gaka-b5x.1). Path is scoped to
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
// gaka-imm: constant-time user enumeration defence. Prior to this fix, the
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
func (h *Handler) Login(c *echo.Context) error {
	var creds model.AuthRequest
	// gaka-bi2: 4 KiB cap. Credentials are two short strings; a fat body here
	// would just amplify the argon2 verify below into a memory DoS.
	if aerr := BindJSONWithLimit(c, &creds, BodyLimitSmall); aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()

	user, err := h.DB.GetUserByName(ctx, creds.Username)
	if err != nil {
		return h.internalErr(c, "user lookup failed", err)
	}
	if user == nil {
		// gaka-imm: burn the same ~10ms of argon2 the found-user branch
		// spends in VerifyPassword. Result discarded — the point is to
		// eliminate the timing gap, not to actually authenticate.
		auth.BurnSentinelVerify(creds.Password)
		return respondErr(c, apierr.InvalidCredentials())
	}
	if !auth.VerifyPasswordWithVersion(creds.Password, user.HashedPassword, user.SaltUsed, user.ArgonVersion) {
		return respondErr(c, apierr.InvalidCredentials())
	}

	// gaka-awh.6 (Bravo MEDIUM): transparent rehash-on-login. If the row is
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
		return h.internalErr(c, "access token creation failed", err)
	}
	h.setRefreshCookie(c, td)
	return c.JSON(http.StatusOK, loginResponse(td, time.Now().UTC()))
}

// Register: POST /auth/register.
func (h *Handler) Register(c *echo.Context) error {
	if !h.Cfg.EnableRegistration {
		return respondErr(c, apierr.DisabledRegistration())
	}
	var creds model.AuthRequest
	// gaka-bi2: 4 KiB cap. Same rationale as Login — credentials are short.
	if aerr := BindJSONWithLimit(c, &creds, BodyLimitSmall); aerr != nil {
		return respondErr(c, aerr)
	}
	// gaka-e5e: enforce the shared password policy BEFORE hashing +
	// inserting. Prior to this check, POST /auth/register accepted empty
	// and toy passwords ("", "a", "1234") and minted a working session.
	// auth.ValidatePassword's sentinel errors are user-safe by design —
	// surface .Error() directly (no internal state leaked).
	if err := auth.ValidatePassword(creds.Password); err != nil {
		return respondErr(c, apierr.BadRequest(err.Error()))
	}
	ctx := c.Request().Context()

	if err := auth.CreateUser(ctx, h.DB, creds.Username, creds.Password); err != nil {
		if errors.Is(err, auth.ErrUserExists) {
			return respondErr(c, apierr.UsernameExists(creds.Username))
		}
		if errors.Is(err, auth.ErrInvalidCredentials) {
			// unreachable via CreateUser; kept for symmetry with Login flow.
			return respondErr(c, apierr.InvalidCredentials())
		}
		return h.internalErr(c, "user creation failed", err)
	}

	td := mkTokenData(creds.Username)
	if err := h.DB.CreateAccessTokens(ctx, td, h.Cfg.SessionExpiry); err != nil {
		return h.internalErr(c, "access token creation failed", err)
	}
	h.setRefreshCookie(c, td)
	return c.JSON(http.StatusOK, loginResponse(td, time.Now().UTC()))
}

// RefreshToken: POST /auth/refresh_token (reads refresh_token cookie).
func (h *Handler) RefreshToken(c *echo.Context) error {
	owner, aerr := h.resolveOwnerFromCookie(c, apierr.MissingRefreshTokenCookie())
	if aerr != nil {
		return respondErr(c, aerr)
	}

	td := mkTokenData(owner)
	if err := h.DB.CreateAccessTokens(c.Request().Context(), td, h.Cfg.SessionExpiry); err != nil {
		return h.internalErr(c, "access token creation failed", err)
	}
	h.setRefreshCookie(c, td)
	return c.JSON(http.StatusOK, loginResponse(td, time.Now().UTC()))
}

// Logout: POST /auth/logout.
func (h *Handler) Logout(c *echo.Context) error {
	tkn, aerr := tokenFromHeader(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	refresh, ok := auth.ParseRefreshCookie(c.Request().Header.Get("Cookie"))
	if !ok {
		return respondErr(c, apierr.MissingRefreshTokenCookie())
	}
	n, err := h.DB.DeleteTokens(c.Request().Context(), tkn, refresh)
	if err != nil {
		return h.internalErr(c, "token deletion failed", err)
	}
	if n < 2 {
		return respondErr(c, apierr.InvalidCredentials())
	}
	// gaka-b5x.1: clear the client-side cookie with matching attributes
	// (Path + Secure + SameSite) so browsers actually evict the entry.
	h.clearRefreshCookie(c)
	return noContent(c)
}

// CreateAPIToken: POST /auth/create_api_token.
func (h *Handler) CreateAPIToken(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	raw, err := auth.CreateAPIToken(c.Request().Context(), h.DB, owner)
	if err != nil {
		return h.internalErr(c, "api token insert failed", err)
	}
	return c.JSON(http.StatusOK, model.TokenResponse{APIToken: raw})
}

// ListAPITokens: GET /auth/tokens.
func (h *Handler) ListAPITokens(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	tokens, err := h.DB.ListApiTokens(c.Request().Context(), owner)
	if err != nil {
		return h.internalErr(c, "api token list failed", err)
	}
	return c.JSON(http.StatusOK, tokens)
}

// DeleteToken: DELETE /auth/token/:id. Deletion is scoped to the requesting
// owner; the response is the same whether or not a row matched (no oracle for
// probing other users' token values).
func (h *Handler) DeleteToken(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id := c.Param("id")
	if err := h.DB.DeleteAuthToken(c.Request().Context(), id, owner); err != nil {
		return h.internalErr(c, "api token deletion failed", err)
	}
	return noContent(c)
}

// UpdateToken: POST /auth/token (rename).
func (h *Handler) UpdateToken(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	var meta model.TokenMetadata
	// gaka-bi2: 4 KiB cap. Token metadata is a name string; no reason to
	// accept a runaway body.
	if aerr := BindJSONWithLimit(c, &meta, BodyLimitSmall); aerr != nil {
		return respondErr(c, aerr)
	}
	if err := h.DB.UpdateTokenMetadata(c.Request().Context(), owner, meta); err != nil {
		return h.internalErr(c, "token metadata update failed", err)
	}
	return noContent(c)
}

// CurrentUser: GET /auth/users/current (Users.hs).
func (h *Handler) CurrentUser(c *echo.Context) error {
	owner, aerr := h.resolveOwnerFromCookie(c, apierr.MissingRefreshTokenCookie())
	if aerr != nil {
		return respondErr(c, aerr)
	}
	return c.JSON(http.StatusOK, model.UserStatusResponse{
		Data: model.UserStatus{
			FullName: owner,
			Email:    owner + "@hakatime.dev",
			Photo:    "",
		},
	})
}
