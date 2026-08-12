// Command boomtime is the CLI entrypoint (mirrors Cli.hs): run, run-migrations,
// create-user, create-token.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/comfyui"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/domains/audiobooks"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/github"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobsevents"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logging"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/notify"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/imagejobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/server"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/worker/labelimages"
	"github.com/joho/godotenv"
	"github.com/labstack/echo/v5"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
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
	root.AddCommand(runCmd(), runMigrationsCmd(), createUserCmd(), createTokenCmd(), userCmd(), rotateEncryptionKeyCmd(), labelImagesCmd(), backfillCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCmd() *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start the server (runs migrations, serves, starts the import worker)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := config.Load()
			cfg.Version = version
			cfg.Branch = branch
			cfg.Commit = commit
			cfg.BuildTime = buildTime
			// gaka-worker-topology: --role overrides BOOM_ROLE when passed;
			// empty (the default) leaves cfg.Role exactly as Load() read it
			// from the env, which itself defaults to "all" — today's
			// single-process behavior, unchanged.
			if role != "" {
				cfg.Role = role
			}
			// Apply BOOM_GRADE_* overrides once at boot so every downstream
			// stats.Grade() picks up the operator's calibration without threading
			// cfg through every renderer.
			stats.DefaultGradeConfig = cfg.Grade
			// gaka-0oe: publish the user-model switch to the process-global the
			// Identify seam reads (avoids threading the flag through every
			// handler's config). Default-off preserves today's behavior.
			auth.SetUserModelEnabled(cfg.FeatureUserModel)
			logger, logHub := logging.Setup(cfg)

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			// gaka-0oe.11: when BOOM_AUTH_PROVIDER=oidc, run OIDC discovery
			// against the issuer and install the OIDCResolver as the active
			// provider (apihelpers.Identify* delegates to it; the /auth/*/oidc
			// handlers type-assert it). Default "local" skips this entirely —
			// no boot-time network dependency, behavior byte-identical.
			// Construct the OIDC resolver whenever OIDC is CONFIGURED (issuer
			// set), so the account-LINK flow (gaka-b5n.4) works even under
			// provider=local. Only make it the ACTIVE login provider when
			// provider=oidc. Discovery failure is fatal only when oidc is
			// active; under local it's a warn (linking just unavailable).
			if cfg.OIDCIssuer != "" {
				oidcResolver, oerr := auth.NewOIDCResolver(ctx, cfg.OIDCIssuer, cfg.OIDCAuthorizeURL, cfg.OIDCClientID, cfg.OIDCClientSecret,
					cfg.OIDCRedirectURL, cfg.OIDCGroupToRole, cfg.OIDCAutoprovision)
				if oerr != nil {
					if cfg.OIDCEnabled() {
						logger.Error("BOOM_AUTH_PROVIDER=oidc but OIDC discovery failed", "err", oerr, "issuer", cfg.OIDCIssuer)
						return fmt.Errorf("oidc init: %w", oerr)
					}
					logger.Warn("OIDC configured but discovery failed; account-linking unavailable", "err", oerr, "issuer", cfg.OIDCIssuer)
				} else {
					auth.SetOIDCResolver(oidcResolver)
					if cfg.OIDCEnabled() {
						auth.SetResolver(oidcResolver)
						logger.Info("OIDC auth provider active", "issuer", cfg.OIDCIssuer,
							"autoprovision", cfg.OIDCAutoprovision)
					} else {
						logger.Info("OIDC configured for account-linking (provider=local)", "issuer", cfg.OIDCIssuer)
					}
				}
			}

			// gaka-2ip Phase 1: per-user GitHub connect. Construct + install the
			// OAuth resolver ONLY when the feature is fully configured (gate on +
			// client id/secret + state signing key). Default-off = this whole
			// block is skipped and the /auth/github/* routes never register —
			// inert, no boot-time dependency, behavior byte-identical.
			if cfg.GithubConnectEnabled() {
				gh := auth.NewGithubOAuthResolver(cfg.GithubOAuthClientID, cfg.GithubOAuthClientSecret, cfg.GithubOAuthRedirectURL)
				auth.SetGithubOAuthResolver(gh)
				logger.Info("GitHub connect enabled (gaka-2ip)", "redirect_url", cfg.GithubOAuthRedirectURL)
			}

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

			// gaka-93f.27: reap avatar renders orphaned by a restart. The render
			// runs as an in-process goroutine (identity.RegenerateAvatar), so any
			// row still 'running' at boot is stale (its goroutine died with the
			// old process) and the FE would poll it forever. Best-effort — a
			// failure here must never block startup.
			if n, rerr := database.ReapOrphanedAvatarRenders(ctx); rerr != nil {
				logger.Warn("avatar reaper failed", "err", rerr)
			} else if n > 0 {
				logger.Info("reaped orphaned avatar renders", "count", n)
			}

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
				// gaka-worker-topology: the startup reconcile (fill in any
				// missing images) is generation work, so it belongs to the
				// worker role only. It ALSO must not run alongside the AMQP
				// consumer (broker=rabbitmq) — the consumer already generates,
				// so both firing double-generates every regen (one via the
				// job, one via the reconcile). LabelImagesReconcileEnabled()
				// gates it: "auto" (default) = only under the in-process broker;
				// "on"/"off" force it. Default role=all + inprocess keeps the
				// old unconditional behavior.
				// Drain pods (BOOM_JOBS_DRAIN) claim+run then exit, so skip the
				// reconcile scan there — it would race shutdown (closed-pool) and
				// re-scan every label on each short-lived pod.
				if cfg.IsWorkerRole() && !cfg.JobsDrain && cfg.LabelImagesReconcileEnabled() {
					go liWorker.Run(ctx)
				}
			}
			if len(cfg.AdminUsers) > 0 {
				logger.Info("admin users configured", "count", len(cfg.AdminUsers))
			}

			// gaka-worker-topology: role/broker-aware wiring. Default
			// (role=all, broker=inprocess) reproduces today's single-
			// process behavior exactly — see the `default:` arm below,
			// byte-identical to the pre-decoupling pool wiring. See
			// docs/design/worker-topology-decoupling.md.
			var (
				e       *echo.Echo
				h       *handler.Handler
				imgPool *imagejobs.Pool
			)
			// Domain-agnostic per-user notification hub. Constructed once per
			// process (before the role split) so BOTH the server WS handler and
			// the jobs block's audiobooks finished-detection publish through the
			// SAME hub. Additive alongside the jobsevents hub — the jobs toasts
			// keep their own hub + stream. On a worker-only role (h==nil) it has
			// no WS readers, matching the in-process posture documented on
			// internal/notify (a cross-pod relay is the split-topology follow-up).
			notifyHub := notify.NewHub()
			if cfg.IsServerRole() {
				e, h = server.NewWithHandler(database, cfg, logger, worker, hub, logHub)
				// Wire the labelimages worker into the handler for the
				// admin regen endpoints. Passing nil is fine when the
				// feature is off — the admin handler detects the nil
				// worker and returns 503.
				h.SetLabelImagesWorker(liWorker)
				// /api/v1/notify/ws fans events to the owning user's browser.
				h.SetNotify(notifyHub)
			}

			// gaka-8bz / worker-topology: image-job queue wiring. Only
			// wire when the feature is enabled — a nil pool/producer keeps
			// the admin handler + WS returning 503, the same behavior as a
			// nil labelimages worker (they gate on both).
			if liWorker != nil {
				concurrency := labelImageConcurrency()
				// The ONE shared regeneration core: this same ExecutorFunc
				// value is handed to BOTH the in-process Pool (inprocess
				// broker) and the AMQPConsumer (rabbitmq broker) below — no
				// per-transport copy. j.ToLabelEntry() is the single named
				// Job->Entry mapping (imagejobs.Job.ToLabelEntry), and
				// RegenerateEntry is the same entrypoint the CLI's
				// RegenerateOne/RegenerateAll ultimately funnel into via
				// Worker.generateAndSave — see internal/worker/labelimages.
				exec := imagejobs.ExecutorFunc(func(execCtx context.Context, j imagejobs.Job) error {
					return liWorker.RegenerateEntry(execCtx, j.ToLabelEntry())
				})

				switch {
				case cfg.BrokerRabbit():
					amqpConn, aerr := amqp.Dial(cfg.RabbitURL)
					if aerr != nil {
						return fmt.Errorf("rabbitmq connect: %w", aerr)
					}
					defer amqpConn.Close()
					rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
					defer rdb.Close()
					bus := imagejobs.NewRedisEventBus(rdb)

					// Cross-pod Admin Logs relay ("worker logs in Admin Logs
					// viewer"): a boomtime-worker pod's own LogHub collects
					// its slog records but nothing reads it (role=worker
					// binds no HTTP API). Relay them over Dragonfly/Redis so
					// the server pod can inject them into ITS hub and the
					// existing /api/v1/logs/ws stream picks them up.
					//
					// Strict role equality (== "worker" / == "server"), NOT
					// IsWorkerRole()/IsServerRole() — those also match
					// role="all", where server and worker are the SAME
					// process sharing ONE hub already; relaying on top of
					// that would inject every record a second time.
					if cfg.Role == "server" {
						go logging.SubscribeRedisIntoHub(ctx, logHub, rdb)
						logger.Info("logging: subscribed to worker log relay", "channel", logging.LogsChannel)
					}
					if cfg.Role == "worker" {
						hostID, herr := os.Hostname()
						if herr != nil || hostID == "" {
							hostID = "unknown-worker-host"
						}
						go logging.RelayHubToRedis(ctx, logHub, rdb, hostID)
						logger.Info("logging: relaying logs to server via redis", "channel", logging.LogsChannel, "host", hostID)
					}

					if cfg.IsServerRole() {
						producerCh, cherr := amqpConn.Channel()
						if cherr != nil {
							return fmt.Errorf("rabbitmq producer channel: %w", cherr)
						}
						defer producerCh.Close()
						producer, perr := imagejobs.NewAMQPProducer(amqpConn, producerCh, cfg.RabbitQueue, rdb, bus, logger)
						if perr != nil {
							return fmt.Errorf("rabbitmq producer: %w", perr)
						}
						// The mirror has no Pool — it exists purely to keep
						// AdminLabelImagesWS truthful by relaying the
						// worker pod's events (see Registry.Apply).
						mirror := imagejobs.NewRegistry(logger)
						go imagejobs.PumpBusIntoRegistry(ctx, bus, mirror)
						h.SetImageJobQueue(producer)
						h.SetImageJobEvents(mirror)
						logger.Info("imagejobs: amqp producer wired", "queue", cfg.RabbitQueue)
					}
					if cfg.IsWorkerRole() {
						consumerCh, cherr := amqpConn.Channel()
						if cherr != nil {
							return fmt.Errorf("rabbitmq consumer channel: %w", cherr)
						}
						defer consumerCh.Close()
						consumer := imagejobs.NewAMQPConsumer(consumerCh, cfg.RabbitQueue, exec, bus, rdb, concurrency, logger)
						go func() {
							if rerr := consumer.Run(ctx); rerr != nil {
								logger.Error("imagejobs: amqp consumer stopped", "err", rerr)
							}
						}()
					}
				default: // "inprocess" — TODAY'S CODE, unchanged. server role only.
					if cfg.IsServerRole() {
						registry := imagejobs.NewRegistry(logger)
						imgPool = imagejobs.NewPool(imagejobs.PoolConfig{
							Concurrency: concurrency,
							Registry:    registry,
							Executor:    exec,
							Logger:      logger,
						})
						imgPool.Start(ctx)
						h.SetImageJobQueue(registry)
						h.SetImageJobEvents(registry)
						logger.Info("imagejobs pool wired", "concurrency", concurrency)
					}
				}
			}

			// catalyst-go-jobs (gaka-hney): ALWAYS wired — the jobs table exists
			// (migration 00054), so the admin Jobs tab + trigger/retry work
			// regardless of the schedule. Additive + independent of the image-job
			// path above; DB-backed queue by default (BOOM_JOBS_PROVIDER=local) or
			// the RabbitMQ provider when opted in. The worker always runs (so
			// triggered jobs process); only the github-refresh SCHEDULE is gated
			// on its interval + FeatureGithubStats.
			{
				jobStore := jobs.NewStore(database.Pool)
				jobReg := jobs.NewRegistry()

				// The github handler is wired HERE (Service + DB in scope) so the
				// jobs package stays domain-free: fan over connected users, refresh
				// each. A rate-limit fails the batch so it retries later; a per-user
				// error (incl. a token SyncUser marks invalid) is logged + skipped.
				githubSvc := github.NewService(database, logger)
				jobReg.Register(github.GithubStatsRefreshKind, jobs.HandlerFunc(func(jctx context.Context, _ jobs.Job) error {
					users, uerr := database.ListUsersWithGithubToken(jctx)
					if uerr != nil {
						return uerr
					}
					for _, u := range users {
						if _, serr := githubSvc.SyncUser(jctx, u); serr != nil {
							if errors.Is(serr, github.ErrRateLimited) {
								return fmt.Errorf("github rate limited at user %q: %w", u, serr)
							}
							logger.Warn("github refresh: user sync failed", "user", u, "err", serr)
						}
					}
					logger.Info("github refresh: batch complete", "users", len(users))
					return nil
				}))

				// catalyst-audiobooks (gaka-books): the Audible forward-sync +
				// one-shot backfill kinds. Wired HERE (Service + DB in scope) so
				// the jobs package stays domain-free, gated on BooksEnabled so
				// main ships dark. The Service publishes finished-book events
				// through the shared notifyHub and mirrors finishes to Hardcover.
				if cfg.BooksEnabled() {
					audioSvc := audiobooks.New(database, amazon.NewStore(database), logger)
					audioSvc.SetNotify(notifyHub)
					audioSvc.SetHardcover(hardcover.NewStore(database))

					// Forward: fan over every connected user, delta-sync each. A
					// per-user error is logged + skipped so one bad credential
					// doesn't fail the batch.
					jobReg.Register(audiobooks.AudibleSyncKind, jobs.HandlerFunc(func(jctx context.Context, _ jobs.Job) error {
						users, uerr := database.ListUsersWithAmazonDevice(jctx)
						if uerr != nil {
							return uerr
						}
						for _, u := range users {
							if _, serr := audioSvc.SyncUser(jctx, u); serr != nil {
								logger.Warn("audible forward: user sync failed", "user", u, "err", serr)
							}
						}
						logger.Info("audible forward: batch complete", "users", len(users))
						return nil
					}))

					// Backfill: one-shot per user (owner-scoped payload), enqueued
					// on demand from the connect flow / admin. Single attempt — an
					// all-time sweep is heavy and re-runnable by hand.
					jobReg.Register(audiobooks.AudibleBackfillKind, jobs.HandlerFunc(func(jctx context.Context, job jobs.Job) error {
						if job.Owner == "" {
							return fmt.Errorf("audible backfill: missing owner")
						}
						return audioSvc.BackfillUser(jctx, job.Owner)
					}))
					logger.Info("jobs: audiobooks handlers registered", "audibleSyncEnabled", cfg.AudibleSyncEnabled())
				}

				// avatar-render kind (gaka-hney.7): render a user's avatar on the
				// worker + toast them on completion. Registered when the shim is
				// configured (same gate as the synchronous path).
				if cfg.LabelImagesEnabled() {
					if shim, serr := comfyui.NewClient(cfg.ComfyUIShimURL); serr == nil && shim != nil {
						jobReg.Register(identity.AvatarRenderKind, jobs.HandlerFunc(func(jctx context.Context, job jobs.Job) error {
							var p identity.AvatarRenderPayload
							if uerr := json.Unmarshal(job.Payload, &p); uerr != nil {
								return uerr
							}
							rctx, cancel := context.WithTimeout(jctx, 25*time.Minute)
							defer cancel()
							return identity.RunAvatarRender(rctx, database, shim, logger, job.Owner, p.Prompt, p.Model, p.Size, p.Seed)
						}))
						logger.Info("jobs: avatar-render handler registered")
					}
				}

				// label-image kind (gaka-hney.3): proves catalyst-go-jobs can run
				// the image-regen path (liWorker.RegenerateOne — the same
				// entrypoint the imagejobs executor funnels into). Behind
				// BOOM_JOBS_UNIFIED (default off) so prod stays on the imagejobs
				// pipeline + its dedicated admin UI; the live cutover (reroute
				// enqueue + migrate that UI, then delete imagejobs) is a deliberate
				// future flip.
				if cfg.JobsUnified && liWorker != nil {
					jobReg.Register(labelimages.RegenJobKind, jobs.HandlerFunc(func(jctx context.Context, job jobs.Job) error {
						var p labelimages.RegenJobPayload
						if uerr := json.Unmarshal(job.Payload, &p); uerr != nil {
							return uerr
						}
						if p.LabelID == "" {
							return fmt.Errorf("label-image job missing labelId")
						}
						// RegenerateEntry (not RegenerateOne) so per-entry
						// model/size/seed overrides survive the fold — same
						// fidelity the imagejobs executor had.
						return liWorker.RegenerateEntry(jctx, p.Entry())
					}))
					logger.Info("jobs: label-image handler registered (BOOM_JOBS_UNIFIED)")
				}

				// Per-kind fleet-wide concurrency caps (job-layer throughput
				// control, NOT per-HTTP-request): each external-API kind shares ONE
				// throttled queue across pods+users. A kind at its cap is excluded
				// from ClaimNext so its backlog stays durably status=queued and
				// drains as slots free; every other kind keeps flowing. Enforced by
				// the KindLimiter wired below (Dragonfly-backed fleet-wide, or an
				// in-process fallback when there's no redis). Unset kinds stay
				// unlimited. Only kinds that actually exist as registered handler
				// kinds are set here.
				//
				// NOTE (kind-name reconciliation vs. the spec):
				//   - the backfill kind is audiobooks.AudibleBackfillKind
				//     ("audiobooks-audible-backfill"), NOT the spec's literal
				//     "books-audible-backfill" — which is not a registered kind, so
				//     the cap is set on the real constant.
				//   - "hardcover-push" is NOT a registered jobs kind (Hardcover is a
				//     mirror inside the audible sync handler, not its own job), so it
				//     is intentionally skipped — no cap to set.
				jobReg.SetConcurrency(github.GithubStatsRefreshKind, 2)  // github-stats-refresh
				jobReg.SetConcurrency(identity.AvatarRenderKind, 1)      // avatar-render
				jobReg.SetConcurrency(labelimages.RegenJobKind, 1)       // label-image
				jobReg.SetConcurrency(audiobooks.AudibleSyncKind, 1)     // audiobooks-audible-sync
				jobReg.SetConcurrency(audiobooks.AudibleBackfillKind, 1) // audiobooks-audible-backfill (spec's "books-audible-backfill")

				hostID, _ := os.Hostname()
				if hostID == "" {
					hostID = "boomtime-jobs"
				}

				var provider jobs.Provider = jobs.NewLocalProvider(jobStore, logger, hostID)
				if cfg.JobsBrokerRabbit() {
					jconn, jerr := amqp.Dial(cfg.RabbitURL)
					if jerr != nil {
						return fmt.Errorf("jobs rabbitmq connect: %w", jerr)
					}
					defer jconn.Close()
					jch, jcherr := jconn.Channel()
					if jcherr != nil {
						return fmt.Errorf("jobs rabbitmq channel: %w", jcherr)
					}
					defer jch.Close()
					amqpProv, aperr := jobs.NewAMQPProvider(jch, cfg.RabbitQueue+".jobs", jobStore, logger, hostID, 4)
					if aperr != nil {
						return fmt.Errorf("jobs rabbitmq provider: %w", aperr)
					}
					provider = amqpProv
				}
				// Kind-routing (gaka-hney): the always-on server excludes heavy
				// kinds; a ScaledJob includes them. Only the local provider filters.
				if lp, ok := provider.(*jobs.LocalProvider); ok {
					lp.SetKindFilter(cfg.JobsKinds, cfg.JobsExcludeKinds)
				}

				// Job-layer concurrency throttle (fleet-wide per-kind caps set on
				// jobReg above). A Dragonfly/Redis client makes the semaphore
				// shared across pods+users; with no BOOM_REDIS_ADDR the limiter
				// falls back to an in-process counter (correct for a single pod —
				// local dev / broker=local). Wired on BOTH provider paths.
				var jobsRedis *redis.Client
				if cfg.RedisAddr != "" {
					jobsRedis = redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
					defer jobsRedis.Close()
				}
				provider.SetLimiter(jobs.NewKindLimiter(jobsRedis))

				// Push hub for job-completion toasts (gaka-hney.6): the provider
				// notifies it on terminal events; /api/v1/jobs/ws fans them to the
				// owning user's browser.
				jobHub := jobsevents.NewHub()
				provider.SetNotifier(jobHub)

				// The HTTP Handler only exists on server roles (line ~271). Worker
				// / ScaledJob-drain pods have h == nil — they need the provider +
				// registry, not the admin/ws wiring — so guard these. (Without the
				// guard, --role=worker nil-panics in SetJobs the moment the jobs
				// block runs, incl. the KEDA drain pod and the image worker.)
				if h != nil {
					h.SetJobs(jobStore, provider) // admin Jobs tab (list/trigger/retry)
					h.SetJobEvents(jobHub)        // /api/v1/jobs/ws push stream
				}

				logger.Info("jobs: wired", "provider", provider.Name(),
					"githubRefreshEnabled", cfg.GithubStatsRefreshEnabled(),
					"githubRefreshInterval", cfg.GithubStatsRefreshInterval.String())

				// ScaledJob one-shot mode (gaka-hney): a KEDA ScaledJob pod sets
				// BOOM_JOBS_DRAIN=true — build the registry (done above), drain
				// every due job to completion, then EXIT. Long jobs run fully (a
				// ScaledJob Job is never killed on scale-down), so no mid-job kill
				// and no ComfyUI redelivery amplification. No scheduler, no HTTP
				// server on these pods.
				if cfg.JobsDrain {
					lp, ok := provider.(*jobs.LocalProvider)
					if !ok {
						return fmt.Errorf("BOOM_JOBS_DRAIN requires the local provider, got %q", provider.Name())
					}
					logger.Info("jobs: drain mode — processing due jobs then exiting")
					if derr := lp.Drain(ctx, jobReg); derr != nil {
						return fmt.Errorf("jobs drain: %w", derr)
					}
					return nil
				}

				// The github-refresh schedule is the only piece gated on config;
				// leader-singleton via the DB, so running it on every server is safe.
				if cfg.IsServerRole() && (cfg.GithubStatsRefreshEnabled() || cfg.AudibleSyncEnabled()) {
					sched := jobs.NewScheduler(jobStore, provider, logger)
					if cfg.GithubStatsRefreshEnabled() {
						if serr := sched.Register(ctx, github.GithubStatsRefreshKind, cfg.GithubStatsRefreshInterval); serr != nil {
							logger.Warn("jobs: schedule register failed", "err", serr)
						}
					}
					// Audible forward sync (gaka-books): leader-singleton via the DB,
					// so running the schedule on every server is safe. The backfill
					// kind is NOT scheduled — it's enqueued on demand.
					if cfg.AudibleSyncEnabled() {
						if serr := sched.Register(ctx, audiobooks.AudibleSyncKind, cfg.AudibleSyncInterval); serr != nil {
							logger.Warn("jobs: audible schedule register failed", "err", serr)
						}
					}
					go sched.Run(ctx)
				}
				// Run the jobs worker on the always-on SERVER (not just the worker
				// role): the LOCAL provider polls the shared jobs table, and prod's
				// dedicated image worker (boomtime-worker, --role=worker) is
				// KEDA-scaled to ZERO when idle, so it can't be relied on to drain
				// DB jobs — they'd sit queued forever. Also run on a dedicated
				// worker role (AMQP topologies). SKIP LOCKED keeps concurrent
				// workers safe.
				if cfg.IsServerRole() || cfg.IsWorkerRole() {
					go func() {
						if rerr := provider.Run(ctx, jobReg); rerr != nil && !errors.Is(rerr, context.Canceled) {
							logger.Error("jobs: provider stopped", "err", rerr)
						}
					}()
				}
			}

			if !cfg.IsServerRole() {
				// --role=worker: no HTTP API. Bind a minimal /healthz for
				// k8s liveness/readiness probes (see
				// k8s/base/worker-deployment.yaml) and block until
				// SIGTERM/SIGINT.
				return runWorkerHealthz(ctx, logger)
			}

			addr := fmt.Sprintf(":%d", cfg.Port)
			logger.Info("starting server", "addr", addr, "env", cfg.Env, "role", cfg.Role)

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
	cmd.Flags().StringVar(&role, "role", "",
		`Override BOOM_ROLE ("server", "worker", or "all"); empty keeps BOOM_ROLE (default "all")`)
	return cmd
}

// runWorkerHealthz binds a minimal /healthz on :8081 for a --role=worker
// process (which serves no HTTP API — see k8s/base/worker-deployment.yaml's
// liveness/readiness probes) and blocks until ctx is cancelled, then drains
// the listener. Returns nil on a clean shutdown.
func runWorkerHealthz(ctx context.Context, logger *slog.Logger) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: ":8081", Handler: mux}
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	logger.Info("worker role started (no HTTP API)", "healthz_addr", srv.Addr)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
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
	// Smart completion: TAB the -u flag to pick an existing user from the DB.
	_ = cmd.RegisterFlagCompletionFunc("username", completeUsernames)
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
