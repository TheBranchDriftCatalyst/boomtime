// Package amazon is the SHARED Amazon integration for the catalyst-books
// (Kindle) and catalyst-audiobooks (Audible) domains. Audible AND Kindle
// authenticate off ONE Amazon device registration (adp_token + RSA private key
// + refresh token), so this package owns that credential end-to-end:
//
//   - the device-registration flow ("Connect Amazon" — see register.go)
//   - encrypted-at-rest storage (users.encrypted_amazon_device;
//     migrations/00057 + internal/db/amazon_device.go), registered in
//     internal/domains so key-rotation + backups cover it automatically
//   - X-ADP-Request-Digest request signing both domains reuse (see signing.go)
//
// It lives at internal/amazon, NOT internal/domains/*, because it is shared
// plumbing — the two ingestion domains import it. See
// docs/design/catalyst-domains-spike.md and docs/design/book-tracking-research.md.
package amazon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// Marketplace is an Amazon locale. Registering/querying against the wrong one
// returns an empty/foreign library, so it is captured at registration time.
type Marketplace string

const (
	MarketplaceUS Marketplace = "us"
	MarketplaceUK Marketplace = "uk"
	MarketplaceDE Marketplace = "de"
	MarketplaceCA Marketplace = "ca"
	MarketplaceAU Marketplace = "au"
)

// DeviceCredential is the durable Amazon device auth captured at registration
// and reused to sign every Audible/Kindle request. It is persisted ONLY
// encrypted (never logged) — see internal/auth/crypto.go for the threat model.
type DeviceCredential struct {
	AdpToken         string      `json:"adp_token"`
	DevicePrivateKey string      `json:"device_private_key"` // PKCS8 PEM RSA private key
	RefreshToken     string      `json:"refresh_token"`
	Marketplace      Marketplace `json:"marketplace"`
	DeviceSerial     string      `json:"device_serial"`
	CustomerID       string      `json:"customer_id,omitempty"` // numeric user_id for whispersync
	RegisteredAt     time.Time   `json:"registered_at"`
}

// ErrNotRegistered is returned when no device credential exists for a user.
var ErrNotRegistered = errors.New("amazon: no device credential registered for user")

// Store persists the shared Amazon device credential, sealing it under
// BOOM_ENCRYPTION_KEY via internal/auth.Encrypt/Decrypt.
type Store struct{ DB *db.DB }

// NewStore wires the credential store to the DB.
func NewStore(database *db.DB) *Store { return &Store{DB: database} }

// Save marshals + encrypts + stores the device credential for username, marking
// the device status valid.
func (s *Store) Save(ctx context.Context, username string, cred DeviceCredential) error {
	blob, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	ct, err := auth.Encrypt(blob)
	if err != nil {
		return err
	}
	return s.DB.SetEncryptedAmazonDevice(ctx, username, ct, db.AmazonDeviceStatusValid)
}

// Load fetches + decrypts the device credential for username, or
// (nil, ErrNotRegistered) when none is stored.
func (s *Store) Load(ctx context.Context, username string) (*DeviceCredential, error) {
	ct, err := s.DB.GetEncryptedAmazonDevice(ctx, username)
	if err != nil {
		return nil, err
	}
	if ct == nil {
		return nil, ErrNotRegistered
	}
	blob, err := auth.Decrypt(ct)
	if err != nil {
		return nil, err
	}
	var cred DeviceCredential
	if err := json.Unmarshal(blob, &cred); err != nil {
		return nil, err
	}
	return &cred, nil
}

// Info returns presence/status without decrypting (for the settings API).
func (s *Store) Info(ctx context.Context, username string) (db.AmazonDeviceInfo, error) {
	return s.DB.GetAmazonDeviceInfo(ctx, username)
}

// Disconnect clears the stored credential (the "Disconnect Amazon" action).
func (s *Store) Disconnect(ctx context.Context, username string) error {
	return s.DB.ClearEncryptedAmazonDevice(ctx, username)
}
