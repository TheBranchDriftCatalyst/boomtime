// Command boomtime is the CLI entrypoint (mirrors Cli.hs): run, run-migrations,
// create-user, create-token.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logging"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/server"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// version, branch, commit, buildTime are stamped in via ldflags at build time:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always --dirty) \
//	                   -X main.branch=$(git branch --show-current) \
//	                   -X main.commit=$(git rev-parse HEAD) \
//	                   -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
// The Dockerfile and Taskfile both pass these. Empty defaults for a bare
// `go run` / `go build` in an untagged working tree. Surfaced by /healthz.
var (
	version   = "dev"
	branch    = ""
	commit    = ""
	buildTime = ""
)

func main() {
	// Load .env if present (dev convenience; direnv handles .envrc in the shell).
	_ = godotenv.Load()

	root := &cobra.Command{
		Use:     "boomtime",
		Short:   "Wakatime-compatible coding-time tracker",
		Version: version,
	}
	root.AddCommand(runCmd(), runMigrationsCmd(), createUserCmd(), createTokenCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Start the server (runs migrations, serves, starts the import worker)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := config.Load()
			cfg.Version = version
			cfg.Branch = branch
			cfg.Commit = commit
			cfg.BuildTime = buildTime
			// Apply BOOM_GRADE_* overrides once at boot so every downstream
			// stats.Grade() picks up the operator's calibration without threading
			// cfg through every renderer.
			stats.DefaultGradeConfig = cfg.Grade
			logger, logHub := logging.Setup(cfg)
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			if err := db.MigrateURL(ctx, cfg.DatabaseURL()); err != nil {
				return fmt.Errorf("migrations: %w", err)
			}
			logger.Info("migrations applied", "version", cfg.Version)

			database, err := db.NewWithObservability(ctx, cfg.DatabaseURL(), db.Options{
				LogQueries:  cfg.DBLogQueries,
				LogArgs:     cfg.DBLogArgs,
				N1Threshold: cfg.DBN1Threshold,
				N1DupThresh: cfg.DBN1DupThresh,
				ExplainSlow: time.Duration(cfg.DBExplainSlowMs) * time.Millisecond,
				Dev:         cfg.IsDev(),
			})
			if err != nil {
				return fmt.Errorf("db connect: %w", err)
			}
			defer database.Close()

			// Durability: mark any queued/running jobs left over from a previous
			// process as failed before accepting new work.
			hub := importer.NewHub()
			worker := importer.NewWorker(ctx, database, logger, hub)
			worker.RecoverInterrupted(ctx)

			e := server.New(database, cfg, logger, worker, hub, logHub)
			addr := fmt.Sprintf(":%d", cfg.Port)
			logger.Info("starting server", "addr", addr, "env", cfg.Env)

			// echo v5's Start installs its own SIGINT/SIGTERM graceful shutdown and
			// returns http.ErrServerClosed on a clean stop.
			if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
}

func runMigrationsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run-migrations",
		Short: "Apply database migrations and exit",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := config.Load()
			// run-migrations doesn't serve HTTP so it doesn't need the LogHub —
			// discard it. Setup is still called so the tee handler is installed
			// for the migration logs.
			logger, _ := logging.Setup(cfg)
			ctx := context.Background()
			if err := db.MigrateURL(ctx, cfg.DatabaseURL()); err != nil {
				return err
			}
			logger.Info("migrations applied successfully")
			return nil
		},
	}
}

func createUserCmd() *cobra.Command {
	var username string
	cmd := &cobra.Command{
		Use:   "create-user",
		Short: "Create a new user account",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := config.Load()
			ctx := context.Background()
			password, err := promptPassword("Set a password: ")
			if err != nil {
				return err
			}
			database, err := db.New(ctx, cfg.DatabaseURL())
			if err != nil {
				return err
			}
			defer database.Close()

			if err := auth.CreateUser(ctx, database, username, password); err != nil {
				if errors.Is(err, auth.ErrUserExists) {
					return fmt.Errorf("user %q already exists", username)
				}
				return err
			}
			fmt.Printf("User %q created.\n", username)
			fmt.Printf("Run \"boomtime create-token -u %s\" to generate a token.\n", username)
			return nil
		},
	}
	cmd.Flags().StringVarP(&username, "username", "u", "", "The user to create")
	_ = cmd.MarkFlagRequired("username")
	return cmd
}

func createTokenCmd() *cobra.Command {
	var username string
	cmd := &cobra.Command{
		Use:   "create-token",
		Short: "Create a new non-expiring API token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := config.Load()
			ctx := context.Background()
			password, err := promptPassword("Password: ")
			if err != nil {
				return err
			}
			database, err := db.New(ctx, cfg.DatabaseURL())
			if err != nil {
				return err
			}
			defer database.Close()

			if err := auth.VerifyUserCredentials(ctx, database, username, password); err != nil {
				return err
			}
			raw, err := auth.CreateAPIToken(ctx, database, username)
			if err != nil {
				return err
			}
			fmt.Println("Please save the token. You won't be able to retrieve it again.")
			fmt.Println(raw)
			return nil
		},
	}
	cmd.Flags().StringVarP(&username, "username", "u", "", "The user the token will be created for")
	_ = cmd.MarkFlagRequired("username")
	return cmd
}

// promptPassword reads a password without echoing (Utils.passwordInput).
func promptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	if term.IsTerminal(int(syscall.Stdin)) {
		b, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	// Non-interactive fallback: read one line via bufio (gaka-0tb). fmt.Scanln
	// splits on whitespace, so a piped password with spaces was silently
	// truncated at the first space and login/create-token failed with a
	// wrong-password error the user couldn't debug.
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
