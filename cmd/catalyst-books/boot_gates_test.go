// boot_gates_test.go — regression tests for the two PROCESS-GLOBAL startup
// gates the standalone catalyst-books binary was missing (audit 2026-09-06,
// composition-config):
//
//	main.go:94 — hardcover.Configure was never called, so BOOM_HARDCOVER_DRYRUN=false
//	             was parsed into cfg and then thrown away: every Hardcover write
//	             stayed blocked while reporting simulated success, forever.
//	main.go:40 — auth.LoadKeyFromEnv was never called at boot, so a prod deploy
//	             without BOOM_ENCRYPTION_KEY started cleanly and only failed at the
//	             END of the Amazon device-registration dance.
//
// Both are asserted through configureProcessGlobals (the seam main() calls) by
// observing the actual process-global state it is supposed to install, not by
// checking that the call was made.
package main

import (
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// A freshly built hardcover.Client inherits the PROCESS-WIDE dry-run default
// that hardcover.Configure installs — which is exactly what the standalone
// binary never set. NewPushService (wired a few lines below the gate in main)
// builds its clients this way, so this is the state the per-row sync button
// actually runs against.
func TestStandaloneBoot_HardcoverDryRunIsWiredFromConfig(t *testing.T) {
	// Restore the package default for any test that runs after this one.
	t.Cleanup(func() { hardcover.Configure(true, nil) })

	t.Run("BOOM_HARDCOVER_DRYRUN=false reaches the hardcover package", func(t *testing.T) {
		hardcover.Configure(true, nil) // start from the fail-safe default
		cfg := &config.Config{HardcoverDryRun: false, Env: "dev"}
		if err := configureProcessGlobals(cfg, quietLogger()); err != nil {
			t.Fatalf("configureProcessGlobals: %v", err)
		}
		if hardcover.NewClient("tok").DryRun() {
			t.Error("clients still block writes after BOOM_HARDCOVER_DRYRUN=false — " +
				"hardcover.Configure was not called, so every Hardcover mutation is silently " +
				"blocked while the UI reports the sync succeeded")
		}
	})

	t.Run("the fail-safe default survives (dry-run stays ON when configured ON)", func(t *testing.T) {
		hardcover.Configure(false, nil) // start from the UNSAFE state
		cfg := &config.Config{HardcoverDryRun: true, Env: "dev"}
		if err := configureProcessGlobals(cfg, quietLogger()); err != nil {
			t.Fatalf("configureProcessGlobals: %v", err)
		}
		if !hardcover.NewClient("tok").DryRun() {
			t.Error("dry-run did not switch back ON — the gate must be able to block writes, not only enable them")
		}
	})
}

// The encryption-key gate must have the SAME env-dependent shape cmd/boomtime
// enforces: warn (and keep booting) in dev, refuse to start in prod. An unset
// BOOM_ENV classifies as prod, which is the deployment shape the audit named.
func TestStandaloneBoot_EncryptionKeyGate(t *testing.T) {
	t.Cleanup(func() { hardcover.Configure(true, nil) })

	// A valid base64 32-byte key for the happy path.
	const goodKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

	cases := []struct {
		name    string
		env     string
		key     string
		wantErr bool
	}{
		{"prod + missing key refuses to boot", "prod", "", true},
		{"production + missing key refuses to boot", "production", "", true},
		{"PROD (case-insensitive) + missing key refuses to boot", "PROD", "", true},
		{"prod + malformed key refuses to boot", "prod", "not-base64!!", true},
		{"prod + valid key boots", "prod", goodKey, false},
		{"dev + missing key still boots (WARN only)", "dev", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(auth.EncryptionKeyEnv, tc.key)
			auth.ResetForTest() // bust the sync.Once memo so the env is re-read
			t.Cleanup(auth.ResetForTest)

			cfg := &config.Config{HardcoverDryRun: true, Env: tc.env}
			err := configureProcessGlobals(cfg, quietLogger())
			if tc.wantErr && err == nil {
				t.Errorf("BOOM_ENV=%q with key %q booted cleanly — a prod standalone deploy "+
					"without a usable BOOM_ENCRYPTION_KEY must fail at startup, not at the end "+
					"of the Amazon connect flow", tc.env, tc.key)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("BOOM_ENV=%q with key %q refused to boot: %v", tc.env, tc.key, err)
			}
		})
	}

	// The gate only bites in prod, so the audit's headline scenario ("operator
	// deploys the standalone image without BOOM_ENCRYPTION_KEY and without
	// setting BOOM_ENV") depends on Load() classifying an UNSET BOOM_ENV as
	// production. Pin that link explicitly — if the default ever flips to dev,
	// the gate above silently stops covering the default deployment.
	t.Run("an unset BOOM_ENV loads as production", func(t *testing.T) {
		// t.Setenv("") would set-but-empty, which os.LookupEnv reports as
		// present; the scenario is a genuinely ABSENT variable.
		prev, had := os.LookupEnv("BOOM_ENV")
		if err := os.Unsetenv("BOOM_ENV"); err != nil {
			t.Fatalf("unset BOOM_ENV: %v", err)
		}
		t.Cleanup(func() {
			if had {
				_ = os.Setenv("BOOM_ENV", prev)
			} else {
				_ = os.Unsetenv("BOOM_ENV")
			}
		})
		if !config.Load().IsProd() {
			t.Error("config.Load() with BOOM_ENV unset is not classified as prod — " +
				"the standalone encryption-key gate would no longer fire on a default deploy")
		}
	})
}
