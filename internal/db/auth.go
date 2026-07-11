// auth.go holds user and token storage: credentials, access/refresh tokens,
// and API-token management.
package db

import (
	"context"
	"errors"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/jackc/pgx/v5"
)

// ---- Users & tokens ----

// GetUserByName returns the stored user credentials or (nil,nil) if absent.
func (d *DB) GetUserByName(ctx context.Context, name string) (*StoredUser, error) {
	row := d.Pool.QueryRow(ctx, `SELECT username, hashed_password, salt_used FROM users WHERE username = $1`, name)
	var u StoredUser
	if err := row.Scan(&u.Username, &u.HashedPassword, &u.SaltUsed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// InsertUser inserts a new user; returns false if the username already exists.
func (d *DB) InsertUser(ctx context.Context, u StoredUser) (bool, error) {
	existing, err := d.GetUserByName(ctx, u.Username)
	if err != nil {
		return false, err
	}
	if existing != nil {
		return false, nil
	}
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, $2, $3)`,
		u.Username, u.HashedPassword, u.SaltUsed)
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetUserByToken returns the owner of an API token, honoring token_expiry
// (CLI/api tokens have null expiry and never expire), and bumps last_usage.
func (d *DB) GetUserByToken(ctx context.Context, token string) (string, bool, error) {
	row := d.Pool.QueryRow(ctx, `
		SELECT owner FROM auth_tokens
		WHERE  token = $1
		AND    COALESCE(token_expiry, (NOW() + interval '1 hours')::timestamp without time zone) > $2`,
		token, time.Now().UTC())
	var owner string
	if err := row.Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	// Bump last usage (best-effort; ignore error impact on the read path).
	_, _ = d.Pool.Exec(ctx, `UPDATE auth_tokens SET last_usage = now()::timestamp WHERE token = $1`, token)
	return owner, true, nil
}

// GetUserByRefreshToken returns the owner of a non-expired refresh token.
func (d *DB) GetUserByRefreshToken(ctx context.Context, token string) (string, bool, error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT owner FROM refresh_tokens WHERE refresh_token = $1 AND token_expiry > $2`,
		token, time.Now().UTC())
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
func (d *DB) CreateAccessTokens(ctx context.Context, td TokenData, expiryHours int64) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx,
		`INSERT INTO auth_tokens(owner, token, token_expiry) VALUES ($1, $2, NOW() + interval '30 minutes')`,
		td.Owner, td.Token); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO refresh_tokens(owner, refresh_token, token_expiry) VALUES ($1, $2, NOW() + interval '1' hour * $3)`,
		td.Owner, td.RefreshToken, expiryHours); err != nil {
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

// InsertAPIToken stores a base64(uuid) token with a null expiry (never expires).
func (d *DB) InsertAPIToken(ctx context.Context, owner, token string) error {
	_, err := d.Pool.Exec(ctx, `INSERT INTO auth_tokens(owner, token) VALUES ($1, $2)`, owner, token)
	return err
}

// DeleteTokens removes an auth token and refresh token, returning rows affected.
func (d *DB) DeleteTokens(ctx context.Context, token, refreshToken string) (int64, error) {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	t1, err := tx.Exec(ctx, `DELETE FROM auth_tokens WHERE token = $1`, token)
	if err != nil {
		return 0, err
	}
	t2, err := tx.Exec(ctx, `DELETE FROM refresh_tokens WHERE refresh_token = $1`, refreshToken)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return t1.RowsAffected() + t2.RowsAffected(), nil
}

// DeleteAuthToken deletes an API token by its value, scoped to its owner so a
// user can only revoke their own tokens.
func (d *DB) DeleteAuthToken(ctx context.Context, token, owner string) error {
	_, err := d.Pool.Exec(ctx, `DELETE FROM auth_tokens WHERE token = $1 AND owner = $2`, token, owner)
	return err
}

// ListApiTokens returns the non-expiring API tokens for a user.
func (d *DB) ListApiTokens(ctx context.Context, owner string) ([]model.StoredApiToken, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT token, last_usage::timestamptz, token_name, token_description
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

// UpdateTokenMetadata renames a token (POST /auth/token).
func (d *DB) UpdateTokenMetadata(ctx context.Context, owner string, m model.TokenMetadata) error {
	_, err := d.Pool.Exec(ctx,
		`UPDATE auth_tokens SET token_name = $3 WHERE token = $1 AND owner = $2`,
		m.TokenID, owner, m.TokenName)
	return err
}
