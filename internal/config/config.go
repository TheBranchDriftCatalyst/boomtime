// Package config parses the BOOM_* environment variables into a Config struct.
// It mirrors hakatime's ServerSettings (App.hs) and CLI DB settings (Cli.hs).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// RemoteWriteConfig configures forwarding heartbeats to another Wakatime server.
type RemoteWriteConfig struct {
	URL   string
	Token string
}

// Config holds all runtime configuration.
type Config struct {
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

	// WakatimeAPIKey is the server-configured key used to import history from
	// wakatime.com when the request body omits apiToken. Sourced from
	// WAKATIME_API_KEY, falling back to BOOM_REMOTE_WRITE_TOKEN. Never exposed.
	WakatimeAPIKey string
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
