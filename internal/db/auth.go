// auth.go holds user and token storage: credentials, access/refresh tokens,
// and API-token management.
//
// gaka-b5x.2 + gaka-uj7: session tokens are stored ONLY as SHA-256 hashes
// at rest (hashed_token / hashed_refresh_token bytea columns). The legacy
// raw `token` / `refresh_token` columns were dropped in migration 00031.
// A DB read never yields a usable session token — the hash is one-way and
// cannot be reversed to authenticate.
package db

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/jackc/pgx/v5"
)

// hashSessionToken returns SHA-256(raw) as bytes. Duplicated from
// internal/auth.HashToken to avoid an import cycle (internal/auth already
// depends on internal/db via service.go). No salt: session tokens are
// 128-bit high-entropy UUIDs so salting adds no pre-image bits, and the
// unsalted hash keeps lookup a single indexed equality on the bytea column.
func hashSessionToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// ---- Users & tokens ----

// GetUserByName returns the stored user credentials or (nil,nil) if absent.
//
// gaka-awh.6: argon_version comes back on the same round-trip so callers can
// dispatch to VerifyPasswordWithVersion (and the Login handler can decide
// whether to bump the row to the current generation).
func (d *DB) GetUserByName(ctx context.Context, name string) (*StoredUser, error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT username, hashed_password, salt_used, argon_version
		 FROM users WHERE username = $1`, name)
	var u StoredUser
	if err := row.Scan(&u.Username, &u.HashedPassword, &u.SaltUsed, &u.ArgonVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// InsertUser inserts a new user; returns false if the username already exists.
//
// gaka-awh.6: argon_version is passed EXPLICITLY (from u.ArgonVersion) so a
// caller that forgets to set it lands the row at 0 — which reads as "unknown
// version" downstream and gets caught by tests. Callers should pass
// auth.ArgonVersionCurrent (2) for every fresh user; only tests planting
// legacy rows should pass 1.
func (d *DB) InsertUser(ctx context.Context, u StoredUser) (bool, error) {
	existing, err := d.GetUserByName(ctx, u.Username)
	if err != nil {
		return false, err
	}
	if existing != nil {
		return false, nil
	}
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used, argon_version)
		 VALUES ($1, $2, $3, $4)`,
		u.Username, u.HashedPassword, u.SaltUsed, u.ArgonVersion)
	if err != nil {
		return false, err
	}
	return true, nil
}

// UpgradeArgonVersion rewrites a user's hashed_password + salt_used + tags
// the row with a new argon_version. Used by the Login handler to
// transparently upgrade a legacy (v1) hash to the current generation (v2)
// on the SAME request that successfully authenticated against the old
// hash. The scope predicate matches on (username, argon_version = old)
// so two concurrent logins for the same user racing on the upgrade only
// end up doing the work ONCE — the second UPDATE's WHERE clause fails and
// affects zero rows, which is fine.
//
// oldVersion is the version the row is CURRENTLY at (what we read on Login);
// newVersion is what we're upgrading it to. Both are passed explicitly to
// keep the SQL a single atomic UPDATE without a SELECT-then-UPDATE race.
func (d *DB) UpgradeArgonVersion(ctx context.Context, username string, newHash, newSalt []byte, oldVersion, newVersion int) error {
	_, err := d.Pool.Exec(ctx,
		`UPDATE users
		 SET hashed_password = $2, salt_used = $3, argon_version = $4
		 WHERE username = $1 AND argon_version = $5`,
		username, newHash, newSalt, newVersion, oldVersion)
	return err
}

// GetUserByToken returns the owner of an API token, honoring token_expiry
// (CLI/api tokens have null expiry and never expire), and bumps last_usage.
// Post-v31 the raw `token` column is dropped — lookup is hashed-only.
func (d *DB) GetUserByToken(ctx context.Context, token string) (string, bool, error) {
	hashed := hashSessionToken(token)
	row := d.Pool.QueryRow(ctx, `
		SELECT owner FROM auth_tokens
		WHERE  hashed_token = $1
		AND    COALESCE(token_expiry, (NOW() + interval '1 hours')::timestamp without time zone) > $2`,
		hashed, time.Now().UTC())
	var owner string
	if err := row.Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	// Bump last usage (best-effort; ignore error impact on the read path).
	_, _ = d.Pool.Exec(ctx,
		`UPDATE auth_tokens SET last_usage = now()::timestamp WHERE hashed_token = $1`,
		hashed)
	return owner, true, nil
}

