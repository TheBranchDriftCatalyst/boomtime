// Package hardcover is the SHARED Hardcover integration for the catalyst-books /
// catalyst-audiobooks domains — the PUSH side. It resolves boomtime reading
// items to a Hardcover book_id + edition_id (the match ladder) and mirrors
// reading state out via Hardcover's GraphQL write API.
//
// It lives at internal/hardcover (a sibling of internal/amazon / internal/github),
// NOT internal/domains/*, because it is shared plumbing: a self-contained
// external-API connector the ingestion domains import. See
// docs/design/catalyst-books-sync-architecture.md §3 and
// docs/design/book-tracking-research.md §3.5.
//
// TOKEN: Hardcover auth is a user-pasted bearer token (from account settings)
// that expires yearly + resets every Jan 1 — so a re-paste is a routine event.
// It is persisted ONLY encrypted (users.encrypted_hardcover_key; migration
// 00059 + internal/db/hardcover_token.go), registered in
// internal/domains/registry.go so key-rotation + backups cover it automatically.
// The plaintext is NEVER logged and NEVER returned by any API.
package hardcover

import (
	"context"
	"encoding/json"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// Store loads/saves the per-user Hardcover bearer token, sealing it under
// BOOM_ENCRYPTION_KEY via internal/auth.Encrypt/Decrypt. It is the only place
// the plaintext token is (briefly) in memory outside a live request.
type Store struct{ DB *db.DB }

// NewStore wires the token store to the DB.
func NewStore(database *db.DB) *Store { return &Store{DB: database} }

// Save encrypts + stores the bearer token for username, recording the given
// status. Empty token is a caller bug (use Clear to remove). The plaintext is
// wrapped as a JSON string so the ciphertext layout is uniform with the other
// domains' encrypted blobs.
func (s *Store) Save(ctx context.Context, username, token string, status db.HardcoverKeyStatus) error {
	blob, err := json.Marshal(token)
	if err != nil {
		return err
	}
	ct, err := auth.Encrypt(blob)
	if err != nil {
		return err
	}
	return s.DB.SetEncryptedHardcoverKey(ctx, username, ct, status)
}

// Load fetches + decrypts the bearer token for username, or ("", false, nil)
// when none is stored. INTERNAL use only — no HTTP path returns this.
func (s *Store) Load(ctx context.Context, username string) (string, bool, error) {
	ct, ok, err := s.DB.GetEncryptedHardcoverKey(ctx, username)
	if err != nil || !ok {
		return "", false, err
	}
	blob, err := auth.Decrypt(ct)
	if err != nil {
		return "", false, err
	}
	var token string
	if err := json.Unmarshal(blob, &token); err != nil {
		return "", false, err
	}
	return token, true, nil
}

// Info returns presence/status without decrypting (for the settings API).
func (s *Store) Info(ctx context.Context, username string) (db.HardcoverKeyInfo, error) {
	return s.DB.GetHardcoverKeyInfo(ctx, username)
}

// Clear removes the stored token (the "Disconnect Hardcover" action).
func (s *Store) Clear(ctx context.Context, username string) error {
	return s.DB.ClearEncryptedHardcoverKey(ctx, username)
}

// MarkInvalid flips the stored token's status to invalid (called after a
// Hardcover 401 during a push). No-op if the user has no stored token.
func (s *Store) MarkInvalid(ctx context.Context, username string) error {
	return s.DB.UpdateHardcoverKeyStatus(ctx, username, db.HardcoverKeyStatusInvalid)
}

// ClientForUser loads the user's token and returns a ready GraphQL client, or
// (nil, false, nil) when the user has not connected Hardcover.
func (s *Store) ClientForUser(ctx context.Context, username string) (*Client, bool, error) {
	token, ok, err := s.Load(ctx, username)
	if err != nil || !ok {
		return nil, false, err
	}
	return NewClient(token), true, nil
}
