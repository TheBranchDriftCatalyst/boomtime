// hardcover_token.go: storage of the per-user encrypted Hardcover API bearer
// token (catalyst-books push target). Modeled 1:1 on github_token.go /
// amazon_device.go.
//
// Hardcover is the SYNC TARGET — boomtime mirrors reading state out to it. The
// plaintext token never touches this layer: callers encrypt with
// internal/auth.Encrypt before Set and decrypt with internal/auth.Decrypt after
// GetEncryptedHardcoverKey. See migrations/00059 for the columns and
// internal/auth/crypto.go for the threat model. The column is registered in
// internal/domains/registry.go so rotate-encryption-key re-encrypts it and the
// DB backup includes it — automatically, by construction.
//
// SECURITY: GetHardcoverKeyInfo is the presence/status probe and deliberately
// does NOT return the ciphertext. Only GetEncryptedHardcoverKey (internal
// fetch, for the hardcover-sync push job) returns the blob.
package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// HardcoverKeyStatus is the last-known validity of a stored Hardcover token.
type HardcoverKeyStatus string

const (
	// HardcoverKeyStatusValid means the connect-time me{} probe succeeded.
	HardcoverKeyStatusValid HardcoverKeyStatus = "valid"
	// HardcoverKeyStatusInvalid means Hardcover rejected the token (401). Set by
	// a later push probe; connect never stores an invalid token. The Jan-1
	// reset makes this a routine "please re-paste" event.
	HardcoverKeyStatusInvalid HardcoverKeyStatus = "invalid"
	// HardcoverKeyStatusUnknown is reserved for a save-without-validate path.
	HardcoverKeyStatusUnknown HardcoverKeyStatus = "unknown"
)

// HardcoverKeyInfo is the read-side aggregate returned by GetHardcoverKeyInfo.
// It NEVER carries the ciphertext.
type HardcoverKeyInfo struct {
	Connected bool
	Status    *string
	CheckedAt *time.Time
}

// SetEncryptedHardcoverKey stores (or overwrites) the encrypted token blob for
// username and records the just-verified status + checked_at wall-clock. Empty
// ciphertext is a caller bug — the clear path is ClearEncryptedHardcoverKey.
// Returns pgx.ErrNoRows if username does not exist.
func (d *DB) SetEncryptedHardcoverKey(ctx context.Context, username string, ciphertext []byte, status HardcoverKeyStatus) error {
	if username == "" {
		return errors.New("SetEncryptedHardcoverKey: empty username")
	}
	if len(ciphertext) == 0 {
		return errors.New("SetEncryptedHardcoverKey: empty ciphertext (use ClearEncryptedHardcoverKey to remove)")
	}
	tag, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET encrypted_hardcover_key  = $2,
		        hardcover_key_status     = $3,
		        hardcover_key_checked_at = now()
		  WHERE username = $1`,
		username, ciphertext, string(status),
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// UpdateHardcoverKeyStatus records the outcome of a later validity check (a
// hardcover.app 401 during a push, for example) WITHOUT touching the
// ciphertext. No-op if the user has no stored token.
func (d *DB) UpdateHardcoverKeyStatus(ctx context.Context, username string, status HardcoverKeyStatus) error {
	if username == "" {
		return errors.New("UpdateHardcoverKeyStatus: empty username")
	}
	_, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET hardcover_key_status     = $2,
		        hardcover_key_checked_at = now()
		  WHERE username = $1
		    AND encrypted_hardcover_key IS NOT NULL`,
		username, string(status),
	)
	return err
}

// GetHardcoverKeyInfo returns connection state + status + checked_at for
// username. NEVER returns the ciphertext. Connected is false when the user has
// no stored token or does not exist.
func (d *DB) GetHardcoverKeyInfo(ctx context.Context, username string) (HardcoverKeyInfo, error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT (encrypted_hardcover_key IS NOT NULL) AS connected,
		        hardcover_key_status, hardcover_key_checked_at
		   FROM users
		  WHERE username = $1`,
		username,
	)
	var connected bool
	var status *string
	var checkedAt *time.Time
	if err := row.Scan(&connected, &status, &checkedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return HardcoverKeyInfo{}, nil
		}
		return HardcoverKeyInfo{}, err
	}
	if !connected {
		return HardcoverKeyInfo{}, nil
	}
	return HardcoverKeyInfo{Connected: true, Status: status, CheckedAt: checkedAt}, nil
}

// GetEncryptedHardcoverKey returns the raw ciphertext for username (internal
// fetch for the hardcover-sync push job). The second return is false when the
// user has no stored token (NULL column) or does not exist.
func (d *DB) GetEncryptedHardcoverKey(ctx context.Context, username string) ([]byte, bool, error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT encrypted_hardcover_key FROM users WHERE username = $1`,
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

// ClearEncryptedHardcoverKey nulls the ciphertext AND its status/checked_at
// siblings so a subsequent presence probe cannot mistake stale metadata for
// current truth. Idempotent: clearing an already-null row is a no-op.
func (d *DB) ClearEncryptedHardcoverKey(ctx context.Context, username string) error {
	if username == "" {
		return errors.New("ClearEncryptedHardcoverKey: empty username")
	}
	_, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET encrypted_hardcover_key  = NULL,
		        hardcover_key_status     = NULL,
		        hardcover_key_checked_at = NULL
		  WHERE username = $1`,
		username,
	)
	return err
}

// ListUsersWithHardcoverKey returns the usernames of every user with a stored
// Hardcover token that isn't known-invalid — the working set for the periodic
// hardcover-sync push job. Known-invalid tokens are skipped so the scheduler
// doesn't hammer Hardcover with credentials it already knows are dead; a user
// re-pasting flips the status back and re-enters the set.
func (d *DB) ListUsersWithHardcoverKey(ctx context.Context) ([]string, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT username FROM users
		  WHERE encrypted_hardcover_key IS NOT NULL
		    AND hardcover_key_status <> 'invalid'
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
