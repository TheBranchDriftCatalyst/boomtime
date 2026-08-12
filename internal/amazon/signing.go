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

// SignedHeaders are the headers to attach to a device-authenticated Amazon
// request. Both Audible (/1.0/*) and Kindle (whispersync / Fiona / sidecar)
// authenticate this way, off the SAME device credential.
type SignedHeaders struct {
	RequestDigest string // -> "x-adp-request-digest"
	AdpToken      string // -> "x-adp-token"
}

// Sign produces the x-adp-request-digest for a device-authenticated request.
//
// Format (from the mkb79/Audible request-signing scheme, see
// docs/design/book-tracking-research.md §2):
//
//	digest    = "SIG1:" + base64(RSA-SHA256(canonical)) + ":" + timestamp
//	canonical = method + "\n" + path + "\n" + timestamp + "\n" + body + "\n" + adp_token
//
// The RSA/SHA-256 mechanics here are standard and unit-tested (signing_test.go);
// the exact CANONICAL string is reverse-engineered and MUST be verified live
// against a real device credential before relying on it — a format mismatch
// surfaces as an Amazon auth error, never a local one.
func Sign(cred *DeviceCredential, method, path string, body []byte, now time.Time) (SignedHeaders, error) {
	if cred == nil {
		return SignedHeaders{}, ErrNotRegistered
	}
	key, err := parseDevicePrivateKey(cred.DevicePrivateKey)
	if err != nil {
		return SignedHeaders{}, err
	}
	ts := now.UTC().Format("2006-01-02T15:04:05.000Z")
	canonical := strings.Join([]string{method, path, ts, string(body), cred.AdpToken}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return SignedHeaders{}, fmt.Errorf("amazon: sign: %w", err)
	}
	return SignedHeaders{
		RequestDigest: "SIG1:" + base64.StdEncoding.EncodeToString(sig) + ":" + ts,
		AdpToken:      cred.AdpToken,
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
