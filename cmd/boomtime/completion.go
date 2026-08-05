package main

// Transparent shell-completion layer (gaka-0oe.10).
//
// Cobra ships a `completion` subcommand that emits zsh/bash/fish/powershell
// scripts; those scripts call boomtime's hidden `__complete` on <TAB>, so
// completion reflects LIVE state — usernames come straight from the DB, roles
// from the auth enum. Install for zsh, e.g.:
//
//	boomtime completion zsh > "${fpath[1]}/_boomtime"   # then restart the shell
//	# or, quick per-session:  source <(boomtime completion zsh)
//
// The point of this file is the reusable GENERATOR: dbEntityCompleter turns any
// "list these entities from an open DB" function into a cobra completion func,
// handling the fast bounded connect, prefix filter, and shell directive. Adding
// completion for a new entity (tokens, spaces, …) is then one small function —
// completion stays consistent across every command.
import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// filterPrefix keeps only items that start with prefix (empty prefix = all).
func filterPrefix(items []string, prefix string) []string {
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

// dbEntityCompleter is the reusable completion generator. Give it a function
// that lists candidate strings from an open *db.DB; it returns a cobra
// ValidArgsFunction that connects (bounded 2s so a <TAB> never hangs the
// shell), lists, prefix-filters, and disables file completion. Errors resolve
// to "no completions" rather than shell noise.
func dbEntityCompleter(list func(ctx context.Context, database *db.DB) ([]string, error)) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
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
		return filterPrefix(items, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// completeUsernames dynamically completes a username argument from the DB.
var completeUsernames = dbEntityCompleter(func(ctx context.Context, database *db.DB) ([]string, error) {
	rows, err := database.ListUsersAdmin(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Username
	}
	return names, nil
})

// completeLabelIds dynamically completes a label-id argument (e.g. the
// `label-images regenerate --id` flag) from the DB catalog. Each candidate
// carries a "\tGlyph Label" description, which cobra renders next to the id in
// shells that support it (zsh/fish).
var completeLabelIds = dbEntityCompleter(func(ctx context.Context, database *db.DB) ([]string, error) {
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

// completeRoles completes a role argument from the auth enum (no DB needed).
func completeRoles(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return filterPrefix(auth.RoleStrings(), toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeUsernameThenRole is the positional completer for `user set-role
// <username> <role>`: usernames at position 0, roles at position 1.
func completeUsernameThenRole(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return completeUsernames(cmd, args, toComplete)
	case 1:
		return completeRoles(cmd, args, toComplete)
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}
