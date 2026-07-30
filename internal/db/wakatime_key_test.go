// wakatime_key_test.go — stdlib (testing.T) coverage of the encrypted
// Wakatime key storage layer (gaka-se2.9). Every subtest names one
// security invariant and, wherever we care whether encryption ACTUALLY
// happened, pins it end-to-end: seal plaintext under an AES-256-GCM AEAD
// (built from a locally-generated 32-byte key), write the ciphertext,
// SELECT the raw column bytes, assert bytes != plaintext AND
// Decrypt(bytes) == plaintext. See CLAUDE.md "Encryption at Rest".
//
// We deliberately do NOT import internal/auth (auth imports db, so the
// reverse would cycle) — instead a small inline AES-GCM helper matches the
// production layout (nonce || sealed).
package db

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"
	"testing"

	"github.com/jackc/pgx/v5"
)

// -- stdlib DB harness (mirrors openTestDBG for testing.T) --

// openTestDB opens the shared isolated boomtime_test database provisioned by
// TestMain. Skips the current test if the DB is unavailable (mirrors ginkgo
// helper openTestDBG). Closes the pool with t.Cleanup.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	if !dbReady {
		t.Skipf("skipping: isolated test database unavailable: %s", dbSkipMsg)
	}
	d, err := New(context.Background(), testDatabaseURL())
	if err != nil {
		t.Skipf("skipping: could not open %s: %v", testDBName, err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// -- inline AES-GCM helper (mirrors internal/auth.Encrypt / Decrypt without
// creating an import cycle). Layout: nonce (12B) || sealed (ct || tag). --

type testAEAD struct{ a cipher.AEAD }

// newTestAEAD builds an AES-256-GCM AEAD from a locally-generated 32-byte
// key and returns both the base64-encoded key (in case a subtest wants to
// exercise the BOOM_ENCRYPTION_KEY env path) and the AEAD.
func newTestAEAD(t *testing.T) (*testAEAD, string) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("gen key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	return &testAEAD{a: aead}, base64.StdEncoding.EncodeToString(key)
}

// Seal produces nonce||ciphertext||tag with a fresh random nonce.
func (a *testAEAD) Seal(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	nonce := make([]byte, a.a.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatalf("gen nonce: %v", err)
	}
	sealed := a.a.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out
}

// Open reverses Seal (returns error, mirroring auth.Decrypt).
func (a *testAEAD) Open(ciphertext []byte) ([]byte, error) {
	n := a.a.NonceSize()
	if len(ciphertext) <= n {
		return nil, io.ErrUnexpectedEOF
	}
	return a.a.Open(nil, ciphertext[:n], ciphertext[n:], nil)
}

// -- helpers for reading raw column state --

func rawWakatimeBlob(t *testing.T, d *DB, ctx context.Context, username string) []byte {
	t.Helper()
	var blob []byte
	if err := d.Pool.QueryRow(ctx,
		`SELECT encrypted_wakatime_key FROM users WHERE username=$1`, username).Scan(&blob); err != nil {
		t.Fatalf("read raw blob: %v", err)
	}
	return blob
}

func rawWakatimeStatus(t *testing.T, d *DB, ctx context.Context, username string) *string {
	t.Helper()
	var s *string
	if err := d.Pool.QueryRow(ctx,
		`SELECT wakatime_key_status FROM users WHERE username=$1`, username).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return s
}

// -- tests --

func TestWakatimeKey(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("SecurityInvariant_EncryptionActuallyHappens_stored_bytes_are_not_plaintext_but_decrypt_back", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		f := newSender(t, d, "wk_enc")
		user := f.Sender()

		plaintext := []byte("waka_secret_plaintext_" + user)
		ciphertext := aead.Seal(t, plaintext)

		if err := d.SetEncryptedWakatimeKey(ctx, user, ciphertext, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("SetEncryptedWakatimeKey: %v", err)
		}

		got := rawWakatimeBlob(t, d, ctx, user)
		if bytes.Contains(got, plaintext) {
			t.Fatalf("stored blob contains plaintext — encryption did not happen")
		}
		if bytes.Equal(got, ciphertext) == false {
			t.Fatalf("stored blob != ciphertext we wrote (want round-trip pinning); got=%x want=%x", got, ciphertext)
		}
		dec, err := aead.Open(got)
		if err != nil {
			t.Fatalf("Decrypt(stored blob): %v", err)
		}
		if !bytes.Equal(dec, plaintext) {
			t.Fatalf("Decrypt roundtrip mismatch: got %q want %q", dec, plaintext)
		}
	})

	t.Run("SecurityInvariant_StatusCoherence_status_valid_ciphertext_present", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		f := newSender(t, d, "wk_status_valid")
		user := f.Sender()

		ct := aead.Seal(t, []byte("plaintext"))
		if err := d.SetEncryptedWakatimeKey(ctx, user, ct, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}

		info, err := d.GetWakatimeKeyInfo(ctx, user)
		if err != nil {
			t.Fatalf("GetWakatimeKeyInfo: %v", err)
		}
		if !info.HasSavedKey {
			t.Fatal("expected HasSavedKey=true right after Set")
		}
		if info.Status == nil || *info.Status != string(WakatimeKeyStatusValid) {
			t.Fatalf("status=%v want=valid", info.Status)
		}
		if info.CheckedAt == nil {
			t.Fatal("checked_at must be stamped on Set")
		}
	})

	t.Run("SecurityInvariant_UpdateStatusInvalid_does_not_touch_ciphertext", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		f := newSender(t, d, "wk_status_flip")
		user := f.Sender()

		ct := aead.Seal(t, []byte("orig-plaintext"))
		if err := d.SetEncryptedWakatimeKey(ctx, user, ct, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}
		before := rawWakatimeBlob(t, d, ctx, user)

		if err := d.UpdateWakatimeKeyStatus(ctx, user, WakatimeKeyStatusInvalid); err != nil {
			t.Fatalf("UpdateWakatimeKeyStatus: %v", err)
		}
		after := rawWakatimeBlob(t, d, ctx, user)
		if !bytes.Equal(before, after) {
			t.Fatalf("ciphertext mutated by status update! before=%x after=%x", before, after)
		}
		st := rawWakatimeStatus(t, d, ctx, user)
		if st == nil || *st != string(WakatimeKeyStatusInvalid) {
			t.Fatalf("status not updated to invalid: got %v", st)
		}
	})

	t.Run("SecurityInvariant_UpdateStatus_on_user_without_key_is_noop_no_error", func(t *testing.T) {
		// Explicitly documented behavior in wakatime_key.go: silent no-op so a
		// buggy caller can't poison an unrelated row (users without a key
		// should not gain a status).
		f := newSender(t, d, "wk_status_noop")
		user := f.Sender()

		if err := d.UpdateWakatimeKeyStatus(ctx, user, WakatimeKeyStatusInvalid); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if s := rawWakatimeStatus(t, d, ctx, user); s != nil {
			t.Fatalf("status set for a user w/ no ciphertext: %v (poisoned row)", *s)
		}
	})

	t.Run("SecurityInvariant_CrossUserIsolation_Get_only_returns_owners_ciphertext", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		fA := newSender(t, d, "wk_iso_a")
		fB := newSender(t, d, "wk_iso_b")
		userA, userB := fA.Sender(), fB.Sender()

		ctA := aead.Seal(t, []byte("plaintext-A"))
		ctB := aead.Seal(t, []byte("plaintext-B"))

		if err := d.SetEncryptedWakatimeKey(ctx, userA, ctA, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set A: %v", err)
		}
		if err := d.SetEncryptedWakatimeKey(ctx, userB, ctB, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set B: %v", err)
		}

		gotA, okA, err := d.GetEncryptedWakatimeKey(ctx, userA)
		if err != nil || !okA {
			t.Fatalf("Get A: err=%v ok=%v", err, okA)
		}
		if !bytes.Equal(gotA, ctA) {
			t.Fatal("Get(A) returned data that is not A's ciphertext")
		}
		if bytes.Equal(gotA, ctB) {
			t.Fatal("SECURITY LEAK: Get(A) returned B's ciphertext")
		}

		gotB, okB, err := d.GetEncryptedWakatimeKey(ctx, userB)
		if err != nil || !okB {
			t.Fatalf("Get B: err=%v ok=%v", err, okB)
		}
		if !bytes.Equal(gotB, ctB) {
			t.Fatal("Get(B) returned data that is not B's ciphertext")
		}
	})

	t.Run("SecurityInvariant_GetOnUnknownUser_returns_ok_false_no_error", func(t *testing.T) {
		_, ok, err := d.GetEncryptedWakatimeKey(ctx, "nonexistent_user_xyz_1234567890")
		if err != nil {
			t.Fatalf("expected nil err on missing user, got %v", err)
		}
		if ok {
			t.Fatal("expected ok=false on missing user")
		}
	})

	t.Run("SecurityInvariant_SetOnUnknownUser_returns_ErrNoRows", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		ct := aead.Seal(t, []byte("orphan"))
		err := d.SetEncryptedWakatimeKey(ctx, "definitely_not_a_user_xyz_9999", ct, WakatimeKeyStatusValid)
		if err == nil {
			t.Fatal("expected error on FK-miss user, got nil")
		}
		if err != pgx.ErrNoRows {
			// SetEncryptedWakatimeKey wraps a 0-rows update as pgx.ErrNoRows.
			t.Fatalf("expected pgx.ErrNoRows, got %v", err)
		}
	})

	t.Run("SecurityInvariant_SetRejectsEmptyCiphertext_no_silent_null", func(t *testing.T) {
		// Empty ciphertext must NOT reach the DB — the "clear" path is a
		// separate function so operators cannot accidentally NULL out every
		// key via a Set(empty).
		f := newSender(t, d, "wk_empty_ct")
		user := f.Sender()
		err := d.SetEncryptedWakatimeKey(ctx, user, nil, WakatimeKeyStatusValid)
		if err == nil {
			t.Fatal("expected error on empty ciphertext, got nil")
		}
	})

	t.Run("SecurityInvariant_SetRejectsEmptyUsername", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		err := d.SetEncryptedWakatimeKey(ctx, "", aead.Seal(t, []byte("x")), WakatimeKeyStatusValid)
		if err == nil {
			t.Fatal("expected error on empty username")
		}
	})

	t.Run("SecurityInvariant_UpdateStatusRejectsEmptyUsername", func(t *testing.T) {
		if err := d.UpdateWakatimeKeyStatus(ctx, "", WakatimeKeyStatusInvalid); err == nil {
			t.Fatal("expected error on empty username")
		}
	})

	t.Run("SecurityInvariant_ClearRejectsEmptyUsername", func(t *testing.T) {
		if err := d.ClearEncryptedWakatimeKey(ctx, ""); err == nil {
			t.Fatal("expected error on empty username")
		}
	})

	t.Run("SecurityInvariant_Clear_nulls_ciphertext_status_and_checked_at_together", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		f := newSender(t, d, "wk_clear")
		user := f.Sender()

		if err := d.SetEncryptedWakatimeKey(ctx, user, aead.Seal(t, []byte("x")), WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}

		if err := d.ClearEncryptedWakatimeKey(ctx, user); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		info, err := d.GetWakatimeKeyInfo(ctx, user)
		if err != nil {
			t.Fatalf("GetWakatimeKeyInfo: %v", err)
		}
		if info.HasSavedKey {
			t.Fatal("HasSavedKey should be false after Clear")
		}
		// Assert all three columns are NULL — a stale status without ciphertext
		// is exactly the "presence probe returns wrong answer" bug we prevent.
		var (
			blob      []byte
			status    *string
			checkedAt *string
		)
		if err := d.Pool.QueryRow(ctx,
			`SELECT encrypted_wakatime_key, wakatime_key_status, wakatime_key_checked_at::text FROM users WHERE username=$1`,
			user).Scan(&blob, &status, &checkedAt); err != nil {
			t.Fatalf("read: %v", err)
		}
		if blob != nil || status != nil || checkedAt != nil {
			t.Fatalf("Clear did not null every column: blob=%v status=%v checkedAt=%v", blob, status, checkedAt)
		}
	})

	t.Run("SecurityInvariant_ClearOnMissingKey_is_idempotent", func(t *testing.T) {
		f := newSender(t, d, "wk_clear_noop")
		user := f.Sender()
		// Should be a no-op, not an error.
		if err := d.ClearEncryptedWakatimeKey(ctx, user); err != nil {
			t.Fatalf("Clear on missing key: %v", err)
		}
	})

	t.Run("SecurityInvariant_ListEncryptedWakatimeKeys_only_returns_rows_with_ciphertext", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		fWith := newSender(t, d, "wk_list_with")
		fWithout := newSender(t, d, "wk_list_without")
		userWith := fWith.Sender()
		userWithout := fWithout.Sender()

		ctA := aead.Seal(t, []byte("plaintext-list"))
		if err := d.SetEncryptedWakatimeKey(ctx, userWith, ctA, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}

		rows, err := d.ListEncryptedWakatimeKeys(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		sawWith, sawWithout := false, false
		for _, r := range rows {
			if r.Username == userWith {
				sawWith = true
				if !bytes.Equal(r.Ciphertext, ctA) {
					t.Errorf("List returned wrong ciphertext for %s", r.Username)
				}
			}
			if r.Username == userWithout {
				sawWithout = true
			}
		}
		if !sawWith {
			t.Errorf("List missing user %s (has ciphertext)", userWith)
		}
		if sawWithout {
			t.Errorf("List INCLUDED user %s that has NULL ciphertext", userWithout)
		}
	})

	t.Run("SecurityInvariant_Rotate_transactional_updates_reencryption_across_multiple_users", func(t *testing.T) {
		oldAEAD, _ := newTestAEAD(t)
		newAEAD, _ := newTestAEAD(t)

		fA := newSender(t, d, "wk_rot_a")
		fB := newSender(t, d, "wk_rot_b")
		userA, userB := fA.Sender(), fB.Sender()

		ptA := []byte("A-plaintext-secret")
		ptB := []byte("B-plaintext-secret")
		ctAOld := oldAEAD.Seal(t, ptA)
		ctBOld := oldAEAD.Seal(t, ptB)

		if err := d.SetEncryptedWakatimeKey(ctx, userA, ctAOld, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set A: %v", err)
		}
		if err := d.SetEncryptedWakatimeKey(ctx, userB, ctBOld, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set B: %v", err)
		}

		// Simulate the rotate command: List -> Decrypt(OLD) -> Encrypt(NEW) ->
		// RotateEncryptedWakatimeKeys(NEW rows in one tx).
		listed, err := d.ListEncryptedWakatimeKeys(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		newRows := make([]EncryptedWakatimeKeyRow, 0, len(listed))
		for _, r := range listed {
			if r.Username != userA && r.Username != userB {
				continue // ignore rows from other tests
			}
			pt, err := oldAEAD.Open(r.Ciphertext)
			if err != nil {
				t.Fatalf("decrypt %s under OLD: %v", r.Username, err)
			}
			newRows = append(newRows, EncryptedWakatimeKeyRow{
				Username:   r.Username,
				Ciphertext: newAEAD.Seal(t, pt),
			})
		}
		n, err := d.RotateEncryptedWakatimeKeys(ctx, newRows)
		if err != nil {
			t.Fatalf("Rotate: %v", err)
		}
		if n != 2 {
			t.Fatalf("rotate updated=%d want=2", n)
		}

		// After rotation: raw bytes decrypt under NEW, NOT under OLD, and
		// plaintext is preserved.
		for _, name := range []string{userA, userB} {
			raw := rawWakatimeBlob(t, d, ctx, name)
			if _, err := oldAEAD.Open(raw); err == nil {
				t.Fatalf("%s: still decrypts under OLD — rotation not applied", name)
			}
			pt, err := newAEAD.Open(raw)
			if err != nil {
				t.Fatalf("%s: does not decrypt under NEW: %v", name, err)
			}
			wantPT := ptA
			if name == userB {
				wantPT = ptB
			}
			if !bytes.Equal(pt, wantPT) {
				t.Fatalf("%s: plaintext lost across rotation: got %q want %q", name, pt, wantPT)
			}
		}
	})

	t.Run("SecurityInvariant_Rotate_all_or_nothing_bad_input_leaves_every_row_untouched", func(t *testing.T) {
		oldAEAD, _ := newTestAEAD(t)
		f := newSender(t, d, "wk_rot_atomic")
		user := f.Sender()

		orig := oldAEAD.Seal(t, []byte("original"))
		if err := d.SetEncryptedWakatimeKey(ctx, user, orig, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}

		// One valid, one empty ciphertext — the empty one must abort the
		// whole tx BEFORE the valid row commits.
		newCT := oldAEAD.Seal(t, []byte("new"))
		bad := []EncryptedWakatimeKeyRow{
			{Username: user, Ciphertext: newCT},
			{Username: user, Ciphertext: nil},
		}
		if _, err := d.RotateEncryptedWakatimeKeys(ctx, bad); err == nil {
			t.Fatal("expected error on empty-ciphertext input to Rotate")
		}
		// Row must still hold the ORIGINAL ciphertext — proof of rollback.
		got := rawWakatimeBlob(t, d, ctx, user)
		if !bytes.Equal(got, orig) {
			t.Fatal("Rotate partially applied — atomicity broken")
		}

		bad2 := []EncryptedWakatimeKeyRow{
			{Username: user, Ciphertext: newCT},
			{Username: "", Ciphertext: newCT},
		}
		if _, err := d.RotateEncryptedWakatimeKeys(ctx, bad2); err == nil {
			t.Fatal("expected error on empty-username input to Rotate")
		}
		got = rawWakatimeBlob(t, d, ctx, user)
		if !bytes.Equal(got, orig) {
			t.Fatal("Rotate partially applied (empty username) — atomicity broken")
		}
	})

	t.Run("SecurityInvariant_Rotate_ignores_users_with_no_ciphertext", func(t *testing.T) {
		// A user w/o a saved key MUST NOT gain one via rotation. The rotate
		// path skips them via `WHERE encrypted_wakatime_key IS NOT NULL`.
		aead, _ := newTestAEAD(t)
		f := newSender(t, d, "wk_rot_noct")
		user := f.Sender()

		// No SetEncryptedWakatimeKey call. Rotate is passed a fake row for this
		// user — the WHERE clause should make the UPDATE a 0-row match.
		junk := aead.Seal(t, []byte("nope"))
		n, err := d.RotateEncryptedWakatimeKeys(ctx, []EncryptedWakatimeKeyRow{
			{Username: user, Ciphertext: junk},
		})
		if err != nil {
			t.Fatalf("Rotate on no-ct user: %v", err)
		}
		if n != 0 {
			t.Fatalf("Rotate updated=%d, want=0 for user w/o ciphertext", n)
		}
		if b := rawWakatimeBlob(t, d, ctx, user); b != nil {
			t.Fatalf("user gained a ciphertext via rotation: %x", b)
		}
	})

	t.Run("SecurityInvariant_Rotate_empty_input_is_noop_no_error", func(t *testing.T) {
		n, err := d.RotateEncryptedWakatimeKeys(ctx, nil)
		if err != nil {
			t.Fatalf("Rotate(nil): %v", err)
		}
		if n != 0 {
			t.Fatalf("Rotate(nil) updated=%d want=0", n)
		}
	})
}
