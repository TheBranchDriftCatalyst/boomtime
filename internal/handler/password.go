package handler

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/labstack/echo/v5"
)

// changePasswordRequest is the body accepted by POST /api/v1/users/current/password.
type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// ChangePassword: POST /api/v1/users/current/password.
//
// Verifies the caller's current password against the stored argon2id hash,
// enforces a reasonable strength policy on the new one, hashes+salts it with
// the same argon2id parameters used by CreateUser, then hands off to
// DB.ChangePasswordAndRevoke which — in a SINGLE transaction — updates the
// users row, deletes every refresh_tokens row for the owner, and deletes
// every OTHER (30-min-expiring) auth_tokens row for the owner. The caller's
// own access token (from resolveUser) is passed through as the exception so
// the caller keeps their session and can navigate away without a bounce.
//
// Wrapping all three writes in one tx closes two gaps Charlie flagged:
//   - CRITICAL: RevokeAllRefreshTokens didn't touch auth_tokens, so a stolen
//     access token stayed live for its remaining ≤30-min TTL after rotation.
//   - LOW: UPDATE users + DELETE refresh_tokens were two separate exec calls,
//     so a process crash between them could leave the password rotated with
//     stale sessions still valid.
func (h *Handler) ChangePassword(c *echo.Context) error {
	callerToken, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	var req changePasswordRequest
	// gaka-bi2: 4 KiB cap. The body is two short strings; anything larger is
	// an attempt to amplify the argon2 verify below into a memory DoS.
	if aerr := BindJSONWithLimit(c, &req, BodyLimitSmall); aerr != nil {
		return respondErr(c, aerr)
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		return respondErr(c, apierr.BadRequest("currentPassword and newPassword are required"))
	}

	ctx := c.Request().Context()
	user, err := h.DB.GetUserByName(ctx, owner)
	if err != nil {
		return h.internalErr(c, "user lookup failed", err)
	}
	if user == nil || !auth.VerifyPasswordWithVersion(req.CurrentPassword, user.HashedPassword, user.SaltUsed, user.ArgonVersion) {
		// 401 per the requirements: distinguishes a wrong current-password
		// from the generic 403 "your access token is bad".
		return respondErr(c, apierr.New(http.StatusUnauthorized, "Current password is incorrect", nil))
	}
	// gaka-0gu: delegate to the shared auth.ValidatePassword extracted during
	// gaka-e5e. This kills the duplicate inline validator that used a
	// byte-based len() check (which under-counted multibyte scripts) and an
	// ASCII-only letter/digit range (which rejected non-Latin passwords). The
	// shared version is rune-counted + unicode.IsLetter/IsDigit — safe for
	// multibyte scripts and identical policy across Register + ChangePassword.
	// Sentinel error text is already user-safe by design (see password_policy.go).
	if err := auth.ValidatePassword(req.NewPassword); err != nil {
		return respondErr(c, apierr.BadRequest(err.Error()))
	}

	newHash, newSalt, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return h.internalErr(c, "password hash failed", err)
	}
	// Atomic: UPDATE users + DELETE refresh_tokens (all) + DELETE auth_tokens
	// (all except the caller's own, and preserving never-expiring API tokens)
	// in ONE transaction. See db.ChangePasswordAndRevoke for the exact SQL.
	if err := h.DB.ChangePasswordAndRevoke(ctx, owner, newHash, newSalt, callerToken); err != nil {
		return h.internalErr(c, "password change failed", err)
	}
	// gaka-awh.2: tag the record with "user" so the LogHub owner-filter
	// (logging.FilterForUser) hides it from other authenticated Logs viewers.
	// Never log the password, hash, or salt — the fact of a change is all
	// that's needed for operator visibility.
	h.Logger.Info("password changed", "user", owner)
	return noContent(c)
}
