// Package config parses the BOOM_* environment variables into a Config struct.
// It mirrors hakatime's ServerSettings (App.hs) and CLI DB settings (Cli.hs).
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
)

// RemoteWriteConfig configures forwarding heartbeats to another Wakatime server.
type RemoteWriteConfig struct {
	URL   string
	Token string
}

// Config holds all runtime configuration.
type Config struct {
	// Version is the git-describe string stamped into the binary at build time
	// (see cmd/boomtime/main.go and the ldflags used by the Dockerfile /
	// Taskfile). Never loaded from the env — the CLI sets it after Load().
	Version string

	// Branch, Commit, BuildTime are stamped into the binary at build time
	// alongside Version. Empty when unset (e.g. bare `go run`). Surfaced by
	// the public /healthz endpoint.
	Branch    string
	Commit    string
	BuildTime string

	Port               int
	APIPrefix          string
	BadgeURL           string
	DashboardPath      string
	ShieldsIOURL       string
	EnableRegistration bool
	SessionExpiry      int64 // hours
	LogLevel           string
	Env                string // "dev" or "prod"
	HTTPLog            bool

	// AuthProvider selects the identity backend (gaka-93f / gaka-0oe.11).
	// "local" = today's username+password + refresh cookie. "oidc" =
	// Authentik (the OIDCResolver, landing in a later bead). Read here now so
	// the public config endpoint and FE can branch the signup CTA before the
	// resolver swap lands. Default "local" preserves today's behavior.
	AuthProvider string

	// OIDC (Authentik) config (gaka-0oe.11). Consumed only when
	// AuthProvider=="oidc"; staged otherwise. See docs/design/user-model-and-
	// oidc.md §6.
	OIDCIssuer string // discovery base, trailing slash required
	// OIDCAuthorizeURL optionally overrides the BROWSER-facing authorization
	// endpoint (BOOM_OIDC_AUTHORIZE_URL). Needed in split-horizon dev: the pod
	// discovers + exchanges tokens via the cluster-internal issuer
	// (authentik-server:9000), but the browser must be redirected to a
	// host-reachable URL (localhost:9000). Empty = use the discovered endpoint.
	OIDCAuthorizeURL  string
	OIDCClientID      string            // OAuth2 client_id (Authentik app)
	OIDCClientSecret  string            // OAuth2 client_secret (from a Secret)
	OIDCRedirectURL   string            // {origin}/auth/callback/oidc
	OIDCGroupToRole   map[string]string // Authentik group name → boomtime role
	OIDCAutoprovision bool              // mint a boomtime user on first login
	OIDCAutolinkEmail bool              // DEPRECATED no-op (gaka-93f.12): username-based autolink was removed as an account-takeover vector. Parsed for env compat only; nothing reads it. Use the authenticated link flow (HandleLink).

	// GitHub stats (gaka-2ip Phase 1): per-user GitHub OAuth-App connect +
	// encrypted token storage. STRICTLY default-off and inert until BOTH the
	// gate is flipped AND the OAuth-App credentials + state signing key are
	// configured. See GithubConnectEnabled() for the exact predicate the
	// routes + public-config flag key off.
	//
	//   FeatureGithubStats       — BOOM_FEATURE_GITHUB_STATS master gate (default false).
	//   GithubOAuthClientID      — BOOM_GITHUB_OAUTH_CLIENT_ID (OAuth-App client_id).
	//   GithubOAuthClientSecret  — BOOM_GITHUB_OAUTH_CLIENT_SECRET (from a Secret).
	//   GithubOAuthRedirectURL   — BOOM_GITHUB_OAUTH_REDIRECT_URL ({origin}/auth/github/callback).
	//   OAuthStateSigningKey     — BOOM_OAUTH_STATE_SIGNING_KEY, the HMAC key for
	//                              the CSRF/owner-binding `state` (internal/oauth).
	FeatureGithubStats      bool
	GithubOAuthClientID     string
	GithubOAuthClientSecret string
	GithubOAuthRedirectURL  string
	OAuthStateSigningKey    string

	// FeatureBilling advertises whether the Stripe SaaS billing surface
	// (checkout / webhooks / tier flips, gaka-93f Phase 4) is live. Default
	// off; flipped on once the billing subsystem ships. Surfaced by
	// /api/v1/config/public so the FE can show/hide pricing + upgrade UI.
	FeatureBilling bool

	// BetaUserRegistration is the server-side kill switch for the beta
	// onboarding preview (the FE ?enable_beta_user_registration=true flow,
	// gaka-93f.1). Default true so the preview works in dev; set false in a
	// shared/prod instance to disable the preview flow entirely regardless of
	// the URL flag. The URL flag is client-driven; this gates it server-side.
	BetaUserRegistration bool

	// FeatureUserModel is the master switch for the user-demarcation substrate
	// (gaka-0oe.1). Default OFF: apihelpers.Identify returns an all-capability
	// Identity so no gate ever fires and behavior is byte-identical to
	// pre-substrate. When ON, the resolver reads the real role/capabilities
	// and fails closed on disabled accounts. The migration + columns exist
	// regardless — this flag only affects the READ/GATE paths.
	FeatureUserModel bool

	// FeatureRollupSkip lets ingest skip the expensive rollup/gap machinery
	// for identities that lack CapGenerateRollups (gaka-0oe.3). No effect
	// unless FeatureUserModel is also on. Surfaced by /healthz for ops.
	FeatureRollupSkip bool

	// FeatureAdminCLI gates the admin CLI-runner HTTP surface
	// (/api/v1/admin/cli/{spec,run,complete} — internal/admin +
	// internal/climeta). Default OFF and fully inert: when off the routes
	// are never registered, so the endpoints 404 like any unknown path.
	// When on, they remain double-gated (BOOM_ADMIN_USERS allowlist via
	// requireAdmin + CapAdmin route middleware).
	FeatureAdminCLI bool

	// CookieSecure controls the Secure attribute on the refresh_token cookie
	// (gaka-b5x part 1). Defaults to true when BOOM_ENV names a production
	// environment ("prod" / "production") so a prod deploy behind TLS never
	// forgets the flag. In dev the default is false so browsers accept the
	// cookie on http://localhost. Explicit override via BOOM_COOKIE_SECURE
	// (true|false) always wins — useful for dev users who want to smoke-test
	// the prod cookie shape against a local HTTPS reverse proxy.
	CookieSecure bool

	DBHost string
	DBPort int
	DBName string
	DBUser string
	DBPass string

	// DB observability (see internal/db/observability.go). Query logging is
	// off by default; arg logging is redacted and off by default.
	DBLogQueries    bool // BOOM_DB_LOG_QUERIES: structured per-query slog logging
	DBLogArgs       bool // BOOM_DB_LOG_ARGS: log (redacted) query args
	DBN1Threshold   int  // BOOM_DB_N1_THRESHOLD: queries/request to WARN
	DBN1DupThresh   int  // BOOM_DB_N1_DUP_THRESHOLD: identical normalized statements/request to WARN
	DBExplainSlowMs int  // BOOM_DB_EXPLAIN_SLOW_MS: dev-only auto-EXPLAIN for reads slower than this (0=off)

	RemoteWrite *RemoteWriteConfig
	GithubToken string

	// FeatureLabelImages is the master switch for the ComfyUI-generated label
	// archetype images (gaka-myv). Requires BOTH FeatureLabelImages=on AND a
	// non-empty ComfyUIShimURL — either missing means the feature is off,
	// including the startup image-generation worker and any regenerate CLI
	// probes. Public /api/v1/labels/:id/image still serves any rows already
	// in the DB even when the flag is later toggled off (the feature gate
	// only guards writes/generation, not reads).
	FeatureLabelImages bool

	// ComfyUIShimURL is the base URL of the comfyui-shim (OpenAI-shaped
	// /v1/images/generations). Empty = feature auto-disabled EVEN IF
	// FeatureLabelImages=on; a WARN is logged at startup in that case so
	// the operator notices the misconfig instead of silently getting no
	// images. See internal/comfyui/client.go for the request contract.
	ComfyUIShimURL string

	// ComfyUIModel is the shim pipeline name to pass in the `model` field
	// on every generation request. Default: sdxl-illustrious-xl (the
	// anime/emblem-friendly SDXL derivative — matches the memeification
	// aesthetic). Operators iterate by changing this env var and rerunning
	// `boomtime label-images regenerate --all` to swap the whole set.
	ComfyUIModel string

	// AdminUsers is the set of usernames allowed to hit admin-only routes
	// (currently: /api/v1/admin/label-images/*, which drives the Admin tab
	// in the FE). Populated from BOOM_ADMIN_USERS as a comma-separated
	// list. Empty (the default) disables admin routes entirely — safer
	// default than "all users are admins".
	AdminUsers map[string]struct{}

	// gaka-9v4: OpenAI-shaped chat completion endpoint used by the
	// avatar prompt-synthesis SSE endpoint (POST /api/v1/admin/avatar/
	// synthesize-prompt). The FE never talks to a third-party LLM
	// directly — every stream flows through the boomtime server so the
	// API key never leaves the host.
	//
	// LLMAPIKey is the Authorization: Bearer value; when empty the
	// endpoint returns 503 with a clear "LLM not configured" message so
	// the operator immediately sees what needs setting.
	// LLMBaseURL defaults to https://api.openai.com/v1 (any OpenAI-
	// compatible provider works — Anthropic-compat proxies, Groq, local
	// llama.cpp servers, etc.).
	// LLMModel defaults to gpt-4o-mini (cheap; fine for a one-paragraph
	// portrait prompt).
	LLMAPIKey  string
	LLMBaseURL string
	LLMModel   string

	// WakatimeAPIKey is the server-configured key used to import history from
	// wakatime.com when the request body omits apiToken. Sourced from
	// WAKATIME_API_KEY, falling back to BOOM_REMOTE_WRITE_TOKEN. Never exposed.
	// TODO: Change this so its gone entirely, this needs to come form the user, and
	// probably be stored encrypted and secure per user
	WakatimeAPIKey string

	// Grade holds the stats-card-with-grade calibration (medians + weights). Env
	// vars BOOM_GRADE_* override individual fields on top of
	// stats.DefaultGradeConfig — see loadGradeConfig below. cmd/boomtime applies
	// this to stats.DefaultGradeConfig at boot so downstream renderers picking
	// stats.Grade() get the tuned config transparently.
	Grade stats.GradeConfig

	// DefaultTimezone (gaka-dg7) is the IANA name applied by db.ResolveTimezone
	// when a user has NOT picked an explicit timezone yet
	// (users.timezone = ''). Sourced from BOOM_DEFAULT_TIMEZONE; validated at
	// Load-time with time.LoadLocation — an invalid value logs a WARN and
	// falls back to "UTC" (never bootloops the server on a bad env var).
	// Empty string means "no operator default; fall through to UTC in the
	// resolver". Users who explicitly pick a zone via the Settings picker
	// always win over this default.
	DefaultTimezone string

	// Role selects which loops this process runs: "server" (HTTP API +
	// cross-pod progress relay, no image Pool/AMQP consumer), "worker"
	// (image-job execution — AMQP consumer or, historically, the in-process
	// Pool — no HTTP API), or "all" (today's single-process behavior).
	// Default "all". BOOM_ROLE, overridable via `boomtime run --role`.
	// See IsServerRole / IsWorkerRole.
	Role string

	// QueueBroker selects the image-job transport (worker-topology
	// decoupling, gaka-8bz follow-up): "inprocess" (today's in-memory
	// Registry+Pool, welded enqueue->execute in one process) or "rabbitmq"
	// (AMQP producer/consumer + Dragonfly/Redis cross-pod progress bus).
	// Default "inprocess" so nothing changes until a deliberate cutover —
	// see BrokerRabbit and docs/design/worker-topology-decoupling.md.
	QueueBroker string

	// RabbitMQ + Dragonfly wiring — only read when QueueBroker=="rabbitmq".
	RabbitURL     string // assembled amqp:// URL (see overlay $(VAR) interpolation)
	RabbitQueue   string // default "boomtime.image-jobs"
	RedisAddr     string // Dragonfly/Redis host:port, e.g. boomtime-cache:6379
	RedisPassword string // usually empty in-cluster
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func getEnvFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return n
		}
	}
	return def
}