// GetUserByRefreshToken returns the owner of a non-expired refresh token
// (post-v31 hashed-only lookup).
func (d *DB) GetUserByRefreshToken(ctx context.Context, token string) (string, bool, error) {
	hashed := hashSessionToken(token)
	row := d.Pool.QueryRow(ctx,
		`SELECT owner FROM refresh_tokens
		 WHERE  hashed_refresh_token = $1
		 AND    token_expiry > $2`,
		hashed, time.Now().UTC())
	var owner string
	if err := row.Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return owner, true, nil
}

// CreateAccessTokens inserts an access token (30 min) and a refresh token
// (expiry hours) then deletes any expired tokens for the owner.
//
// gaka-b5x.2: new rows store only the SHA-256 of the raw token in
// hashed_token / hashed_refresh_token. The legacy `token` / `refresh_token`
// columns are left NULL for new rows; the DB no longer holds a usable
// session token for any session minted post-migration 00026.
func (d *DB) CreateAccessTokens(ctx context.Context, td TokenData, expiryHours int64) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx,
		`INSERT INTO auth_tokens(owner, hashed_token, token_expiry)
		 VALUES ($1, $2, NOW() + interval '30 minutes')`,
		td.Owner, hashSessionToken(td.Token)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO refresh_tokens(owner, hashed_refresh_token, token_expiry)
		 VALUES ($1, $2, NOW() + interval '1' hour * $3)`,
		td.Owner, hashSessionToken(td.RefreshToken), expiryHours); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM auth_tokens WHERE owner = $1 AND token_expiry < NOW()`, td.Owner); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM refresh_tokens WHERE owner = $1 AND token_expiry < NOW()`, td.Owner); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// InsertAPIToken stores the SHA-256 of a base64(uuid) token with a null
// expiry (never expires). The raw token value is thrown away at the
// boundary — a DB read no longer yields a usable API token
// (gaka-b5x.2).
func (d *DB) InsertAPIToken(ctx context.Context, owner, token string) error {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO auth_tokens(owner, hashed_token) VALUES ($1, $2)`,
		owner, hashSessionToken(token))
	return err
}

// DeleteTokens removes an auth token and refresh token, returning rows
// affected. Post-v31 hashed-only lookup.
func (d *DB) DeleteTokens(ctx context.Context, token, refreshToken string) (int64, error) {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	t1, err := tx.Exec(ctx,
		`DELETE FROM auth_tokens WHERE hashed_token = $1`,
		hashSessionToken(token))
	if err != nil {
		return 0, err
	}
	t2, err := tx.Exec(ctx,
		`DELETE FROM refresh_tokens WHERE hashed_refresh_token = $1`,
		hashSessionToken(refreshToken))
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return t1.RowsAffected() + t2.RowsAffected(), nil
}

// DeleteAuthToken deletes an API token by its 12-char display prefix,
// scoped to its owner. Post-v31 the identifier is always the first 12
// chars of the hex-encoded hashed_token — the raw column is gone.
func (d *DB) DeleteAuthToken(ctx context.Context, token, owner string) error {
	_, err := d.Pool.Exec(ctx, `
		DELETE FROM auth_tokens
		WHERE owner = $2
		  AND LEFT(encode(hashed_token, 'hex'), 12) = $1`, token, owner)
	return err
}

// ListApiTokens returns the non-expiring API tokens for a user. Identifier
// is the first 12 chars of the hex-encoded hashed_token — stable across
// requests, revealed once to the client but not derivable back to the raw
// token (hash is one-way).
func (d *DB) ListApiTokens(ctx context.Context, owner string) ([]model.StoredApiToken, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT LEFT(encode(hashed_token, 'hex'), 12) AS id,
		       last_usage::timestamptz, token_name, token_description
		FROM auth_tokens
		WHERE owner = $1 AND token_expiry IS NULL`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.StoredApiToken{}
	for rows.Next() {
		var t model.StoredApiToken
		var lu *time.Time
		if err := rows.Scan(&t.ID, &lu, &t.Name, &t.Desc); err != nil {
			return nil, err
		}
		t.LastUsage = lu
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTokenMetadata renames a token by its 12-char hashed_token prefix.
func (d *DB) UpdateTokenMetadata(ctx context.Context, owner string, m model.TokenMetadata) error {
	_, err := d.Pool.Exec(ctx,
		`UPDATE auth_tokens SET token_name = $3
		 WHERE owner = $2
		   AND LEFT(encode(hashed_token, 'hex'), 12) = $1`,
		m.TokenID, owner, m.TokenName)
	return err
}

// UpdatePassword rewrites the argon2id hash + salt for a user
// (POST /api/v1/users/current/password). Callers are responsible for
// verifying the current password before calling.
//
// gaka-awh.6: this path always writes at the CURRENT generation — anyone
// calling UpdatePassword is producing a fresh hash from a plaintext they
// just verified, so there is no reason NOT to bump the version.
func (d *DB) UpdatePassword(ctx context.Context, username string, hashedPassword, salt []byte) error {
	_, err := d.Pool.Exec(ctx,
		`UPDATE users
		 SET hashed_password = $1, salt_used = $2, argon_version = 2
		 WHERE username = $3`,
		hashedPassword, salt, username)
	return err
}

// RevokeAllRefreshTokens deletes every refresh_tokens row for the owner.
// Used by ChangePassword to force re-login on any other device holding a
// still-valid refresh cookie for the account.
func (d *DB) RevokeAllRefreshTokens(ctx context.Context, username string) error {
	_, err := d.Pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE owner = $1`, username)
	return err
}

