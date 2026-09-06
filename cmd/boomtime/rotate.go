// rotate.go: `boomtime rotate-encryption-key` — one-shot re-encryption of
// every users.encrypted_wakatime_key blob under a new BOOM_ENCRYPTION_KEY.
//
// Why a separate command instead of a hot swap: the singleton AEAD in
// internal/auth is loaded once at boot and cached (sync.Once). A live server
// swap would strand every existing ciphertext (Decrypt fails auth). This
// command runs OFFLINE against the DB, holds both OLD + NEW AEADs in-hand,
// and commits every re-encryption in a single transaction so an interrupted
// run leaves the population coherent (either all rows use the new key or
// none do).
//
// Operator workflow:
//
//  1. Generate the new key: openssl rand -base64 32
//  2. STOP boomtime (or scale to zero). This is a CORRECTNESS REQUIREMENT, not
//     a caution — see "Concurrent writers" below.
//  3. Run: boomtime rotate-encryption-key --old $OLD_B64 --new $NEW_B64
//  4. Update BOOM_ENCRYPTION_KEY in the environment to the new key.
//  5. Start boomtime.
//
// Concurrent writers (audit 2026-09-06). This doc used to say writes during
// rotation were safe because a blob encrypted under OLD "would decrypt fine
// after cutover". That is false, and the failure is silent + permanent:
// runRotate reads the population with ListEncryptedColumn OUTSIDE any
// transaction the writer participates in, so a secret a live server encrypts
// under OLD after that snapshot is either (a) overwritten by the rotation's
// re-encrypted STALE value — the user's save is lost — or (b) never seen at
// all, leaving OLD ciphertext behind that no longer decrypts once the env is
// switched. Exactly the stranding boom-6jm.7 exists to prevent.
//
// Since we cannot stop the operator from ignoring step 2, the command DETECTS
// the case: after the commit it re-reads every registered column and verifies
// each blob decrypts under --new (verifyDecryptable). Any straggler is
// reported by table.column + key while the operator still holds BOTH keys and
// can re-run — instead of surfacing weeks later as an undecryptable secret.
//
// Failure model: if ANY row fails to decrypt under --old (the operator
// supplied the wrong old key, or the row was already re-encrypted, or the
// blob was tampered with), the command aborts BEFORE any write and reports
// the affected username. No partial rotation is possible.
package main

import (
	"context"
	"crypto/cipher"
	"errors"
	"fmt"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/domaincols"
	"github.com/spf13/cobra"
)

