// config_more_test.go (gaka-d6x) — closes the coverage gap on the
// internal/config package. Each spec pins a NAMED INVARIANT documented in the
// It string; nothing here is a bare "insert x; get x" roundtrip.
//
// Coverage priority order (per task rules):
//  1. security / cross-user isolation → AdminUsers set semantics
//     (empty vs. populated, whitespace, IsAdmin default-deny)
//  2. state-machine gates → LabelImagesEnabled two-key AND, LLMEnabled
//     trims whitespace-only keys
//  3. error paths → validateDefaultTimezone rejects garbage and empties
//     still fall through; getEnvFloat parse-failure honors default
//  4. happy path → DatabaseURL formatting, IsDev case-insensitivity,
//     Grade env override wiring, remote-write both-or-nothing rule
package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ============================================================================
// Security-critical: admin allowlist
// ============================================================================

var _ = Describe("AdminUsers (default-deny access control)", func() {
	It("empty BOOM_ADMIN_USERS → IsAdmin('anyone') is false (no accidental all-admin)", func() {
		clearEnv()
		c := Load()
		// Invariant: an unset allowlist must NEVER grant admin — the whole
		// point of the "empty = nobody" semantic is that a fresh deploy
		// isn't secretly wide open.
		Expect(c.AdminUsers).To(BeNil())
		Expect(c.IsAdmin("root")).To(BeFalse())
		Expect(c.IsAdmin("")).To(BeFalse())
		Expect(c.IsAdmin("admin")).To(BeFalse())
	})

	It("whitespace-only entries and empty commas collapse to nil (no phantom admin)", func() {
		clearEnv()
		// Attacker or typo: ",,,   ,\t,". If we naively split-and-add, we
		// would insert "" as an admin username and IsAdmin("") would return
		// true — a silent auth bypass on any handler that forgets to check
		// the caller has a non-empty name. parseAdminUsers must reject.
		setenv("BOOM_ADMIN_USERS", ",,,   ,\t,")
		c := Load()
		Expect(c.AdminUsers).To(BeNil(), "phantom entries must not create admins")
		Expect(c.IsAdmin("")).To(BeFalse(),
			"anonymous session must never be treated as admin")
	})

	It("comma+whitespace list trims each entry — 'alice ,  bob' admits both", func() {
		clearEnv()
		setenv("BOOM_ADMIN_USERS", "alice ,  bob , ,carol")
		c := Load()
		// Invariant: trimming is applied per-entry so operators can format
		// the list for readability without accidentally locking themselves
		// out ("alice " != "alice" would).
		Expect(c.AdminUsers).To(HaveLen(3))
		Expect(c.IsAdmin("alice")).To(BeTrue())
		Expect(c.IsAdmin("bob")).To(BeTrue())
		Expect(c.IsAdmin("carol")).To(BeTrue())
		// Cross-user isolation: a name that only overlaps as a substring is
		// NOT an admin. The set is exact-match, not a prefix / infix scan.
		Expect(c.IsAdmin("ali")).To(BeFalse())
		Expect(c.IsAdmin("alicee")).To(BeFalse())
		Expect(c.IsAdmin("Alice")).To(BeFalse(),
			"case-sensitive lookup — 'Alice' MUST NOT match 'alice'")
	})

	It("populated allowlist still denies unlisted users (no allow-listed-adjacent bypass)", func() {
		clearEnv()
		setenv("BOOM_ADMIN_USERS", "alice")
		c := Load()
		Expect(c.IsAdmin("alice")).To(BeTrue())
		// Cross-user isolation: presence of one admin does not leak to any
		// other username. This is the property that keeps the admin routes
		// safe even if a non-admin account gets compromised.
		Expect(c.IsAdmin("bob")).To(BeFalse())
		Expect(c.IsAdmin("mallory")).To(BeFalse())
	})
})

// ============================================================================
// State-machine gates: feature flags with AND-of-two-things semantics
// ============================================================================

