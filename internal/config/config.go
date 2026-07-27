// Package config parses the BOOM_* environment variables into a Config struct.
// It mirrors hakatime's ServerSettings (App.hs) and CLI DB settings (Cli.hs).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

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

	return c
}

// HasServerWakatimeKey reports whether a server-configured import key is present.
func (c *Config) HasServerWakatimeKey() bool {
	return c.WakatimeAPIKey != ""
}

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

// LLMEnabled reports whether an LLM API key is configured for the avatar
// prompt-synthesis endpoint (gaka-9v4). Handlers gate on this and return
// 503 when off so the FE renders a clear "server not configured" state
// instead of a mystery 500. BaseURL + Model always have defaults, so only
// the key gates the feature.
func (c *Config) LLMEnabled() bool {
	return strings.TrimSpace(c.LLMAPIKey) != ""
}
