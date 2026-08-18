// cli_http_test.go — HTTP-level ginkgo coverage for the admin CLI-runner
// (BOOM_FEATURE_ADMIN_CLI). Named invariants under test:
//
//   - flag off ⇒ the /api/v1/admin/cli/* routes DO NOT EXIST (404), not
//     "exist but reject" — the feature is inert on a default boot;
//   - admin gate: unauth'd ⇒ 4xx, non-admin ⇒ 403 (no allowlist leak);
//   - spec: registry ∩ annotation ∩ availability (github-stats hidden until
//     FeatureGithubStats), param shapes as the FE contract expects;
//   - run: typed-binder 400s, unknown-command 404, dry-run-by-default for
//     mutating commands, confirm-sentinel required to apply, captured output
//     for the readonly commands;
//   - complete: contextual DB-backed username completion via the SAME cobra
//     completer the shell uses.
package admin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/labstack/echo/v5"
	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/climeta"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// cliRouter builds a fresh Echo with the FULL admin route table registered
// against the harness handler — the CLI routes among them only when the
// harness config has FeatureAdminCLI on (set BEFORE calling this: routes are
// wired at registration time, exactly like production).
func cliRouter(hz *testutil.Harness) *echo.Echo {
	e := echo.New()
	admin.Register(e, hz.H.Admin)
	return e
}

var _ = Describe("Admin CLI-runner: feature flag gate (BOOM_FEATURE_ADMIN_CLI)", func() {
	It("does not register the routes at all when the flag is off", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		// Default harness config: FeatureAdminCLI=false.
		e := cliRouter(hz)
		user, token := hz.MintUser("cli_gateoff")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		// Even a fully-authorized admin sees a plain 404 — the routes were
		// never wired, so this is indistinguishable from any unknown path.
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/cli/spec", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token,
			map[string]any{"command": "user list", "flags": map[string]any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/complete", token,
			map[string]any{"command": "user show", "toComplete": ""})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})
})

var _ = Describe("Admin CLI-runner: auth gates", func() {
	It("rejects unauth'd (4xx) and non-admin (403) callers on all three endpoints", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		hz.Cfg.FeatureAdminCLI = true
		e := cliRouter(hz)

		// (1) no token → 4xx (MissingAuth envelope), never 200.
		for _, probe := range []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, "/api/v1/admin/cli/spec", nil},
			{http.MethodPost, "/api/v1/admin/cli/run", map[string]any{"command": "user list"}},
			{http.MethodPost, "/api/v1/admin/cli/complete", map[string]any{"command": "user show"}},
		} {
			rec := doJSONReqG(e, probe.method, probe.path, "", probe.body)
			Expect(rec.Code).To(BeNumerically(">=", 400),
				"%s %s must reject unauth'd callers", probe.method, probe.path)
		}

		// (2) valid token, not on the admin allowlist → 403; the body must
		// not leak the resolved username or any allowlist member.
		nonAdmin, nonAdminToken := hz.MintUser("cli_nonadmin")
		hz.Cfg.AdminUsers = map[string]struct{}{"secret-admin-carol": {}}
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/cli/spec", nonAdminToken, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
		Expect(rec.Body.String()).NotTo(ContainSubstring(nonAdmin))
		Expect(rec.Body.String()).NotTo(ContainSubstring("secret-admin-carol"))

		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", nonAdminToken,
			map[string]any{"command": "user list", "flags": map[string]any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
	})
})

