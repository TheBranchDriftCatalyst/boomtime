// crypto_ginkgo_test.go — ginkgo mirror of crypto_test.go (gaka-0vp).
// 1:1 case map (4 stdlib TestXxx):
//
//	TestRoundTrip                → Encrypt/Decrypt > "round-trips; nonce is fresh per call"
//	TestDecryptWithWrongKey      → Decrypt > "wrong key → auth failure (not garbage)"
//	TestDecryptTamperedCiphertext→ Decrypt > 4 tamper flavors
//	TestLoadKeyFromEnvUnset      → LoadKeyFromEnv > "unset env → ErrKeyUnset; ops refuse"
//	TestLoadKeyFromEnvInvalid    → LoadKeyFromEnv > 2 invalid flavors
//
// Uses os.Setenv + DeferCleanup to bridge the stdlib t.Setenv+t.Cleanup
// pattern (see the `setenv` helper in the config file for the same idiom).
package auth

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	ginkgoTestKeyBase64 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	ginkgoAltKeyBase64  = "IAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
)

// installKey mirrors the stdlib file's withKey — sets BOOM_ENCRYPTION_KEY,
// resets the singleton, and registers DeferCleanup to restore.
func installKey(key string) {
	prev, hadPrev := os.LookupEnv(EncryptionKeyEnv)
	os.Setenv(EncryptionKeyEnv, key)
	resetEncryptionStateForTest()
	Expect(LoadKeyFromEnv()).To(Succeed())
	DeferCleanup(func() {
		if hadPrev {
			os.Setenv(EncryptionKeyEnv, prev)
		} else {
			os.Unsetenv(EncryptionKeyEnv)
		}
		resetEncryptionStateForTest()
	})
}

var _ = Describe("Encrypt/Decrypt", func() {
	It("round-trips plaintext and uses a fresh nonce per call", func() {
		installKey(ginkgoTestKeyBase64)

		plaintext := []byte("waka_51ee7a20-not-a-real-key-just-for-tests")
		c1, err := Encrypt(plaintext)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(c1)).To(BeNumerically(">", gcmNonceSize))

		got, err := Decrypt(c1)
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Equal(got, plaintext)).To(BeTrue())

		// Fresh nonce → different ciphertext.
		c2, err := Encrypt(plaintext)
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Equal(c1, c2)).To(BeFalse(),
			"two Encrypt calls on same plaintext should not be equal — nonce not random")
	})

	It("Decrypt with the wrong key returns GCM auth failure (not garbage)", func() {
		installKey(ginkgoTestKeyBase64)
		sealed, err := Encrypt([]byte("secret-token"))
		Expect(err).NotTo(HaveOccurred())

		installKey(ginkgoAltKeyBase64)
		got, err := Decrypt(sealed)
		Expect(err).To(HaveOccurred())
		Expect(got).To(BeNil())
	})
})

var _ = Describe("Decrypt with a tampered ciphertext", func() {
	// Preserves the 4 stdlib mutation cases as named DescribeTable entries.
	DescribeTable("mutation → error",
		func(mutate func([]byte) []byte, wantSentinel error) {
			installKey(ginkgoTestKeyBase64)
			sealed, err := Encrypt([]byte("another-secret"))
			Expect(err).NotTo(HaveOccurred())

			tampered := mutate(sealed)
			got, err := Decrypt(tampered)
			Expect(err).To(HaveOccurred())
			Expect(got).To(BeNil())
			if wantSentinel != nil {
				Expect(errors.Is(err, wantSentinel)).To(BeTrue(),
					"expected errors.Is(err, %v), got %v", wantSentinel, err)
			}
		},
		Entry("flip bit in ciphertext body",
			func(b []byte) []byte {
				out := append([]byte(nil), b...)
				out[gcmNonceSize+1] ^= 0x01
				return out
			},
			nil, // GCM auth failure (no specific sentinel)
		),
		Entry("flip bit in nonce prefix",
			func(b []byte) []byte {
				out := append([]byte(nil), b...)
				out[0] ^= 0x80
				return out
			},
			nil,
		),
		Entry("truncate to below nonce size",
			func(b []byte) []byte { return b[:gcmNonceSize-1] },
			ErrMalformedCiphertext,
		),
		Entry("truncate tag",
			func(b []byte) []byte { return b[:len(b)-1] },
			nil,
		),
	)
})

var _ = Describe("LoadKeyFromEnv", func() {
	It("unset env → ErrKeyUnset; Encrypt/Decrypt refuse with the same sentinel", func() {
		os.Setenv(EncryptionKeyEnv, "")
		resetEncryptionStateForTest()
		DeferCleanup(resetEncryptionStateForTest)

		Expect(errors.Is(LoadKeyFromEnv(), ErrKeyUnset)).To(BeTrue())
		Expect(IsEncryptionKeyConfigured()).To(BeFalse())

		_, err := Encrypt([]byte("x"))
		Expect(errors.Is(err, ErrKeyUnset)).To(BeTrue())
		_, err = Decrypt([]byte("x"))
		Expect(errors.Is(err, ErrKeyUnset)).To(BeTrue())
	})

	DescribeTable("invalid key → ErrKeyInvalid",
		func(envValue string) {
			os.Setenv(EncryptionKeyEnv, envValue)
			resetEncryptionStateForTest()
			DeferCleanup(resetEncryptionStateForTest)
			Expect(errors.Is(LoadKeyFromEnv(), ErrKeyInvalid)).To(BeTrue())
		},
		Entry("non-base64 garbage", "!!! not base64 !!!"),
		Entry("wrong length (base64 of 16 bytes → AES-128)",
			base64.StdEncoding.EncodeToString(make([]byte, 16))),
	)
})
