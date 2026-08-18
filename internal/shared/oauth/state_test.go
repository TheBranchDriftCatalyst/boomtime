// state_test.go — the signed OAuth state primitive (gaka-2ip Phase 1). Covers
// the happy path plus every rejection the CSRF/owner-binding guard MUST make:
// tampered payload, tampered signature, wrong signing key, expired, future, and
// malformed.
package oauth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	key := []byte("super-secret-signing-key-32-bytes!")
	now := time.Now()
	state, err := Sign(key, "alice", now)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	owner, err := Verify(key, state, now, 10*time.Minute)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if owner != "alice" {
		t.Fatalf("owner=%q want alice", owner)
	}
}

func TestVerifyRejectsTamperedOwner(t *testing.T) {
	key := []byte("k")
	now := time.Now()
	state, _ := Sign(key, "alice", now)
	// Flip the payload (first base64 segment) — any change breaks the HMAC.
	dot := strings.IndexByte(state, '.')
	tampered := "Zm9v" + state[dot:] // "foo" payload, original signature
	if _, err := Verify(key, tampered, now, 10*time.Minute); !errors.Is(err, ErrStateSignature) {
		t.Fatalf("expected ErrStateSignature, got %v", err)
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	key := []byte("k")
	now := time.Now()
	state, _ := Sign(key, "alice", now)
	tampered := state[:len(state)-2] + "AA"
	if _, err := Verify(key, tampered, now, 10*time.Minute); err == nil {
		t.Fatal("expected error on tampered signature")
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	now := time.Now()
	state, _ := Sign([]byte("key-one"), "alice", now)
	if _, err := Verify([]byte("key-two"), state, now, 10*time.Minute); !errors.Is(err, ErrStateSignature) {
		t.Fatalf("expected ErrStateSignature for wrong key, got %v", err)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	key := []byte("k")
	issued := time.Now().Add(-30 * time.Minute)
	state, _ := Sign(key, "alice", issued)
	// Verify "now" is 30m after issue; maxAge is 10m → expired.
	if _, err := Verify(key, state, time.Now(), 10*time.Minute); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("expected ErrStateExpired, got %v", err)
	}
}

func TestVerifyRejectsFutureIssuedAt(t *testing.T) {
	key := []byte("k")
	issued := time.Now().Add(1 * time.Hour) // well beyond clockSkew
	state, _ := Sign(key, "alice", issued)
	if _, err := Verify(key, state, time.Now(), 10*time.Minute); !errors.Is(err, ErrStateFuture) {
		t.Fatalf("expected ErrStateFuture, got %v", err)
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	key := []byte("k")
	now := time.Now()
	for _, s := range []string{"", "nodot", ".onlysig", "onlypayload.", "@@@.@@@"} {
		if _, err := Verify(key, s, now, time.Minute); err == nil {
			t.Fatalf("expected error for malformed state %q", s)
		}
	}
}

func TestSignVerifyRequireKey(t *testing.T) {
	if _, err := Sign(nil, "alice", time.Now()); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("Sign with empty key: got %v", err)
	}
	if _, err := Verify(nil, "x.y", time.Now(), time.Minute); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("Verify with empty key: got %v", err)
	}
}

func TestFreshWithinWindowPasses(t *testing.T) {
	key := []byte("k")
	issued := time.Now().Add(-5 * time.Minute)
	state, _ := Sign(key, "bob", issued)
	owner, err := Verify(key, state, time.Now(), 10*time.Minute)
	if err != nil {
		t.Fatalf("Verify within window: %v", err)
	}
	if owner != "bob" {
		t.Fatalf("owner=%q want bob", owner)
	}
}
