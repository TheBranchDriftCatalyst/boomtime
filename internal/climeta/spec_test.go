package climeta_test

// spec_test.go — unit coverage for the CommandSpec builder: the double
// allowlist (annotated ∩ registered), availability gating, param synthesis
// (types, positionals, enum, completable), and the exclusion list (nothing
// secret/TTY/lifecycle-shaped ever appears in the registry).

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/climeta"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
)

func specByCommand(t *testing.T, specs []climeta.CommandSpec, command string) climeta.CommandSpec {
	t.Helper()
	for _, s := range specs {
		if s.Command == command {
			return s
		}
	}
	t.Fatalf("command %q not in specs: %v", command, specs)
	return climeta.CommandSpec{}
}

func paramByName(t *testing.T, spec climeta.CommandSpec, name string) climeta.CmdParam {
	t.Helper()
	for _, p := range spec.Params {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("param %q not in spec %q: %+v", name, spec.Command, spec.Params)
	return climeta.CmdParam{}
}

func TestRegistryExactPhase1Surface(t *testing.T) {
	reg := climeta.Registry()
	want := []string{"backfill last-context", "backfill github-stats", "hardcover dedup-reads", "user list", "user show"}
	if len(reg) != len(want) {
		t.Fatalf("registry has %d entries, want exactly %d: %v", len(reg), len(want), reg)
	}
	for _, k := range want {
		if _, ok := reg[k]; !ok {
			t.Errorf("registry missing %q", k)
		}
	}
	// The exclusion list is a security invariant, not a TODO: secret
	// material, TTY prompts, and process lifecycle must never be runnable
	// from the web.
	for _, banned := range []string{
		"rotate-encryption-key", "create-token", "create-user",
		"run", "run-migrations", "completion",
		"user set-role", "user disable", "user enable",
	} {
		if _, ok := reg[banned]; ok {
			t.Errorf("registry must NEVER contain %q", banned)
		}
	}
}

func TestBuildSpecsAnnotatedIntersectionAndAvailability(t *testing.T) {
	// FeatureGithubStats off (zero config) → github-stats is unavailable. The
	// always-available set is: backfill last-context, hardcover dedup-reads,
	// user list, user show = 4.
	specs := climeta.BuildSpecs(&config.Config{})
	if len(specs) != 4 {
		t.Fatalf("with github-stats off want 4 specs, got %d: %v", len(specs), specs)
	}
	for _, s := range specs {
		if s.Command == "backfill github-stats" {
			t.Fatalf("github-stats must be hidden when FeatureGithubStats=false")
		}
	}

	// Flag on → github-stats joins them = 5.
	specs = climeta.BuildSpecs(&config.Config{FeatureGithubStats: true})
	if len(specs) != 5 {
		t.Fatalf("with github-stats on want 5 specs, got %d: %v", len(specs), specs)
	}

	// A nil cfg must not panic and keeps availability-gated commands hidden.
	specs = climeta.BuildSpecs(nil)
	if len(specs) != 4 {
		t.Fatalf("nil cfg want 4 specs, got %d", len(specs))
	}
}

func TestBuildSpecRejectsUnannotatedAndMismatched(t *testing.T) {
	// Unannotated command def → not web-visible even with a registry entry.
	unannotated := climeta.RegistryEntry{
		Classification: climeta.ClassReadonly,
		NewCommand:     func() *cobra.Command { return &cobra.Command{Use: "bare"} },
	}
	if _, ok := climeta.BuildSpec("bare", unannotated); ok {
		t.Error("unannotated command must not produce a spec (double allowlist)")
	}

	// Annotation present but disagreeing with the registry classification →
	// fail closed (drift must hide the command, not run it misclassified).
	mismatched := climeta.RegistryEntry{
		Classification: climeta.ClassReadonly,
		NewCommand: func() *cobra.Command {
			return &cobra.Command{
				Use:         "drifted",
				Annotations: map[string]string{climeta.WebAnnotation: climeta.ClassMutating},
			}
		},
	}
	if _, ok := climeta.BuildSpec("drifted", mismatched); ok {
		t.Error("classification mismatch must not produce a spec")
	}

	// nil NewCommand → no spec, no panic.
	if _, ok := climeta.BuildSpec("nil", climeta.RegistryEntry{}); ok {
		t.Error("nil NewCommand must not produce a spec")
	}
}

func TestBuildSpecParamSynthesis(t *testing.T) {
	cfg := &config.Config{FeatureGithubStats: true}
	specs := climeta.BuildSpecs(cfg)

	// backfill last-context: mutating, dry-run supported, bool flag param.
	lc := specByCommand(t, specs, "backfill last-context")
	if lc.Classification != climeta.ClassMutating || !lc.DryRunSupported {
		t.Errorf("last-context: want mutating+dryRunSupported, got %+v", lc)
	}
	dr := paramByName(t, lc, "dry-run")
	if dr.Type != "bool" || dr.Positional || dr.Required || dr.Completable || dr.Secret {
		t.Errorf("dry-run param wrong shape: %+v", dr)
	}
	if dr.Default != "false" {
		t.Errorf("dry-run default should introspect as \"false\", got %q", dr.Default)
	}

	// backfill github-stats: mutating, NO dry-run, --user flag completable.
	gs := specByCommand(t, specs, "backfill github-stats")
	if gs.DryRunSupported {
		t.Errorf("github-stats must not report dry-run support")
	}
	user := paramByName(t, gs, "user")
	if user.Type != "string" || !user.Completable || user.Positional {
		t.Errorf("github-stats --user wrong shape: %+v", user)
	}

	// user list: readonly, zero params.
	ul := specByCommand(t, specs, "user list")
	if ul.Classification != climeta.ClassReadonly || len(ul.Params) != 0 {
		t.Errorf("user list: want readonly with no params, got %+v", ul)
	}

	// user show: readonly with one REQUIRED, COMPLETABLE positional
	// "username" parsed from the cobra Use line.
	us := specByCommand(t, specs, "user show")
	if len(us.Params) != 1 {
		t.Fatalf("user show: want exactly 1 param, got %+v", us.Params)
	}
	uname := us.Params[0]
	if uname.Name != "username" || !uname.Positional || !uname.Required || !uname.Completable || uname.Type != "string" {
		t.Errorf("user show positional wrong shape: %+v", uname)
	}
}

func TestBuildSpecEnumSynthesis(t *testing.T) {
	// Synthetic entry: an enum-closed flag reports Type=enum + the value set.
	entry := climeta.RegistryEntry{
		Classification: climeta.ClassReadonly,
		NewCommand: func() *cobra.Command {
			cmd := &cobra.Command{
				Use:         "with-role",
				Annotations: map[string]string{climeta.WebAnnotation: climeta.ClassReadonly},
			}
			cmd.Flags().String("role", "", "role to filter by")
			return cmd
		},
		Enums: map[string][]string{"role": {"full", "light", "service", "admin"}},
	}
	spec, ok := climeta.BuildSpec("with-role", entry)
	if !ok {
		t.Fatal("annotated entry must build")
	}
	role := paramByName(t, spec, "role")
	if role.Type != "enum" || len(role.Enum) != 4 {
		t.Errorf("enum param wrong shape: %+v", role)
	}
}

func TestSecretParamHeuristic(t *testing.T) {
	// Secret marking rides on the param NAME — a future registry addition
	// with a credential-shaped flag fails safe into masked handling.
	entry := climeta.RegistryEntry{
		Classification: climeta.ClassReadonly,
		NewCommand: func() *cobra.Command {
			cmd := &cobra.Command{
				Use:         "with-secret",
				Annotations: map[string]string{climeta.WebAnnotation: climeta.ClassReadonly},
			}
			cmd.Flags().String("api-token", "", "credential")
			cmd.Flags().String("verbose-name", "", "not a credential")
			return cmd
		},
	}
	spec, ok := climeta.BuildSpec("with-secret", entry)
	if !ok {
		t.Fatal("annotated entry must build")
	}
	if !paramByName(t, spec, "api-token").Secret {
		t.Error("api-token must be marked Secret")
	}
	if paramByName(t, spec, "verbose-name").Secret {
		t.Error("verbose-name must not be marked Secret")
	}
}
