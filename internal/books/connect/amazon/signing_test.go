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

// The POST body is folded into the canonical string, and until liberation
// (boom-w20s) every caller passed a nil body — so this is the first exercise of
// that element. It matters because a body/signature mismatch surfaces as an
// Amazon-side auth error that looks nothing like a local bug.
//
// This verifies the CRYPTO and the ASSEMBLY (that the body bytes land in the
// canonical string verbatim, and that changing them changes the signature). The
// canonical FORMAT itself is still a live-verification item — see the admin
// liberation probe, boom-w20s.19.
func TestSignWithBodyIncludesBodyInCanonicalString(t *testing.T) {
	pemStr, key := testKeyPEM(t)
	cred := &DeviceCredential{AdpToken: "adp-tok", DevicePrivateKey: pemStr}
	now := time.Date(2026, 8, 24, 1, 2, 3, 0, time.UTC)
	const path = "/1.0/content/B0TEST/licenserequest"
	body := []byte(`{"consumption_type":"Download","quality":"High"}`)

	h, err := Sign(cred, "POST", path, body, now)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	sd := strings.SplitN(h.Signature, ":", 2)
	if len(sd) != 2 {
		t.Fatalf("signature shape: %q", h.Signature)
	}
	sig, err := base64.StdEncoding.DecodeString(sd[0])
	if err != nil {
		t.Fatalf("sig b64: %v", err)
	}
	canonical := strings.Join([]string{"POST", path, sd[1], string(body), "adp-tok"}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("POST signature does not verify over a canonical string containing the body: %v", err)
	}
}

// The signature must be sensitive to the body. If it were not, re-marshalling
// between signing and sending would be harmless — and the whole reason
// SignedPost takes []byte rather than a struct would evaporate.
func TestSignBodySensitivity(t *testing.T) {
	pemStr, _ := testKeyPEM(t)
	cred := &DeviceCredential{AdpToken: "adp-tok", DevicePrivateKey: pemStr}
	now := time.Date(2026, 8, 24, 1, 2, 3, 0, time.UTC)
	const path = "/1.0/content/B0TEST/licenserequest"

	// Semantically identical JSON, different bytes — exactly what an innocent
	// re-marshal produces.
	a, err := Sign(cred, "POST", path, []byte(`{"a":1,"b":2}`), now)
	if err != nil {
		t.Fatalf("Sign a: %v", err)
	}
	b, err := Sign(cred, "POST", path, []byte(`{"b":2,"a":1}`), now)
	if err != nil {
		t.Fatalf("Sign b: %v", err)
	}
	if a.Signature == b.Signature {
		t.Fatal("signature is INSENSITIVE to the body — a re-marshal would go undetected")
	}

	// An empty body must also differ from a populated one.
	empty, err := Sign(cred, "POST", path, nil, now)
	if err != nil {
		t.Fatalf("Sign empty: %v", err)
	}
	if empty.Signature == a.Signature {
		t.Fatal("nil body and populated body produced the same signature")
	}
}
