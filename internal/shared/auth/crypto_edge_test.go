// crypto_edge_test.go — gaka-se2.4 SECURITY-CRITICAL coverage.
//
// Pins load-bearing invariants NOT covered by the existing crypto_test.go
// happy-path roundtrip:
//
//  1. IsEncryptionKeyConfigured across env-unset / valid / malformed base64 /
//     wrong-length flavors — because the "config-ok" oracle is what the
//     startup path uses to decide whether encryption features are inert.
//  2. NewAEADFromBase64 length + base64 error surface — the rotate CLI holds
//     old + new AEADs in-hand at once via this constructor, so wrong key
//     shape MUST error, not silently produce a broken cipher.
//  3. EncryptWith nonce freshness — 2 calls with same plaintext + same
//     AEAD must produce different ciphertexts (no cross-user leakage).
//  4. DecryptWith under a DIFFERENT AEAD than what sealed the blob returns
//     an error AND nil plaintext (THE load-bearing rotation-authenticity
//     test).
//  5. DecryptWith on a bit-flipped ciphertext errors (GCM tag detects
//     tamper).
//  6. DecryptWith on a ciphertext shorter than nonce+1 returns
//     ErrMalformedCiphertext (distinguishable from auth failure).
//  7. NewRawToken 100 samples: all distinct + all parse as UUIDv4 (prevents
//     accidental drift to a lower-entropy token generator).
package auth

import (
	"bytes"
	"encoding/base64"
	"errors"
	"regexp"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// keyA / keyB — two distinct 32-byte AES-256 keys used for the rotation /
// cross-key-authenticity tests. Values are arbitrary but stable.
const (
	edgeKeyABase64 = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" // "0123456789abcdef0123456789abcdef"
	edgeKeyBBase64 = "ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA=" // "fedcba9876543210fedcba9876543210"
)

var _ = Describe("IsEncryptionKeyConfigured (gaka-se2.4)", func() {
	AfterEach(func() {
		// Restore singleton so following specs are not poisoned.
		resetEncryptionStateForTest()
	})

	It("returns false when BOOM_ENCRYPTION_KEY is unset", func() {
		GinkgoT().Setenv(EncryptionKeyEnv, "")
		resetEncryptionStateForTest()
		Expect(IsEncryptionKeyConfigured()).To(BeFalse())
	})

	It("returns true when BOOM_ENCRYPTION_KEY is a valid base64 32-byte value", func() {
		GinkgoT().Setenv(EncryptionKeyEnv, edgeKeyABase64)
		resetEncryptionStateForTest()
		Expect(IsEncryptionKeyConfigured()).To(BeTrue())
	})

	It("returns false when BOOM_ENCRYPTION_KEY is malformed base64", func() {
		GinkgoT().Setenv(EncryptionKeyEnv, "!!! not base64 !!!")
		resetEncryptionStateForTest()
		Expect(IsEncryptionKeyConfigured()).To(BeFalse())
	})

	It("returns false when BOOM_ENCRYPTION_KEY decodes to the wrong length (16 bytes = AES-128, not AES-256)", func() {
		GinkgoT().Setenv(EncryptionKeyEnv, base64.StdEncoding.EncodeToString(make([]byte, 16)))
		resetEncryptionStateForTest()
		Expect(IsEncryptionKeyConfigured()).To(BeFalse())
	})
})

var _ = Describe("NewAEADFromBase64 (gaka-se2.4 rotation constructor)", func() {
	It("returns a usable AEAD for a valid base64 32-byte key", func() {
		aead, err := NewAEADFromBase64(edgeKeyABase64)
		Expect(err).NotTo(HaveOccurred())
		Expect(aead).NotTo(BeNil())
		Expect(aead.NonceSize()).To(Equal(gcmNonceSize))
	})

	It("returns ErrKeyInvalid for a 31-byte key (below AES-256 length)", func() {
		short := base64.StdEncoding.EncodeToString(make([]byte, aesKeySize-1))
		aead, err := NewAEADFromBase64(short)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrKeyInvalid)).To(BeTrue(),
			"31-byte key must surface as ErrKeyInvalid, not a raw aes error")
		Expect(aead).To(BeNil())
	})

	It("returns ErrKeyInvalid for a 33-byte key (above AES-256 length)", func() {
		long := base64.StdEncoding.EncodeToString(make([]byte, aesKeySize+1))
		aead, err := NewAEADFromBase64(long)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrKeyInvalid)).To(BeTrue())
		Expect(aead).To(BeNil())
	})

	It("returns ErrKeyInvalid for malformed base64", func() {
		aead, err := NewAEADFromBase64("!!!not-base64!!!")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrKeyInvalid)).To(BeTrue())
		Expect(aead).To(BeNil())
	})
})

