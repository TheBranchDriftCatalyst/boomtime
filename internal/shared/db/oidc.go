// oidc.go — server-side OIDC session store + external-identity provisioning
// (boom-0oe.11). Session cookie values + provider refresh tokens are stored
// SHA-256-hashed only (same posture as auth.go's hashed_token columns).
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---- OIDC browser sessions ----

// CreateOIDCSession stores an OIDC session: hashed opaque cookie → (username,
// id_token expiry, encrypted provider refresh). rawSessionID is plaintext (only
// its SHA-256 is persisted). encryptedRefresh is the AES-256-GCM ciphertext of
// the provider refresh_token (boom-93f.11.6) — stored recoverably so
// /auth/refresh_token can do a refresh-grant; pass nil when there is none or
// when BOOM_ENCRYPTION_KEY is unset (session still works, silent refresh off).
func (d *DB) CreateOIDCSession(ctx context.Context, rawSessionID, username string, expiry time.Time, encryptedRefresh []byte) error {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO oidc_sessions (hashed_session_id, username, id_token_expiry, encrypted_refresh)
		 VALUES ($1, $2, $3, $4)`,
		hashSessionToken(rawSessionID), username, expiry, encryptedRefresh)
	return err
}

// GetOIDCSessionRefresh returns the encrypted provider refresh + id_token expiry
// for a NON-expired OIDC session cookie (boom-93f.11.6). ok=false when the
// session is absent/expired; encryptedRefresh is nil when none was stored.
func (d *DB) GetOIDCSessionRefresh(ctx context.Context, rawSessionID string) (encryptedRefresh []byte, expiry time.Time, ok bool, err error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT encrypted_refresh, id_token_expiry FROM oidc_sessions
		 WHERE hashed_session_id = $1 AND id_token_expiry > $2`,
		hashSessionToken(rawSessionID), time.Now().UTC())
	if serr := row.Scan(&encryptedRefresh, &expiry); serr != nil {
		if errors.Is(serr, pgx.ErrNoRows) {
			return nil, time.Time{}, false, nil
		}
		return nil, time.Time{}, false, serr
	}
	return encryptedRefresh, expiry, true, nil
}

// RotateOIDCSession extends a session in place: new id_token expiry + the
// (possibly rotated) encrypted provider refresh, keyed by the SAME cookie
// (boom-93f.11.6). Silent session extension — no new cookie is minted.
func (d *DB) RotateOIDCSession(ctx context.Context, rawSessionID string, newExpiry time.Time, encryptedRefresh []byte) error {
	_, err := d.Pool.Exec(ctx,
		`UPDATE oidc_sessions SET id_token_expiry = $2, encrypted_refresh = $3
		 WHERE hashed_session_id = $1`,
		hashSessionToken(rawSessionID), newExpiry, encryptedRefresh)
	return err
}

// GetOIDCSessionUser returns the username for a non-expired OIDC session cookie,
// or ("", false, nil) if absent/expired.
func (d *DB) GetOIDCSessionUser(ctx context.Context, rawSessionID string) (string, bool, error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT username FROM oidc_sessions
		 WHERE hashed_session_id = $1 AND id_token_expiry > $2`,
		hashSessionToken(rawSessionID), time.Now().UTC())
	var username string
	if err := row.Scan(&username); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return username, true, nil
}

// DeleteOIDCSession removes an OIDC session (logout).
func (d *DB) DeleteOIDCSession(ctx context.Context, rawSessionID string) error {
	_, err := d.Pool.Exec(ctx, `DELETE FROM oidc_sessions WHERE hashed_session_id = $1`,
		hashSessionToken(rawSessionID))
	return err
}

// ---- External identities + provisioning ----

// GetUserByExternalIdentity resolves (provider, sub) to a boomtime username,
// or ("", false, nil) if there's no link.
func (d *DB) GetUserByExternalIdentity(ctx context.Context, provider, sub string) (string, bool, error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT username FROM user_external_identities WHERE provider = $1 AND sub = $2`,
		provider, sub)
	var username string
	if err := row.Scan(&username); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return username, true, nil
}

// TouchExternalIdentity refreshes the cached email/claims + last_seen_at for an
// existing (provider, sub) on a returning login.
func (d *DB) TouchExternalIdentity(ctx context.Context, provider, sub, email string, claimsJSON []byte) error {
	_, err := d.Pool.Exec(ctx,
		`UPDATE user_external_identities SET email = $3, claims = $4, last_seen_at = now()
		 WHERE provider = $1 AND sub = $2`,
		provider, sub, email, claimsJSON)
	return err
}

