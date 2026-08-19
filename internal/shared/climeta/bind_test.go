package climeta_test

// bind_test.go — unit coverage for the typed binder: unknown-key rejection,
// per-type coercion strictness, required enforcement, enum membership,
// dry-run defaulting, and positional routing.

import (
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/climeta"
)

// synthetic spec exercising every param type in one place.
func binderSpec() climeta.CommandSpec {
	return climeta.CommandSpec{
		Command:         "synthetic cmd",
		Classification:  climeta.ClassMutating,
		DryRunSupported: true,
		Params: []climeta.CmdParam{
			{Name: "username", Type: "string", Positional: true, Required: true},
			{Name: "note", Type: "string", Positional: true, Required: false},
			{Name: "dry-run", Type: "bool"},
			{Name: "limit", Type: "int"},
			{Name: "tags", Type: "stringSlice"},
			{Name: "role", Type: "enum", Enum: []string{"full", "admin"}},
			{Name: "label", Type: "string"},
		},
	}
}

func TestBindRejectsUnknownKeys(t *testing.T) {
	_, err := climeta.BindRunArgs(binderSpec(), map[string]any{"username": "a", "bogus": 1})
	if err == nil || !strings.Contains(err.Error(), `unknown parameter "bogus"`) {
		t.Fatalf("want unknown-parameter error, got %v", err)
	}
}

func TestBindRequiredEnforced(t *testing.T) {
	_, err := climeta.BindRunArgs(binderSpec(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), `missing required parameter "username"`) {
		t.Fatalf("want missing-required error, got %v", err)
	}
}

func TestBindDryRunDefaultsTrueForMutating(t *testing.T) {
	args, err := climeta.BindRunArgs(binderSpec(), map[string]any{"username": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if !args.Bool("dry-run") {
		t.Error("absent dry-run must default TRUE on a mutating command that supports it")
	}

	// Explicit false is preserved (the apply path; confirm is enforced by
	// the endpoint, not the binder).
	args, err = climeta.BindRunArgs(binderSpec(), map[string]any{"username": "a", "dry-run": false})
	if err != nil {
		t.Fatal(err)
	}
	if args.Bool("dry-run") {
		t.Error("explicit dry-run=false must be preserved")
	}

	// Readonly specs get no synthetic dry-run.
	ro := climeta.CommandSpec{
		Command:        "ro",
		Classification: climeta.ClassReadonly,
		Params:         []climeta.CmdParam{},
	}
	args, err = climeta.BindRunArgs(ro, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := args.Flags[climeta.DryRunFlag]; ok {
		t.Error("readonly command must not grow a dry-run flag")
	}
}

func TestBindTypeCoercionStrictness(t *testing.T) {
	base := map[string]any{"username": "a"}
	cases := []struct {
		name string
		key  string
		val  any
		want string // substring of the error; "" = must succeed
	}{
		{"bool from string rejected", "dry-run", "true", "must be a boolean"},
		{"bool ok", "dry-run", true, ""},
		{"int from fraction rejected", "limit", 1.5, "must be an integer"},
		{"int from string rejected", "limit", "3", "must be an integer"},
		{"int ok", "limit", float64(3), ""},
		{"slice from string rejected", "tags", "a,b", "array of strings"},
		{"slice with non-string rejected", "tags", []any{"a", 2}, "array of strings"},
		{"slice ok", "tags", []any{"a", "b"}, ""},
		{"enum non-member rejected", "role", "root", "must be one of"},
		{"enum non-string rejected", "role", 1, "must be a string"},
		{"enum ok", "role", "admin", ""},
		{"string from number rejected", "label", 7, "must be a string"},
		{"string ok", "label", "x", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{tc.key: tc.val}
			for k, v := range base {
				raw[k] = v
			}
			_, err := climeta.BindRunArgs(binderSpec(), raw)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBindPositionalRouting(t *testing.T) {
	args, err := climeta.BindRunArgs(binderSpec(), map[string]any{
		"username": "alice",
		"note":     "hi",
		"label":    "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if args.Pos(0) != "alice" || args.Pos(1) != "hi" {
		t.Errorf("positionals must land in declared order: %v", args.Positional)
	}
	if _, ok := args.Flags["username"]; ok {
		t.Error("positional values must not leak into Flags")
	}
	if args.Str("label") != "x" {
		t.Errorf("flag value lost: %v", args.Flags)
	}

	// Absent optional positional keeps indexing stable (empty slot).
	args, err = climeta.BindRunArgs(binderSpec(), map[string]any{"username": "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if args.Pos(0) != "bob" || args.Pos(1) != "" {
		t.Errorf("optional positional slot must be stable: %v", args.Positional)
	}

	// Out-of-range access is safe.
	if args.Pos(99) != "" || args.Pos(-1) != "" {
		t.Error("Pos out of range must return empty")
	}
}

func TestBindDoesNotMutateCallerMap(t *testing.T) {
	raw := map[string]any{"username": "a"}
	if _, err := climeta.BindRunArgs(binderSpec(), raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["dry-run"]; ok {
		t.Error("binder must not write the dry-run default into the caller's map")
	}
}