var _ = Describe("EncryptWith (gaka-se2.4 rotation-writer)", func() {
	It("uses a fresh nonce every call — same plaintext + same AEAD produces different ciphertexts", func() {
		aead, err := NewAEADFromBase64(edgeKeyABase64)
		Expect(err).NotTo(HaveOccurred())

		plaintext := []byte("waka_rotation-fixture-not-a-real-key")
		c1, err := EncryptWith(aead, plaintext)
		Expect(err).NotTo(HaveOccurred())
		c2, err := EncryptWith(aead, plaintext)
		Expect(err).NotTo(HaveOccurred())

		Expect(bytes.Equal(c1, c2)).To(BeFalse(),
			"two EncryptWith calls on identical plaintext must not collide — nonce not fresh")
		// And both should be at least nonce+tag long.
		Expect(len(c1)).To(BeNumerically(">", gcmNonceSize))
		Expect(len(c2)).To(BeNumerically(">", gcmNonceSize))
	})

	It("round-trips under the SAME AEAD (sanity — pins the read side of rotation)", func() {
		aead, err := NewAEADFromBase64(edgeKeyABase64)
		Expect(err).NotTo(HaveOccurred())
		plaintext := []byte("rotation-round-trip-fixture")
		sealed, err := EncryptWith(aead, plaintext)
		Expect(err).NotTo(HaveOccurred())
		got, err := DecryptWith(aead, sealed)
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Equal(got, plaintext)).To(BeTrue())
	})
})

var _ = Describe("DecryptWith (gaka-se2.4 authenticity contract)", func() {
	It("DecryptWith on ciphertext encrypted under a DIFFERENT key returns error and NEVER garbage plaintext", func() {
		// THE load-bearing security test. If this passes with a non-nil
		// plaintext or a nil error, rotation is silently broken and every
		// re-encrypted row is a landmine.
		aeadA, err := NewAEADFromBase64(edgeKeyABase64)
		Expect(err).NotTo(HaveOccurred())
		aeadB, err := NewAEADFromBase64(edgeKeyBBase64)
		Expect(err).NotTo(HaveOccurred())

		sealed, err := EncryptWith(aeadA, []byte("cross-key-authenticity-fixture"))
		Expect(err).NotTo(HaveOccurred())

		got, err := DecryptWith(aeadB, sealed)
		Expect(err).To(HaveOccurred(), "cross-key decrypt MUST error, never surface plaintext")
		Expect(got).To(BeNil(), "cross-key decrypt MUST return nil plaintext, not garbage bytes")
	})

	It("errors when the last byte of the ciphertext is flipped (GCM tag catches tamper)", func() {
		aead, err := NewAEADFromBase64(edgeKeyABase64)
		Expect(err).NotTo(HaveOccurred())
		sealed, err := EncryptWith(aead, []byte("tag-tamper-fixture"))
		Expect(err).NotTo(HaveOccurred())

		tampered := append([]byte(nil), sealed...)
		tampered[len(tampered)-1] ^= 0x01 // flip last byte (inside the GCM tag)

		got, err := DecryptWith(aead, tampered)
		Expect(err).To(HaveOccurred())
		Expect(got).To(BeNil())
	})

	It("errors when a body byte is flipped (GCM tag catches body tamper too)", func() {
		aead, err := NewAEADFromBase64(edgeKeyABase64)
		Expect(err).NotTo(HaveOccurred())
		sealed, err := EncryptWith(aead, []byte("body-tamper-fixture"))
		Expect(err).NotTo(HaveOccurred())

		tampered := append([]byte(nil), sealed...)
		tampered[gcmNonceSize+1] ^= 0x80 // flip a body byte

		got, err := DecryptWith(aead, tampered)
		Expect(err).To(HaveOccurred())
		Expect(got).To(BeNil())
	})

	It("returns ErrMalformedCiphertext for a blob shorter than nonce+1 (distinguishable from auth failure)", func() {
		aead, err := NewAEADFromBase64(edgeKeyABase64)
		Expect(err).NotTo(HaveOccurred())

		// Exactly gcmNonceSize (12) bytes — one byte too short to have any tag/body.
		short := make([]byte, gcmNonceSize)
		got, err := DecryptWith(aead, short)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrMalformedCiphertext)).To(BeTrue(),
			"short ciphertext must surface as ErrMalformedCiphertext so operators can tell 'truncated' from 'tampered'")
		Expect(got).To(BeNil())
	})

	It("returns ErrMalformedCiphertext for an empty blob", func() {
		aead, err := NewAEADFromBase64(edgeKeyABase64)
		Expect(err).NotTo(HaveOccurred())
		got, err := DecryptWith(aead, []byte{})
		Expect(errors.Is(err, ErrMalformedCiphertext)).To(BeTrue())
		Expect(got).To(BeNil())
	})
})