// changePasswordFaultInjector is a nil-in-production test seam: if non-nil,
// it is called AFTER the UPDATE users row runs inside the tx but BEFORE the
// two DELETEs. Returning a non-nil error causes the transaction to roll back,
// giving TestChangePassword_AtomicOnDBError a deterministic way to prove that
// the whole ChangePasswordAndRevoke unit-of-work is atomic. Tests set/clear it
// via SetChangePasswordFaultInjector.
var changePasswordFaultInjector func() error

// SetChangePasswordFaultInjector installs a hook that runs inside the
// ChangePasswordAndRevoke transaction after the UPDATE users step. Pass nil
// to clear. Intended ONLY for tests — production callers must not use this.
func SetChangePasswordFaultInjector(fn func() error) { changePasswordFaultInjector = fn }

// ChangePasswordAndRevoke is the atomic unit-of-work for
// POST /api/v1/users/current/password. In a SINGLE transaction it:
//
//  1. UPDATE users SET hashed_password=$2, salt_used=$3 WHERE username=$1
//  2. DELETE FROM refresh_tokens WHERE owner=$1
//     (revokes EVERY refresh session — including the caller's — so any
//      cookie-holding browser is bounced on next /auth/refresh_token)
//  3. DELETE FROM auth_tokens WHERE owner=$1 AND token <> $4 AND token_expiry IS NOT NULL
//     (revokes every OTHER live access token, closing the 30-minute window
//      Charlie's red-team called out. exceptToken is the caller's own access
//      token from resolveUser — kept alive so the change-password response
//      still comes back to a logged-in session. Non-expiring API tokens
//      (token_expiry IS NULL) are left alone: password change does NOT
//      revoke API keys — those are managed explicitly via the tokens UI.)
//
// If any step fails (including a fault injected via
// SetChangePasswordFaultInjector) the tx rolls back, leaving password + tokens
// untouched. This closes the "process dies mid-write and old sessions still
// work with the new password" gap Charlie flagged as LOW.
func (d *DB) ChangePasswordAndRevoke(ctx context.Context, username string, hashedPassword, salt []byte, exceptToken string) error {
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// gaka-awh.6: password rotation always writes at the CURRENT argon
	// generation (v2) — we just produced a fresh hash from a verified
	// plaintext, no reason to persist a legacy-params hash.
	if _, err = tx.Exec(ctx,
		`UPDATE users
		 SET hashed_password = $1, salt_used = $2, argon_version = 2
		 WHERE username = $3`,
		hashedPassword, salt, username); err != nil {
		return err
	}
	if fn := changePasswordFaultInjector; fn != nil {
		if err := fn(); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM refresh_tokens WHERE owner = $1`, username); err != nil {
		return err
	}
	// exceptToken carries the caller's own access token from resolveUser so
	// the caller isn't force-logged-out by the very request they just made.
	// The `token_expiry IS NOT NULL` guard preserves non-expiring API tokens
	// (which have NULL expiry) so password rotation doesn't nuke CLI keys.
	//
	// gaka-b5x.2: post-hashing, the "skip me" predicate must match against
	// the caller's token via hashed_token (new rows) OR raw token (legacy).
	// Post-v31 hashed-only lookup — the caller's exception is identified by
	// hashed_token alone.
	if _, err = tx.Exec(ctx,
		`DELETE FROM auth_tokens
		 WHERE owner = $1
		 AND   hashed_token <> $2
		 AND   token_expiry IS NOT NULL`,
		username, hashSessionToken(exceptToken)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
