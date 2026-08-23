// Package oauth provides the CSRF `state` primitive shared by the per-user
// third-party OAuth connect flows (boom-2ip Phase 1: GitHub). The state binds
// the OAuth round-trip to the boomtime user who initiated it and is signed with
// an HMAC so the callback can trust the owner without any server-side session
// store — the whole trust chain lives in the signature.
//
// THREAT MODEL
//
//	The authorize→callback round-trip leaves boomtime's control (it goes through
//	github.com and back), so the `state` param is fully attacker-visible and
//	attacker-supplied on the callback. Without a signature an attacker could:
//	  A) forge a callback for a DIFFERENT owner (connect their GitHub to a
//	     victim's account, or vice-versa) — the HMAC over the owner defeats this;
//	  B) replay an old captured state indefinitely — the issued-at + max-age
//	     freshness check bounds the window;
//	  C) tamper any field — any bit flip changes the payload and fails the
//	     constant-time HMAC compare.
//
//	The signing key (BOOM_OAUTH_STATE_SIGNING_KEY) is server-side only and never
//	leaves the process. A random nonce is mixed in so two states minted for the
//	same owner in the same second still differ.
//
// PAYLOAD LAYOUT
//
//	state = base64url(payload) + "." + base64url(HMAC-SHA256(key, payload))
//	payload = owner "|" nonceHex "|" issuedAtUnix
//
//	'|' is a safe delimiter because ValidateUsername (internal/auth) rejects it
//	in any boomtime username, so the owner field can never inject a delimiter.
package oauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Errors returned by Verify. All are safe to log — none carry key material.
var (
	// ErrStateMalformed: the state isn't payload.signature or a field is
	// missing / non-numeric. Treated the same as a signature failure by the
	// caller (redirect with a generic error), but distinguished for logs.
	ErrStateMalformed = errors.New("oauth state is malformed")
	// ErrStateSignature: the HMAC did not verify (tampered / wrong key / forged).
	ErrStateSignature = errors.New("oauth state signature mismatch")
	// ErrStateExpired: the state is older than the allowed max-age (replay).
	ErrStateExpired = errors.New("oauth state has expired")
	// ErrStateFuture: issued-at is in the future beyond a small skew (clock
	// tampering / forged timestamp) — fail closed rather than trust it.
	ErrStateFuture = errors.New("oauth state issued-at is in the future")
	// ErrNoSigningKey: Sign/Verify called with an empty signing key. The
	// caller must gate on config before ever reaching here.
	ErrNoSigningKey = errors.New("oauth state signing key is empty")
)

// clockSkew tolerates a small amount of issued-at-in-the-future before
// rejecting as ErrStateFuture (NTP jitter across the pod and the operator's
// clock). Small on purpose — this is not a validity window, just skew slack.
const clockSkew = 2 * time.Minute

// Sign mints a signed state for owner using signingKey and the supplied issue
// time (now). A fresh 8-byte random nonce is embedded so repeated calls never
// collide. Returns ErrNoSigningKey if signingKey is empty (caller must gate on
// config first).
func Sign(signingKey []byte, owner string, now time.Time) (string, error) {
	if len(signingKey) == 0 {
		return "", ErrNoSigningKey
	}
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	payload := owner + "|" + hex.EncodeToString(nonce) + "|" + strconv.FormatInt(now.Unix(), 10)
	sig := mac(signingKey, []byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify checks the signature + freshness of state and returns the embedded
// owner. maxAge bounds how old the issued-at may be (replay window). now is the
// reference time (injected for tests). The signature is compared in constant
// time BEFORE the freshness check so a tampered state never reveals timing
// about its fields.
func Verify(signingKey []byte, state string, now time.Time, maxAge time.Duration) (string, error) {
	if len(signingKey) == 0 {
		return "", ErrNoSigningKey
	}
	dot := strings.IndexByte(state, '.')
	if dot <= 0 || dot == len(state)-1 {
		return "", ErrStateMalformed
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(state[:dot])
	if err != nil {
		return "", ErrStateMalformed
	}
	sigRaw, err := base64.RawURLEncoding.DecodeString(state[dot+1:])
	if err != nil {
		return "", ErrStateMalformed
	}

	// Constant-time signature check first — fail closed before parsing fields.
	want := mac(signingKey, payloadRaw)
	if !hmac.Equal(want, sigRaw) {
		return "", ErrStateSignature
	}

	parts := strings.Split(string(payloadRaw), "|")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", ErrStateMalformed
	}
	owner := parts[0]
	issuedUnix, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", ErrStateMalformed
	}
	issuedAt := time.Unix(issuedUnix, 0)
	if issuedAt.After(now.Add(clockSkew)) {
		return "", ErrStateFuture
	}
	if now.Sub(issuedAt) > maxAge {
		return "", ErrStateExpired
	}
	return owner, nil
}

func mac(key, msg []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(msg)
	return h.Sum(nil)
}
