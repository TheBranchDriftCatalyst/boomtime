package climeta

// Transparent shell-completion layer (boom-0oe.10), relocated verbatim from
// cmd/boomtime/completion.go so the admin CLI-runner can drive the SAME
// completion funcs the shell uses (cmd/boomtime re-imports these via thin
// aliases, so `boomtime completion zsh` behavior is unchanged).
//
// Cobra ships a `completion` subcommand that emits zsh/bash/fish/powershell
// scripts; those scripts call boomtime's hidden `__complete` on <TAB>, so
// completion reflects LIVE state — usernames come straight from the DB, roles
// from the auth enum. Install for zsh, e.g.:
//
//	boomtime completion zsh > "${fpath[1]}/_boomtime"   # then restart the shell
//	# or, quick per-session:  source <(boomtime completion zsh)
//
// The point of this file is the reusable GENERATOR: DBEntityCompleter turns any
// "list these entities from an open DB" function into a cobra completion func,
// handling the fast bounded connect, prefix filter, and shell directive. Adding
// completion for a new entity (tokens, spaces, …) is then one small function —
// completion stays consistent across every command.
import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// FilterPrefix keeps only items that start with prefix (empty prefix = all).
func FilterPrefix(items []string, prefix string) []string {
	if prefix == "" {
		return items
	}
	var out []string
	for _, s := range items {
		if strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}

// DBLister lists candidate completion strings from an ALREADY-OPEN pool.
// It is the shared query lambda both completion transports run:
//   - shell <TAB>: DBEntityCompleter wraps it in a cobra func that opens its
//     own bounded connection (no pool exists in a shell process);
//   - web (admin CLI-runner): registry ArgLister/FlagListers hand the SAME
//     lambda to CompleteWithDB, which runs it against the server's existing
//     pool (h.DB) — the HTTP endpoint never opens a new pool per request.
type DBLister func(ctx context.Context, database *db.DB) ([]string, error)

// DBEntityCompleter is the reusable completion generator. Give it a function
// that lists candidate strings from an open *db.DB; it returns a cobra
// completion func that connects (bounded 2s so a <TAB> never hangs the
// shell), lists, prefix-filters, and disables file completion. Errors resolve
// to "no completions" rather than shell noise.
func DBEntityCompleter(list DBLister) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		database, err := db.New(ctx, config.Load().DatabaseURL())
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		defer database.Close()
		items, err := list(ctx, database)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return FilterPrefix(items, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// ListUsernames is the username query lambda shared by both completion
// transports (see DBLister).
var ListUsernames DBLister = func(ctx context.Context, database *db.DB) ([]string, error) {
	rows, err := database.ListUsersAdmin(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Username
	}
	return names, nil
}

// CompleteUsernames dynamically completes a username argument from the DB.
var CompleteUsernames = DBEntityCompleter(ListUsernames)

// CompleteLabelIDs dynamically completes a label-id argument (e.g. the
// `label-images regenerate --id` flag) from the DB catalog. Each candidate
// carries a "\tGlyph Label" description, which cobra renders next to the id in
// shells that support it (zsh/fish).
var CompleteLabelIDs = DBEntityCompleter(func(ctx context.Context, database *db.DB) ([]string, error) {
	labels, err := database.ListLabels(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(labels))
	for i, l := range labels {
		desc := strings.TrimSpace(l.Glyph + " " + l.Label)
		if desc == "" {
			ids[i] = l.ID
		} else {
			ids[i] = l.ID + "\t" + desc
		}
	}
	return ids, nil
})

// CompleteRoles completes a role argument from the auth enum (no DB needed).
func CompleteRoles(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return FilterPrefix(auth.RoleStrings(), toComplete), cobra.ShellCompDirectiveNoFileComp
}

// CompleteUsernameThenRole is the positional completer for `user set-role
// <username> <role>`: usernames at position 0, roles at position 1.
func CompleteUsernameThenRole(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return CompleteUsernames(cmd, args, toComplete)
	case 1:
		return CompleteRoles(cmd, args, toComplete)
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}
