// config_ginkgo_test.go — ginkgo mirror of config_test.go (gaka-0vp).
// 1:1 case map (5 top-level TestXxx, several with subtests):
//
//	TestLoadDefaults              → Load > "defaults"
//	TestWakatimeAPIKeyPrecedence  → Load > "wakatime api key precedence" (3 Its)
//	TestGetEnvInt                 → getEnvInt > 3 named entries
//	TestCookieSecureDefaults      → cookie secure derivation > DescribeTable of 6
//	TestGetEnvBool                → getEnvBool > 4 groups
//
// Uses GinkgoT() to bridge into stdlib-typed helpers (clearConfigEnv,
// t.Setenv) — those helpers still take *testing.T; ginkgo returns a
// compatible-enough shim.
package config

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// clearEnv is the ginkgo variant of clearConfigEnv — takes no *testing.T
// and uses os.Unsetenv directly. Register a restore via DeferCleanup so
// the pre-test environment survives.
func clearEnv() {
	saved := map[string]string{}
	keys := []string{
		"BOOM_PORT", "BOOM_API_PREFIX", "BOOM_BADGE_URL", "BOOM_DASHBOARD_PATH",
		"BOOM_SHIELDS_IO_URL", "BOOM_ENABLE_REGISTRATION", "BOOM_SESSION_EXPIRY",
		"BOOM_LOG_LEVEL", "BOOM_ENV", "BOOM_HTTP_LOG", "BOOM_COOKIE_SECURE",
		"BOOM_DB_HOST", "BOOM_DB_PORT", "BOOM_DB_NAME", "BOOM_DB_USER", "BOOM_DB_PASS",
		"BOOM_REMOTE_WRITE_URL", "BOOM_REMOTE_WRITE_TOKEN",
		"WAKATIME_API_KEY", "GITHUB_TOKEN",
	}
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			saved[k] = v
		}
		os.Unsetenv(k)
	}
	DeferCleanup(func() {
		for _, k := range keys {
			if v, ok := saved[k]; ok {
				os.Setenv(k, v)
			} else {
				os.Unsetenv(k)
			}
		}
	})
}

// setenv wraps os.Setenv with a DeferCleanup to restore the prior value.
func setenv(k, v string) {
	prev, hadPrev := os.LookupEnv(k)
	os.Setenv(k, v)
	DeferCleanup(func() {
		if hadPrev {
			os.Setenv(k, prev)
		} else {
			os.Unsetenv(k)
		}
	})
}

var _ = Describe("Load", func() {
	It("returns documented defaults when every env var is unset", func() {
		clearEnv()
		c := Load()
		Expect(c.Port).To(Equal(8080))
		Expect(c.EnableRegistration).To(BeTrue())
		Expect(c.SessionExpiry).To(BeEquivalentTo(24))
		Expect(c.DBPort).To(Equal(5432))
		Expect(c.ShieldsIOURL).To(Equal("https://img.shields.io"))
	})

	Describe("wakatime api key precedence", func() {
		It("WAKATIME_API_KEY wins over BOOM_REMOTE_WRITE_TOKEN", func() {
			clearEnv()
			setenv("WAKATIME_API_KEY", "primary")
			setenv("BOOM_REMOTE_WRITE_TOKEN", "fallback")
			c := Load()
			Expect(c.WakatimeAPIKey).To(Equal("primary"))
			Expect(c.HasServerWakatimeKey()).To(BeTrue())
		})

		It("falls back to BOOM_REMOTE_WRITE_TOKEN when WAKATIME_API_KEY is unset", func() {
			clearEnv()
			setenv("BOOM_REMOTE_WRITE_TOKEN", "fallback")
			c := Load()
			Expect(c.WakatimeAPIKey).To(Equal("fallback"))
			Expect(c.HasServerWakatimeKey()).To(BeTrue())
		})

		It("both unset → empty and HasServerWakatimeKey false", func() {
			clearEnv()
			c := Load()
			Expect(c.WakatimeAPIKey).To(BeEmpty())
			Expect(c.HasServerWakatimeKey()).To(BeFalse())
		})
	})
})

