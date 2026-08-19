// Package climeta is the importable home of boomtime's CLI command bodies,
// completion funcs, and the curated web-run registry (admin CLI-runner
// backend, BOOM_FEATURE_ADMIN_CLI).
//
// The cobra command constructors + run* bodies used to live in package main
// (cmd/boomtime), which internal/admin cannot import. They moved here so the
// admin HTTP surface can (a) introspect the SAME *cobra.Command definitions
// the CLI builds (flags, usage, annotations) and (b) run the SAME in-process
// run* bodies the cobra RunE wrappers call — with typed args, never a shell.
//
// SECURITY MODEL (the whole point):
//   - NEVER a subprocess or shell. Running a command from the web = calling
//     the in-process Go function with typed arguments. No argv is ever
//     assembled.
//   - Double allowlist: a command is web-runnable only when it BOTH carries a
//     `web` cobra annotation on its command definition AND has a hand-written
//     entry in the registry below. Omission from either = invisible.
//   - Classification: readonly commands run freely; mutating commands default
//     to dry-run (when supported) and require an explicit confirm sentinel to
//     apply; destructive commands are excluded from the registry entirely
//     (rotate-encryption-key, create-token, create-user, run, run-migrations,
//     completion are deliberately absent).
package climeta

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// WebAnnotation is the cobra Annotations key that opts a command definition
// into the web allowlist. cobra ignores unknown annotation keys for help /
// completion / execution, so the marker is purely additive to the CLI.
const WebAnnotation = "web"

// Classification values for the WebAnnotation. destructive exists as a named
// class for completeness but no destructive command is ever registered — the
// run endpoint refuses the class outright as defense-in-depth.
const (
	ClassReadonly    = "readonly"
	ClassMutating    = "mutating"
	ClassDestructive = "destructive"
)

// RunArgs carries the typed, validated arguments for one web-initiated run.
// Flags holds coerced values keyed by flag name; Positional holds positional
// argument values in declared order. Built exclusively by BindRunArgs — the
// invokers can trust the types.
type RunArgs struct {
	Flags      map[string]any
	Positional []string
}

// Bool returns the named flag as a bool (false when absent).
func (a RunArgs) Bool(name string) bool { v, _ := a.Flags[name].(bool); return v }

// Str returns the named flag as a string ("" when absent).
func (a RunArgs) Str(name string) string { v, _ := a.Flags[name].(string); return v }

// Int returns the named flag as an int (0 when absent).
func (a RunArgs) Int(name string) int { v, _ := a.Flags[name].(int); return v }

// Strs returns the named flag as a []string (nil when absent).
func (a RunArgs) Strs(name string) []string { v, _ := a.Flags[name].([]string); return v }

// Pos returns the positional argument at index i ("" when out of range).
func (a RunArgs) Pos(i int) string {
	if i < 0 || i >= len(a.Positional) {
		return ""
	}
	return a.Positional[i]
}

