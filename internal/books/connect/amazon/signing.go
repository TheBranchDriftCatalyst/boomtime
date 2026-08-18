package amazon

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SignedHeaders are the ADP request-signing headers to attach to a
// device-authenticated Amazon request. Both Audible (/1.0/*) and Kindle
// (whispersync / Fiona / sidecar) authenticate this way, off the SAME device
// credential. Header names + formats match mkb79/Audible's sign_request.
type SignedHeaders struct {
	AdpToken  string // -> "x-adp-token"
	AdpAlg    string // -> "x-adp-alg"        == "SHA256withRSA:1.0"
	Signature string // -> "x-adp-signature"  == base64(RSA-SHA256(canonical)) + ":" + date
}

// adpSignAlg is the fixed x-adp-alg value.
const adpSignAlg = "SHA256withRSA:1.0"

// Sign produces the ADP signing headers for a device-authenticated request,
// matching mkb79/Audible's sign_request:
//
//	canonical = method + "\n" + path + "\n" + date + "\n" + body + "\n" + adp_token
//	x-adp-signature = base64(RSA-SHA256(canonical)) + ":" + date
//	x-adp-alg       = "SHA256withRSA:1.0"
//	x-adp-token     = adp_token
//
// The RSA/SHA-256 mechanics are unit-tested (signing_test.go). Verify the exact
// canonical string live against a real device credential before relying on it —
// a format mismatch surfaces as an Amazon auth error, never a local one.
func Sign(cred *DeviceCredential, method, path string, body []byte, now time.Time) (SignedHeaders, error) {
	if cred == nil {
		return SignedHeaders{}, ErrNotRegistered
	}
	key, err := parseDevicePrivateKey(cred.DevicePrivateKey)
	if err != nil {
		return SignedHeaders{}, err
	}
	date := now.UTC().Format("2006-01-02T15:04:05.000Z")
	canonical := strings.Join([]string{method, path, date, string(body), cred.AdpToken}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return SignedHeaders{}, fmt.Errorf("amazon: sign: %w", err)
	}
	return SignedHeaders{
		AdpToken:  cred.AdpToken,
		AdpAlg:    adpSignAlg,
		Signature: base64.StdEncoding.EncodeToString(sig) + ":" + date,
	}, nil
}

// parseDevicePrivateKey accepts a PKCS8 (preferred) or PKCS1 PEM RSA key.
func parseDevicePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("amazon: device private key is not valid PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("amazon: device private key is not RSA")
		}
		return rk, nil
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}
