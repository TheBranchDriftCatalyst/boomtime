package db

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// boom-93f.11.6: the OIDC session must store the provider refresh RECOVERABLY
// (encrypted ciphertext round-trips byte-for-byte — unlike the old SHA-256 hash,
// which could never be USED for a refresh-grant) and rotate in place so
// /auth/refresh_token can silently extend the session. Runs against the isolated
// boomtime_test DB (skips when no DB — see harness_test.go).
func TestOIDCSessionRefreshRoundTripAndRotate(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	const name = "oidcref_gaka93f116"
	del := func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM oidc_sessions WHERE username=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, name)
	}
	del()
	t.Cleanup(del)
	if ok, err := d.InsertUser(ctx, StoredUser{
		Username: name, HashedPassword: []byte{1}, SaltUsed: []byte{1}, ArgonVersion: 2,
	}); err != nil || !ok {
		t.Fatalf("InsertUser(%s) = %v, %v; want true, nil", name, ok, err)
	}

	const sid = "oidcref-session"
	ct1 := []byte("ciphertext-v1-\x00\x01\x02") // includes NUL to prove bytea fidelity
	if err := d.CreateOIDCSession(ctx, sid, name, time.Now().Add(time.Hour), ct1); err != nil {
		t.Fatalf("CreateOIDCSession: %v", err)
	}

	// Recoverable: the stored refresh round-trips exactly (not hashed).
	got, exp, ok, err := d.GetOIDCSessionRefresh(ctx, sid)
	if err != nil || !ok {
		t.Fatalf("GetOIDCSessionRefresh: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(got, ct1) {
		t.Fatalf("stored refresh = %q, want %q (must round-trip byte-for-byte)", got, ct1)
	}

	// Rotate in place: new expiry + new ciphertext, SAME cookie.
	newExp := exp.Add(2 * time.Hour)
	ct2 := []byte("ciphertext-v2")
	if err := d.RotateOIDCSession(ctx, sid, newExp, ct2); err != nil {
		t.Fatalf("RotateOIDCSession: %v", err)
	}
	got2, exp2, ok, err := d.GetOIDCSessionRefresh(ctx, sid)
	if err != nil || !ok {
		t.Fatalf("GetOIDCSessionRefresh after rotate: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(got2, ct2) {
		t.Fatalf("rotated refresh = %q, want %q", got2, ct2)
	}
	if !exp2.After(exp) {
		t.Fatalf("rotate did not extend expiry: %v is not after %v", exp2, exp)
	}

	// A session past its expiry yields ok=false — the rotate path must not
	// resurrect one IdentifyOwnerFromCookie would already have rejected.
	if err := d.RotateOIDCSession(ctx, sid, time.Now().Add(-time.Minute), ct2); err != nil {
		t.Fatalf("RotateOIDCSession (force expire): %v", err)
	}
	if _, _, ok, _ := d.GetOIDCSessionRefresh(ctx, sid); ok {
		t.Fatal("GetOIDCSessionRefresh must skip an expired session")
	}
}