var _ = Describe("Admin CLI-runner: GET /api/v1/admin/cli/spec", func() {
	It("serves the registry∩annotation catalog with FE-contract param shapes", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		hz.Cfg.FeatureAdminCLI = true
		e := cliRouter(hz)
		user, token := hz.MintUser("cli_spec_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/cli/spec", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		var env struct {
			Commands []struct {
				Command         string `json:"command"`
				Classification  string `json:"classification"`
				DryRunSupported bool   `json:"dryRunSupported"`
				Params          []struct {
					Name        string `json:"name"`
					Type        string `json:"type"`
					Positional  bool   `json:"positional"`
					Required    bool   `json:"required"`
					Completable bool   `json:"completable"`
				} `json:"params"`
			} `json:"commands"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())

		byCmd := map[string]int{}
		for i, c := range env.Commands {
			byCmd[c.Command] = i
		}
		// github-stats hidden: harness FeatureGithubStats defaults false.
		Expect(byCmd).NotTo(HaveKey("backfill github-stats"))
		Expect(byCmd).To(HaveKey("user list"))
		Expect(byCmd).To(HaveKey("user show"))
		Expect(byCmd).To(HaveKey("backfill last-context"))

		lc := env.Commands[byCmd["backfill last-context"]]
		Expect(lc.Classification).To(Equal("mutating"))
		Expect(lc.DryRunSupported).To(BeTrue())
		Expect(lc.Params).To(HaveLen(1))
		Expect(lc.Params[0].Name).To(Equal("dry-run"))
		Expect(lc.Params[0].Type).To(Equal("bool"))

		us := env.Commands[byCmd["user show"]]
		Expect(us.Classification).To(Equal("readonly"))
		Expect(us.Params).To(HaveLen(1))
		Expect(us.Params[0].Name).To(Equal("username"))
		Expect(us.Params[0].Positional).To(BeTrue())
		Expect(us.Params[0].Required).To(BeTrue())
		Expect(us.Params[0].Completable).To(BeTrue())

		// Flip the feature on and github-stats appears (availability is
		// evaluated per-request against live config).
		hz.Cfg.FeatureGithubStats = true
		rec = doJSONReqG(e, http.MethodGet, "/api/v1/admin/cli/spec", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("backfill github-stats"))
	})
})

var _ = Describe("Admin CLI-runner: POST /api/v1/admin/cli/run", func() {
	var (
		hz    *testutil.Harness
		e     *echo.Echo
		token string
	)
	BeforeEach(func() {
		// Isolated DB: the confirm-sentinel spec APPLIES `backfill
		// last-context`, which rewrites placeholder heartbeats table-wide —
		// never run that against the shared test DB other packages seed.
		hz = testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "clirun"))
		hz.Cfg.FeatureAdminCLI = true
		e = cliRouter(hz)
		var user string
		user, token = hz.MintUser("cli_run_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
	})

	It("404s an unknown command and an availability-gated one identically", func() {
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token,
			map[string]any{"command": "rotate-encryption-key", "flags": map[string]any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))

		// github-stats is registered but unavailable (flag off) → same 404.
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token,
			map[string]any{"command": "backfill github-stats", "flags": map[string]any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("400s unknown flags and mistyped values (typed binder)", func() {
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token,
			map[string]any{"command": "user list", "flags": map[string]any{"bogus": true}})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("unknown parameter"))

		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token,
			map[string]any{"command": "backfill last-context", "flags": map[string]any{"dry-run": "yes"}})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("must be a boolean"))

		// Missing required positional.
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token,
			map[string]any{"command": "user show", "flags": map[string]any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("username"))
	})

	It("runs readonly commands freely and captures their output", func() {
		seeded, _ := hz.MintUser("cli_run_seeded")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token,
			map[string]any{"command": "user list", "flags": map[string]any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var resp struct {
			OK         bool   `json:"ok"`
			Output     string `json:"output"`
			ExitError  string `json:"exitError"`
			DryRun     bool   `json:"dryRun"`
			DurationMs int64  `json:"durationMs"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.OK).To(BeTrue())
		Expect(resp.ExitError).To(BeEmpty())
		Expect(resp.DryRun).To(BeFalse(), "readonly commands have no dry-run notion")
		Expect(resp.Output).To(ContainSubstring(seeded))

		// user show with the positional keyed by name in flags.
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token,
			map[string]any{"command": "user show", "flags": map[string]any{"username": seeded}})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.OK).To(BeTrue())
		Expect(resp.Output).To(ContainSubstring("role:"))
	})

	It("defaults mutating runs to dry-run and demands the confirm sentinel to apply", func() {
		// (1) no flags at all → server defaults dry-run TRUE.
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token,
			map[string]any{"command": "backfill last-context", "flags": map[string]any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var resp struct {
			OK     bool   `json:"ok"`
			Output string `json:"output"`
			DryRun bool   `json:"dryRun"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.OK).To(BeTrue())
		Expect(resp.DryRun).To(BeTrue(), "absent dry-run must default TRUE for a mutating command")
		Expect(resp.Output).To(ContainSubstring("dry-run"))

		// (2) explicit apply without confirm → 400, nothing runs.
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token,
			map[string]any{"command": "backfill last-context", "flags": map[string]any{"dry-run": false}})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("confirm"))

		// (3) wrong confirm value → still 400 (sentinel is the exact command).
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token, map[string]any{
			"command": "backfill last-context",
			"flags":   map[string]any{"dry-run": false},
			"confirm": "yes",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))

		// (4) confirm == command → the apply path runs for real.
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token, map[string]any{
			"command": "backfill last-context",
			"flags":   map[string]any{"dry-run": false},
			"confirm": "backfill last-context",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.OK).To(BeTrue())
		Expect(resp.DryRun).To(BeFalse())
		Expect(resp.Output).To(ContainSubstring("rebuilt rollups"))
	})

	It("runs backfill github-stats end-to-end through the apply path (key fail-fast, then real result)", func() {
		hz.Cfg.FeatureGithubStats = true // availability gate

		body := map[string]any{
			"command": "backfill github-stats",
			"flags":   map[string]any{},
			"confirm": "backfill github-stats",
		}
		var resp struct {
			OK        bool   `json:"ok"`
			Output    string `json:"output"`
			ExitError string `json:"exitError"`
			DryRun    bool   `json:"dryRun"`
		}

		// (0) mutating WITHOUT dry-run support ⇒ every run is an apply ⇒
		// confirm sentinel is mandatory even with empty flags.
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token,
			map[string]any{"command": "backfill github-stats", "flags": map[string]any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("confirm"))

		// (a) no BOOM_ENCRYPTION_KEY ⇒ the web Invoke fail-fasts with ONE
		// clear top-level error BEFORE the per-user loop (QA fix 3), not a
		// per-user error cascade.
		prev, had := os.LookupEnv(auth.EncryptionKeyEnv)
		Expect(os.Unsetenv(auth.EncryptionKeyEnv)).To(Succeed())
		auth.ResetForTest()
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.OK).To(BeFalse())
		Expect(resp.ExitError).To(ContainSubstring("cannot decrypt stored tokens"))
		if had {
			Expect(os.Setenv(auth.EncryptionKeyEnv, prev)).To(Succeed())
		}

		// (b) valid key installed ⇒ RunBackfillGithubStats really executes.
		// The isolated DB has no users with a linked GitHub token, so the
		// deterministic, network-free result is the "nothing to do" path.
		installEncryptionKeyForTest()
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.OK).To(BeTrue())
		Expect(resp.DryRun).To(BeFalse())
		Expect(resp.Output).To(ContainSubstring("no users with a linked GitHub token"))
	})

	It("rejects dry-run against a command that has no dry-run flag (github-stats)", func() {
		hz.Cfg.FeatureGithubStats = true
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token, map[string]any{
			"command": "backfill github-stats",
			"flags":   map[string]any{"dry-run": true},
			"confirm": "backfill github-stats",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("unknown parameter"))
	})

	It("refuses a destructive-classified registry entry outright, never invoking it", func() {
		// The shipping registry contains nothing destructive, so this
		// defense-in-depth branch is unreachable with real commands —
		// inject a synthetic entry to prove the refusal actually fires and
		// that no confirm value can get past it.
		invoked := false
		climeta.Registry()["danger wipe"] = climeta.RegistryEntry{
			Classification: climeta.ClassDestructive,
			RequiredCap:    auth.CapAdmin,
			NewCommand: func() *cobra.Command {
				return &cobra.Command{
					Use:         "wipe",
					Short:       "synthetic destructive test entry",
					Annotations: map[string]string{climeta.WebAnnotation: climeta.ClassDestructive},
				}
			},
			Invoke: func(_ context.Context, _ *db.DB, _ climeta.RunArgs, _ io.Writer) error {
				invoked = true
				return nil
			},
		}
		DeferCleanup(func() { delete(climeta.Registry(), "danger wipe") })

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token, map[string]any{
			"command": "danger wipe",
			"flags":   map[string]any{},
			"confirm": "danger wipe", // even the correct sentinel must not help
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
		Expect(rec.Body.String()).To(ContainSubstring("destructive"))
		Expect(invoked).To(BeFalse(), "a destructive entry must NEVER be invoked")
	})

	It("surfaces a failing command as ok=false with exitError, still HTTP 200", func() {
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/run", token,
			map[string]any{"command": "user show", "flags": map[string]any{"username": "no_such_user_xyz"}})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var resp struct {
			OK        bool   `json:"ok"`
			ExitError string `json:"exitError"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.OK).To(BeFalse())
		Expect(resp.ExitError).To(ContainSubstring("no such user"))
	})

	It("400s malformed JSON (BindJSONWithLimit branch)", func() {
		rec := doRawG(e, http.MethodPost, "/api/v1/admin/cli/run", token, []byte(`{not-json`))
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})
})

var _ = Describe("Admin CLI-runner: POST /api/v1/admin/cli/complete", func() {
	It("serves contextual DB-backed username suggestions via the shell's own completer", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		hz.Cfg.FeatureAdminCLI = true
		e := cliRouter(hz)
		user, token := hz.MintUser("cli_comp_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		seeded, _ := hz.MintUser("cli_comp_target")

		// NOTE (pool reuse, QA fix 1): no BOOM_DB_* env is pointed at the
		// harness DB here, ON PURPOSE. The web path must serve suggestions
		// from the handler's own pool (h.DB) via the registry DBLister — if
		// it ever regresses to the cobra completer's config.Load()
		// self-connect, it would look at the wrong database and the seeded
		// username below would not come back.

		// (1) positional completion for `user show`, prefix-filtered by
		// toComplete — proves the DB round-trip AND the prefix threading.
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/complete", token,
			map[string]any{"command": "user show", "args": []string{}, "toComplete": "cli_comp_target"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var resp struct {
			Suggestions []struct {
				Value string `json:"value"`
			} `json:"suggestions"`
			Directive struct {
				NoFileComp bool `json:"noFileComp"`
			} `json:"directive"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		values := []string{}
		for _, s := range resp.Suggestions {
			values = append(values, s.Value)
		}
		Expect(values).To(ContainElement(seeded))
		Expect(resp.Directive.NoFileComp).To(BeTrue())

		// (2) a non-matching prefix filters everything out.
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/complete", token,
			map[string]any{"command": "user show", "toComplete": "zzz_no_such_prefix"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Suggestions).To(BeEmpty())

		// (3) a command with no ArgCompleter yields empty suggestions, 200.
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/complete", token,
			map[string]any{"command": "user list", "toComplete": ""})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Suggestions).To(BeEmpty())

		// (4) unknown command → 404.
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/complete", token,
			map[string]any{"command": "create-token", "toComplete": ""})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("completes flag values via FlagCompleters (backfill github-stats --user)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		hz.Cfg.FeatureAdminCLI = true
		hz.Cfg.FeatureGithubStats = true // availability gate for github-stats
		e := cliRouter(hz)
		user, token := hz.MintUser("cli_comp_flag_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		// As above: no env redirection — FlagListers must serve from h.DB.

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/complete", token,
			map[string]any{"command": "backfill github-stats", "flag": "user", "toComplete": "cli_comp_flag_admin"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var resp struct {
			Suggestions []struct {
				Value string `json:"value"`
			} `json:"suggestions"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		values := []string{}
		for _, s := range resp.Suggestions {
			values = append(values, s.Value)
		}
		Expect(values).To(ContainElement(user))

		// A flag nobody registered a completer for → empty, 200.
		rec = doJSONReqG(e, http.MethodPost, "/api/v1/admin/cli/complete", token,
			map[string]any{"command": "backfill last-context", "flag": "dry-run", "toComplete": ""})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Suggestions).To(BeEmpty())
	})
})
