package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/auth"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/model"
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

// setRefreshCookie writes the refresh_token cookie: HttpOnly, SameSite=Strict.
// Path is scoped to the app root (prefix + "/") rather than "/auth" so the
// cookie is also sent on the cookie-authenticated import WebSocket handshake
// (/import/jobs/:id/ws), which the browser can't send an Authorization header on.
func (h *Handler) setRefreshCookie(c *echo.Context, td db.TokenData) {
	prefix := strings.TrimSuffix(h.Cfg.APIPrefix, "/")
	c.SetCookie(&http.Cookie{
		Name:     "refresh_token",
		Value:    td.RefreshToken,
		Path:     prefix + "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
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
func (h *Handler) Login(c *echo.Context) error {
	var creds model.AuthRequest
	if err := c.Bind(&creds); err != nil {
		return respondErr(c, apierr.Generic())
	}
	ctx := c.Request().Context()

	user, err := h.DB.GetUserByName(ctx, creds.Username)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if user == nil || !auth.VerifyPassword(creds.Password, user.HashedPassword, user.SaltUsed) {
		return respondErr(c, apierr.InvalidCredentials())
	}

	td := mkTokenData(creds.Username)
	if err := h.DB.CreateAccessTokens(ctx, td, h.Cfg.SessionExpiry); err != nil {
		return respondErr(c, apierr.Generic())
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
	if err := c.Bind(&creds); err != nil {
		return respondErr(c, apierr.Generic())
	}
	ctx := c.Request().Context()

	hash, salt, err := auth.HashPassword(creds.Password)
	if err != nil {
		return respondErr(c, apierr.RegisterError())
	}
	created, err := h.DB.InsertUser(ctx, db.StoredUser{
		Username: creds.Username, HashedPassword: hash, SaltUsed: salt,
	})
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if !created {
		return respondErr(c, apierr.UsernameExists(creds.Username))
	}

	td := mkTokenData(creds.Username)
	if err := h.DB.CreateAccessTokens(ctx, td, h.Cfg.SessionExpiry); err != nil {
		return respondErr(c, apierr.Generic())
	}
	h.setRefreshCookie(c, td)
	return c.JSON(http.StatusOK, loginResponse(td, time.Now().UTC()))
}

// RefreshToken: POST /auth/refresh_token (reads refresh_token cookie).
func (h *Handler) RefreshToken(c *echo.Context) error {
	refresh, ok := auth.ParseRefreshCookie(c.Request().Header.Get("Cookie"))
	if !ok {
		return respondErr(c, apierr.MissingRefreshTokenCookie())
	}
	ctx := c.Request().Context()

	owner, ok, err := h.DB.GetUserByRefreshToken(ctx, refresh)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if !ok {
		return respondErr(c, apierr.ExpiredRefreshToken())
	}

	td := mkTokenData(owner)
	if err := h.DB.CreateAccessTokens(ctx, td, h.Cfg.SessionExpiry); err != nil {
		return respondErr(c, apierr.Generic())
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
		return respondErr(c, apierr.Generic())
	}
	if n < 2 {
		return respondErr(c, apierr.InvalidCredentials())
	}
	return noContent(c)
}

// CreateAPIToken: POST /auth/create_api_token.
func (h *Handler) CreateAPIToken(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	raw := auth.NewRawToken()
	if err := h.DB.InsertAPIToken(c.Request().Context(), owner, auth.ToBase64(raw)); err != nil {
		return respondErr(c, apierr.Generic())
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
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, tokens)
}

// DeleteToken: DELETE /auth/token/:id.
func (h *Handler) DeleteToken(c *echo.Context) error {
	_, _, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id := c.Param("id")
	if err := h.DB.DeleteAuthToken(c.Request().Context(), id); err != nil {
		return respondErr(c, apierr.Generic())
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
	if err := c.Bind(&meta); err != nil {
		return respondErr(c, apierr.Generic())
	}
	if err := h.DB.UpdateTokenMetadata(c.Request().Context(), owner, meta); err != nil {
		return respondErr(c, apierr.Generic())
	}
	return noContent(c)
}

// CurrentUser: GET /auth/users/current (Users.hs).
func (h *Handler) CurrentUser(c *echo.Context) error {
	refresh, ok := auth.ParseRefreshCookie(c.Request().Header.Get("Cookie"))
	if !ok {
		return respondErr(c, apierr.MissingRefreshTokenCookie())
	}
	owner, ok, err := h.DB.GetUserByRefreshToken(c.Request().Context(), refresh)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if !ok {
		return respondErr(c, apierr.ExpiredRefreshToken())
	}
	return c.JSON(http.StatusOK, model.UserStatusResponse{
		Data: model.UserStatus{
			FullName: owner,
			Email:    owner + "@hakatime.dev",
			Photo:    "",
		},
	})
}