func rotateEncryptionKeyCmd() *cobra.Command {
	var oldB64, newB64 string
	cmd := &cobra.Command{
		Use:   "rotate-encryption-key",
		Short: "Re-encrypt every stored user secret under a new BOOM_ENCRYPTION_KEY",
		Long: `Re-encrypt every registered per-user encrypted secret from --old to --new in a
SINGLE transaction across ALL columns. The column set is the internal/domains
registry (wakatime + github + amazon device + any future domain), so a new
domain's secret is never stranded on rotation. Aborts BEFORE any write if any
row fails to decrypt under --old (reports the affected row). See internal/auth
package docs for the threat model + payload layout.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if oldB64 == "" || newB64 == "" {
				return errors.New("both --old and --new are required (base64-encoded 32-byte keys)")
			}
			if oldB64 == newB64 {
				return errors.New("--old and --new are the same key — nothing to rotate")
			}

			cfg := config.Load()
			ctx := context.Background()

			return runRotate(ctx, cfg.DatabaseURL(), oldB64, newB64, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&oldB64, "old", "", "Base64-encoded old key (currently in use)")
	cmd.Flags().StringVar(&newB64, "new", "", "Base64-encoded new key (target)")
	_ = cmd.MarkFlagRequired("old")
	_ = cmd.MarkFlagRequired("new")
	return cmd
}

// runRotate is the extracted body so the smoke test can exercise the full
// pipeline (parse keys, list rows, re-encrypt, commit, count) against an
// in-process DB without shelling through cobra + config.Load.
func runRotate(ctx context.Context, databaseURL, oldB64, newB64 string, out interface{ Write([]byte) (int, error) }) error {
	oldAEAD, err := auth.NewAEADFromBase64(oldB64)
	if err != nil {
		return fmt.Errorf("--old: %w", err)
	}
	newAEAD, err := auth.NewAEADFromBase64(newB64)
	if err != nil {
		return fmt.Errorf("--new: %w", err)
	}

	database, err := db.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("db connect: %w", err)
	}
	defer database.Close()

	// Iterate the DOMAIN REGISTRY: EVERY registered domain's encrypted column
	// (boomtime/wakatime, github, books/amazon+hardcover, + any future domain) is
	// re-encrypted, so a new domain's secret is never stranded on rotation. Decrypt
	// + re-encrypt EVERY row across ALL columns BEFORE any write — a single decrypt
	// failure under --old aborts with the affected row and the DB is left untouched.
	// The registry aggregates each Module's EncryptedColumns() (boom-zp2s P1); the
	// order matches the pre-registry list, so rotation is byte-identical.
	cols := buildDomainRegistry().EncryptedColumns()
	updates := make([]db.EncryptedColumnUpdate, 0, len(cols))
	total := 0
	for _, c := range cols {
		rows, lerr := database.ListEncryptedColumn(ctx, c.Table, c.Column, c.KeyColumn)
		if lerr != nil {
			return fmt.Errorf("list %s.%s: %w", c.Table, c.Column, lerr)
		}
		reenc := make([]db.EncryptedColumnRow, 0, len(rows))
		for _, r := range rows {
			pt, derr := auth.DecryptWith(oldAEAD, r.Ciphertext)
			if derr != nil {
				return fmt.Errorf("decrypt %s (%s=%q) failed under --old: %w — aborting (no rows written)", c.Column, c.KeyColumn, r.Key, derr)
			}
			ct, eerr := auth.EncryptWith(newAEAD, pt)
			if eerr != nil {
				return fmt.Errorf("re-encrypt %s (%s=%q) failed under --new: %w — aborting (no rows written)", c.Column, c.KeyColumn, r.Key, eerr)
			}
			reenc = append(reenc, db.EncryptedColumnRow{Key: r.Key, Ciphertext: ct})
		}
		total += len(reenc)
		updates = append(updates, db.EncryptedColumnUpdate{
			Table: c.Table, Column: c.Column, KeyColumn: c.KeyColumn, Rows: reenc,
		})
	}

	if total == 0 {
		fmt.Fprintln(out, "No encrypted secrets found — nothing to rotate.")
		return nil
	}

	// Single transaction across ALL columns — every ciphertext is rewritten
	// under --new or none is.
	counts, err := database.RotateEncryptedColumns(ctx, updates)
	if err != nil {
		return fmt.Errorf("commit rotation: %w", err)
	}
	for _, c := range cols {
		key := c.Table + "." + c.Column
		fmt.Fprintf(out, "Rotated %d row(s) for %s (domain %s).\n", counts[key], key, c.Domain)
	}

	// Post-commit straggler check. See the "Concurrent writers" section in the
	// package doc: a live server that encrypted a secret under --old after our
	// ListEncryptedColumn snapshot leaves a blob the rotation never rewrote,
	// and that blob becomes permanently undecryptable the moment
	// BOOM_ENCRYPTION_KEY flips. Catch it HERE, while the operator still holds
	// both keys, rather than weeks later at a failed credential read.
	stragglers, verr := verifyDecryptable(ctx, database, cols, newAEAD)
	if verr != nil {
		return fmt.Errorf("post-rotation verification failed (rotation IS committed; re-run to confirm): %w", verr)
	}
	if len(stragglers) > 0 {
		fmt.Fprintf(out, "\nWARNING: %d row(s) do NOT decrypt under --new:\n", len(stragglers))
		for _, s := range stragglers {
			fmt.Fprintf(out, "  - %s\n", s)
		}
		return fmt.Errorf(
			"%d row(s) were written under the OLD key AFTER the rotation snapshot and are NOT rotated "+
				"(a boomtime process was still running): %v — STOP boomtime and re-run "+
				"rotate-encryption-key --old <OLD> --new <NEW>; do NOT set BOOM_ENCRYPTION_KEY to the new "+
				"key until this reports clean, or those secrets become undecryptable",
			len(stragglers), stragglers)
	}

	fmt.Fprintln(out, "Verified: every stored secret decrypts under the new key.")
	fmt.Fprintln(out, "Remember to set BOOM_ENCRYPTION_KEY to the new value before restarting boomtime.")
	return nil
}

// verifyDecryptable re-reads every registered encrypted column and reports the
// rows whose ciphertext does NOT open under aead, as "table.column keycol=key"
// descriptors.
//
// It exists to catch the concurrent-writer hole documented at the top of this
// file, and deliberately reports only LOCATIONS: never the ciphertext, never
// the decrypted plaintext, and never the key material. A caller printing this
// straight to an operator's terminal (runRotate does) must stay safe.
//
// A non-nil error means the verification itself could not run (a DB read
// failed); an empty slice with a nil error means the population is clean.
func verifyDecryptable(ctx context.Context, database *db.DB, cols []domaincols.EncryptedColumn, aead cipher.AEAD) ([]string, error) {
	var bad []string
	for _, c := range cols {
		rows, err := database.ListEncryptedColumn(ctx, c.Table, c.Column, c.KeyColumn)
		if err != nil {
			return nil, fmt.Errorf("re-list %s.%s: %w", c.Table, c.Column, err)
		}
		for _, r := range rows {
			if _, derr := auth.DecryptWith(aead, r.Ciphertext); derr != nil {
				bad = append(bad, fmt.Sprintf("%s.%s (%s=%q)", c.Table, c.Column, c.KeyColumn, r.Key))
			}
		}
	}
	return bad, nil
}