// LinkExternalIdentity attaches (provider, sub) to an EXISTING username. Used
// by the authenticated account-link flow (HandleLink) — binding is always to
// the caller's own logged-in session. Fails on the UNIQUE(provider, sub)
// constraint if that identity is already linked (surfaced as a 409 conflict).
func (d *DB) LinkExternalIdentity(ctx context.Context, username, provider, sub, email string, claimsJSON []byte) error {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO user_external_identities (username, provider, sub, email, claims)
		 VALUES ($1, $2, $3, $4, $5)`,
		username, provider, sub, email, claimsJSON)
	return err
}

// ExternalIdentityRow is a linked identity rendered in account settings.
type ExternalIdentityRow struct {
	Provider   string
	Sub        string
	Email      string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// ListExternalIdentities returns a user's linked external identities.
func (d *DB) ListExternalIdentities(ctx context.Context, username string) ([]ExternalIdentityRow, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT provider, sub, COALESCE(email,''), created_at, last_seen_at
		 FROM user_external_identities WHERE username = $1 ORDER BY provider`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExternalIdentityRow
	for rows.Next() {
		var r ExternalIdentityRow
		if err := rows.Scan(&r.Provider, &r.Sub, &r.Email, &r.CreatedAt, &r.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteExternalIdentity unlinks a provider from a user. Returns false if there
// was no such link.
func (d *DB) DeleteExternalIdentity(ctx context.Context, username, provider string) (bool, error) {
	tag, err := d.Pool.Exec(ctx,
		`DELETE FROM user_external_identities WHERE username = $1 AND provider = $2`, username, provider)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// CountExternalIdentities counts a user's linked identities (unlink guard).
func (d *DB) CountExternalIdentities(ctx context.Context, username string) (int, error) {
	var n int
	err := d.Pool.QueryRow(ctx,
		`SELECT count(*) FROM user_external_identities WHERE username = $1`, username).Scan(&n)
	return n, err
}

// HasUsablePassword reports whether the user has non-empty password material
// (i.e. can log in locally). OIDC-provisioned users have empty material.
func (d *DB) HasUsablePassword(ctx context.Context, username string) (bool, error) {
	var ok bool
	err := d.Pool.QueryRow(ctx,
		`SELECT length(hashed_password) > 0 FROM users WHERE username = $1`, username).Scan(&ok)
	return ok, err
}

// UserExists reports whether a username is taken. General-purpose helper
// (previously the autolink-by-username check, removed in boom-93f.12).
func (d *DB) UserExists(ctx context.Context, username string) (bool, error) {
	var exists bool
	err := d.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)`, username).Scan(&exists)
	return exists, err
}

// ProvisionOIDCUser mints a NEW user (no password → local login disabled) plus
// its external-identity link, in one transaction. The username is uniquified
// with a numeric suffix if taken; role lands on the users row. Returns the
// final username.
func (d *DB) ProvisionOIDCUser(ctx context.Context, preferredUsername, provider, sub, email, role string, claimsJSON []byte) (string, error) {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	// boom-93f.19: pick + claim the username in ONE atomic step. The old
	// EXISTS-then-INSERT uniquify had a TOCTOU race — two first-logins picking
	// the same free name would both pass the EXISTS check, then the second
	// INSERT would 23505 and surface as a 500. Instead try preferred,
	// preferred-2, preferred-3, … via INSERT … ON CONFLICT (username) DO NOTHING
	// RETURNING: a name already taken (even by a not-yet-committed concurrent txn,
	// which we block on until it resolves) yields no row so we advance the suffix,
	// and DO NOTHING never raises 23505 — so it can't abort this transaction. For
	// the common non-racing case this inserts `preferred` on the first iteration,
	// identical to before.
	//
	// Empty password material = local login path is inert for this user
	// (VerifyPassword fails on an empty hash). Role from the provider groups;
	// capabilities '{}' means "use the role's defaults".
	username := ""
	for i := 1; i < 1000; i++ {
		candidate := preferredUsername
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", preferredUsername, i)
		}
		var got string
		err := tx.QueryRow(ctx,
			`INSERT INTO users (username, hashed_password, salt_used, argon_version, role, capabilities)
			 VALUES ($1, ''::bytea, ''::bytea, 2, $2, '{}'::jsonb)
			 ON CONFLICT (username) DO NOTHING
			 RETURNING username`,
			candidate, role).Scan(&got)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // taken (possibly by a concurrent txn) — advance the suffix
		}
		if err != nil {
			return "", err
		}
		username = got
		break
	}
	if username == "" {
		return "", errors.New("could not find a free username after 1000 attempts")
	}

	if _, err = tx.Exec(ctx,
		`INSERT INTO user_external_identities (username, provider, sub, email, claims)
		 VALUES ($1, $2, $3, $4, $5)`,
		username, provider, sub, email, claimsJSON); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return username, nil
}
