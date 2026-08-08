// cli_run_unit_test.go — in-package unit tests for the CLI-runner's audit
// hygiene: maskFlags must replace the VALUE of any Secret-marked param with
// "***" (the redactArgs masking convention) before the flags map reaches the
// audit log, while non-secret values pass through untouched. Phase-1
// commands have no secret flags, so this pins the forward-proofing seam.
package admin

import (
	"fmt"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/climeta"
)

func TestMaskFlagsMasksSecretParamsOnly(t *testing.T) {
	spec := climeta.CommandSpec{
		Command: "future cmd",
		Params: []climeta.CmdParam{
			{Name: "api-token", Type: "string", Secret: true},
			{Name: "user", Type: "string"},
			{Name: "dry-run", Type: "bool"},
		},
	}
	raw := map[string]any{
		"api-token": "waka_super_secret_value",
		"user":      "alice",
		"dry-run":   true,
	}

	got := maskFlags(spec, raw)

	if got["api-token"] != "***" {
		t.Errorf("secret param value must be masked, got %v", got["api-token"])
	}
	if got["user"] != "alice" || got["dry-run"] != true {
		t.Errorf("non-secret values must pass through: %v", got)
	}
	// The caller's map must not be mutated (it is still needed by the
	// request flow).
	if raw["api-token"] != "waka_super_secret_value" {
		t.Error("maskFlags must not mutate the caller's map")
	}
}

func TestCappedWriterBoundsDuringWrites(t *testing.T) {
	// The cap must apply DURING writes (bounded memory while Invoke runs),
	// not as a post-hoc truncation — a runaway command's excess bytes are
	// discarded, never buffered.
	w := &cappedWriter{max: 10}

	n, err := w.Write([]byte("12345"))
	if n != 5 || err != nil {
		t.Fatalf("in-bounds write: n=%d err=%v", n, err)
	}
	if w.truncated {
		t.Fatal("must not report truncated before the cap is hit")
	}

	// Straddling write: only the room-remaining prefix is kept, but the
	// FULL length is reported so fmt.Fprintf never sees a short write.
	n, err = w.Write([]byte("6789ABCDEF"))
	if n != 10 || err != nil {
		t.Fatalf("straddling write must report full length: n=%d err=%v", n, err)
	}
	if got := w.buf.String(); got != "123456789A" {
		t.Fatalf("buffer must hold exactly the first max bytes: %q", got)
	}
	if !w.truncated {
		t.Fatal("straddling write must set truncated")
	}

	// Past-cap writes are pure discards; buffer must not grow.
	n, err = w.Write([]byte("more"))
	if n != 4 || err != nil {
		t.Fatalf("past-cap write must still report success: n=%d err=%v", n, err)
	}
	if w.buf.Len() != 10 {
		t.Fatalf("buffer grew past cap: %d", w.buf.Len())
	}

	// Empty write at cap is a no-op, not a truncation signal by itself.
	w2 := &cappedWriter{max: 0}
	if n, _ := w2.Write(nil); n != 0 || w2.truncated {
		t.Fatalf("empty write must not flag truncation: n=%d truncated=%v", n, w2.truncated)
	}
}

func TestCappedWriterNeverErrorsUnderFprintf(t *testing.T) {
	// The command bodies write via fmt.Fprintf — a capped sink returning an
	// error or short write would abort a command mid-run. Prove a large
	// Fprintf stream completes cleanly with only the cap retained.
	w := &cappedWriter{max: 64}
	for i := 0; i < 100; i++ {
		if _, err := fmt.Fprintf(w, "line %03d: %s\n", i, strings.Repeat("x", 32)); err != nil {
			t.Fatalf("Fprintf against capped writer errored at line %d: %v", i, err)
		}
	}
	if w.buf.Len() != 64 || !w.truncated {
		t.Fatalf("want exactly 64 retained bytes + truncated, got len=%d truncated=%v", w.buf.Len(), w.truncated)
	}
}

func TestMaskFlagsUnknownKeysPassThrough(t *testing.T) {
	// maskFlags runs after the binder in the happy path, but the audit line
	// must be safe to build from ANY raw map — unknown keys just pass
	// through (they were rejected before Invoke anyway).
	got := maskFlags(climeta.CommandSpec{}, map[string]any{"stray": "v"})
	if got["stray"] != "v" {
		t.Errorf("unknown keys pass through unmasked: %v", got)
	}
}
