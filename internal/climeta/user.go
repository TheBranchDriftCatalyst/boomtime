package climeta

// `boomtime user list` / `boomtime user show` — the READ-ONLY half of the
// offline user administration commands (gaka-0oe.10), relocated from
// cmd/boomtime/user.go so the admin CLI-runner can introspect the command
// defs and call the same bodies in-process. The state-changing siblings
// (set-role / disable / enable) deliberately stay in cmd/boomtime — they are
// not web-exposed (deferred), so they carry no annotation and no registry
// entry.
import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// openDB opens a DB connection for a CLI command using the loaded config.
func openDB(ctx context.Context) (*db.DB, error) {
	return db.New(ctx, config.Load().DatabaseURL())
}

// NewUserListCmd builds the `user list` command def — shared by the CLI
// (cmd/boomtime) and the admin CLI-runner's spec introspection.
func NewUserListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all users with role + status",
		Args:  cobra.NoArgs,
		// Web allowlist (admin CLI-runner): pure read, runs freely.
		Annotations: map[string]string{WebAnnotation: ClassReadonly},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := context.Background()
			database, err := openDB(ctx)
			if err != nil {
				return err
			}
			defer database.Close()
			return RunUserList(ctx, database, cmd.OutOrStdout())
		},
	}
}

// NewUserShowCmd builds the `user show <username>` command def — shared by the
// CLI (cmd/boomtime) and the admin CLI-runner's spec introspection.
func NewUserShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "show <username>",
		Short:             "Show a user's role, status, and capability overrides",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: CompleteUsernames,
		// Web allowlist (admin CLI-runner): pure read, runs freely.
		Annotations: map[string]string{WebAnnotation: ClassReadonly},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			database, err := openDB(ctx)
			if err != nil {
				return err
			}
			defer database.Close()
			return RunUserShow(ctx, database, args[0], cmd.OutOrStdout())
		},
	}
}

// RunUserList is the extracted `user list` body: every user's username, role,
// and status as a tab-aligned table. Extracted from the RunE (which wrote
// straight to os.Stdout) so the admin CLI-runner can capture the output.
func RunUserList(ctx context.Context, database *db.DB, out io.Writer) error {
	rows, err := database.ListUsersAdmin(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no users")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "USERNAME\tROLE\tSTATUS")
	for _, r := range rows {
		status := "active"
		if r.DisabledAt != nil {
			status = "disabled " + r.DisabledAt.UTC().Format("2006-01-02")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.Username, r.Role, status)
	}
	return w.Flush()
}

// RunUserShow is the extracted `user show <username>` body: role, status, and
// the raw capability-override blob for one user.
func RunUserShow(ctx context.Context, database *db.DB, username string, out io.Writer) error {
	u, err := database.GetUserFullByName(ctx, username)
	if err != nil {
		return err
	}
	if u == nil {
		return fmt.Errorf("no such user: %q", username)
	}
	status := "active"
	if u.DisabledAt != nil {
		status = "disabled at " + u.DisabledAt.UTC().Format("2006-01-02 15:04:05 MST")
	}
	caps := string(u.Capabilities)
	if caps == "" || caps == "{}" {
		caps = "{} (role defaults)"
	}
	fmt.Fprintf(out, "username:     %s\n", u.Username)
	fmt.Fprintf(out, "role:         %s\n", u.Role)
	fmt.Fprintf(out, "status:       %s\n", status)
	fmt.Fprintf(out, "capabilities: %s\n", caps)
	return nil
}