var _ = Describe("LabelImagesEnabled (gaka-myv two-key gate)", func() {
	It("flag off + URL empty → disabled", func() {
		clearEnv()
		c := Load()
		Expect(c.LabelImagesEnabled()).To(BeFalse())
	})

	It("flag ON + URL empty → STILL disabled (URL is a hard requirement)", func() {
		clearEnv()
		setenv("BOOM_FEATURE_LABEL_IMAGES", "true")
		c := Load()
		// Invariant: turning the flag on without a shim URL must NOT try to
		// generate images (the client would panic / dial nothing). The
		// startup WARN referenced in the field docs is the operator-visible
		// signal; this test is the code-side gate.
		Expect(c.FeatureLabelImages).To(BeTrue())
		Expect(c.ComfyUIShimURL).To(BeEmpty())
		Expect(c.LabelImagesEnabled()).To(BeFalse(),
			"flag alone must not enable the feature")
	})

	It("flag OFF + URL set → STILL disabled (flag is the master switch)", func() {
		clearEnv()
		setenv("BOOM_COMFYUI_SHIM_URL", "http://comfyui.local:8188")
		c := Load()
		Expect(c.LabelImagesEnabled()).To(BeFalse(),
			"URL alone must not enable the feature — flag is master")
	})

	It("flag ON + non-empty URL → enabled", func() {
		clearEnv()
		setenv("BOOM_FEATURE_LABEL_IMAGES", "on")
		setenv("BOOM_COMFYUI_SHIM_URL", "http://comfyui.local:8188")
		c := Load()
		Expect(c.LabelImagesEnabled()).To(BeTrue())
	})

	It("flag ON + whitespace-only URL → disabled (URL must be meaningfully set)", func() {
		clearEnv()
		setenv("BOOM_FEATURE_LABEL_IMAGES", "true")
		setenv("BOOM_COMFYUI_SHIM_URL", "   \t  ")
		c := Load()
		// Invariant: a "   " URL is functionally not set — TrimSpace inside
		// LabelImagesEnabled prevents an operator from believing the
		// feature is on when the value is only whitespace.
		Expect(c.LabelImagesEnabled()).To(BeFalse())
	})
})

var _ = Describe("LLMEnabled (gaka-9v4 API-key gate)", func() {
	It("unset key → disabled → handler must 503 with clear message", func() {
		clearEnv()
		c := Load()
		Expect(c.LLMEnabled()).To(BeFalse())
	})

	It("whitespace-only key → still disabled (no accidental 'on' from a stray space)", func() {
		clearEnv()
		setenv("BOOM_LLM_API_KEY", "   \n\t   ")
		c := Load()
		// Invariant: a whitespace-only key is treated as unset because the
		// upstream provider would reject it anyway — better to gate here so
		// the FE sees the same "not configured" 503 either way.
		Expect(c.LLMEnabled()).To(BeFalse())
	})

	It("non-empty key → enabled; baseURL default is honored", func() {
		clearEnv()
		setenv("BOOM_LLM_API_KEY", "sk-xxx")
		c := Load()
		Expect(c.LLMEnabled()).To(BeTrue())
		Expect(c.LLMBaseURL).To(Equal("https://api.openai.com/v1"))
		Expect(c.LLMModel).To(Equal("gpt-4o-mini"))
	})

	It("trailing slashes on BOOM_LLM_BASE_URL are stripped (no '//v1/foo' concatenation bugs)", func() {
		clearEnv()
		setenv("BOOM_LLM_BASE_URL", "https://proxy.example/openai///")
		c := Load()
		// Invariant: url + "/v1/chat/completions" downstream must never
		// yield a "https://x///v1/..." — TrimRight("/") in Load protects
		// callers from operator-supplied trailing slashes.
		Expect(c.LLMBaseURL).To(Equal("https://proxy.example/openai"))
	})
})

// ============================================================================
// Error paths: validators and default fall-through
// ============================================================================

var _ = Describe("validateDefaultTimezone (gaka-dg7 startup validator)", func() {
	It("unset → returns empty (resolver falls through to UTC)", func() {
		clearEnv()
		c := Load()
		// Invariant: no operator opinion → empty string, not "UTC". The
		// db.ResolveTimezone code path is what decides UTC as the ultimate
		// fallback; the config only signals "operator did/didn't override".
		Expect(c.DefaultTimezone).To(BeEmpty())
	})

	It("whitespace only → treated as unset (empty result, no WARN storm)", func() {
		clearEnv()
		setenv("BOOM_DEFAULT_TIMEZONE", "   \t  \n ")
		c := Load()
		Expect(c.DefaultTimezone).To(BeEmpty())
	})

	It("valid IANA name → preserved verbatim", func() {
		clearEnv()
		setenv("BOOM_DEFAULT_TIMEZONE", "America/Chicago")
		c := Load()
		Expect(c.DefaultTimezone).To(Equal("America/Chicago"))
	})

	It("bogus timezone → empty (fallback), does NOT panic or bootloop", func() {
		clearEnv()
		// Invariant: an invalid zone name (typo, deprecated alias not in
		// the tzdata bundle) must be caught at Load-time — not deferred to
		// every AT TIME ZONE query at Postgres time, which would silently
		// break stats aggregation for every user.
		setenv("BOOM_DEFAULT_TIMEZONE", "Mars/Olympus_Mons")
		c := Load()
		Expect(c.DefaultTimezone).To(BeEmpty(),
			"unknown zone must fall back to empty, never propagate to PG")
	})

	It("known-invalid syntactic form (empty-looking after trim of exotic chars)", func() {
		clearEnv()
		setenv("BOOM_DEFAULT_TIMEZONE", "Not_A_Zone/Name-With-Dashes")
		c := Load()
		Expect(c.DefaultTimezone).To(BeEmpty())
	})
})