var _ = Describe("ResetForTest (gaka-se2.4 cross-package test hook)", func() {
	// ResetForTest is the EXPORTED variant of resetEncryptionStateForTest so
	// importer / handler tests can bust the sync.Once cache after they swap
	// BOOM_ENCRYPTION_KEY. Cover it here so a rename / refactor of the
	// singleton is caught immediately, not later when a downstream test
	// starts leaking state across specs.
	It("busts the singleton so a subsequent LoadKeyFromEnv re-parses the env", func() {
		GinkgoT().Setenv(EncryptionKeyEnv, edgeKeyABase64)
		resetEncryptionStateForTest()
		Expect(LoadKeyFromEnv()).To(Succeed())
		Expect(IsEncryptionKeyConfigured()).To(BeTrue())

		// Swap the env to an INVALID value; without ResetForTest the sync.Once
		// would still return "configured".
		GinkgoT().Setenv(EncryptionKeyEnv, "!!!not-base64!!!")
		ResetForTest() // exported variant — the whole point of this spec
		Expect(IsEncryptionKeyConfigured()).To(BeFalse(),
			"ResetForTest must actually reset the singleton or downstream tests race on stale state")

		// Cleanup: put back a valid key so we don't poison later specs, then reset.
		GinkgoT().Setenv(EncryptionKeyEnv, "")
		resetEncryptionStateForTest()
	})
})

var _ = Describe("NewRawToken (gaka-se2.4 API-token entropy)", func() {
	// UUIDv4 pattern: 8-4-4-4-12 hex, with version nibble = 4 in the 3rd
	// group and variant nibble in {8,9,a,b} at the start of the 4th group.
	// Anchored so accidental drift to a longer/shorter format is caught.
	var uuidV4Pat = regexp.MustCompile(
		`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
	)

	It("100 samples are all distinct AND all match the UUIDv4 shape", func() {
		const n = 100
		seen := make(map[string]struct{}, n)
		for i := 0; i < n; i++ {
			tok := NewRawToken()
			Expect(tok).To(MatchRegexp(uuidV4Pat.String()),
				"sample %d does not match UUIDv4 shape: %q", i, tok)
			// Also independently parse via google/uuid so a bug in the regex
			// doesn't hide a bug in the token (belt+braces).
			parsed, err := uuid.Parse(tok)
			Expect(err).NotTo(HaveOccurred(), "sample %d failed uuid.Parse: %q", i, tok)
			Expect(parsed.Version()).To(BeEquivalentTo(4),
				"sample %d is not UUIDv4 (got version %d): %q", i, parsed.Version(), tok)
			seen[tok] = struct{}{}
		}
		Expect(seen).To(HaveLen(n),
			"NewRawToken produced a collision in %d samples — entropy source is broken", n)
	})
})
