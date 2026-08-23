// github_token_test.go — stdlib coverage of the encrypted GitHub token storage
// layer (boom-2ip Phase 1). Mirrors wakatime_key_test.go: every subtest names a
// security invariant and, where we care whether encryption ACTUALLY happened,
// pins it end-to-end (seal → store → SELECT raw bytes → assert bytes !=
// plaintext AND decrypt-back == plaintext).
//
// Reuses the inline testAEAD helper + openTestDB + newSender from the sibling
// _test.go files in this package (no internal/auth import — auth imports db, so
// the reverse would cycle).
package db

import (
	"bytes"
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func rawGithubBlob(t *testing.T, d *DB, ctx context.Context, username string) []byte {
	t.Helper()
	var blob []byte
	if err := d.Pool.QueryRow(ctx,
		`SELECT encrypted_github_token FROM users WHERE username=$1`, username).Scan(&blob); err != nil {
		t.Fatalf("read raw github blob: %v", err)
	}
	return blob
}

func TestGithubToken(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("SecurityInvariant_EncryptionActuallyHappens_stored_bytes_are_not_plaintext_but_decrypt_back", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		f := newSender(t, d, "gh_enc")
		user := f.Sender()

		plaintext := []byte("gho_secret_plaintext_" + user)
		ciphertext := aead.Seal(t, plaintext)

		if err := d.SetEncryptedGithubToken(ctx, user, ciphertext, "octocat", GithubTokenStatusValid); err != nil {
			t.Fatalf("SetEncryptedGithubToken: %v", err)
		}

		got := rawGithubBlob(t, d, ctx, user)
		if bytes.Contains(got, plaintext) {
			t.Fatalf("stored blob contains plaintext — encryption did not happen")
		}
		if !bytes.Equal(got, ciphertext) {
			t.Fatalf("stored blob != ciphertext we wrote; got=%x want=%x", got, ciphertext)
		}
		dec, err := aead.Open(got)
		if err != nil {
			t.Fatalf("Decrypt(stored blob): %v", err)
		}
		if !bytes.Equal(dec, plaintext) {
			t.Fatalf("Decrypt roundtrip mismatch: got %q want %q", dec, plaintext)
		}
	})

	t.Run("SecurityInvariant_GetGithubTokenInfo_never_returns_ciphertext_only_status_and_login", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		f := newSender(t, d, "gh_info")
		user := f.Sender()

		ct := aead.Seal(t, []byte("plaintext-token"))
		if err := d.SetEncryptedGithubToken(ctx, user, ct, "myhandle", GithubTokenStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}

		info, err := d.GetGithubTokenInfo(ctx, user)
		if err != nil {
			t.Fatalf("GetGithubTokenInfo: %v", err)
		}
		// The struct type structurally CANNOT carry the ciphertext — this
		// subtest documents that invariant + pins the non-secret fields.
		if !info.Connected {
			t.Fatal("expected Connected=true right after Set")
		}
		if info.Login == nil || *info.Login != "myhandle" {
			t.Fatalf("login=%v want=myhandle", info.Login)
		}
		if info.Status == nil || *info.Status != string(GithubTokenStatusValid) {
			t.Fatalf("status=%v want=valid", info.Status)
		}
		if info.CheckedAt == nil {
			t.Fatal("checked_at must be stamped on Set")
		}
	})

	t.Run("SecurityInvariant_GetEncryptedGithubToken_returns_blob_for_internal_use", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		f := newSender(t, d, "gh_fetch")
		user := f.Sender()

		ct := aead.Seal(t, []byte("internal-fetch"))
		if err := d.SetEncryptedGithubToken(ctx, user, ct, "h", GithubTokenStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}
		blob, ok, err := d.GetEncryptedGithubToken(ctx, user)
		if err != nil || !ok {
			t.Fatalf("GetEncryptedGithubToken: ok=%v err=%v", ok, err)
		}
		if !bytes.Equal(blob, ct) {
			t.Fatal("internal fetch returned wrong ciphertext")
		}
	})

	t.Run("SecurityInvariant_UpdateStatusInvalid_does_not_touch_ciphertext_or_login", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		f := newSender(t, d, "gh_status_flip")
		user := f.Sender()

		ct := aead.Seal(t, []byte("orig"))
		if err := d.SetEncryptedGithubToken(ctx, user, ct, "keepme", GithubTokenStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}
		before := rawGithubBlob(t, d, ctx, user)

		if err := d.UpdateGithubTokenStatus(ctx, user, GithubTokenStatusInvalid); err != nil {
			t.Fatalf("UpdateGithubTokenStatus: %v", err)
		}
		after := rawGithubBlob(t, d, ctx, user)
		if !bytes.Equal(before, after) {
			t.Fatalf("ciphertext mutated by status update! before=%x after=%x", before, after)
		}
		info, err := d.GetGithubTokenInfo(ctx, user)
		if err != nil {
			t.Fatalf("info: %v", err)
		}
		if info.Status == nil || *info.Status != string(GithubTokenStatusInvalid) {
			t.Fatalf("status not updated to invalid: %v", info.Status)
		}
		if info.Login == nil || *info.Login != "keepme" {
			t.Fatalf("login clobbered by status update: %v", info.Login)
		}
	})

	t.Run("SecurityInvariant_UpdateStatus_on_user_without_token_is_noop", func(t *testing.T) {
		f := newSender(t, d, "gh_status_noop")
		user := f.Sender()
		if err := d.UpdateGithubTokenStatus(ctx, user, GithubTokenStatusInvalid); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		info, err := d.GetGithubTokenInfo(ctx, user)
		if err != nil {
			t.Fatalf("info: %v", err)
		}
		if info.Status != nil {
			t.Fatalf("status set for a user w/ no token: %v (poisoned row)", *info.Status)
		}
	})

	t.Run("SecurityInvariant_CrossUserIsolation_GetEncrypted_only_returns_owners_ciphertext", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		fA := newSender(t, d, "gh_iso_a")
		fB := newSender(t, d, "gh_iso_b")
		userA, userB := fA.Sender(), fB.Sender()

		ctA := aead.Seal(t, []byte("token-A"))
		ctB := aead.Seal(t, []byte("token-B"))
		if err := d.SetEncryptedGithubToken(ctx, userA, ctA, "a", GithubTokenStatusValid); err != nil {
			t.Fatalf("Set A: %v", err)
		}
		if err := d.SetEncryptedGithubToken(ctx, userB, ctB, "b", GithubTokenStatusValid); err != nil {
			t.Fatalf("Set B: %v", err)
		}
		gotA, okA, err := d.GetEncryptedGithubToken(ctx, userA)
		if err != nil || !okA {
			t.Fatalf("Get A: err=%v ok=%v", err, okA)
		}
		if !bytes.Equal(gotA, ctA) || bytes.Equal(gotA, ctB) {
			t.Fatal("SECURITY LEAK: Get(A) returned data that is not exclusively A's ciphertext")
		}
	})

	t.Run("SecurityInvariant_GetInfoOnUnknownUser_returns_disconnected_no_error", func(t *testing.T) {
		info, err := d.GetGithubTokenInfo(ctx, "nonexistent_gh_user_xyz_1234567890")
		if err != nil {
			t.Fatalf("expected nil err on missing user, got %v", err)
		}
		if info.Connected {
			t.Fatal("expected Connected=false on missing user")
		}
	})

	t.Run("SecurityInvariant_SetOnUnknownUser_returns_ErrNoRows", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		ct := aead.Seal(t, []byte("orphan"))
		err := d.SetEncryptedGithubToken(ctx, "definitely_not_a_gh_user_9999", ct, "x", GithubTokenStatusValid)
		if err != pgx.ErrNoRows {
			t.Fatalf("expected pgx.ErrNoRows, got %v", err)
		}
	})

	t.Run("SecurityInvariant_SetRejectsEmptyCiphertext_and_EmptyUsername", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		f := newSender(t, d, "gh_empty")
		user := f.Sender()
		if err := d.SetEncryptedGithubToken(ctx, user, nil, "x", GithubTokenStatusValid); err == nil {
			t.Fatal("expected error on empty ciphertext")
		}
		if err := d.SetEncryptedGithubToken(ctx, "", aead.Seal(t, []byte("x")), "x", GithubTokenStatusValid); err == nil {
			t.Fatal("expected error on empty username")
		}
	})

	t.Run("SecurityInvariant_Clear_nulls_token_login_status_and_checked_at_together", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		f := newSender(t, d, "gh_clear")
		user := f.Sender()

		if err := d.SetEncryptedGithubToken(ctx, user, aead.Seal(t, []byte("x")), "gone", GithubTokenStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := d.ClearEncryptedGithubToken(ctx, user); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		info, err := d.GetGithubTokenInfo(ctx, user)
		if err != nil {
			t.Fatalf("info: %v", err)
		}
		if info.Connected {
			t.Fatal("Connected should be false after Clear")
		}
		var (
			blob      []byte
			login     *string
			status    *string
			checkedAt *string
		)
		if err := d.Pool.QueryRow(ctx,
			`SELECT encrypted_github_token, github_login, github_token_status, github_token_checked_at::text FROM users WHERE username=$1`,
			user).Scan(&blob, &login, &status, &checkedAt); err != nil {
			t.Fatalf("read: %v", err)
		}
		if blob != nil || login != nil || status != nil || checkedAt != nil {
			t.Fatalf("Clear did not null every column: blob=%v login=%v status=%v checkedAt=%v", blob, login, status, checkedAt)
		}
	})

	t.Run("SecurityInvariant_ClearRejectsEmptyUsername_and_is_idempotent", func(t *testing.T) {
		if err := d.ClearEncryptedGithubToken(ctx, ""); err == nil {
			t.Fatal("expected error on empty username")
		}
		f := newSender(t, d, "gh_clear_noop")
		if err := d.ClearEncryptedGithubToken(ctx, f.Sender()); err != nil {
			t.Fatalf("Clear on missing token: %v", err)
		}
	})

	t.Run("SecurityInvariant_List_only_returns_rows_with_ciphertext", func(t *testing.T) {
		aead, _ := newTestAEAD(t)
		fWith := newSender(t, d, "gh_list_with")
		fWithout := newSender(t, d, "gh_list_without")
		userWith, userWithout := fWith.Sender(), fWithout.Sender()

		ct := aead.Seal(t, []byte("plaintext-list"))
		if err := d.SetEncryptedGithubToken(ctx, userWith, ct, "h", GithubTokenStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}
		rows, err := d.ListEncryptedGithubTokens(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		sawWith, sawWithout := false, false
		for _, r := range rows {
			if r.Username == userWith {
				sawWith = true
				if !bytes.Equal(r.Ciphertext, ct) {
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
			t.Errorf("List INCLUDED user %s with NULL ciphertext", userWithout)
		}
	})

	t.Run("SecurityInvariant_Rotate_transactional_reencryption_across_users", func(t *testing.T) {
		oldAEAD, _ := newTestAEAD(t)
		newAEAD, _ := newTestAEAD(t)
		fA := newSender(t, d, "gh_rot_a")
		fB := newSender(t, d, "gh_rot_b")
		userA, userB := fA.Sender(), fB.Sender()

		ptA := []byte("A-token-secret")
		ptB := []byte("B-token-secret")
		if err := d.SetEncryptedGithubToken(ctx, userA, oldAEAD.Seal(t, ptA), "a", GithubTokenStatusValid); err != nil {
			t.Fatalf("Set A: %v", err)
		}
		if err := d.SetEncryptedGithubToken(ctx, userB, oldAEAD.Seal(t, ptB), "b", GithubTokenStatusValid); err != nil {
			t.Fatalf("Set B: %v", err)
		}

		listed, err := d.ListEncryptedGithubTokens(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		newRows := make([]EncryptedGithubTokenRow, 0, len(listed))
		for _, r := range listed {
			if r.Username != userA && r.Username != userB {
				continue
			}
			pt, err := oldAEAD.Open(r.Ciphertext)
			if err != nil {
				t.Fatalf("decrypt %s under OLD: %v", r.Username, err)
			}
			newRows = append(newRows, EncryptedGithubTokenRow{Username: r.Username, Ciphertext: newAEAD.Seal(t, pt)})
		}
		n, err := d.RotateEncryptedGithubTokens(ctx, newRows)
		if err != nil {
			t.Fatalf("Rotate: %v", err)
		}
		if n != 2 {
			t.Fatalf("rotate updated=%d want=2", n)
		}
		for _, name := range []string{userA, userB} {
			raw := rawGithubBlob(t, d, ctx, name)
			if _, err := oldAEAD.Open(raw); err == nil {
				t.Fatalf("%s: still decrypts under OLD — rotation not applied", name)
			}
			pt, err := newAEAD.Open(raw)
			if err != nil {
				t.Fatalf("%s: does not decrypt under NEW: %v", name, err)
			}
			want := ptA
			if name == userB {
				want = ptB
			}
			if !bytes.Equal(pt, want) {
				t.Fatalf("%s: plaintext lost across rotation: got %q want %q", name, pt, want)
			}
		}
	})

	t.Run("SecurityInvariant_Rotate_all_or_nothing_bad_input_leaves_rows_untouched", func(t *testing.T) {
		oldAEAD, _ := newTestAEAD(t)
		f := newSender(t, d, "gh_rot_atomic")
		user := f.Sender()
		orig := oldAEAD.Seal(t, []byte("original"))
		if err := d.SetEncryptedGithubToken(ctx, user, orig, "h", GithubTokenStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}
		bad := []EncryptedGithubTokenRow{
			{Username: user, Ciphertext: oldAEAD.Seal(t, []byte("new"))},
			{Username: user, Ciphertext: nil},
		}
		if _, err := d.RotateEncryptedGithubTokens(ctx, bad); err == nil {
			t.Fatal("expected error on empty-ciphertext input to Rotate")
		}
		if got := rawGithubBlob(t, d, ctx, user); !bytes.Equal(got, orig) {
			t.Fatal("Rotate partially applied — atomicity broken")
		}
	})

	t.Run("SecurityInvariant_RotateEncryptedSecrets_commits_both_columns_together", func(t *testing.T) {
		oldAEAD, _ := newTestAEAD(t)
		newAEAD, _ := newTestAEAD(t)
		f := newSender(t, d, "gh_rot_both")
		user := f.Sender()

		wkPT := []byte("wk-secret")
		ghPT := []byte("gh-secret")
		if err := d.SetEncryptedWakatimeKey(ctx, user, oldAEAD.Seal(t, wkPT), WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set wk: %v", err)
		}
		if err := d.SetEncryptedGithubToken(ctx, user, oldAEAD.Seal(t, ghPT), "h", GithubTokenStatusValid); err != nil {
			t.Fatalf("Set gh: %v", err)
		}

		wkRows := []EncryptedWakatimeKeyRow{{Username: user, Ciphertext: newAEAD.Seal(t, wkPT)}}
		ghRows := []EncryptedGithubTokenRow{{Username: user, Ciphertext: newAEAD.Seal(t, ghPT)}}
		wkN, ghN, err := d.RotateEncryptedSecrets(ctx, wkRows, ghRows)
		if err != nil {
			t.Fatalf("RotateEncryptedSecrets: %v", err)
		}
		if wkN != 1 || ghN != 1 {
			t.Fatalf("counts wkN=%d ghN=%d want 1,1", wkN, ghN)
		}
		// Both columns now decrypt under NEW.
		wkRaw := rawWakatimeBlob(t, d, ctx, user)
		if pt, err := newAEAD.Open(wkRaw); err != nil || !bytes.Equal(pt, wkPT) {
			t.Fatalf("wakatime not rotated under NEW: pt=%q err=%v", pt, err)
		}
		ghRaw := rawGithubBlob(t, d, ctx, user)
		if pt, err := newAEAD.Open(ghRaw); err != nil || !bytes.Equal(pt, ghPT) {
			t.Fatalf("github not rotated under NEW: pt=%q err=%v", pt, err)
		}
	})

	t.Run("SecurityInvariant_RotateEncryptedSecrets_atomic_github_failure_rolls_back_wakatime", func(t *testing.T) {
		oldAEAD, _ := newTestAEAD(t)
		newAEAD, _ := newTestAEAD(t)
		f := newSender(t, d, "gh_rot_rollback")
		user := f.Sender()

		wkOrig := oldAEAD.Seal(t, []byte("wk-orig"))
		if err := d.SetEncryptedWakatimeKey(ctx, user, wkOrig, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set wk: %v", err)
		}
		if err := d.SetEncryptedGithubToken(ctx, user, oldAEAD.Seal(t, []byte("gh-orig")), "h", GithubTokenStatusValid); err != nil {
			t.Fatalf("Set gh: %v", err)
		}

		// A valid wakatime rewrite paired with a BAD github row (empty ct) must
		// abort the whole tx — the wakatime column must still hold its original.
		wkRows := []EncryptedWakatimeKeyRow{{Username: user, Ciphertext: newAEAD.Seal(t, []byte("wk-new"))}}
		ghRows := []EncryptedGithubTokenRow{{Username: user, Ciphertext: nil}}
		if _, _, err := d.RotateEncryptedSecrets(ctx, wkRows, ghRows); err == nil {
			t.Fatal("expected error when a github row is invalid")
		}
		if got := rawWakatimeBlob(t, d, ctx, user); !bytes.Equal(got, wkOrig) {
			t.Fatal("wakatime column was written despite github failure — atomicity broken")
		}
	})
}
