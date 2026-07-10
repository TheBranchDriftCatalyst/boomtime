package config

import (
	"os"
	"testing"
)

// clearConfigEnv unsets every env var Load reads so a test starts from a known
// clean slate. It uses t.Setenv first (which registers auto-restore of the
// original value) and then unsets, so LookupEnv reports the var as absent —
// matching a fresh process rather than an empty-string override.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"HAKA_PORT", "HAKA_API_PREFIX", "HAKA_BADGE_URL", "HAKA_DASHBOARD_PATH",
		"HAKA_SHIELDS_IO_URL", "HAKA_ENABLE_REGISTRATION", "HAKA_SESSION_EXPIRY",
		"HAKA_LOG_LEVEL", "HAKA_ENV", "HAKA_HTTP_LOG",
		"HAKA_DB_HOST", "HAKA_DB_PORT", "HAKA_DB_NAME", "HAKA_DB_USER", "HAKA_DB_PASS",
		"HAKA_REMOTE_WRITE_URL", "HAKA_REMOTE_WRITE_TOKEN",
		"WAKATIME_API_KEY", "GITHUB_TOKEN",
	} {
		t.Setenv(k, "") // registers restore of the pre-test value
		os.Unsetenv(k)  // make LookupEnv report absent
	}
}

func TestLoadDefaults(t *testing.T) {
	clearConfigEnv(t)
	c := Load()

	if c.Port != 8080 {
		t.Errorf("Port = %d, want 8080", c.Port)
	}
	if !c.EnableRegistration {
		t.Error("EnableRegistration = false, want true")
	}
	if c.SessionExpiry != 24 {
		t.Errorf("SessionExpiry = %d, want 24", c.SessionExpiry)
	}
	if c.DBPort != 5432 {
		t.Errorf("DBPort = %d, want 5432", c.DBPort)
	}
	if c.ShieldsIOURL != "https://img.shields.io" {
		t.Errorf("ShieldsIOURL = %q, want default", c.ShieldsIOURL)
	}
}

func TestWakatimeAPIKeyPrecedence(t *testing.T) {
	t.Run("WAKATIME_API_KEY wins", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("WAKATIME_API_KEY", "primary")
		t.Setenv("HAKA_REMOTE_WRITE_TOKEN", "fallback")
		c := Load()
		if c.WakatimeAPIKey != "primary" {
			t.Errorf("WakatimeAPIKey = %q, want primary", c.WakatimeAPIKey)
		}
		if !c.HasServerWakatimeKey() {
			t.Error("HasServerWakatimeKey = false, want true")
		}
	})

	t.Run("falls back to remote write token", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("HAKA_REMOTE_WRITE_TOKEN", "fallback")
		c := Load()
		if c.WakatimeAPIKey != "fallback" {
			t.Errorf("WakatimeAPIKey = %q, want fallback", c.WakatimeAPIKey)
		}
		if !c.HasServerWakatimeKey() {
			t.Error("HasServerWakatimeKey = false, want true")
		}
	})

	t.Run("both unset -> empty", func(t *testing.T) {
		clearConfigEnv(t)
		c := Load()
		if c.WakatimeAPIKey != "" {
			t.Errorf("WakatimeAPIKey = %q, want empty", c.WakatimeAPIKey)
		}
		if c.HasServerWakatimeKey() {
			t.Error("HasServerWakatimeKey = true, want false")
		}
	})
}

func TestGetEnvInt(t *testing.T) {
	t.Run("unset -> default", func(t *testing.T) {
		clearConfigEnv(t)
		if got := getEnvInt("HAKA_PORT", 8080); got != 8080 {
			t.Errorf("got %d, want default 8080", got)
		}
	})
	t.Run("invalid -> default", func(t *testing.T) {
		t.Setenv("HAKA_PORT", "notanumber")
		if got := getEnvInt("HAKA_PORT", 8080); got != 8080 {
			t.Errorf("got %d, want default 8080 on invalid", got)
		}
	})
	t.Run("valid (trimmed) -> parsed", func(t *testing.T) {
		t.Setenv("HAKA_PORT", "  9090  ")
		if got := getEnvInt("HAKA_PORT", 8080); got != 9090 {
			t.Errorf("got %d, want 9090", got)
		}
	})
}

func TestGetEnvBool(t *testing.T) {
	trueVals := []string{"true", "1", "yes", "on", "TRUE", "On"}
	for _, v := range trueVals {
		t.Setenv("HAKA_HTTP_LOG", v)
		if !getEnvBool("HAKA_HTTP_LOG", false) {
			t.Errorf("getEnvBool(%q) = false, want true", v)
		}
	}
	falseVals := []string{"false", "0", "no", "off", "FALSE", "Off"}
	for _, v := range falseVals {
		t.Setenv("HAKA_HTTP_LOG", v)
		if getEnvBool("HAKA_HTTP_LOG", true) {
			t.Errorf("getEnvBool(%q) = true, want false", v)
		}
	}

	t.Run("unset -> default", func(t *testing.T) {
		clearConfigEnv(t)
		if !getEnvBool("HAKA_HTTP_LOG", true) {
			t.Error("unset should return default true")
		}
		if getEnvBool("HAKA_HTTP_LOG", false) {
			t.Error("unset should return default false")
		}
	})

	t.Run("invalid -> default", func(t *testing.T) {
		t.Setenv("HAKA_HTTP_LOG", "maybe")
		if !getEnvBool("HAKA_HTTP_LOG", true) {
			t.Error("invalid should return default true")
		}
	})
}
