package main

import (
	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books"
)

// booksCmd is the `boomtime books …` parent for catalyst-books operator
// commands. Like hardcoverCmd, the subcommands themselves live in
// internal/books so the admin CLI-runner can introspect + invoke the same
// definitions in-process — the parent here is only wiring.
func booksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "books",
		Short: "catalyst-books maintenance commands",
	}
	cmd.AddCommand(books.NewLiberateCmd())
	cmd.AddCommand(books.NewLiberationStatusCmd())
	return cmd
}