var _ = Describe("getEnvInt", func() {
	It("unset → default", func() {
		clearEnv()
		Expect(getEnvInt("BOOM_PORT", 8080)).To(Equal(8080))
	})

	It("invalid → default", func() {
		setenv("BOOM_PORT", "notanumber")
		Expect(getEnvInt("BOOM_PORT", 8080)).To(Equal(8080))
	})

	It("valid (with surrounding whitespace) → parsed", func() {
		setenv("BOOM_PORT", "  9090  ")
		Expect(getEnvInt("BOOM_PORT", 8080)).To(Equal(9090))
	})
})

var _ = Describe("CookieSecure derivation (gaka-b5x.1)", func() {
	DescribeTable("env + explicit → Secure flag",
		func(env, explicit string, want bool) {
			clearEnv()
			setenv("BOOM_ENV", env)
			if explicit != "" {
				setenv("BOOM_COOKIE_SECURE", explicit)
			}
			c := Load()
			Expect(c.CookieSecure).To(Equal(want))
		},
		Entry("prod default → Secure", "prod", "", true),
		Entry("production default → Secure", "production", "", true),
		Entry("PROD (case-insensitive) default → Secure", "PROD", "", true),
		Entry("dev default → not Secure", "dev", "", false),
		// gaka-93f.19: prod IGNORES an explicit false (forced Secure); dev honors
		// an explicit override in either direction.
		Entry("prod + explicit false → forced Secure (prod-force)", "prod", "false", true),
		Entry("production + explicit false → forced Secure", "production", "false", true),
		Entry("dev + explicit 1 overrides", "dev", "1", true),
		Entry("dev + explicit false stays not Secure", "dev", "false", false),
	)

	// gaka-93f.19: the prod-force is a security floor — a prod deploy that copied
	// BOOM_COOKIE_SECURE=false from a dev env must NOT ship a non-Secure refresh
	// cookie over TLS. dev keeps its explicit-override freedom.
	It("prod + BOOM_COOKIE_SECURE=false is forced back to Secure=true", func() {
		clearEnv()
		setenv("BOOM_ENV", "prod")
		setenv("BOOM_COOKIE_SECURE", "false")
		Expect(Load().CookieSecure).To(BeTrue())
	})

	It("dev + BOOM_COOKIE_SECURE=false is honored (no prod-force off prod)", func() {
		clearEnv()
		setenv("BOOM_ENV", "dev")
		setenv("BOOM_COOKIE_SECURE", "false")
		Expect(Load().CookieSecure).To(BeFalse())
	})
})

var _ = Describe("getEnvBool", func() {
	It("accepts true-ish values", func() {
		for _, v := range []string{"true", "1", "yes", "on", "TRUE", "On"} {
			setenv("BOOM_HTTP_LOG", v)
			Expect(getEnvBool("BOOM_HTTP_LOG", false)).To(BeTrue(),
				"value = %q", v)
		}
	})

	It("accepts false-ish values", func() {
		for _, v := range []string{"false", "0", "no", "off", "FALSE", "Off"} {
			setenv("BOOM_HTTP_LOG", v)
			Expect(getEnvBool("BOOM_HTTP_LOG", true)).To(BeFalse(),
				"value = %q", v)
		}
	})

	It("unset → default", func() {
		clearEnv()
		Expect(getEnvBool("BOOM_HTTP_LOG", true)).To(BeTrue())
		Expect(getEnvBool("BOOM_HTTP_LOG", false)).To(BeFalse())
	})

	It("invalid → default", func() {
		setenv("BOOM_HTTP_LOG", "maybe")
		Expect(getEnvBool("BOOM_HTTP_LOG", true)).To(BeTrue())
	})
})
