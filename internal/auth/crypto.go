// Package auth — crypto.go: symmetric encryption at rest for user secrets
// (currently: the imported Wakatime API key stored on users.encrypted_wakatime_key).
//
// THREAT MODEL
//
//	Attacker A) Full database read (SQL injection, stolen pg_dump, leaked
//	   backup, compromised replica). Without BOOM_ENCRYPTION_KEY the ciphertext
//	   is opaque: AES-256-GCM's confidentiality guarantees hold and the attacker
//	   cannot recover the plaintext Wakatime key. Every row uses a fresh random
//	   12-byte nonce so identical plaintexts do NOT produce identical
//	   ciphertexts (no leakage across users).
//
//	Attacker B) Tampering with the ciphertext (row rewrite in the DB). GCM's
//	   16-byte authentication tag detects any bit flip and Decrypt returns an
//	   error — the plaintext is never surfaced from a tampered blob.
//
//	Attacker C) Full disk / process memory access on the server. Out of scope:
//	   BOOM_ENCRYPTION_KEY lives in the process env, so anyone who can read
//	   /proc/<pid>/environ or attach a debugger already has the plaintext. This
//	   layer is defense-in-depth for at-rest DB compromise, not memory
//	   exfiltration.
//
// KEY MANAGEMENT
//
//	BOOM_ENCRYPTION_KEY is a base64-encoded 32-byte (256-bit) symmetric key.
//	Load on first use (LoadKeyFromEnv is idempotent + thread-safe via
//	sync.Once). Startup logs a WARNING when the env is unset but does NOT
//	fail — the feature simply stays inert (Encrypt/Decrypt error out) until an
//	operator configures the env and restarts. This trades a slightly quieter
//	failure mode for a working `docker compose up` on dev machines that never
//	touch the wakatime-key save path.
//
//	Key rotation is out of scope for the initial ship (see follow-up bead):
//	rotating the env would strand every existing ciphertext (Decrypt fails
//	auth). A future version can add a key-id byte to the payload prefix and a
//	map of keys, or a re-encrypt-on-decrypt path.
//
// PAYLOAD LAYOUT
//
//	[ 12-byte nonce | ciphertext | 16-byte GCM tag ]
//
//	The tag is appended by the crypto/cipher AEAD.Seal implementation, so the
//	stored blob is exactly nonce || Seal(nonce, plaintext). Decrypt splits the
//	first 12 bytes as the nonce and hands the rest to AEAD.Open. A blob under
//	13 bytes (nonce + at least one tagged byte) is rejected as malformed.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// EncryptionKeyEnv is the env var name used to source the symmetric key.
const EncryptionKeyEnv = "BOOM_ENCRYPTION_KEY"

// aesKeySize is the required raw-key length (bytes). 32 = AES-256.
const aesKeySize = 32

// gcmNonceSize is the standard 12-byte nonce for AES-GCM. Storing longer
// nonces is a footgun (AEAD.NonceSize() must match).
const gcmNonceSize = 12

// Errors returned by the crypto helpers. All are safe to bubble up to logs
// (they never carry key material or plaintext).
var (
	// ErrKeyUnset is returned by Encrypt/Decrypt when BOOM_ENCRYPTION_KEY has
	// not been configured. Handlers should surface this as a 5xx-ish "server
	// misconfigured" message, never leak it to the caller as a stack trace.
	ErrKeyUnset = errors.New("BOOM_ENCRYPTION_KEY is not set — encrypted-at-rest features are disabled")

	// ErrKeyInvalid is returned when BOOM_ENCRYPTION_KEY is set but fails to
	// base64-decode to exactly 32 bytes. This is a config error the operator
	// must fix; the process still starts (see package doc) but any call to
	// Encrypt/Decrypt returns this error.
	ErrKeyInvalid = errors.New("BOOM_ENCRYPTION_KEY must be base64-encoded exactly 32 bytes (AES-256)")

	// ErrMalformedCiphertext is returned when the stored blob is too short to
	// contain a nonce + tag. Distinguishing this from a GCM auth failure lets
	// the caller tell "someone truncated the row" from "the ciphertext was
	// tampered with".
	ErrMalformedCiphertext = errors.New("ciphertext is malformed (too short)")
)

// encryptionState holds the lazily-loaded AES-GCM AEAD. The zero value is
// ready to use; the load happens in LoadKeyFromEnv (idempotent + safe under
// concurrent access via sync.Once).
type encryptionState struct {
	once   sync.Once
	aead   cipher.AEAD
	loaded bool // true iff aead is non-nil after the once.Do
	err    error
}

// pkg-level singleton; the whole process shares one key.
var encState = &encryptionState{}

