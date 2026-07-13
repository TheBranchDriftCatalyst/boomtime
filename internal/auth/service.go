// service.go: composed account-lifecycle operations that BOTH the CLI
// (cmd/boomtime create-user / create-token) and the HTTP handler
// (Register / CreateAPIToken) share (gaka-0tb). The pure auth primitives
// (HashPassword, VerifyPassword, NewRawToken, ToBase64) still live in
// auth.go; this file glues them to the db package so callers don't
// re-implement the hash+insert / verify+mint dance and drift over time.
package auth

import (
	"context"
	"errors"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// ErrUserExists is returned by CreateUser when the username is taken.
var ErrUserExists = errors.New("user already exists")

// ErrInvalidCredentials is returned by VerifyUserCredentials for an unknown
// user or a wrong password. Same message for both so callers can't probe
// for username existence via error differentiation.
var ErrInvalidCredentials = errors.New("wrong username or password")

// CreateUser hashes the password (Argon2id + random salt) and inserts a fresh
// users row. Returns ErrUserExists when the row already exists; any other
// error is a real infra failure.
func CreateUser(ctx context.Context, database *db.DB, username, password string) error {
	hash, salt, err := HashPassword(password)
	if err != nil {
		return err
	}
	created, err := database.InsertUser(ctx, db.StoredUser{
		Username: username, HashedPassword: hash, SaltUsed: salt,
	})
	if err != nil {
		return err
	}
	if !created {
		return ErrUserExists
	}
	return nil
}

// CreateAPIToken mints a fresh raw token, base64-encodes it for storage, and
// inserts an auth_tokens row. Returns the RAW token (the caller shows it to
// the user; the DB only ever keeps the encoded form).
func CreateAPIToken(ctx context.Context, database *db.DB, username string) (string, error) {
	raw := NewRawToken()
	if err := database.InsertAPIToken(ctx, username, ToBase64(raw)); err != nil {
		return "", err
	}
	return raw, nil
}

// VerifyUserCredentials looks up the user and checks the password against the
// stored Argon2id hash. Returns ErrInvalidCredentials for both unknown user
// and wrong password so callers can't distinguish (username-enumeration
// defense).
func VerifyUserCredentials(ctx context.Context, database *db.DB, username, password string) error {
	user, err := database.GetUserByName(ctx, username)
	if err != nil {
		return err
	}
	if user == nil || !VerifyPassword(password, user.HashedPassword, user.SaltUsed) {
		return ErrInvalidCredentials
	}
	return nil
}
