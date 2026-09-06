// rotate_straggler_test.go — regression test for the rotate-encryption-key
// concurrent-writer hole (audit 2026-09-06, composition-config medium;
// cmd/boomtime/rotate.go:15).
//
// The command's operator doc used to bless rotating with boomtime still
// running ("its blob would decrypt fine after cutover"). It would not: a
// secret the live server encrypts under the OLD key AFTER runRotate's
// ListEncryptedColumn snapshot is never rewritten, and the moment
// BOOM_ENCRYPTION_KEY flips to the new key that row is permanently
// undecryptable. The doc is corrected AND the command now detects the case
// (verifyDecryptable) while the operator still holds both keys.
//
// This drives verifyDecryptable directly against a MIXED-KEY population —
// exactly the state a straggling write leaves behind — because there is no way
// to inject a write into the middle of runRotate's snapshot/commit window from
// outside the process.
package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

func TestRotate_VerifyDetectsStragglerWrittenUnderOldKey(t *testing.T) {
	// Isolated DB (own suffix): verifyDecryptable scans the WHOLE population,
	// so it must not see the rotate smoke test's users or anyone else's.
	database := testutil.OpenIsolatedDB(t, "rotateverify")
	ctx := context.Background()
	if _, err := database.Pool.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatalf("clean users: %v", err)
	}

	const (
		oldB64 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
		newB64 = "IAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	)
	oldAEAD, err := auth.NewAEADFromBase64(oldB64)
	if err != nil {
		t.Fatalf("old AEAD: %v", err)
	}
	newAEAD, err := auth.NewAEADFromBase64(newB64)
	if err != nil {
		t.Fatalf("new AEAD: %v", err)
	}

	cols := buildDomainRegistry().EncryptedColumns()
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	// The post-rotation state: everyone rotated to NEW...
	rotated := "rot_ok_" + stamp
	seedUser(t, database, rotated)
	okCT, err := auth.EncryptWith(newAEAD, []byte("rotated-secret"))
	if err != nil {
		t.Fatalf("encrypt under new: %v", err)
	}
	if err := database.SetEncryptedWakatimeKey(ctx, rotated, okCT, db.WakatimeKeyStatusValid); err != nil {
		t.Fatalf("seed rotated row: %v", err)
	}

	// A clean population must verify clean — otherwise every successful
	// rotation would end in a scary false alarm.
	if bad, verr := verifyDecryptable(ctx, database, cols, newAEAD); verr != nil {
		t.Fatalf("verify clean population: %v", verr)
	} else if len(bad) != 0 {
		t.Fatalf("clean population reported stragglers: %v", bad)
	}

	// ...except one user who saved a Wakatime key on the still-running server
	// AFTER the snapshot, so their blob is still under OLD.
	straggler := "rot_straggler_" + stamp
	seedUser(t, database, straggler)
	staleCT, err := auth.EncryptWith(oldAEAD, []byte("saved-during-rotation"))
	if err != nil {
		t.Fatalf("encrypt under old: %v", err)
	}
	if err := database.SetEncryptedWakatimeKey(ctx, straggler, staleCT, db.WakatimeKeyStatusValid); err != nil {
		t.Fatalf("seed straggler row: %v", err)
	}

	bad, verr := verifyDecryptable(ctx, database, cols, newAEAD)
	if verr != nil {
		t.Fatalf("verify mixed population: %v", verr)
	}
	if len(bad) != 1 {
		t.Fatalf("stragglers = %v (len %d), want exactly the one row still encrypted under the OLD key — "+
			"an undetected straggler becomes permanently undecryptable the moment BOOM_ENCRYPTION_KEY flips", bad, len(bad))
	}
	if !strings.Contains(bad[0], straggler) {
		t.Errorf("straggler report %q does not name the affected row (%s) — the operator cannot act on it", bad[0], straggler)
	}
	if !strings.Contains(bad[0], "users.encrypted_wakatime_key") {
		t.Errorf("straggler report %q does not name table.column — the operator cannot tell which secret is at risk", bad[0])
	}
	// The report is printed straight to an operator terminal: it must carry
	// LOCATIONS only, never key material.
	if strings.Contains(bad[0], "saved-during-rotation") {
		t.Error("straggler report leaked the decrypted plaintext")
	}
	if strings.Contains(bad[0], string(staleCT)) {
		t.Error("straggler report leaked the raw ciphertext")
	}
}

// The whole detector rests on runRotate's snapshot NOT being isolated from a
// concurrent writer. Pin the consequence end-to-end: a row written under OLD
// after a completed rotation makes the command fail loudly instead of exiting 0.
func TestRotate_ReportsStragglerInsteadOfSucceedingQuietly(t *testing.T) {
	database := testutil.OpenIsolatedDB(t, "rotatestrag")
	ctx := context.Background()
	if _, err := database.Pool.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatalf("clean users: %v", err)
	}

	const (
		oldB64 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
		newB64 = "IAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	)
	oldAEAD, err := auth.NewAEADFromBase64(oldB64)
	if err != nil {
		t.Fatalf("old AEAD: %v", err)
	}

	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	victim := "rot_live_" + stamp
	seedUser(t, database, victim)
	ct, err := auth.EncryptWith(oldAEAD, []byte("waka-secret"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := database.SetEncryptedWakatimeKey(ctx, victim, ct, db.WakatimeKeyStatusValid); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Rotate OLD -> NEW. Now simulate the live server writing one more secret
	// under OLD after the command finished its snapshot: a SECOND rotation of
	// the same pair must report that row rather than claiming success.
	var buf strings.Builder
	if rerr := runRotate(ctx, isolatedDBURL("rotatestrag"), oldB64, newB64, &buf); rerr != nil {
		t.Fatalf("first rotation: %v", rerr)
	}
	if !strings.Contains(buf.String(), "Verified") {
		t.Errorf("a clean rotation should report verification; got:\n%s", buf.String())
	}

	late := "rot_late_" + stamp
	seedUser(t, database, late)
	lateCT, err := auth.EncryptWith(oldAEAD, []byte("saved-mid-rotation"))
	if err != nil {
		t.Fatalf("encrypt late: %v", err)
	}
	if err := database.SetEncryptedWakatimeKey(ctx, late, lateCT, db.WakatimeKeyStatusValid); err != nil {
		t.Fatalf("seed late: %v", err)
	}

	// Re-listing under --old now fails on the already-rotated rows, which is
	// the pre-existing abort path. The straggler detector's job is the OTHER
	// direction: verify the committed population against NEW.
	cols := buildDomainRegistry().EncryptedColumns()
	newAEAD, err := auth.NewAEADFromBase64(newB64)
	if err != nil {
		t.Fatalf("new AEAD: %v", err)
	}
	bad, verr := verifyDecryptable(ctx, database, cols, newAEAD)
	if verr != nil {
		t.Fatalf("verify: %v", verr)
	}
	if len(bad) != 1 || !strings.Contains(bad[0], late) {
		t.Errorf("stragglers = %v, want exactly the late writer %s — without this the operator flips "+
			"BOOM_ENCRYPTION_KEY and strands that secret silently", bad, late)
	}
}
