package auth

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

// testKey is a deterministic 32-byte key used across the crypto tests. Never
// use a fixed key in production — this is just so tests are hermetic and
// re-runnable without disturbing developer envs.
const testKeyBase64 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

// altKeyBase64 is a different 32-byte key used to prove that a ciphertext
// sealed under one key cannot be opened under another (Decrypt returns the
// GCM authentication-failure error, not silent gibberish).
const altKeyBase64 = "IAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// withKey installs a fresh encryption-state singleton, sets BOOM_ENCRYPTION_KEY
// to key for the duration of the test, and restores the previous singleton on
// cleanup. Must be called before Encrypt/Decrypt in every test.
func withKey(t *testing.T, key string) {
	t.Helper()
	t.Setenv(EncryptionKeyEnv, key)
	resetEncryptionStateForTest()
	if err := LoadKeyFromEnv(); err != nil {
		t.Fatalf("LoadKeyFromEnv with valid key returned error: %v", err)
	}
	t.Cleanup(func() {
		resetEncryptionStateForTest()
	})
}

// TestRoundTrip verifies that Encrypt/Decrypt recover the exact plaintext and
// that two encryptions of the same plaintext produce different ciphertexts
// (fresh nonce per Encrypt).
func TestRoundTrip(t *testing.T) {
	withKey(t, testKeyBase64)

	plaintext := []byte("waka_51ee7a20-not-a-real-key-just-for-tests")
	c1, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(c1) <= gcmNonceSize {
		t.Fatalf("ciphertext too short (%d bytes) — nonce prefix missing?", len(c1))
	}

	got, err := Decrypt(c1)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Decrypt roundtrip mismatch:\n  got: %q\n want: %q", got, plaintext)
	}

	// A second encryption must use a fresh nonce (so identical plaintext →
	// different stored blob). This is what stops an attacker with DB read from
	// noticing that user A and user B share the same Wakatime key.
	c2, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt (second call): %v", err)
	}
	if bytes.Equal(c1, c2) {
		t.Fatalf("two Encrypt calls on the same plaintext produced identical ciphertext — nonce is not random")
	}
}

// TestDecryptWithWrongKey seals under one key and tries to open under a
// different key. GCM must reject with an auth failure, NOT return garbage.
func TestDecryptWithWrongKey(t *testing.T) {
	withKey(t, testKeyBase64)

	sealed, err := Encrypt([]byte("secret-token"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Swap keys and try to open. Same nonce, different key → auth failure.
	withKey(t, altKeyBase64)
	got, err := Decrypt(sealed)
	if err == nil {
		t.Fatalf("Decrypt with wrong key returned no error and %q — expected auth failure", got)
	}
	if got != nil {
		t.Fatalf("Decrypt returned plaintext bytes despite error: %q", got)
	}
}

// TestDecryptTamperedCiphertext flips a bit in the ciphertext body and in the
// nonce prefix, both of which must trigger a GCM auth failure. A tampered
// blob MUST NOT decrypt to a partial or padded plaintext.
func TestDecryptTamperedCiphertext(t *testing.T) {
	withKey(t, testKeyBase64)

	sealed, err := Encrypt([]byte("another-secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	tests := []struct {
		name  string
		mutate func([]byte) []byte
	}{
		{
			name: "flip bit in ciphertext body",
			mutate: func(b []byte) []byte {
				out := append([]byte(nil), b...)
				// Flip a bit past the nonce prefix.
				out[gcmNonceSize+1] ^= 0x01
				return out
			},
		},
		{
			name: "flip bit in nonce prefix",
			mutate: func(b []byte) []byte {
				out := append([]byte(nil), b...)
				out[0] ^= 0x80
				return out
			},
		},
		{
			name: "truncate to below nonce size",
			mutate: func(b []byte) []byte {
				return b[:gcmNonceSize-1]
			},
		},
		{
			name: "truncate tag",
			mutate: func(b []byte) []byte {
				return b[:len(b)-1]
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tampered := tc.mutate(sealed)
			got, err := Decrypt(tampered)
			if err == nil {
				t.Fatalf("Decrypt on tampered ciphertext returned no error and %q", got)
			}
			if got != nil {
				t.Fatalf("Decrypt returned plaintext bytes despite tamper: %q", got)
			}
			// The truncation-below-nonce case surfaces as ErrMalformedCiphertext
			// (a distinct sentinel); the other three are GCM auth failures.
			if tc.name == "truncate to below nonce size" && !errors.Is(err, ErrMalformedCiphertext) {
				t.Fatalf("truncation should be ErrMalformedCiphertext, got %v", err)
			}
		})
	}
}

// TestLoadKeyFromEnvUnset covers the developer-friendly branch: no env, no
// panic — Encrypt/Decrypt just refuse to run with ErrKeyUnset.
func TestLoadKeyFromEnvUnset(t *testing.T) {
	t.Setenv(EncryptionKeyEnv, "")
	resetEncryptionStateForTest()
	defer resetEncryptionStateForTest()

	if err := LoadKeyFromEnv(); !errors.Is(err, ErrKeyUnset) {
		t.Fatalf("LoadKeyFromEnv with unset env: got %v, want ErrKeyUnset", err)
	}
	if IsEncryptionKeyConfigured() {
		t.Fatal("IsEncryptionKeyConfigured returned true with unset env")
	}
	if _, err := Encrypt([]byte("x")); !errors.Is(err, ErrKeyUnset) {
		t.Fatalf("Encrypt with unset env: got %v, want ErrKeyUnset", err)
	}
	if _, err := Decrypt([]byte("x")); !errors.Is(err, ErrKeyUnset) {
		t.Fatalf("Decrypt with unset env: got %v, want ErrKeyUnset", err)
	}
}

// TestLoadKeyFromEnvInvalid covers the "operator typed a bad key" branches:
// both non-base64 garbage and a base64 blob of the wrong length must produce
// ErrKeyInvalid (not a panic, not a wrong-sized silent AES key).
func TestLoadKeyFromEnvInvalid(t *testing.T) {
	// Non-base64.
	t.Run("non-base64", func(t *testing.T) {
		t.Setenv(EncryptionKeyEnv, "!!! not base64 !!!")
		resetEncryptionStateForTest()
		defer resetEncryptionStateForTest()
		if err := LoadKeyFromEnv(); !errors.Is(err, ErrKeyInvalid) {
			t.Fatalf("got %v, want ErrKeyInvalid", err)
		}
	})
	// Base64 of the wrong size (16 bytes → AES-128, forbidden by policy).
	t.Run("wrong-length", func(t *testing.T) {
		short := base64.StdEncoding.EncodeToString(make([]byte, 16))
		t.Setenv(EncryptionKeyEnv, short)
		resetEncryptionStateForTest()
		defer resetEncryptionStateForTest()
		if err := LoadKeyFromEnv(); !errors.Is(err, ErrKeyInvalid) {
			t.Fatalf("got %v, want ErrKeyInvalid", err)
		}
	})
}
