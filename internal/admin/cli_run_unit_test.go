// cli_run_unit_test.go — in-package unit tests for the CLI-runner's audit
// hygiene: maskFlags must replace the VALUE of any Secret-marked param with
// "***" (the redactArgs masking convention) before the flags map reaches the
// audit log, while non-secret values pass through untouched. Phase-1
// commands have no secret flags, so this pins the forward-proofing seam.
package admin

import (
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

func TestMaskFlagsUnknownKeysPassThrough(t *testing.T) {
	// maskFlags runs after the binder in the happy path, but the audit line
	// must be safe to build from ANY raw map — unknown keys just pass
	// through (they were rejected before Invoke anyway).
	got := maskFlags(climeta.CommandSpec{}, map[string]any{"stray": "v"})
	if got["stray"] != "v" {
		t.Errorf("unknown keys pass through unmasked: %v", got)
	}
}
