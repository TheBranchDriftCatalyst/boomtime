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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logging"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/backfilljobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/imagejobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/server"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	labelcatalog "github.com/TheBranchDriftCatalyst/boomtime/internal/labelcatalog"
	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/worker/labelimages"
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
	root.AddCommand(runCmd(), runMigrationsCmd(), createUserCmd(), createTokenCmd(), rotateEncryptionKeyCmd(), labelImagesCmd(), backfillCmd())

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

			// gaka-6jm.2: probe the at-rest encryption key at boot. We
			// deliberately do NOT fail startup on a missing/invalid key in
			// dev/test so existing dev stacks still run — the check is a
			// WARNING and any downstream Encrypt/Decrypt call surfaces the
			// real error when the feature is exercised.
			//
			// gaka-6jm.9: production is different. If BOOM_ENV=prod|production
			// and the key is missing/invalid, exit(1) with a clear log — a
			// silent WARN in prod is how you ship a "never persisted a single
			// Wakatime key and nobody noticed for a month" incident.
			if err := auth.LoadKeyFromEnv(); err != nil {
				if isProdEnv(cfg.Env) {
					logger.Error("BOOM_ENCRYPTION_KEY is required when BOOM_ENV=prod/production",
						"err", err,
						"remediation", "generate with: openssl rand -base64 32 and set BOOM_ENCRYPTION_KEY")
					return fmt.Errorf("BOOM_ENCRYPTION_KEY required in production: %w", err)
				}
				logger.Warn("BOOM_ENCRYPTION_KEY not configured — encrypted-at-rest features are inert",
					"err", err,
					"remediation", "generate with: openssl rand -base64 32 and set BOOM_ENCRYPTION_KEY in .env")
			} else {
				logger.Info("BOOM_ENCRYPTION_KEY loaded — AES-256-GCM ready")
			}

			// gaka-n5r: refuse to start in prod without a CORS allowlist. In dev
			// we fall through — server.New() logs a WARN and defaults to
			// localhost origins so local flows keep working. In prod, an unset
			// allowlist means either (a) the operator forgot and every attacker
			// origin will be denied, breaking their own frontend, or (b) they
			// wanted "no CORS" — neither is safe to guess at, so we fail loud.
			if isProdEnv(cfg.Env) && strings.TrimSpace(os.Getenv("BOOM_CORS_ALLOWED_ORIGINS")) == "" {
				logger.Error("BOOM_CORS_ALLOWED_ORIGINS is required when BOOM_ENV=prod/production",
					"remediation", "set BOOM_CORS_ALLOWED_ORIGINS=https://your.public.hostname (comma-separate multiple)")
				return fmt.Errorf("BOOM_CORS_ALLOWED_ORIGINS required in production")
			}

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

			// gaka-awh.5: legacy raw-token backfill now lives in migration
			// 00030_backfill_hashed_tokens.sql (SQL via pgcrypto.digest).
			// No boot-time step required.

			// Durability: mark any queued/running jobs left over from a previous
			// process as failed before accepting new work.
			hub := importer.NewHub()
			worker := importer.NewWorker(ctx, database, logger, hub)
			worker.RecoverInterrupted(ctx)

			// gaka-myv: label-images generation worker. NewWorker returns nil
			// when the feature is off (flag unset OR shim URL unset); a
			// non-nil worker generates any missing images in a detached
			// goroutine so the HTTP server binds immediately. If the flag is
			// on but the URL is missing we already treat the feature as off
			// via LabelImagesEnabled(); log a WARN so the operator notices
			// the misconfig.
			if cfg.FeatureLabelImages && !cfg.LabelImagesEnabled() {
				logger.Warn("BOOM_FEATURE_LABEL_IMAGES=on but BOOM_COMFYUI_SHIM_URL is unset — feature is inert",
					"remediation", "set BOOM_COMFYUI_SHIM_URL=http://host:8012 or unset BOOM_FEATURE_LABEL_IMAGES")
			}
			liWorker, err := labelimages.NewWorker(cfg, database, logger)
			if err != nil {
				return fmt.Errorf("labelimages worker: %w", err)
			}
			if liWorker != nil {
				logger.Info("labelimages worker enabled",
					"shim_url", cfg.ComfyUIShimURL, "model", cfg.ComfyUIModel)
				go liWorker.Run(ctx)
			}
			if len(cfg.AdminUsers) > 0 {
				logger.Info("admin users configured", "count", len(cfg.AdminUsers))
			}

			e, h := server.NewWithHandler(database, cfg, logger, worker, hub, logHub)
			// Wire the labelimages worker into the handler for the admin
			// regen endpoints. Passing nil is fine when the feature is off
			// — the admin handler detects the nil worker and returns 503.
			h.SetLabelImagesWorker(liWorker)

			// gaka-vh8: in-memory backfill job registry. Always non-nil in
			// prod; the CLI is the executor so we don't need a Pool here,
			// just a registry the HTTP + WS handlers can mutate.
			backfillRegistry := backfilljobs.NewRegistry(logger)
			h.SetBackfillJobQueue(backfillRegistry)
			logger.Info("backfilljobs registry wired")

			// gaka-8bz: in-memory job queue + worker pool for label-image
			// regens. Only wire when the feature is enabled — a nil pool
			// keeps the admin handler + WS returning 503, which is the
			// same behavior as a nil labelimages worker (they gate on both).
			var imgPool *imagejobs.Pool
			if liWorker != nil {
				concurrency := labelImageConcurrency()
				registry := imagejobs.NewRegistry(logger)
				exec := imagejobs.ExecutorFunc(func(execCtx context.Context, j imagejobs.Job) error {
					return liWorker.RegenerateEntry(execCtx, labelcatalog.Entry{
						ID:          j.LabelID,
						Description: j.Description,
						Prompt:      j.Prompt,
						Model:       j.Model,
						Size:        j.Size,
						Seed:        j.Seed,
					})
				})
				imgPool = imagejobs.NewPool(imagejobs.PoolConfig{
					Concurrency: concurrency,
					Registry:    registry,
					Executor:    exec,
					Logger:      logger,
				})
				imgPool.Start(ctx)
				h.SetImageJobQueue(registry)
				logger.Info("imagejobs pool wired", "concurrency", concurrency)
			}
			addr := fmt.Sprintf(":%d", cfg.Port)
			logger.Info("starting server", "addr", addr, "env", cfg.Env)

			// echo v5's Start installs its own SIGINT/SIGTERM graceful shutdown and
			// returns http.ErrServerClosed on a clean stop.
			if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			// After echo returns (either shutdown signal or an error above)
			// stop the imagejobs pool so in-flight generations get a
			// chance to observe context cancellation and exit. ComfyUI
			// calls that ignore ctx will time out this Stop — that's
			// fine, in-flight state is not durable across restarts by
			// design (gaka-8bz).
			if imgPool != nil {
				imgPool.Stop(30 * time.Second)
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
			// gaka-e5e: the CLI previously skipped strength validation
			// entirely, so `boomtime create-user -u foo` with an empty or
			// toy password minted a functional-but-trivially-compromised
			// account. Prompt in a loop on an interactive TTY until we
			// get a policy-compliant password; on piped stdin (heredoc,
			// CI) reject with a non-zero exit so the caller notices.
			password, err := promptStrongPassword("Set a password: ")
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
			raw, err := auth.CreateAPIToken(ctx, database, username, "")
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

// labelImageConcurrency reads BOOM_LABEL_IMAGE_CONCURRENCY as an int in
// the range [1, 16], defaulting to 2. Values outside that band are
// clamped rather than rejected — the pool never runs unbounded, and 16
// is comfortably above any realistic parallel-generation budget on a
// single M-series machine.
func labelImageConcurrency() int {
	const def = 2
	raw := strings.TrimSpace(os.Getenv("BOOM_LABEL_IMAGE_CONCURRENCY"))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def
	}
	if n > 16 {
		return 16
	}
	return n
}