// loadGradeConfig starts from stats.DefaultGradeConfig and applies any
// BOOM_GRADE_* overrides. Unset vars keep the shipped calibration; invalid
// values are ignored (getEnvFloat / getEnvInt fall back on parse error).
func loadGradeConfig() stats.GradeConfig {
	d := stats.DefaultGradeConfig
	return stats.GradeConfig{
		StreakMedian:    getEnvFloat("BOOM_GRADE_STREAK_MEDIAN", d.StreakMedian),
		StreakWeight:    getEnvFloat("BOOM_GRADE_STREAK_WEIGHT", d.StreakWeight),
		ActiveMedian:    getEnvFloat("BOOM_GRADE_ACTIVE_MEDIAN", d.ActiveMedian),
		ActiveWeight:    getEnvFloat("BOOM_GRADE_ACTIVE_WEIGHT", d.ActiveWeight),
		LanguagesMedian: getEnvFloat("BOOM_GRADE_LANGUAGES_MEDIAN", d.LanguagesMedian),
		LanguagesWeight: getEnvFloat("BOOM_GRADE_LANGUAGES_WEIGHT", d.LanguagesWeight),
		ProjectsMedian:  getEnvFloat("BOOM_GRADE_PROJECTS_MEDIAN", d.ProjectsMedian),
		ProjectsWeight:  getEnvFloat("BOOM_GRADE_PROJECTS_WEIGHT", d.ProjectsWeight),
		DailyAvgMedian:  getEnvFloat("BOOM_GRADE_DAILY_AVG_MEDIAN", d.DailyAvgMedian),
		DailyAvgWeight:  getEnvFloat("BOOM_GRADE_DAILY_AVG_WEIGHT", d.DailyAvgWeight),
		HoursMedian:     getEnvFloat("BOOM_GRADE_HOURS_MEDIAN", d.HoursMedian),
		HoursWeight:     getEnvFloat("BOOM_GRADE_HOURS_WEIGHT", d.HoursWeight),
		MinRangeDays:    getEnvInt("BOOM_GRADE_MIN_RANGE_DAYS", d.MinRangeDays),
	}
}

func getEnvBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}

// isProdEnvName mirrors cmd/boomtime.isProdEnv but stays inside package config
// so downstream defaults (like CookieSecure) can key off it without importing
// main. Kept private — external callers should read Config.CookieSecure /
// Config.IsDev instead of re-deriving the classification.
func isProdEnvName(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "prod", "production":
		return true
	}
	return false
}

// Load reads configuration from the environment, applying hakatime's defaults.
func Load() *Config {
	env := getEnv("BOOM_ENV", "prod")
	dev := env == "dev"
	// In dev, default the DB query tracer + slow-query EXPLAIN on so they're
	// visible in the Logs tab; both remain overridable via their BOOM_DB_* vars.
	explainSlowDefault := 0
	if dev {
		explainSlowDefault = 250
	}
	c := &Config{
		Port:               getEnvInt("BOOM_PORT", 8080),
		APIPrefix:          getEnv("BOOM_API_PREFIX", ""),
		BadgeURL:           getEnv("BOOM_BADGE_URL", ""),
		DashboardPath:      getEnv("BOOM_DASHBOARD_PATH", ""),
		ShieldsIOURL:       getEnv("BOOM_SHIELDS_IO_URL", "https://img.shields.io"),
		EnableRegistration: getEnvBool("BOOM_ENABLE_REGISTRATION", true),
		SessionExpiry:      int64(getEnvInt("BOOM_SESSION_EXPIRY", 24)),
		LogLevel:           getEnv("BOOM_LOG_LEVEL", "info"),
		Env:                env,
		HTTPLog:            getEnvBool("BOOM_HTTP_LOG", true),

		// gaka-93f: user-model / OIDC / billing advertisement flags. All
		// default to today's behavior (local auth, no billing) and are
		// surfaced read-only via GET /api/v1/config/public.
		AuthProvider:      getEnv("BOOM_AUTH_PROVIDER", "local"),
		OIDCIssuer:        getEnv("BOOM_OIDC_ISSUER", ""),
		OIDCAuthorizeURL:  getEnv("BOOM_OIDC_AUTHORIZE_URL", ""),
		OIDCClientID:      getEnv("BOOM_OIDC_CLIENT_ID", ""),
		OIDCClientSecret:  getEnv("BOOM_OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURL:   getEnv("BOOM_OIDC_REDIRECT_URL", ""),
		OIDCGroupToRole:   parseGroupToRole(getEnv("BOOM_AUTHENTIK_GROUP_TO_ROLE", "")),
		OIDCAutoprovision: getEnvBool("BOOM_OIDC_AUTOPROVISION", false),
		OIDCAutolinkEmail: getEnvBool("BOOM_OIDC_AUTOLINK_EMAIL", false),
		// gaka-2ip Phase 1: per-user GitHub connect. Gate default OFF; the
		// three OAuth values + the state signing key stay empty until an
		// operator provisions them, and GithubConnectEnabled() stays false
		// until ALL are present — so this is inert on a default boot.
		FeatureGithubStats:      getEnvBool("BOOM_FEATURE_GITHUB_STATS", false),
		GithubOAuthClientID:     getEnv("BOOM_GITHUB_OAUTH_CLIENT_ID", ""),
		GithubOAuthClientSecret: getEnv("BOOM_GITHUB_OAUTH_CLIENT_SECRET", ""),
		GithubOAuthRedirectURL:  getEnv("BOOM_GITHUB_OAUTH_REDIRECT_URL", ""),
		OAuthStateSigningKey:    getEnv("BOOM_OAUTH_STATE_SIGNING_KEY", ""),

		FeatureBilling:       getEnvBool("BOOM_FEATURE_BILLING", false),
		BetaUserRegistration: getEnvBool("BOOM_BETA_USER_REGISTRATION", true),
		FeatureUserModel:     getEnvBool("BOOM_FEATURE_USER_MODEL", false),
		FeatureRollupSkip:    getEnvBool("BOOM_FEATURE_ROLLUP_SKIP", false),
		FeatureAdminCLI:      getEnvBool("BOOM_FEATURE_ADMIN_CLI", false),

		DBHost: getEnv("BOOM_DB_HOST", "localhost"),
		DBPort: getEnvInt("BOOM_DB_PORT", 5432),
		DBName: getEnv("BOOM_DB_NAME", "boomtime"),
		DBUser: getEnv("BOOM_DB_USER", "test"),
		DBPass: getEnv("BOOM_DB_PASS", "test"),

		DBLogQueries:    getEnvBool("BOOM_DB_LOG_QUERIES", dev),
		DBLogArgs:       getEnvBool("BOOM_DB_LOG_ARGS", false),
		DBN1Threshold:   getEnvInt("BOOM_DB_N1_THRESHOLD", 20),
		DBN1DupThresh:   getEnvInt("BOOM_DB_N1_DUP_THRESHOLD", 10),
		DBExplainSlowMs: getEnvInt("BOOM_DB_EXPLAIN_SLOW_MS", explainSlowDefault),

		GithubToken: getEnv("GITHUB_TOKEN", ""),

		// gaka-myv: ComfyUI-generated label archetype images. Feature gate
		// requires BOTH flag-on AND a non-empty shim URL. See docs on the
		// FeatureLabelImages / ComfyUIShimURL / ComfyUIModel fields above.
		FeatureLabelImages: getEnvBool("BOOM_FEATURE_LABEL_IMAGES", false),
		ComfyUIShimURL:     getEnv("BOOM_COMFYUI_SHIM_URL", ""),
		ComfyUIModel:       getEnv("BOOM_COMFYUI_MODEL", "sdxl-illustrious-xl"),
		AdminUsers:         parseAdminUsers(getEnv("BOOM_ADMIN_USERS", "")),

		// gaka-9v4: LLM (OpenAI-compat) for avatar prompt synthesis SSE.
		LLMAPIKey:  getEnv("BOOM_LLM_API_KEY", ""),
		LLMBaseURL: strings.TrimRight(getEnv("BOOM_LLM_BASE_URL", "https://api.openai.com/v1"), "/"),
		LLMModel:   getEnv("BOOM_LLM_MODEL", "gpt-4o-mini"),

		// gaka-b5x.1: cookie Secure flag. Default = "true in prod, false in
		// dev". BOOM_COOKIE_SECURE=true|false forces either mode explicitly.
		CookieSecure: getEnvBool("BOOM_COOKIE_SECURE", isProdEnvName(env)),
	}

	// gaka-93f.19: in a production env the refresh cookie MUST carry Secure
	// (it only travels over TLS). An explicit BOOM_COOKIE_SECURE=false there is
	// almost certainly a copied-from-dev misconfig that would expose the cookie
	// over plain HTTP — ignore it, force Secure=true, and WARN so the operator
	// sees the override was overridden rather than silently taking effect. The
	// dev default (false, for http://localhost) is untouched.
	if isProdEnvName(env) && !c.CookieSecure {
		slog.Warn("BOOM_COOKIE_SECURE=false ignored in a production environment — forcing Secure=true on the refresh cookie",
			"env", env)
		c.CookieSecure = true
	}

	rwURL := getEnv("BOOM_REMOTE_WRITE_URL", "")
	rwToken := getEnv("BOOM_REMOTE_WRITE_TOKEN", "")
	if rwURL != "" && rwToken != "" {
		c.RemoteWrite = &RemoteWriteConfig{URL: rwURL, Token: rwToken}
	}

	// Effective import key: WAKATIME_API_KEY, else BOOM_REMOTE_WRITE_TOKEN.
	c.WakatimeAPIKey = getEnv("WAKATIME_API_KEY", "")
	if c.WakatimeAPIKey == "" {
		c.WakatimeAPIKey = rwToken
	}

	c.Grade = loadGradeConfig()

	// gaka-worker-topology: role/broker gate for the image-job pipeline.
	// Both default to today's single-process, in-memory behavior — see the
	// Role / QueueBroker doc comments above.
	c.Role = getEnv("BOOM_ROLE", "all")
	c.QueueBroker = getEnv("BOOM_QUEUE_BROKER", "inprocess")
	c.RabbitURL = getEnv("BOOM_RABBITMQ_URL", "")
	c.RabbitQueue = getEnv("BOOM_RABBITMQ_QUEUE", "boomtime.image-jobs")
	c.RedisAddr = getEnv("BOOM_REDIS_ADDR", "")
	c.RedisPassword = getEnv("BOOM_REDIS_PASSWORD", "")

	// gaka-dg7: operator-wide default TZ for users with no explicit pick.
	// Validate here so an invalid IANA name never lands into the resolver —
	// a bogus value from the env would cause every subsequent AT TIME ZONE
	// query to error at PG time. Silent fall-through to "UTC" plus a WARN.
	c.DefaultTimezone = validateDefaultTimezone(getEnv("BOOM_DEFAULT_TIMEZONE", ""))

	return c
}

// validateDefaultTimezone parses `raw` against time.LoadLocation. Returns raw
// when valid; empty string (meaning "no default; UTC wins in the resolver")
// when raw is empty; empty string with a WARN log when raw is set but
// invalid so the operator sees the misconfig without a server crash.
func validateDefaultTimezone(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if _, err := time.LoadLocation(raw); err != nil {
		slog.Warn("invalid BOOM_DEFAULT_TIMEZONE — falling back to UTC in the resolver",
			"raw", raw, "err", err)
		return ""
	}
	return raw
}

// HasServerWakatimeKey reports whether a server-configured import key is present.
func (c *Config) HasServerWakatimeKey() bool {
	return c.WakatimeAPIKey != ""
}

// GithubTokenValue satisfies stats.Config — the accessor exists so the
// stats domain can read GitHub token state live per-request WITHOUT
// importing internal/config (which imports internal/stats for
// GradeConfig; the reverse import would form a cycle). Live reads matter
// because commits_test mutates hz.Cfg.GithubToken after handler
// construction and expects the change to take effect.
func (c *Config) GithubTokenValue() string { return c.GithubToken }

// DefaultTimezoneValue satisfies stats.Config — same import-cycle
// rationale as GithubTokenValue. Threaded through db.ResolveTimezone as
// the 3-level chain's operator default.
func (c *Config) DefaultTimezoneValue() string { return c.DefaultTimezone }

// DatabaseURL returns a pgx-compatible connection string.
func (c *Config) DatabaseURL() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		c.DBUser, c.DBPass, c.DBHost, c.DBPort, c.DBName)
}

