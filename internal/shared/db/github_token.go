// github_token.go: storage of the per-user encrypted GitHub OAuth access token
// (gaka-2ip Phase 1). Modeled 1:1 on wakatime_key.go.
//
// The plaintext token never touches this layer. Callers are expected to
// encrypt with internal/auth.Encrypt before Set and decrypt with
// internal/auth.Decrypt after GetEncryptedGithubToken. See migrations/00048 for
// the columns and internal/auth/crypto.go for the threat model + payload
// layout.
//
// SECURITY: GetGithubTokenInfo is the presence/status probe and deliberately
// does NOT return the ciphertext — the status API path uses it so an accidental
// decrypt is impossible. Only GetEncryptedGithubToken (internal fetch, for a
// future stats-pull worker) returns the blob.
package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// GithubTokenStatus is the last-known validity of a stored GitHub token.
// Mirrors the text written to users.github_token_status; unknown values are
// passed through and treated conservatively by the FE.
type GithubTokenStatus string

const (
	// GithubTokenStatusValid means the connect-time GET /user probe succeeded.
	GithubTokenStatusValid GithubTokenStatus = "valid"
	// GithubTokenStatusInvalid means GitHub rejected the token (401/403). Set
	// by a later validity check; connect never stores an invalid token.
	GithubTokenStatusInvalid GithubTokenStatus = "invalid"
	// GithubTokenStatusUnknown is reserved for a save-without-validate path.
	GithubTokenStatusUnknown GithubTokenStatus = "unknown"
)

// GithubTokenInfo is the read-side aggregate returned by GetGithubTokenInfo.
// It NEVER carries the ciphertext — the status/disconnect API reads this so a
// decrypt can't accidentally happen on the presence path.
type GithubTokenInfo struct {
	Connected bool
	Login     *string
	Status    *string
	CheckedAt *time.Time
}

// SetEncryptedGithubToken stores (or overwrites) the encrypted token blob for
// username AND records the just-verified status + login + checked_at
// wall-clock. The combined write keeps status coherent with the ciphertext.
//
// Passing an empty ciphertext is a caller bug — the "clear" path is
// ClearEncryptedGithubToken. Returns pgx.ErrNoRows if username does not exist.
func (d *DB) SetEncryptedGithubToken(ctx context.Context, username string, ciphertext []byte, login string, status GithubTokenStatus) error {
	if username == "" {
		return errors.New("SetEncryptedGithubToken: empty username")
	}
	if len(ciphertext) == 0 {
		return errors.New("SetEncryptedGithubToken: empty ciphertext (use ClearEncryptedGithubToken to remove)")
	}
	tag, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET encrypted_github_token  = $2,
		        github_login            = $3,
		        github_token_status     = $4,
		        github_token_checked_at = now()
		  WHERE username = $1`,
		username, ciphertext, login, string(status),
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// UpdateGithubTokenStatus records the outcome of a later validity check (an
// api.github.com 401 during a stats pull, for example) WITHOUT touching the
// ciphertext or login. A no-op if the user has no stored token (a caller
// updating status for a user with no token is a bug, but we don't poison
// unrelated rows).
func (d *DB) UpdateGithubTokenStatus(ctx context.Context, username string, status GithubTokenStatus) error {
	if username == "" {
		return errors.New("UpdateGithubTokenStatus: empty username")
	}
	_, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET github_token_status     = $2,
		        github_token_checked_at = now()
		  WHERE username = $1
		    AND encrypted_github_token IS NOT NULL`,
		username, string(status),
	)
	return err
}

// GetGithubTokenInfo returns the connection state + login + status + checked_at
// for username. It NEVER returns the ciphertext — the status/disconnect API
// path calls this so the token can't leak. Connected is false when the user has
// no stored token (NULL column) or does not exist.
func (d *DB) GetGithubTokenInfo(ctx context.Context, username string) (GithubTokenInfo, error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT (encrypted_github_token IS NOT NULL) AS connected,
		        github_login, github_token_status, github_token_checked_at
		   FROM users
		  WHERE username = $1`,
		username,
	)
	var connected bool
	var login, status *string
	var checkedAt *time.Time
	if err := row.Scan(&connected, &login, &status, &checkedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GithubTokenInfo{}, nil
		}
		return GithubTokenInfo{}, err
	}
	if !connected {
		return GithubTokenInfo{}, nil
	}
	return GithubTokenInfo{
		Connected: true,
		Login:     login,
		Status:    status,
		CheckedAt: checkedAt,
	}, nil
}

// GetEncryptedGithubToken returns the encrypted blob for username. The second
// return value is false when the user has no stored token (NULL column) or the
// user does not exist. INTERNAL use only (a future stats-pull worker decrypts
// with auth.Decrypt) — no HTTP path returns this.
func (d *DB) GetEncryptedGithubToken(ctx context.Context, username string) ([]byte, bool, error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT encrypted_github_token FROM users WHERE username = $1`,
		username,
	)
	var blob []byte
	if err := row.Scan(&blob); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(blob) == 0 {
		return nil, false, nil
	}
	return blob, true, nil
}

