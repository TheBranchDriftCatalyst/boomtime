// Package auth handles password hashing (Argon2id), API-token encoding, and
// refresh-token cookie construction/parsing. Ports PasswordUtils.hs + parts of Utils.hs.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

const (
	saltLen   = 64 // bytes (hashSaltLen in PasswordUtils.hs)
	keyLen    = 64 // bytes (hashOutputLen in PasswordUtils.hs)
	argonTime = 1
	argonMem  = 64 * 1024 // KiB
	argonPar  = 4
)

// HashPassword generates a 64-byte random salt and an Argon2id hash of the
// password. Both are stored in the bytea columns hashed_password / salt_used.
func HashPassword(password string) (hash, salt []byte, err error) {
	salt = make([]byte, saltLen)
	if _, err = rand.Read(salt); err != nil {
		return nil, nil, err
	}
	hash = argon2.IDKey([]byte(password), salt, argonTime, argonMem, argonPar, keyLen)
	return hash, salt, nil
}

// VerifyPassword recomputes the Argon2id hash with the stored salt and compares
// it in constant time (validatePassword in PasswordUtils.hs).
func VerifyPassword(password string, storedHash, storedSalt []byte) bool {
	computed := argon2.IDKey([]byte(password), storedSalt, argonTime, argonMem, argonPar, keyLen)
	return subtle.ConstantTimeCompare(computed, storedHash) == 1
}

// NewRawToken returns a random UUIDv4 string (the raw token handed to the user).
func NewRawToken() string {
	return uuid.NewString()
}

// ToBase64 base64-encodes a string (Utils.toBase64). Used to derive the stored
// token value from the raw UUID string.
func ToBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// ParseAuthHeader strips the "Basic " prefix from an Authorization header value.
// The remaining value is already base64(uuidString) — i.e. exactly what is stored
// in auth_tokens.token, so it is compared directly.
// Returns the token and true if the header was present and well-formed.
func ParseAuthHeader(header string) (string, bool) {
	if header == "" {
		return "", false
	}
	rest, ok := strings.CutPrefix(header, "Basic")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// ParseRefreshCookie extracts the refresh_token value from a raw Cookie header.
func ParseRefreshCookie(cookieHeader string) (string, bool) {
	for _, part := range strings.Split(cookieHeader, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && kv[0] == "refresh_token" {
			return kv[1], true
		}
	}
	return "", false
}