// IsDev reports whether the server runs in development mode (text logs).
func (c *Config) IsDev() bool {
	return strings.EqualFold(c.Env, "dev")
}

// parseAdminUsers splits a comma-separated username list into a set. Empty
// input returns nil so IsAdmin(u) is a cheap "always false" for the
// default off configuration.
func parseAdminUsers(csv string) map[string]struct{} {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	out := map[string]struct{}{}
	for _, name := range strings.Split(csv, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseGroupToRole parses BOOM_AUTHENTIK_GROUP_TO_ROLE — a comma-separated list
// of "group:role" pairs (e.g. "boomtime-admin:admin,boomtime-full:full") — into
// a map. Malformed pairs (no colon, empty side) are skipped. First match wins
// at resolve time (see auth.RoleFromGroups); an identity in no mapped group
// falls back to RoleLight (fail-closed to the cheapest tier).
func parseGroupToRole(csv string) map[string]string {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(csv, ",") {
		i := strings.IndexByte(pair, ':')
		if i < 0 {
			continue
		}
		group := strings.TrimSpace(pair[:i])
		role := strings.TrimSpace(pair[i+1:])
		if group != "" && role != "" {
			out[group] = role
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IsAdmin reports whether `username` is on the admin allowlist. Empty list
// (default) means nobody is an admin — safer than "everybody is an admin".
func (c *Config) IsAdmin(username string) bool {
	if len(c.AdminUsers) == 0 {
		return false
	}
	_, ok := c.AdminUsers[username]
	return ok
}

// LabelImagesEnabled reports whether the label-images feature (gaka-myv) is
// operationally on: BOTH the master flag must be set AND a shim URL must be
// configured. Callers can key any generation-side branch off this single
// method — reads (GET /api/v1/labels/:id/image) do NOT check this so already-
// generated images keep serving after a flag flip.
func (c *Config) LabelImagesEnabled() bool {
	return c.FeatureLabelImages && strings.TrimSpace(c.ComfyUIShimURL) != ""
}

// IsServerRole reports whether this process should run the HTTP API +
// cross-pod progress relay. True for both "server" and "all" (the
// default) — only an explicit "worker" role turns this off.
func (c *Config) IsServerRole() bool {
	return c.Role == "server" || c.Role == "all"
}

// IsWorkerRole reports whether this process should run image-job execution
// (the AMQP consumer under broker=rabbitmq, or start the label-images
// reconcile loop). True for both "worker" and "all" (the default) — only
// an explicit "server" role turns this off.
func (c *Config) IsWorkerRole() bool {
	return c.Role == "worker" || c.Role == "all"
}

// BrokerRabbit reports whether the image-job transport is RabbitMQ+Dragonfly
// rather than the default in-process Registry+Pool. Case-insensitive so a
// stray BOOM_QUEUE_BROKER=RabbitMQ doesn't silently fall through to inprocess.
func (c *Config) BrokerRabbit() bool {
	return strings.EqualFold(c.QueueBroker, "rabbitmq")
}

// LLMEnabled reports whether an LLM API key is configured for the avatar
// prompt-synthesis endpoint (gaka-9v4). Handlers gate on this and return
// 503 when off so the FE renders a clear "server not configured" state
// instead of a mystery 500. BaseURL + Model always have defaults, so only
// the key gates the feature.
func (c *Config) LLMEnabled() bool {
	return strings.TrimSpace(c.LLMAPIKey) != ""
}

// GithubConnectEnabled reports whether the per-user GitHub connect feature
// (gaka-2ip Phase 1) is operationally live. INERT-SAFE by construction: it
// requires the master gate AND a full OAuth-App credential set AND the state
// signing key. Any one missing → false, so the /auth/github/* routes don't
// register, the /api/v1/config/public flag stays false, and the FE card renders
// nothing. The signing key is folded into the predicate on purpose — the CSRF
// `state` cannot be minted safely without it, so "configured" must include it.
func (c *Config) GithubConnectEnabled() bool {
	return c.FeatureGithubStats &&
		strings.TrimSpace(c.GithubOAuthClientID) != "" &&
		strings.TrimSpace(c.GithubOAuthClientSecret) != "" &&
		strings.TrimSpace(c.OAuthStateSigningKey) != ""
}

// OIDCEnabled reports whether the OIDC (Authentik) auth provider is selected
// (gaka-93f / gaka-0oe.11). Derived from BOOM_AUTH_PROVIDER. The FE reads this
// via /api/v1/config/public to swap the signup CTA to "Continue with
// Authentik" and to hide the local password form when appropriate.
func (c *Config) OIDCEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(c.AuthProvider), "oidc")
}