// ListUsersWithGithubToken returns the usernames of every user with a stored
// GitHub token that isn't known-invalid — the working set for the periodic
// github-stats refresh job (gaka-hney.1). Known-invalid tokens are skipped so
// the scheduler doesn't hammer GitHub with credentials it already knows are
// dead; a user re-connecting flips the status back and re-enters the set.
func (d *DB) ListUsersWithGithubToken(ctx context.Context) ([]string, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT username FROM users
		  WHERE encrypted_github_token IS NOT NULL
		    AND github_token_status <> 'invalid'
		  ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ClearEncryptedGithubToken nulls the ciphertext AND the login/status/checked_at
// columns so a subsequent presence probe cannot mistake stale metadata for
// current truth. Idempotent: clearing an already-null row is a no-op.
func (d *DB) ClearEncryptedGithubToken(ctx context.Context, username string) error {
	if username == "" {
		return errors.New("ClearEncryptedGithubToken: empty username")
	}
	_, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET encrypted_github_token  = NULL,
		        github_login            = NULL,
		        github_token_status     = NULL,
		        github_token_checked_at = NULL
		  WHERE username = $1`,
		username,
	)
	return err
}

// EncryptedGithubTokenRow is one row of the rotation-input set: the raw
// ciphertext for a user with a stored token. Returned by
// ListEncryptedGithubTokens; used by rotate-encryption-key to hold the "before"
// state while re-encrypting.
type EncryptedGithubTokenRow struct {
	Username   string
	Ciphertext []byte
}

// ListEncryptedGithubTokens returns every user with a non-null
// encrypted_github_token. Ordered by username so rotation progress output is
// deterministic.
func (d *DB) ListEncryptedGithubTokens(ctx context.Context) ([]EncryptedGithubTokenRow, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT username, encrypted_github_token
		   FROM users
		  WHERE encrypted_github_token IS NOT NULL
		  ORDER BY username`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EncryptedGithubTokenRow
	for rows.Next() {
		var r EncryptedGithubTokenRow
		if err := rows.Scan(&r.Username, &r.Ciphertext); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RotateEncryptedGithubTokens writes the caller's already-re-encrypted
// ciphertexts back in a single transaction. Either every row updates or none
// do. Status/login columns are deliberately untouched (the plaintext is the
// same). Returns the count of rows updated.
//
// Callers MUST NOT pass an empty ciphertext; guard upstream. This standalone
// method exists for unit testing — production rotation goes through
// RotateEncryptedSecrets so BOTH encrypted columns commit atomically together.
func (d *DB) RotateEncryptedGithubTokens(ctx context.Context, rows []EncryptedGithubTokenRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	updated, err := rotateGithubRowsTx(ctx, tx, rows)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return updated, nil
}

// rotateGithubRowsTx applies the github-token re-encryption UPDATEs on an
// already-open transaction. Shared by RotateEncryptedGithubTokens (standalone
// tx) and RotateEncryptedSecrets (combined tx with the wakatime rotation).
func rotateGithubRowsTx(ctx context.Context, tx pgx.Tx, rows []EncryptedGithubTokenRow) (int, error) {
	var updated int
	for _, r := range rows {
		if r.Username == "" {
			return 0, errors.New("RotateEncryptedGithubTokens: empty username in input")
		}
		if len(r.Ciphertext) == 0 {
			return 0, errors.New("RotateEncryptedGithubTokens: empty ciphertext in input")
		}
		tag, err := tx.Exec(ctx,
			`UPDATE users
			    SET encrypted_github_token = $2
			  WHERE username = $1
			    AND encrypted_github_token IS NOT NULL`,
			r.Username, r.Ciphertext,
		)
		if err != nil {
			return 0, err
		}
		updated += int(tag.RowsAffected())
	}
	return updated, nil
}

// rotateWakatimeRowsTx applies the wakatime-key re-encryption UPDATEs on an
// already-open transaction. Shared by RotateEncryptedWakatimeKeys (standalone
// tx) and RotateEncryptedSecrets (combined tx). Extracted so the combined
// rotation reuses the exact same per-row guards.
func rotateWakatimeRowsTx(ctx context.Context, tx pgx.Tx, rows []EncryptedWakatimeKeyRow) (int, error) {
	var updated int
	for _, r := range rows {
		if r.Username == "" {
			return 0, errors.New("RotateEncryptedWakatimeKeys: empty username in input")
		}
		if len(r.Ciphertext) == 0 {
			return 0, errors.New("RotateEncryptedWakatimeKeys: empty ciphertext in input")
		}
		tag, err := tx.Exec(ctx,
			`UPDATE users
			    SET encrypted_wakatime_key = $2
			  WHERE username = $1
			    AND encrypted_wakatime_key IS NOT NULL`,
			r.Username, r.Ciphertext,
		)
		if err != nil {
			return 0, err
		}
		updated += int(tag.RowsAffected())
	}
	return updated, nil
}

// RotateEncryptedSecrets re-encrypts BOTH the wakatime keys and github tokens
// in a SINGLE transaction (gaka-2ip Phase 1). This is what
// `boomtime rotate-encryption-key` calls so an interrupted rotation can never
// leave one encrypted column on the new key and the other on the old — either
// every ciphertext across both columns is rewritten or none is. Returns the
// per-column update counts.
func (d *DB) RotateEncryptedSecrets(ctx context.Context, wakatime []EncryptedWakatimeKeyRow, github []EncryptedGithubTokenRow) (wakatimeUpdated, githubUpdated int, err error) {
	if len(wakatime) == 0 && len(github) == 0 {
		return 0, 0, nil
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	wakatimeUpdated, err = rotateWakatimeRowsTx(ctx, tx, wakatime)
	if err != nil {
		return 0, 0, err
	}
	githubUpdated, err = rotateGithubRowsTx(ctx, tx, github)
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return wakatimeUpdated, githubUpdated, nil
}
