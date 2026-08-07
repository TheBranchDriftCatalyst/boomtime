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
//  2. Stop boomtime (or scale to zero) — writes during rotation are safe but
//     any Encrypt call by the running server would use the OLD key and its
//     blob would decrypt fine after cutover, so this is more of a caution
//     than a correctness requirement.
//  3. Run: boomtime rotate-encryption-key --old $OLD_B64 --new $NEW_B64
//  4. Update BOOM_ENCRYPTION_KEY in the environment to the new key.
//  5. Start boomtime.
//
// Failure model: if ANY row fails to decrypt under --old (the operator
// supplied the wrong old key, or the row was already re-encrypted, or the
// blob was tampered with), the command aborts BEFORE any write and reports
// the affected username. No partial rotation is possible.
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/spf13/cobra"
)

func rotateEncryptionKeyCmd() *cobra.Command {
	var oldB64, newB64 string
	cmd := &cobra.Command{
		Use:   "rotate-encryption-key",
		Short: "Re-encrypt every stored user secret under a new BOOM_ENCRYPTION_KEY",
		Long: `Re-encrypt every users.encrypted_wakatime_key AND users.encrypted_github_token
blob from --old to --new in a SINGLE transaction across both columns. Aborts
BEFORE any write if any row fails to decrypt under --old (reports the affected
username). See internal/auth package docs for the threat model + payload layout.`,
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

	wakatimeRows, err := database.ListEncryptedWakatimeKeys(ctx)
	if err != nil {
		return fmt.Errorf("list encrypted wakatime keys: %w", err)
	}
	// gaka-2ip Phase 1: the GitHub token column is ALSO encrypted under
	// BOOM_ENCRYPTION_KEY, so it MUST be rotated in lockstep — a new encrypted
	// column not wired here would strand every stored GitHub token on the next
	// key rotation. List it alongside wakatime and re-encrypt both.
	githubRows, err := database.ListEncryptedGithubTokens(ctx)
	if err != nil {
		return fmt.Errorf("list encrypted github tokens: %w", err)
	}
	if len(wakatimeRows) == 0 && len(githubRows) == 0 {
		fmt.Fprintln(out, "No encrypted secrets found — nothing to rotate.")
		return nil
	}

	// Decrypt + re-encrypt EVERY row (across BOTH columns) before any write.
	// If a single row fails to decrypt under --old we abort with the affected
	// username; the DB state is untouched.
	reencWakatime := make([]db.EncryptedWakatimeKeyRow, 0, len(wakatimeRows))
	for _, r := range wakatimeRows {
		pt, derr := auth.DecryptWith(oldAEAD, r.Ciphertext)
		if derr != nil {
			return fmt.Errorf("decrypt wakatime key failed for user %q under --old: %w — aborting (no rows written)", r.Username, derr)
		}
		ct, eerr := auth.EncryptWith(newAEAD, pt)
		if eerr != nil {
			return fmt.Errorf("re-encrypt wakatime key failed for user %q under --new: %w — aborting (no rows written)", r.Username, eerr)
		}
		reencWakatime = append(reencWakatime, db.EncryptedWakatimeKeyRow{
			Username:   r.Username,
			Ciphertext: ct,
		})
	}
	reencGithub := make([]db.EncryptedGithubTokenRow, 0, len(githubRows))
	for _, r := range githubRows {
		pt, derr := auth.DecryptWith(oldAEAD, r.Ciphertext)
		if derr != nil {
			return fmt.Errorf("decrypt github token failed for user %q under --old: %w — aborting (no rows written)", r.Username, derr)
		}
		ct, eerr := auth.EncryptWith(newAEAD, pt)
		if eerr != nil {
			return fmt.Errorf("re-encrypt github token failed for user %q under --new: %w — aborting (no rows written)", r.Username, eerr)
		}
		reencGithub = append(reencGithub, db.EncryptedGithubTokenRow{
			Username:   r.Username,
			Ciphertext: ct,
		})
	}

	// Single transaction across BOTH columns — either every ciphertext is
	// rewritten under --new or none is.
	wkUpdated, ghUpdated, err := database.RotateEncryptedSecrets(ctx, reencWakatime, reencGithub)
	if err != nil {
		return fmt.Errorf("commit rotation: %w", err)
	}

	fmt.Fprintf(out, "Rotated %d encrypted Wakatime key(s) and %d encrypted GitHub token(s).\n", wkUpdated, ghUpdated)
	fmt.Fprintln(out, "Remember to set BOOM_ENCRYPTION_KEY to the new value before restarting boomtime.")
	return nil
}
