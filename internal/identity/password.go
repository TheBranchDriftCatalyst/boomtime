package identity

import (
	"fmt"
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
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
//
// boom-bi2: 4 KiB cap on the body. It is two short strings; anything larger is
// an attempt to amplify the argon2 verify below into a memory DoS —
// apiroute.NoContentBody binds under the same BodyLimitSmall cap the
// hand-rolled BindJSONWithLimit call used to.
func (h *Handler) ChangePassword(c *echo.Context, req changePasswordRequest) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return aerr
	}
	// ChangePassword revokes every OTHER session but keeps the caller's live —
	// so it still needs the raw bearer token. Identify already validated it;
	// re-parse the header for the token value.
	callerToken, _ := apihelpers.TokenFromHeader(c)
	// newPassword is required on every path. Checked BEFORE auth.ValidatePassword
	// so an empty value answers the shared "required" envelope rather than
	// ErrPasswordTooShort — a named ordering invariant, asserted by body content
	// in auth_cluster_coverage_test.go.
	if req.NewPassword == "" {
		return apierr.BadRequest("currentPassword and newPassword are required")
	}

	ctx := c.Request().Context()
	user, err := h.DB.GetUserByName(ctx, owner)
	if err != nil {
		return fmt.Errorf("user lookup failed: %w", err)
	}
	// SET-INITIAL-PASSWORD path. An OIDC-provisioned row stores ''::bytea
	// password material (db.ProvisionOIDCUser), so there is no current password
	// to prove — VerifyPasswordWithVersion rejects an empty stored hash by
	// design (boom-93f.19). Requiring currentPassword unconditionally therefore
	// wedged those accounts permanently: they could not obtain local login, and
	// UnlinkIdentity refused to release their only sign-in method with "set a
	// password first" — a remedy that existed nowhere (no other password
	// surface, no CLI command). If the IdP were decommissioned the account was
	// unrecoverable without manual SQL.
	//
	// Relaxing the check ONLY when the account provably has no password grants
	// no new authority: the caller already holds a valid session for this exact
	// account, and the write still goes through ChangePasswordAndRevoke (which
	// kills every other session). Accounts that DO have a password are
	// completely unchanged — currentPassword stays required and verified, so
	// this is not a reset bypass.
	hasPassword := user != nil && len(user.HashedPassword) > 0
	switch {
	case hasPassword:
		// Empty-guard fires BEFORE argon2 so the body carries the "required"
		// envelope exclusively, never the verify-side 401 text (named ordering
		// invariant, auth_cluster_coverage_test.go).
		if req.CurrentPassword == "" {
			return apierr.BadRequest("currentPassword and newPassword are required")
		}
		if !auth.VerifyPasswordWithVersion(req.CurrentPassword, user.HashedPassword, user.SaltUsed, user.ArgonVersion) {
			// 401 per the requirements: distinguishes a wrong current-password
			// from the generic 403 "your access token is bad".
			return apierr.New(http.StatusUnauthorized, "Current password is incorrect", nil)
		}
	case user == nil, req.CurrentPassword != "":
		// Row vanished between Identify and here, or the caller supplied a
		// current password for an account that has none — nothing it could ever
		// match. Same 401 envelope as before, so neither case is newly
		// distinguishable.
		return apierr.New(http.StatusUnauthorized, "Current password is incorrect", nil)
	}
	// boom-0gu: delegate to the shared auth.ValidatePassword extracted during
	// boom-e5e. This kills the duplicate inline validator that used a
	// byte-based len() check (which under-counted multibyte scripts) and an
	// ASCII-only letter/digit range (which rejected non-Latin passwords). The
	// shared version is rune-counted + unicode.IsLetter/IsDigit — safe for
	// multibyte scripts and identical policy across Register + ChangePassword.
	// Sentinel error text is already user-safe by design (see password_policy.go).
	if err := auth.ValidatePassword(req.NewPassword); err != nil {
		return apierr.BadRequest(err.Error())
	}

	newHash, newSalt, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return fmt.Errorf("password hash failed: %w", err)
	}
	// Atomic: UPDATE users + DELETE refresh_tokens (all) + DELETE auth_tokens
	// (all except the caller's own, and preserving never-expiring API tokens)
	// in ONE transaction. See db.ChangePasswordAndRevoke for the exact SQL.
	if err := h.DB.ChangePasswordAndRevoke(ctx, owner, newHash, newSalt, callerToken); err != nil {
		return fmt.Errorf("password change failed: %w", err)
	}
	// boom-awh.2: tag the record with "user" so the LogHub owner-filter
	// (logging.FilterForUser) hides it from other authenticated Logs viewers.
	// Never log the password, hash, or salt — the fact of a change is all
	// that's needed for operator visibility.
	h.Logger.Info("password changed", "user", owner)
	return nil
}