// RegistryEntry maps one command path to its vetted in-process invoker plus
// the metadata the spec/run/complete endpoints need. Invoke closures call the
// SAME Run* bodies the cobra RunE wrappers call — never a subprocess.
type RegistryEntry struct {
	// Classification must match the command's WebAnnotation value —
	// BuildSpec enforces the equality, so a drifted entry disappears from
	// the web surface instead of running under the wrong class.
	Classification string
	// DryRunSupported marks commands with a real --dry-run flag. The run
	// endpoint defaults dry-run to TRUE for mutating commands that support
	// it; applying (dry-run=false) requires the confirm sentinel.
	DryRunSupported bool
	// RequiredCap is the capability the run/complete/spec routes gate on
	// (defense-in-depth behind requireAdmin; inert until user-model is on).
	RequiredCap auth.Capability
	// NewCommand builds the command definition the CLI itself uses — the
	// single source of truth for flags/usage/annotations that the spec
	// builder introspects.
	NewCommand func() *cobra.Command
	// Available optionally gates the command on server config (e.g.
	// github-stats requires cfg.FeatureGithubStats). nil = always available.
	Available func(cfg *config.Config) bool
	// Invoke runs the command in-process with typed args, writing all
	// human-readable output to out (the run endpoint passes a BOUNDED
	// writer, so a runaway command cannot buffer unboundedly).
	Invoke func(ctx context.Context, database *db.DB, args RunArgs, out io.Writer) error
	// ArgCompleter completes positional arguments (nil = no completion).
	// Used by the CLI shell (<TAB>) and as the web fallback for completers
	// with no DBLister form; it self-opens a bounded connection.
	ArgCompleter cobra.CompletionFunc
	// FlagCompleters completes flag values, keyed by flag name. Same
	// transport split as ArgCompleter.
	FlagCompleters map[string]cobra.CompletionFunc
	// ArgLister / FlagListers are the pool-reusing web counterparts of
	// ArgCompleter / FlagCompleters: the SAME underlying list query, run by
	// the complete endpoint against the server's existing pool (h.DB) via
	// CompleteWithDB — the web path never opens a fresh pool per request.
	ArgLister   DBLister
	FlagListers map[string]DBLister
	// Enums optionally closes a param (flag or positional, keyed by name)
	// over a fixed value set: the spec reports Type "enum" and the binder
	// enforces membership. (e.g. a future role param → auth.RoleStrings()).
	Enums map[string][]string
}

// registry is the hand-written web-run allowlist — the second half of the
// double allowlist (the first is the WebAnnotation on the command def).
// Keys are full command paths as typed on the CLI.
//
// Deliberately EXCLUDED (secret material / TTY / process lifecycle):
// rotate-encryption-key, create-token, create-user, run, run-migrations,
// completion, label-images, user set-role/disable/enable (state-changing
// user admin is deferred).
var registry = map[string]RegistryEntry{
	"backfill last-context": {
		Classification:  ClassMutating,
		DryRunSupported: true,
		RequiredCap:     auth.CapAdmin,
		NewCommand:      NewBackfillLastContextCmd,
		Invoke: func(ctx context.Context, database *db.DB, args RunArgs, out io.Writer) error {
			return RunBackfillLastContext(ctx, database, args.Bool("dry-run"), out)
		},
	},
	// gaka-zp2s: the DOMAIN-coupled commands "backfill github-stats" (github) and
	// "hardcover dedup-reads" (books) register themselves into this map via
	// climeta.Register from their own packages' init() — see internal/boomtime/github
	// and internal/books. This keeps climeta domain-free (the CLI framework never
	// imports a data domain), so it can fold under internal/shared.
	"user list": {
		Classification: ClassReadonly,
		RequiredCap:    auth.CapAdmin,
		NewCommand:     NewUserListCmd,
		Invoke: func(ctx context.Context, database *db.DB, _ RunArgs, out io.Writer) error {
			return RunUserList(ctx, database, out)
		},
	},
	"user show": {
		Classification: ClassReadonly,
		RequiredCap:    auth.CapAdmin,
		NewCommand:     NewUserShowCmd,
		ArgCompleter:   CompleteUsernames,
		ArgLister:      ListUsernames,
		Invoke: func(ctx context.Context, database *db.DB, args RunArgs, out io.Writer) error {
			return RunUserShow(ctx, database, args.Pos(0), out)
		},
	},
}

// Registry returns the web-run allowlist. Callers must treat the map as
// read-only.
func Registry() map[string]RegistryEntry { return registry }

// Register adds a command to the web-run allowlist (gaka-zp2s). It is the seam
// domain packages use to contribute their own vetted commands from init() —
// keeping climeta itself free of any data-domain import. Registration order is
// immaterial (the map is keyed by command path); a duplicate path overwrites.
// Not safe for concurrent use — call only from package init(), before serving.
func Register(path string, e RegistryEntry) { registry[path] = e }
