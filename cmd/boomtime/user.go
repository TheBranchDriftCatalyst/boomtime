package main

// `boomtime user ...` — offline user administration for the user-model
// substrate (gaka-0oe.10): set-role, disable, enable, list, show. No HTTP
// surface for the state-changing commands; these are operator tools run
// against the DB directly. Every entity/role argument is TAB-completable via
// the completion layer (internal/climeta, aliased in completion.go).
//
// The READ-ONLY pair (list, show) is built by internal/climeta so the admin
// CLI-runner (BOOM_FEATURE_ADMIN_CLI) can introspect + invoke the same
// definitions in-process. set-role / disable / enable stay here: they are
// deliberately NOT web-exposed (no annotation, no registry entry).
import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/climeta"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// openDB opens a DB connection for a CLI command using the loaded config.
func openDB(ctx context.Context) (*db.DB, error) {
	return db.New(ctx, config.Load().DatabaseURL())
}

func userCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage users: roles, enable/disable, list, show",
		Long: "Offline user administration for the user-model substrate " +
			"(gaka-0oe). Roles gate capabilities when BOOM_FEATURE_USER_MODEL=on; " +
			"a disabled user fails closed on every auth path.",
	}
	cmd.AddCommand(climeta.NewUserListCmd(), climeta.NewUserShowCmd(), userSetRoleCmd(), userDisableCmd(), userEnableCmd())
	return cmd
}

func userSetRoleCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "set-role <username> <role>",
		Short:             "Set a user's role (" + fmt.Sprint(auth.RoleStrings()) + ")",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeUsernameThenRole,
		RunE: func(_ *cobra.Command, args []string) error {
			username, role := args[0], args[1]
			if !auth.ValidRole(role) {
				return fmt.Errorf("invalid role %q; valid roles: %v", role, auth.RoleStrings())
			}
			ctx := context.Background()
			database, err := openDB(ctx)
			if err != nil {
				return err
			}
			defer database.Close()
			ok, err := database.SetUserRole(ctx, username, role)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no such user: %q", username)
			}
			fmt.Printf("user %q role set to %q\n", username, role)
			return nil
		},
	}
}

func userDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "disable <username>",
		Short:             "Disable a user (fails closed on every auth path when the substrate is on)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUsernames,
		RunE:              func(_ *cobra.Command, args []string) error { return setDisabled(args[0], true) },
	}
}

func userEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "enable <username>",
		Short:             "Re-enable a disabled user",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUsernames,
		RunE:              func(_ *cobra.Command, args []string) error { return setDisabled(args[0], false) },
	}
}

func setDisabled(username string, disable bool) error {
	ctx := context.Background()
	database, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer database.Close()
	ok, err := database.SetUserDisabled(ctx, username, disable)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no such user: %q", username)
	}
	verb := "enabled"
	if disable {
		verb = "disabled"
	}
	fmt.Printf("user %q %s\n", username, verb)
	return nil
}