var _ = Describe("getEnvFloat (parse-failure isolation)", func() {
	It("unset → default", func() {
		clearEnv()
		Expect(getEnvFloat("BOOM_GRADE_STREAK_MEDIAN", 12.5)).To(Equal(12.5))
	})

	It("invalid → default (not zero, not panic)", func() {
		clearEnv()
		setenv("BOOM_GRADE_STREAK_MEDIAN", "not-a-float")
		// Invariant: a typo in ONE BOOM_GRADE_* var must not silently zero
		// out that median (which would divide-by-zero in the CDF later).
		// getEnvFloat returns the default on parse error so the shipped
		// calibration keeps working.
		Expect(getEnvFloat("BOOM_GRADE_STREAK_MEDIAN", 5)).To(Equal(5.0))
	})

	It("valid (with whitespace) → parsed", func() {
		clearEnv()
		setenv("BOOM_GRADE_STREAK_MEDIAN", "  17.25  ")
		Expect(getEnvFloat("BOOM_GRADE_STREAK_MEDIAN", 5)).To(Equal(17.25))
	})

	It("scientific notation is honored", func() {
		clearEnv()
		setenv("BOOM_GRADE_STREAK_MEDIAN", "1.5e2")
		Expect(getEnvFloat("BOOM_GRADE_STREAK_MEDIAN", 5)).To(Equal(150.0))
	})
})

// ============================================================================
// Happy-path invariants with meaningful assertions
// ============================================================================

var _ = Describe("DatabaseURL formatting", func() {
	It("interpolates every field in the pgx-expected order", func() {
		clearEnv()
		setenv("BOOM_DB_HOST", "db.internal")
		setenv("BOOM_DB_PORT", "6543")
		setenv("BOOM_DB_NAME", "boom_prod")
		setenv("BOOM_DB_USER", "boom")
		setenv("BOOM_DB_PASS", "s3cret")
		c := Load()
		// Invariant: pgx expects postgres://USER:PASS@HOST:PORT/DBNAME. Any
		// swapping (e.g. host/port transposition) silently misroutes traffic
		// or connects to the wrong DB — this pins the exact template.
		Expect(c.DatabaseURL()).To(Equal(
			"postgres://boom:s3cret@db.internal:6543/boom_prod?sslmode=disable"))
	})

	It("sslmode=disable is ALWAYS present (matches boomtime's local-cluster assumption)", func() {
		clearEnv()
		c := Load()
		// Invariant: boomtime's Postgres deployment terminates TLS at the
		// pod/host boundary — the pgx URL never asks for SSL. If a future
		// refactor forgot to append ?sslmode=disable, every connection
		// against a cluster with `hostssl` off would fail.
		Expect(c.DatabaseURL()).To(HaveSuffix("?sslmode=disable"))
	})
})

