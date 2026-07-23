// rotate_test.go: end-to-end smoke test for `boomtime rotate-encryption-key`.
//
// Boots the isolated test DB, seeds two users with real ciphertext under an
// OLD key, runs runRotate with an OLD → NEW pair, and verifies:
//
//   - Every seeded row was updated.
//   - The new ciphertext round-trips under NEW (and NOT under OLD).
//   - Passing the wrong OLD key aborts before ANY row is written.
//
// This is a smoke test in the "does the pipeline plumb together" sense — the
// unit-level guarantees (single-transaction, decrypt-under-wrong-key returns
// an error, base64 length validation) already live in the auth + db packages.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// isolatedDBURL mirrors testutil.OpenIsolatedDB's DSN-mangling so runRotate
// can connect to the same isolated database this test seeds. Kept local
// (rather than exported from testutil) because it's the only caller that
// needs the raw URL — testutil's own tests use the *db.DB directly.
func isolatedDBURL(suffix string) string {
	base := testutil.DatabaseURL() // postgres://test:test@localhost:5432/boomtime_test?...
	// Naive rewrite: swap the last /<name>? segment. The default matches
	// dbNameFromURL's logic in testutil.
	// e.g. postgres://.../boomtime_test?...  →  postgres://.../boomtime_test_<suffix>?...
	q := strings.Index(base, "?")
	if q < 0 {
		q = len(base)
	}
	slash := strings.LastIndex(base[:q], "/")
	if slash < 0 {
		return base // fallback: shouldn't happen with a valid DSN
	}
	return base[:slash+1] + base[slash+1:q] + "_" + suffix + base[q:]
}

// TestRotateSmoke seeds encrypted keys under OLD, runs runRotate, and asserts
// the population is decryptable under NEW (and not under OLD).
func TestRotateSmoke(t *testing.T) {
	// Isolated DB: rotation touches every users row and we don't want other
	// packages' seeded users to be part of the population.
	database := testutil.OpenIsolatedDB(t, "rotate")
	ctx := context.Background()
	// Nuke any leftover users from a previous run — the isolated DB persists
	// between runs on the same machine.
	if _, err := database.Pool.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatalf("clean users: %v", err)
	}

	// Deterministic OLD + NEW keys. Different byte patterns so a mis-decrypted
	// ciphertext under NEW would surface as an auth failure, not incidental
	// success on a matching prefix.
	oldB64 := "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	newB64 := "IAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

	oldAEAD, err := auth.NewAEADFromBase64(oldB64)
	if err != nil {
		t.Fatalf("build old AEAD: %v", err)
	}
	newAEAD, err := auth.NewAEADFromBase64(newB64)
	if err != nil {
		t.Fatalf("build new AEAD: %v", err)
	}

	// Seed two users with distinct plaintexts so a swap-under-wrong-nonce bug
	// would surface as decoded-wrong-plaintext.
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	users := []struct {
		name      string
		plaintext string
	}{
		{name: "rotate_alice_" + stamp, plaintext: "waka_alice_plaintext"},
		{name: "rotate_bob_" + stamp, plaintext: "waka_bob_totally_different"},
	}
	for _, u := range users {
		seedUser(t, database, u.name)
		ct, err := auth.EncryptWith(oldAEAD, []byte(u.plaintext))
		if err != nil {
			t.Fatalf("seed encrypt for %s: %v", u.name, err)
		}
		if err := database.SetEncryptedWakatimeKey(ctx, u.name, ct, db.WakatimeKeyStatusValid); err != nil {
			t.Fatalf("seed SetEncryptedWakatimeKey for %s: %v", u.name, err)
		}
	}

	dsn := isolatedDBURL("rotate")

	// Assert a wrong OLD aborts before ANY row is written. A random 32-byte
	// key different from both real ones — decrypting real ciphertext under it
	// must fail auth.
	bogusB64 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x77}, 32))
	var buf bytes.Buffer
	if err := runRotate(ctx, dsn, bogusB64, newB64, &buf); err == nil {
		t.Fatalf("expected rotate with wrong OLD to fail, got nil error")
	} else if !strings.Contains(err.Error(), "aborting (no rows written)") {
		t.Fatalf("wrong OLD error should mention abort: %v", err)
	}
	// Confirm nothing changed: rows still decrypt under REAL oldAEAD.
	for _, u := range users {
		blob, has, err := database.GetEncryptedWakatimeKey(ctx, u.name)
		if err != nil || !has {
			t.Fatalf("post-abort read for %s: has=%v err=%v", u.name, has, err)
		}
		got, derr := auth.DecryptWith(oldAEAD, blob)
		if derr != nil {
			t.Fatalf("post-abort decrypt with real OLD failed for %s: %v", u.name, derr)
		}
		if string(got) != u.plaintext {
			t.Fatalf("post-abort plaintext mismatch for %s: got %q want %q", u.name, got, u.plaintext)
		}
	}

	// Now the happy path.
	buf.Reset()
	if err := runRotate(ctx, dsn, oldB64, newB64, &buf); err != nil {
		t.Fatalf("rotate happy path: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Rotated") {
		t.Fatalf("rotate output missing summary: %q", out)
	}

	// Every row now decrypts under NEW and NOT under OLD.
	for _, u := range users {
		blob, has, err := database.GetEncryptedWakatimeKey(ctx, u.name)
		if err != nil || !has {
			t.Fatalf("post-rotate read for %s: has=%v err=%v", u.name, has, err)
		}
		if _, derr := auth.DecryptWith(oldAEAD, blob); derr == nil {
			t.Fatalf("post-rotate ciphertext for %s STILL decrypts under OLD — rotation didn't take", u.name)
		}
		got, derr := auth.DecryptWith(newAEAD, blob)
		if derr != nil {
			t.Fatalf("post-rotate decrypt with NEW failed for %s: %v", u.name, derr)
		}
		if string(got) != u.plaintext {
			t.Fatalf("post-rotate plaintext mismatch for %s: got %q want %q", u.name, got, u.plaintext)
		}
	}
}

// seedUser inserts a bare users row (no auth token needed — this test doesn't
// hit the HTTP surface). Registers cleanup so a rerun doesn't accumulate.
func seedUser(t *testing.T, database *db.DB, username string) {
	t.Helper()
	ctx := context.Background()
	hash, salt, err := auth.HashPassword("pw-" + username)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := database.InsertUser(ctx, db.StoredUser{Username: username, HashedPassword: hash, SaltUsed: salt, ArgonVersion: auth.ArgonVersionCurrent}); err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool.Exec(context.Background(),
			`DELETE FROM users WHERE username=$1`, username)
	})
}
