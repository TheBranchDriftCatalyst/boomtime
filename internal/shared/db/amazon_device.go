// amazon_device.go: storage of the per-user encrypted Amazon device credential
// (catalyst-books + catalyst-audiobooks). Modeled 1:1 on github_token.go /
// wakatime_key.go.
//
// ONE Amazon device registration authenticates BOTH Audible and Kindle
// (adp_token + RSA private key + refresh token). The plaintext blob never
// touches this layer — callers encrypt with internal/auth.Encrypt before Set
// and decrypt with internal/auth.Decrypt after GetEncryptedAmazonDevice. See
// migrations/00057 for the columns and internal/auth/crypto.go for the threat
// model. The column is registered in internal/domains/registry.go so
// rotate-encryption-key re-encrypts it and the DB backup includes it.
//
// SECURITY: GetAmazonDeviceInfo is the presence/status probe and deliberately
// does NOT return the ciphertext. Only GetEncryptedAmazonDevice (internal
// fetch, for the ingest workers) returns the blob.
package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// AmazonDeviceStatus is the last-known validity of a stored Amazon device auth.
type AmazonDeviceStatus string

const (
	// AmazonDeviceStatusValid means the last Amazon API call under this device
	// credential succeeded.
	AmazonDeviceStatusValid AmazonDeviceStatus = "valid"
	// AmazonDeviceStatusInvalid means Amazon rejected the credential (auth
	// error / deregistered device). Set by a later ingest probe.
	AmazonDeviceStatusInvalid AmazonDeviceStatus = "invalid"
	// AmazonDeviceStatusUnknown is reserved for a save-without-validate path.
	AmazonDeviceStatusUnknown AmazonDeviceStatus = "unknown"
)

// AmazonDeviceInfo is the read-side aggregate returned by GetAmazonDeviceInfo.
// It NEVER carries the ciphertext.
type AmazonDeviceInfo struct {
	Connected bool
	Status    *string
	CheckedAt *time.Time
}

// SetEncryptedAmazonDevice stores (or overwrites) the encrypted device blob for
// username and records the status + checked_at wall-clock. Empty ciphertext is
// a caller bug — the clear path is ClearEncryptedAmazonDevice. Returns
// pgx.ErrNoRows if username does not exist.
func (d *DB) SetEncryptedAmazonDevice(ctx context.Context, username string, ciphertext []byte, status AmazonDeviceStatus) error {
	if username == "" {
		return errors.New("SetEncryptedAmazonDevice: empty username")
	}
	if len(ciphertext) == 0 {
		return errors.New("SetEncryptedAmazonDevice: empty ciphertext (use ClearEncryptedAmazonDevice to remove)")
	}
	tag, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET encrypted_amazon_device  = $2,
		        amazon_device_status     = $3,
		        amazon_device_checked_at = now()
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

// UpdateAmazonDeviceStatus records the outcome of a later validity check WITHOUT
// touching the ciphertext. No-op if the user has no stored credential.
func (d *DB) UpdateAmazonDeviceStatus(ctx context.Context, username string, status AmazonDeviceStatus) error {
	if username == "" {
		return errors.New("UpdateAmazonDeviceStatus: empty username")
	}
	_, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET amazon_device_status     = $2,
		        amazon_device_checked_at = now()
		  WHERE username = $1
		    AND encrypted_amazon_device IS NOT NULL`,
		username, string(status),
	)
	return err
}

// GetAmazonDeviceInfo returns connection state + status + checked_at for
// username. NEVER returns the ciphertext. Connected is false when the user has
// no stored credential or does not exist.
func (d *DB) GetAmazonDeviceInfo(ctx context.Context, username string) (AmazonDeviceInfo, error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT (encrypted_amazon_device IS NOT NULL) AS connected,
		        amazon_device_status, amazon_device_checked_at
		   FROM users
		  WHERE username = $1`,
		username,
	)
	var connected bool
	var status *string
	var checkedAt *time.Time
	if err := row.Scan(&connected, &status, &checkedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AmazonDeviceInfo{}, nil
		}
		return AmazonDeviceInfo{}, err
	}
	return AmazonDeviceInfo{Connected: connected, Status: status, CheckedAt: checkedAt}, nil
}

// GetEncryptedAmazonDevice returns the raw ciphertext for username (internal
// fetch for the ingest workers), or (nil, nil) if none is stored.
func (d *DB) GetEncryptedAmazonDevice(ctx context.Context, username string) ([]byte, error) {
	var ct []byte
	err := d.Pool.QueryRow(ctx,
		`SELECT encrypted_amazon_device FROM users WHERE username = $1`, username).Scan(&ct)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return ct, nil
}

// ClearEncryptedAmazonDevice removes the stored credential + status for username
// (the "Disconnect Amazon" path).
func (d *DB) ClearEncryptedAmazonDevice(ctx context.Context, username string) error {
	_, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET encrypted_amazon_device  = NULL,
		        amazon_device_status     = NULL,
		        amazon_device_checked_at = NULL
		  WHERE username = $1`,
		username,
	)
	return err
}

// ListUsersWithAmazonDevice returns every username with a stored Amazon device
// credential — the fan-out set for the Audible/Kindle sync jobs.
func (d *DB) ListUsersWithAmazonDevice(ctx context.Context) ([]string, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT username FROM users WHERE encrypted_amazon_device IS NOT NULL ORDER BY username`)
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
