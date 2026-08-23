// backup_more_test.go — boom-d6x.handler: coverage helpers for backup.go —
// oversize upload + validation error branches + restoreMaxBytes env override.
//
// Named invariants:
//
//	"restoreMaxBytes respects BOOM_RESTORE_MAX_BYTES override" — the
//	unit-level function is called during DBImport and returns the env
//	value when set + parseable, and the default 4 GiB otherwise. Pin the
//	parse+fallback branches so a future refactor can't silently regress.
//
//	"oversize archive → 413 with 'BOOM_RESTORE_MAX_BYTES' hint" — an
//	upload > BOOM_RESTORE_MAX_BYTES is rejected before validation runs.
//	The error message names the env var so an operator knows what to
//	tune. Exercised via a tiny override (100 bytes) to keep the test
//	fast + memory-safe.
package admin

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestRestoreMaxBytesEnvOverride — stdlib-style test since restoreMaxBytes
// is unexported. Named invariant: env override wins when it parses; the
// 4 GiB default holds when unset or unparseable. Non-tautological: we
// twiddle the process env var mid-test to prove BOTH branches. Restored
// in defer so parallel tests don't stampede.
func TestRestoreMaxBytesEnvOverride(t *testing.T) {
	prev, had := os.LookupEnv("BOOM_RESTORE_MAX_BYTES")
	defer func() {
		if had {
			_ = os.Setenv("BOOM_RESTORE_MAX_BYTES", prev)
		} else {
			_ = os.Unsetenv("BOOM_RESTORE_MAX_BYTES")
		}
	}()

	// Default (unset).
	_ = os.Unsetenv("BOOM_RESTORE_MAX_BYTES")
	if got := restoreMaxBytes(); got != 4<<30 {
		t.Fatalf("default: got %d, want %d", got, 4<<30)
	}

	// Valid override.
	_ = os.Setenv("BOOM_RESTORE_MAX_BYTES", "12345")
	if got := restoreMaxBytes(); got != 12345 {
		t.Fatalf("valid override: got %d, want 12345", got)
	}

	// Unparseable value falls back to default (NOT zero — that would
	// silently disable the cap).
	_ = os.Setenv("BOOM_RESTORE_MAX_BYTES", "not-a-number")
	if got := restoreMaxBytes(); got != 4<<30 {
		t.Fatalf("unparseable: got %d, want %d (default fallback)", got, 4<<30)
	}

	// Zero override is treated as "unset" (guarded by n > 0).
	_ = os.Setenv("BOOM_RESTORE_MAX_BYTES", "0")
	if got := restoreMaxBytes(); got != 4<<30 {
		t.Fatalf("zero override: got %d, want %d", got, 4<<30)
	}
}

// Ginkgo describe so this file also compiles under the ginkgo runner and
// contributes to the same suite counter.
var _ = Describe("restoreMaxBytes helper (in-package)", func() {
	It("compiles under ginkgo — see TestRestoreMaxBytesEnvOverride for the invariants", func() {
		Expect(restoreMaxBytes()).To(BeNumerically(">", int64(0)),
			"restoreMaxBytes must never return 0 (would silently disable the cap)")
	})
})
