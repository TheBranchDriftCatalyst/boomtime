package liberate_test

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
)

// sealForTest builds a voucher the way Amazon does, so the service's REAL
// voucher-decrypt step runs against it — only the transport is stubbed.
//
// As in voucher_test.go, the derivation is spelled out literally rather than
// reusing the implementation's helper: if the concat order in keyMaterial ever
// changes, the service integration test must break too, not silently agree.
func sealForTest(cred *amazon.DeviceCredential, asin string) string {
	digest := sha256.Sum256([]byte(amazon.DeviceType() + cred.DeviceSerial + cred.CustomerID + asin))
	block, err := aes.NewCipher(digest[0:16])
	if err != nil {
		panic(err)
	}
	plain := []byte(`{"key":"000102030405060708090a0b0c0d0e0f","iv":"0f0e0d0c0b0a09080706050403020100"}`)
	for len(plain)%aes.BlockSize != 0 {
		plain = append(plain, 0)
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, digest[16:32]).CryptBlocks(out, plain)
	return base64.StdEncoding.EncodeToString(out)
}

// unused keeps the linter quiet if a helper is temporarily unreferenced.
var _ = func(t *testing.T) {}
