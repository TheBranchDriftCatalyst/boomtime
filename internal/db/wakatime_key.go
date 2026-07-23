// wakatime_key.go: storage of the per-user encrypted Wakatime API key.
//
// The plaintext key never touches this layer. Callers are expected to
// encrypt with internal/auth.Encrypt before Set and decrypt with
// internal/auth.Decrypt after Get. See migrations/00023 for the ciphertext
// column, migrations/00024 for the status/checked_at columns, and
// internal/auth/crypto.go for the threat model + payload layout.
package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// WakatimeKeyStatus is the last-known validity of a saved key. Mirrors the
// text values written to users.wakatime_key_status; the FE treats unknown
// values conservatively as null (no dot color).
type WakatimeKeyStatus string

const (
	// WakatimeKeyStatusValid means the last probe (save-time /users/current
	// check, or a completed import run) succeeded against wakatime.com.
	WakatimeKeyStatusValid WakatimeKeyStatus = "valid"
	// WakatimeKeyStatusInvalid means the last probe was rejected (401/403).
	WakatimeKeyStatusInvalid WakatimeKeyStatus = "invalid"
	// WakatimeKeyStatusUnknown is reserved for future flows (save-without-
	// validate). Currently unused on the write path but the read path
	// handles it.
	WakatimeKeyStatusUnknown WakatimeKeyStatus = "unknown"
)

// WakatimeKeyInfo is the read-side aggregate returned by
// GetWakatimeKeyInfo. Blob is the raw ciphertext; the caller decides whether
// to decrypt (import path) or ignore (presence probe).
type WakatimeKeyInfo struct {
	HasSavedKey bool
	Blob        []byte
	Status      *string
	CheckedAt   *time.Time
}

// SetEncryptedWakatimeKey stores (or overwrites) the encrypted key blob for
// username AND records the just-verified status + checked_at wall-clock. The
// combined write keeps status coherent with the ciphertext: an operator
// staring at a row with `status='valid'` can trust that THIS blob was the
// one verified.
//
// Passing an empty ciphertext is a caller bug — the "clear" path is
// ClearEncryptedWakatimeKey. Returns an error if username does not exist so
// the caller can distinguish an FK violation from a successful no-op.
func (d *DB) SetEncryptedWakatimeKey(ctx context.Context, username string, ciphertext []byte, status WakatimeKeyStatus) error {
	if username == "" {
		return errors.New("SetEncryptedWakatimeKey: empty username")
	}
	if len(ciphertext) == 0 {
		return errors.New("SetEncryptedWakatimeKey: empty ciphertext (use ClearEncryptedWakatimeKey to remove)")
	}
	tag, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET encrypted_wakatime_key   = $2,
		        wakatime_key_status      = $3,
		        wakatime_key_checked_at  = now()
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

// UpdateWakatimeKeyStatus records the outcome of a later validity check (an
// import-time 401 detection, for example) WITHOUT touching the ciphertext.
// A no-op if the user has no saved key (a caller updating status for a user
// with no key is a bug, but we don't want to poison unrelated rows).
func (d *DB) UpdateWakatimeKeyStatus(ctx context.Context, username string, status WakatimeKeyStatus) error {
	if username == "" {
		return errors.New("UpdateWakatimeKeyStatus: empty username")
	}
	_, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET wakatime_key_status     = $2,
		        wakatime_key_checked_at = now()
		  WHERE username = $1
		    AND encrypted_wakatime_key IS NOT NULL`,
		username, string(status),
	)
	return err
}

// GetEncryptedWakatimeKey returns the encrypted blob for username. The
// second return value is false when the user has no saved key (NULL column)
// or the user does not exist. Callers that also want the status should use
// GetWakatimeKeyInfo.
func (d *DB) GetEncryptedWakatimeKey(ctx context.Context, username string) ([]byte, bool, error) {
	info, err := d.GetWakatimeKeyInfo(ctx, username)
	if err != nil || !info.HasSavedKey {
		return nil, false, err
	}
	return info.Blob, true, nil
}

// GetWakatimeKeyInfo returns the ciphertext + status + checked_at for the
// caller. Callers wanting only the presence probe (the API GET path) can
// ignore Blob to avoid an accidental decrypt.
func (d *DB) GetWakatimeKeyInfo(ctx context.Context, username string) (WakatimeKeyInfo, error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT encrypted_wakatime_key, wakatime_key_status, wakatime_key_checked_at
		   FROM users
		  WHERE username = $1`,
		username,
	)
	var blob []byte
	var status *string
	var checkedAt *time.Time
	if err := row.Scan(&blob, &status, &checkedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WakatimeKeyInfo{}, nil
		}
		return WakatimeKeyInfo{}, err
	}
	if len(blob) == 0 {
		return WakatimeKeyInfo{}, nil
	}
	return WakatimeKeyInfo{
		HasSavedKey: true,
		Blob:        blob,
		Status:      status,
		CheckedAt:   checkedAt,
	}, nil
}

// EncryptedWakatimeKeyRow is one row of the rotation-input set: the raw
// ciphertext for a user with a saved key. Returned by
// ListEncryptedWakatimeKeys; used by the rotate-encryption-key CLI to hold
// the "before" state while re-encrypting.
type EncryptedWakatimeKeyRow struct {
	Username   string
	Ciphertext []byte
}

// ListEncryptedWakatimeKeys returns every user with a non-null
// encrypted_wakatime_key. Ordered by username so operator-facing progress
// output is deterministic across runs. The rotate-encryption-key command
// reads this set, decrypts each blob under the OLD key, re-encrypts under
// the NEW key, and hands the results to RotateEncryptedWakatimeKeys for a
// single-transaction write.
func (d *DB) ListEncryptedWakatimeKeys(ctx context.Context) ([]EncryptedWakatimeKeyRow, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT username, encrypted_wakatime_key
		   FROM users
		  WHERE encrypted_wakatime_key IS NOT NULL
		  ORDER BY username`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EncryptedWakatimeKeyRow
	for rows.Next() {
		var r EncryptedWakatimeKeyRow
		if err := rows.Scan(&r.Username, &r.Ciphertext); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RotateEncryptedWakatimeKeys writes the caller's already-re-encrypted
// ciphertexts back in a single transaction. Either every row updates or none
// do — an operator interrupted halfway through can safely re-run without
// worrying about a mixed-key population. Status columns are deliberately
// untouched: the plaintext is the same, so the last-known validity is still
// meaningful. Returns the count of rows updated.
//
// Callers MUST NOT pass an empty ciphertext (that would nuke the key column
// silently); guard upstream.
func (d *DB) RotateEncryptedWakatimeKeys(ctx context.Context, rows []EncryptedWakatimeKeyRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

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

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return updated, nil
}

// ClearEncryptedWakatimeKey nulls the ciphertext AND the status/checked_at
// columns so a subsequent presence probe cannot mistake stale metadata for
// current truth. Idempotent: clearing an already-null row is a no-op.
func (d *DB) ClearEncryptedWakatimeKey(ctx context.Context, username string) error {
	if username == "" {
		return errors.New("ClearEncryptedWakatimeKey: empty username")
	}
	_, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET encrypted_wakatime_key   = NULL,
		        wakatime_key_status      = NULL,
		        wakatime_key_checked_at  = NULL
		  WHERE username = $1`,
		username,
	)
	return err
}
