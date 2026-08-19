package main

import (
	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books"
)

// hardcoverCmd is the `boomtime hardcover …` parent. Subcommands live in
// internal/climeta so the admin CLI-runner can introspect + invoke the same defs
// in-process (mirrors backfill).
func hardcoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hardcover",
		Short: "Hardcover maintenance commands",
	}
	cmd.AddCommand(books.NewDedupReadsCmd())
	return cmd
}