// isProdEnv reports whether BOOM_ENV names a production environment. Matches
// both "prod" (the config default + longtime shorthand) and "production"
// (docker/k8s convention). Case-insensitive so a stray BOOM_ENV=PROD doesn't
// silently sneak past the startup gate.
func isProdEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "prod", "production":
		return true
	}
	return false
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

// promptStrongPassword prompts for a password and enforces
// auth.ValidatePassword before returning. On an interactive TTY it re-prompts
// (up to maxPasswordAttempts) on a policy failure so a human can correct their
// typo without restarting the command. On non-interactive input (piped
// stdin, heredoc, CI) it returns the policy error verbatim after ONE attempt
// — re-prompting a pipe would loop forever consuming EOF. Used by
// `boomtime create-user` (gaka-e5e).
func promptStrongPassword(prompt string) (string, error) {
	interactive := term.IsTerminal(int(syscall.Stdin))
	const maxPasswordAttempts = 3
	attempts := 1
	if interactive {
		attempts = maxPasswordAttempts
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		pw, err := promptPassword(prompt)
		if err != nil {
			return "", err
		}
		if err := auth.ValidatePassword(pw); err != nil {
			lastErr = err
			// Print to stderr so the message isn't swallowed if stdout is
			// being captured by whatever's piping.
			fmt.Fprintf(os.Stderr, "password rejected: %s\n", err.Error())
			continue
		}
		return pw, nil
	}
	if lastErr != nil {
		return "", fmt.Errorf("password rejected: %w", lastErr)
	}
	// Unreachable — attempts >= 1 always sets lastErr on a rejected
	// password and returns on an accepted one — but the compiler doesn't
	// know that, so return a defensive error.
	return "", fmt.Errorf("no password provided")
}
