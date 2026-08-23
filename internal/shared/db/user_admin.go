// user_admin.go — offline user-administration reads/writes backing the
// `boomtime user ...` CLI (boom-0oe.10). These touch the user-model substrate
// columns (role, disabled_at — migration 00046). They are operator tools with
// no HTTP surface; the flag-gated Identity path is what enforces them at
// request time.
package db

import (
	"context"
	"time"
)

// UserAdminRow is the compact users view rendered by `boomtime user list` and
// the admin caps dashboard. Capabilities is the raw per-user override JSONB
// ('{}' when unset) — parse with auth.BuildIdentity to get EFFECTIVE caps.
type UserAdminRow struct {
	Username     string
	Role         string
	Capabilities []byte
	DisabledAt   *time.Time
}

// ListUsersAdmin returns every user's (username, role, capabilities,
// disabled_at), ordered by username, for the admin CLI + caps dashboard.
func (d *DB) ListUsersAdmin(ctx context.Context) ([]UserAdminRow, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT username, role, capabilities, disabled_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UserAdminRow
	for rows.Next() {
		var r UserAdminRow
		if err := rows.Scan(&r.Username, &r.Role, &r.Capabilities, &r.DisabledAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetUserRole sets users.role for username. Returns false if no such user.
// The caller validates the role string (auth.ValidRole) before calling.
func (d *DB) SetUserRole(ctx context.Context, username, role string) (bool, error) {
	tag, err := d.Pool.Exec(ctx,
		`UPDATE users SET role = $2 WHERE username = $1`, username, role)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// SetUserDisabled sets (disable=true → disabled_at = now()) or clears
// (disable=false → disabled_at = NULL) the disabled flag. Returns false if no
// such user. Disabling is idempotent (re-disabling just refreshes the
// timestamp), so RowsAffected==0 unambiguously means "no such user".
//
// boom-93f.15: disabling is a KILL SWITCH, effective regardless of
// BOOM_FEATURE_USER_MODEL. Setting disabled_at alone was inert under the
// default flag-off posture (the flag-gated Identity path skipped the check and
// the Login path only checked when the flag was on), so a "disabled" account
// kept working. Now disabling ALSO purges every live credential — local access
// + refresh tokens and OIDC sessions — in one transaction, so existing sessions
// die immediately; new logins are blocked by the Login handler's (now
// unconditional) disabled check. This keeps the hot request path query-free
// under flag-off while still making the switch real.
func (d *DB) SetUserDisabled(ctx context.Context, username string, disable bool) (bool, error) {
	if !disable {
		tag, err := d.Pool.Exec(ctx, `UPDATE users SET disabled_at = NULL WHERE username = $1`, username)
		if err != nil {
			return false, err
		}
		return tag.RowsAffected() > 0, nil
	}

	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `UPDATE users SET disabled_at = now() WHERE username = $1`, username)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil // no such user → nothing to revoke
	}
	// Revoke all live credentials for the account.
	if _, err := tx.Exec(ctx, `DELETE FROM auth_tokens WHERE owner = $1`, username); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM refresh_tokens WHERE owner = $1`, username); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM oidc_sessions WHERE username = $1`, username); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