// LoadKeyFromEnv reads BOOM_ENCRYPTION_KEY, base64-decodes it, verifies the
// length, and constructs the AES-GCM AEAD. Idempotent + thread-safe: repeated
// calls (including from concurrent goroutines) all reach the same terminal
// state.
//
// Returns nil on success, ErrKeyUnset when the env is empty, or ErrKeyInvalid
// when the base64 or length is wrong. The internal state is memoized so
// subsequent Encrypt/Decrypt calls do not re-parse the env.
func LoadKeyFromEnv() error {
	encState.once.Do(func() {
		raw := os.Getenv(EncryptionKeyEnv)
		if raw == "" {
			encState.err = ErrKeyUnset
			return
		}
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			encState.err = fmt.Errorf("%w: base64 decode failed", ErrKeyInvalid)
			return
		}
		if len(key) != aesKeySize {
			encState.err = fmt.Errorf("%w: got %d bytes, want %d", ErrKeyInvalid, len(key), aesKeySize)
			return
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			encState.err = fmt.Errorf("aes.NewCipher: %w", err)
			return
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			encState.err = fmt.Errorf("cipher.NewGCM: %w", err)
			return
		}
		encState.aead = aead
		encState.loaded = true
	})
	return encState.err
}

// IsEncryptionKeyConfigured reports whether the env is set + valid. Safe to
// call at startup for a heads-up WARNING log. Does NOT surface an error and
// does NOT panic on missing/invalid keys — the "hard fail" happens lazily on
// first Encrypt/Decrypt so dev flows without a key still boot.
func IsEncryptionKeyConfigured() bool {
	return LoadKeyFromEnv() == nil
}

// resetEncryptionStateForTest is a test-only hook to re-parse the env after a
// t.Setenv. NOT exported outside the package; call from *_test.go only.
func resetEncryptionStateForTest() {
	encState = &encryptionState{}
}

// ResetForTest is the exported cross-package variant of
// resetEncryptionStateForTest — used by importer / handler tests that install
// a deterministic BOOM_ENCRYPTION_KEY via t.Setenv and need to bust the
// sync.Once cache. Do NOT call from non-test code.
func ResetForTest() {
	resetEncryptionStateForTest()
}

// Encrypt seals plaintext with AES-256-GCM using a fresh random 12-byte
// nonce. The returned blob is nonce || ciphertext-with-tag and is what should
// be persisted to users.encrypted_wakatime_key.
//
// Callers MUST NOT log the plaintext, and MUST NOT log this return value in a
// way that survives (the ciphertext alone is safe, but pairs with a leaked
// key). An empty plaintext is legal but pointless; callers should check length
// upstream.
func Encrypt(plaintext []byte) ([]byte, error) {
	if err := LoadKeyFromEnv(); err != nil {
		return nil, err
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	// Prepend the nonce so Decrypt can recover it. AEAD.Seal appends the tag
	// to the ciphertext, so the final layout is [nonce | ciphertext | tag].
	sealed := encState.aead.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

// Decrypt reverses Encrypt. Any tampering with the stored blob (including
// truncation, bit flips, and swapping across users) is detected by GCM's
// authentication tag and surfaces as an error, NOT a partially-recovered
// plaintext.
func Decrypt(ciphertext []byte) ([]byte, error) {
	if err := LoadKeyFromEnv(); err != nil {
		return nil, err
	}
	// Need at least nonceSize + 1 byte of tagged ciphertext to be a candidate.
	// AEAD.Open handles the tag validation; we just guard the split.
	if len(ciphertext) <= gcmNonceSize {
		return nil, ErrMalformedCiphertext
	}
	nonce := ciphertext[:gcmNonceSize]
	sealed := ciphertext[gcmNonceSize:]
	plaintext, err := encState.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		// Deliberately do NOT wrap the underlying GCM error verbatim: callers
		// might log it, and while the message doesn't leak key material, the
		// less variance in the surface, the smaller the oracle.
		return nil, fmt.Errorf("decrypt: authentication failed")
	}
	return plaintext, nil
}

// NewAEADFromBase64 constructs a fresh AES-256-GCM AEAD from a base64-encoded
// 32-byte key WITHOUT touching the package-level singleton. Used by the
// rotate-encryption-key command so it can hold OLD + NEW ciphers in-hand at
// once without stomping BOOM_ENCRYPTION_KEY. Same error surface as
// LoadKeyFromEnv (ErrKeyInvalid for wrong length / malformed base64).
func NewAEADFromBase64(b64Key string) (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(b64Key)
	if err != nil {
		return nil, fmt.Errorf("%w: base64 decode failed", ErrKeyInvalid)
	}
	if len(key) != aesKeySize {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrKeyInvalid, len(key), aesKeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	return aead, nil
}

// EncryptWith seals plaintext with the caller-supplied AEAD. Mirrors Encrypt's
// nonce prefix layout so blobs are interchangeable with the singleton path
// (a blob written with EncryptWith(newAEAD, ...) can be Decrypt()'d after
// BOOM_ENCRYPTION_KEY is swapped to the new key and the process restarted).
// Rotation-only path — production writes should keep using Encrypt.
func EncryptWith(aead cipher.AEAD, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	sealed := aead.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

// DecryptWith opens ciphertext with the caller-supplied AEAD. Same layout /
// error surface as Decrypt; the "authentication failed" wrapping is preserved
// so rotation output can log without leaking the underlying GCM message.
func DecryptWith(aead cipher.AEAD, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) <= gcmNonceSize {
		return nil, ErrMalformedCiphertext
	}
	nonce := ciphertext[:gcmNonceSize]
	sealed := ciphertext[gcmNonceSize:]
	plaintext, err := aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: authentication failed")
	}
	return plaintext, nil
}
