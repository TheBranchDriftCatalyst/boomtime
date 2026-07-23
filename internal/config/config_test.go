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
		"BOOM_PORT", "BOOM_API_PREFIX", "BOOM_BADGE_URL", "BOOM_DASHBOARD_PATH",
		"BOOM_SHIELDS_IO_URL", "BOOM_ENABLE_REGISTRATION", "BOOM_SESSION_EXPIRY",
		"BOOM_LOG_LEVEL", "BOOM_ENV", "BOOM_HTTP_LOG", "BOOM_COOKIE_SECURE",
		"BOOM_DB_HOST", "BOOM_DB_PORT", "BOOM_DB_NAME", "BOOM_DB_USER", "BOOM_DB_PASS",
		"BOOM_REMOTE_WRITE_URL", "BOOM_REMOTE_WRITE_TOKEN",
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
		t.Setenv("BOOM_REMOTE_WRITE_TOKEN", "fallback")
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
		t.Setenv("BOOM_REMOTE_WRITE_TOKEN", "fallback")
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
		if got := getEnvInt("BOOM_PORT", 8080); got != 8080 {
			t.Errorf("got %d, want default 8080", got)
		}
	})
	t.Run("invalid -> default", func(t *testing.T) {
		t.Setenv("BOOM_PORT", "notanumber")
		if got := getEnvInt("BOOM_PORT", 8080); got != 8080 {
			t.Errorf("got %d, want default 8080 on invalid", got)
		}
	})
	t.Run("valid (trimmed) -> parsed", func(t *testing.T) {
		t.Setenv("BOOM_PORT", "  9090  ")
		if got := getEnvInt("BOOM_PORT", 8080); got != 9090 {
			t.Errorf("got %d, want 9090", got)
		}
	})
}

// TestCookieSecureDefaults is the unit-layer probe for gaka-b5x.1. It catches
// the specific bug of "the cookie ships without Secure on a plaintext HTTP
// prod deploy" by asserting the derivation: prod → true, dev → false,
// explicit BOOM_COOKIE_SECURE always wins. Would fail if a future refactor
// dropped the Secure flag from the config path OR flipped the default.
func TestCookieSecureDefaults(t *testing.T) {
	cases := []struct {
		name     string
		env      string
		explicit string // "" = unset
		want     bool
	}{
		{name: "prod default → Secure", env: "prod", want: true},
		{name: "production default → Secure", env: "production", want: true},
		{name: "PROD (case) default → Secure", env: "PROD", want: true},
		{name: "dev default → not Secure", env: "dev", want: false},
		{name: "prod + BOOM_COOKIE_SECURE=false overrides", env: "prod", explicit: "false", want: false},
		{name: "dev + BOOM_COOKIE_SECURE=1 overrides", env: "dev", explicit: "1", want: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("BOOM_ENV", tc.env)
			if tc.explicit != "" {
				t.Setenv("BOOM_COOKIE_SECURE", tc.explicit)
			}
			c := Load()
			if c.CookieSecure != tc.want {
				t.Errorf("CookieSecure = %v, want %v (BOOM_ENV=%q BOOM_COOKIE_SECURE=%q)",
					c.CookieSecure, tc.want, tc.env, tc.explicit)
			}
		})
	}
}

func TestGetEnvBool(t *testing.T) {
	trueVals := []string{"true", "1", "yes", "on", "TRUE", "On"}
	for _, v := range trueVals {
		t.Setenv("BOOM_HTTP_LOG", v)
		if !getEnvBool("BOOM_HTTP_LOG", false) {
			t.Errorf("getEnvBool(%q) = false, want true", v)
		}
	}
	falseVals := []string{"false", "0", "no", "off", "FALSE", "Off"}
	for _, v := range falseVals {
		t.Setenv("BOOM_HTTP_LOG", v)
		if getEnvBool("BOOM_HTTP_LOG", true) {
			t.Errorf("getEnvBool(%q) = true, want false", v)
		}
	}

	t.Run("unset -> default", func(t *testing.T) {
		clearConfigEnv(t)
		if !getEnvBool("BOOM_HTTP_LOG", true) {
			t.Error("unset should return default true")
		}
		if getEnvBool("BOOM_HTTP_LOG", false) {
			t.Error("unset should return default false")
		}
	})

	t.Run("invalid -> default", func(t *testing.T) {
		t.Setenv("BOOM_HTTP_LOG", "maybe")
		if !getEnvBool("BOOM_HTTP_LOG", true) {
			t.Error("invalid should return default true")
		}
	})
}
