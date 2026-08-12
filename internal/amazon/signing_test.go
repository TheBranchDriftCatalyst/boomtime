package amazon

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func testKeyPEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), key
}

// The RSA/SHA-256 signing mechanics + canonical assembly are verifiable in
// isolation (the exact canonical FORMAT still needs live Amazon verification,
// but the crypto is correct): a digest we produce must verify under the public
// key over the same canonical string.
func TestSignProducesVerifiableDigest(t *testing.T) {
	pemStr, key := testKeyPEM(t)
	cred := &DeviceCredential{AdpToken: "adp-tok", DevicePrivateKey: pemStr}
	now := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)

	h, err := Sign(cred, "GET", "/1.0/library", nil, now)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if h.AdpToken != "adp-tok" || h.AdpAlg != "SHA256withRSA:1.0" {
		t.Fatalf("headers wrong: token=%q alg=%q", h.AdpToken, h.AdpAlg)
	}
	// x-adp-signature = base64(sig) + ":" + date. base64-std never contains ':'
	// so the FIRST ':' splits sig from the (colon-bearing) ISO date.
	sd := strings.SplitN(h.Signature, ":", 2)
	if len(sd) != 2 {
		t.Fatalf("signature shape: %q", h.Signature)
	}
	sig, err := base64.StdEncoding.DecodeString(sd[0])
	if err != nil {
		t.Fatalf("sig b64: %v", err)
	}
	canonical := strings.Join([]string{"GET", "/1.0/library", sd[1], "", "adp-tok"}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature does not verify against the public key: %v", err)
	}

	if _, err := Sign(nil, "GET", "/x", nil, now); err != ErrNotRegistered {
		t.Fatalf("Sign(nil) = %v, want ErrNotRegistered", err)
	}
}

func TestImportAuthFile(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	blob := []byte(`{"adp_token":"tok","device_private_key":"PEMDATA","refresh_token":"rt",` +
		`"locale":"uk","device_info":{"device_serial_number":"SER"},"customer_info":{"user_id":"UID"}}`)
	cred, err := ImportAuthFile(blob, now)
	if err != nil {
		t.Fatalf("ImportAuthFile: %v", err)
	}
	if cred.AdpToken != "tok" || cred.Marketplace != MarketplaceUK ||
		cred.CustomerID != "UID" || cred.DeviceSerial != "SER" || cred.RefreshToken != "rt" {
		t.Fatalf("parsed cred wrong: %+v", cred)
	}
	if _, err := ImportAuthFile([]byte(`{}`), now); err == nil {
		t.Fatal("expected error on missing adp_token/device_private_key")
	}
}
