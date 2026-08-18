package climeta

// spec.go — CommandSpec/CmdParam synthesis for the admin CLI-runner
// (GET /api/v1/admin/cli/spec). BuildSpecs introspects each registry
// command's *cobra.Command (built via the shared constructor) so the web
// spec can NEVER drift from the CLI definition: flags, usage strings, and
// defaults all come from the same cobra defs the shell sees.
//
// A command appears in the spec only when it is BOTH annotated
// (Annotations["web"]) AND registered (registry) AND available under the
// current config — the same double allowlist the run endpoint enforces.

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
)

// CommandSpec is one web-runnable command as served to the FE.
type CommandSpec struct {
	// Command is the full CLI path ("backfill last-context") — also the run
	// endpoint's lookup key AND the confirm sentinel value for applying a
	// mutating command.
	Command         string     `json:"command"`
	Short           string     `json:"short"`
	Long            string     `json:"long,omitempty"`
	Classification  string     `json:"classification"` // readonly | mutating | destructive
	DryRunSupported bool       `json:"dryRunSupported"`
	Params          []CmdParam `json:"params"`
}

// CmdParam is one typed input (positional argument or flag) of a command.
type CmdParam struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Usage     string `json:"usage,omitempty"`
	// Type ∈ bool | string | int | stringSlice | enum.
	Type    string   `json:"type"`
	Default string   `json:"default,omitempty"`
	Enum    []string `json:"enum,omitempty"`
	// Positional params are sent by name like flags in the run request's
	// `flags` object; the binder routes them into RunArgs.Positional in
	// declared order.
	Positional bool `json:"positional"`
	Required   bool `json:"required"`
	// Secret marks values that must be masked in any UI echo / audit trail.
	Secret bool `json:"secret"`
	// Completable = the complete endpoint can offer suggestions for this
	// param (ArgCompleter for positionals, FlagCompleters[name] for flags).
	Completable bool `json:"completable"`
}

// BuildSpecs returns the CommandSpec for every registered ∩ annotated ∩
// available command, sorted by command path for a stable wire order.
func BuildSpecs(cfg *config.Config) []CommandSpec {
	paths := make([]string, 0, len(registry))
	for p := range registry {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	specs := make([]CommandSpec, 0, len(paths))
	for _, p := range paths {
		entry := registry[p]
		if entry.Available != nil && !entry.Available(cfg) {
			continue
		}
		if spec, ok := BuildSpec(p, entry); ok {
			specs = append(specs, spec)
		}
	}
	return specs
}

// BuildSpec introspects one registry entry's command def. Returns ok=false
// when the command is NOT web-annotated or its annotation disagrees with the
// registry classification — either way the command silently disappears from
// the web surface (fail-closed on drift).
func BuildSpec(path string, entry RegistryEntry) (CommandSpec, bool) {
	if entry.NewCommand == nil {
		return CommandSpec{}, false
	}
	cmd := entry.NewCommand()
	class, annotated := cmd.Annotations[WebAnnotation]
	if !annotated || class != entry.Classification {
		return CommandSpec{}, false
	}

	spec := CommandSpec{
		Command:         path,
		Short:           cmd.Short,
		Long:            cmd.Long,
		Classification:  entry.Classification,
		DryRunSupported: entry.DryRunSupported,
		Params:          []CmdParam{},
	}

	// Positionals first (in declared order), parsed from the cobra Use line:
	// `show <username>` → required positional "username"; `[name]` → optional.
	for _, pos := range parsePositionals(cmd.Use) {
		p := CmdParam{
			Name:        pos.name,
			Type:        "string",
			Positional:  true,
			Required:    pos.required,
			Secret:      secretParam(pos.name),
			Completable: entry.ArgCompleter != nil,
		}
		if enum, ok := entry.Enums[pos.name]; ok {
			p.Type = "enum"
			p.Enum = enum
		}
		spec.Params = append(spec.Params, p)
	}

	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" || f.Hidden {
			return
		}
		p := CmdParam{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Usage:       f.Usage,
			Type:        paramType(f),
			Default:     f.DefValue,
			Required:    requiredFlag(f),
			Secret:      secretParam(f.Name),
			Completable: entry.FlagCompleters[f.Name] != nil,
		}
		if enum, ok := entry.Enums[f.Name]; ok {
			p.Type = "enum"
			p.Enum = enum
		}
		spec.Params = append(spec.Params, p)
	})

	return spec, true
}

// positional is one parsed token from a cobra Use line.
type positional struct {
	name     string
	required bool
}

// parsePositionals extracts positional-arg names from a cobra Use string:
// `<name>` = required, `[name]` = optional. The first token (the command verb
// itself) never matches either shape, so it is naturally skipped.
func parsePositionals(use string) []positional {
	var out []positional
	for _, tok := range strings.Fields(use) {
		switch {
		case strings.HasPrefix(tok, "<") && strings.HasSuffix(tok, ">"):
			out = append(out, positional{name: strings.Trim(tok, "<>"), required: true})
		case strings.HasPrefix(tok, "[") && strings.HasSuffix(tok, "]"):
			out = append(out, positional{name: strings.Trim(tok, "[]"), required: false})
		}
	}
	return out
}

// paramType maps a pflag value type onto the wire Type vocabulary. Anything
// unrecognized degrades to "string" — the binder then accepts a string and
// the CLI-side flag parsing semantics still apply in the RunE.
func paramType(f *pflag.Flag) string {
	switch f.Value.Type() {
	case "bool":
		return "bool"
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "count":
		return "int"
	case "stringSlice", "stringArray":
		return "stringSlice"
	default:
		return "string"
	}
}

// requiredFlag reports whether the flag was MarkFlagRequired'd (cobra records
// that as an annotation on the pflag).
func requiredFlag(f *pflag.Flag) bool {
	return len(f.Annotations[cobra.BashCompOneRequiredFlag]) > 0
}

// secretParam flags names that smell like credentials so the FE masks the
// input and the audit trail masks the value. Phase-1 commands have no secret
// params; the heuristic exists so a future registry addition fails safe.
func secretParam(name string) bool {
	n := strings.ToLower(name)
	for _, marker := range []string{"token", "secret", "password", "key"} {
		if strings.Contains(n, marker) {
			return true
		}
	}
	return false
}