var _ = Describe("IsDev (case-insensitive dev-mode gate)", func() {
	It("BOOM_ENV=dev → IsDev true", func() {
		clearEnv()
		setenv("BOOM_ENV", "dev")
		c := Load()
		Expect(c.IsDev()).To(BeTrue())
		// Dev implies query tracing + slow-EXPLAIN default on.
		Expect(c.DBLogQueries).To(BeTrue())
		Expect(c.DBExplainSlowMs).To(Equal(250))
	})

	It("BOOM_ENV=DEV / Dev → IsDev true (case-insensitive via EqualFold)", func() {
		clearEnv()
		setenv("BOOM_ENV", "DEV")
		Expect(Load().IsDev()).To(BeTrue())
		setenv("BOOM_ENV", "Dev")
		Expect(Load().IsDev()).To(BeTrue())
	})

	It("BOOM_ENV=prod → IsDev false, defaults revert", func() {
		clearEnv()
		setenv("BOOM_ENV", "prod")
		c := Load()
		Expect(c.IsDev()).To(BeFalse())
		// Invariant: production must NOT default to query tracing (log
		// volume + PII risk) and MUST NOT default to auto-EXPLAIN on every
		// slow read (extra load on the query planner).
		Expect(c.DBLogQueries).To(BeFalse())
		Expect(c.DBExplainSlowMs).To(Equal(0))
	})

	It("unrecognized env (e.g. 'staging') → NOT dev (no accidental verbose logging)", func() {
		clearEnv()
		setenv("BOOM_ENV", "staging")
		c := Load()
		// Invariant: IsDev is a strict "dev" check, not "not prod". A
		// staging deployment should NOT get dev-only defaults like query
		// logging on — the operator has to opt in explicitly.
		Expect(c.IsDev()).To(BeFalse())
		Expect(c.DBLogQueries).To(BeFalse())
	})
})

var _ = Describe("RemoteWrite composition (both-or-nothing rule)", func() {
	It("URL set, token empty → RemoteWrite nil (no half-configured POST loop)", func() {
		clearEnv()
		setenv("BOOM_REMOTE_WRITE_URL", "https://forward.example/api/v1/heartbeats")
		c := Load()
		// Invariant: a half-configured remote-write is a footgun — we'd
		// send heartbeats to a real endpoint with an empty bearer token
		// and log 401s forever. Config MUST require both to enable it.
		Expect(c.RemoteWrite).To(BeNil())
	})

	It("token set, URL empty → RemoteWrite nil", func() {
		clearEnv()
		setenv("BOOM_REMOTE_WRITE_TOKEN", "tok")
		c := Load()
		Expect(c.RemoteWrite).To(BeNil())
	})

	It("both set → RemoteWrite populated with EXACT fields (no swap)", func() {
		clearEnv()
		setenv("BOOM_REMOTE_WRITE_URL", "https://forward.example/api")
		setenv("BOOM_REMOTE_WRITE_TOKEN", "tok-abc")
		c := Load()
		Expect(c.RemoteWrite).ToNot(BeNil())
		// Anti-swap: URL is URL, Token is Token. A field transposition
		// would send the token as a URL — instant DNS / 4xx storm.
		Expect(c.RemoteWrite.URL).To(Equal("https://forward.example/api"))
		Expect(c.RemoteWrite.Token).To(Equal("tok-abc"))
	})

	It("token also seeds WakatimeAPIKey as a fallback (documented precedence)", func() {
		clearEnv()
		setenv("BOOM_REMOTE_WRITE_URL", "https://forward.example/api")
		setenv("BOOM_REMOTE_WRITE_TOKEN", "shared-tok")
		c := Load()
		// Invariant: when WAKATIME_API_KEY is unset, the same token used
		// for remote-write is reused as the import key. This is the
		// documented "one key does both" convenience in Config.WakatimeAPIKey.
		Expect(c.WakatimeAPIKey).To(Equal("shared-tok"))
		Expect(c.HasServerWakatimeKey()).To(BeTrue())
	})
})

var _ = Describe("isProdEnvName classification", func() {
	It("only 'prod' and 'production' (case/whitespace-insensitive) count as prod", func() {
		// Table pinning: every value here must classify to the given bool.
		// Regression would show up if e.g. "staging" started returning true.
		cases := map[string]bool{
			"prod":         true,
			"production":   true,
			"PROD":         true,
			"Production":   true,
			"  prod  ":     true,
			"\tPRODUCTION": true,
			"dev":          false,
			"staging":      false,
			"test":         false,
			"":             false,
			"produ":        false, // no partial match
			"prod-eu":      false, // no substring match
		}
		for in, want := range cases {
			Expect(isProdEnvName(in)).To(Equal(want),
				"isProdEnvName(%q) mismatch", in)
		}
	})
})

var _ = Describe("HasServerWakatimeKey", func() {
	It("empty key → false; any non-empty key → true (regardless of source)", func() {
		clearEnv()
		Expect(Load().HasServerWakatimeKey()).To(BeFalse())

		setenv("WAKATIME_API_KEY", "primary")
		Expect(Load().HasServerWakatimeKey()).To(BeTrue())
	})
})
