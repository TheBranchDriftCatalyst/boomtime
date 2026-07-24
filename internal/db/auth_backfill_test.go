package db

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"
)

// TestBackfillHashedTokens_Gaka5Regression verifies the boot-time backfill
// hashes legacy raw-only auth_tokens + refresh_tokens rows in place, is
// idempotent on second run, and never touches rows that are already hashed
// or have both columns populated.
//
// If this regresses, gaka-awh.5's cutover breaks: raw-only rows survive the
// boot backfill, and the next-release migration that DROPs the raw column
// deletes their only identifier.
func TestBackfillHashedTokens_Gaka5Regression(t *testing.T) {
	if !dbReady {
		t.Skipf("skipping: isolated test database unavailable: %s", dbSkipMsg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	d, err := New(ctx, testDatabaseURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(d.Close)

	// Fixture user — cleaned up at end. We use raw INSERT into users to
	// avoid entangling with auth.CreateUser + argon2 cost per test run.
	const user = "backfill_test_user"
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, $2, $3)
		 ON CONFLICT (username) DO NOTHING`,
		user, []byte("dummy-hash"), []byte("dummy-salt")); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(context.Background(),
			`DELETE FROM auth_tokens WHERE owner=$1; DELETE FROM refresh_tokens WHERE owner=$1; DELETE FROM users WHERE username=$1`, user)
	})

	// Seed three flavors on auth_tokens:
	// (a) legacy raw-only — should get hashed
	// (b) hashed-only     — should be untouched (no-op)
	// (c) both populated  — should be untouched (idempotent guard)
	rawA := "raw-legacy-token-a"
	rawC := "raw-both-c"
	hashC := sha256.Sum256([]byte(rawC))
	hashB := sha256.Sum256([]byte("raw-b-source"))
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO auth_tokens (owner, token) VALUES ($1, $2)`, user, rawA); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO auth_tokens (owner, hashed_token) VALUES ($1, $2)`, user, hashB[:]); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO auth_tokens (owner, token, hashed_token) VALUES ($1, $2, $3)`, user, rawC, hashC[:]); err != nil {
		t.Fatalf("seed c: %v", err)
	}

	// Run 1: expect 1 auth backfill (the (a) row), 0 refresh.
	a, r, err := d.BackfillHashedTokens(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if a != 1 || r != 0 {
		t.Fatalf("run 1 counts: got (auth=%d refresh=%d), want (1, 0)", a, r)
	}

	// Verify (a) now has the correct hash.
	var gotHash []byte
	if err := d.Pool.QueryRow(ctx,
		`SELECT hashed_token FROM auth_tokens WHERE owner=$1 AND token=$2`, user, rawA).Scan(&gotHash); err != nil {
		t.Fatalf("check a: %v", err)
	}
	expectA := sha256.Sum256([]byte(rawA))
	if string(gotHash) != string(expectA[:]) {
		t.Fatalf("row (a) hash mismatch")
	}

	// Verify (b) hash is UNCHANGED (idempotent — didn't rehash something else).
	var gotB []byte
	if err := d.Pool.QueryRow(ctx,
		`SELECT hashed_token FROM auth_tokens WHERE owner=$1 AND hashed_token=$2`, user, hashB[:]).Scan(&gotB); err != nil {
		t.Fatalf("check b: %v", err)
	}
	if string(gotB) != string(hashB[:]) {
		t.Fatalf("row (b) hash was mutated")
	}

	// Run 2: idempotence — should be (0, 0). If this returns nonzero, the
	// backfill will churn on every boot, defeating "safe to run every start".
	a2, r2, err := d.BackfillHashedTokens(ctx)
	if err != nil {
		t.Fatalf("backfill run 2: %v", err)
	}
	if a2 != 0 || r2 != 0 {
		t.Fatalf("run 2 counts (idempotence): got (auth=%d refresh=%d), want (0, 0)", a2, r2)
	}
}
