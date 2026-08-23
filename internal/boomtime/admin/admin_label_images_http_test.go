// admin_label_images_http_test.go — HTTP-level ginkgo coverage for the
// admin label-images cluster (boom-d6x.handler). Complements the wire
// unit suite in admin_label_images_test.go by hitting the routes
// through Router() and asserting NAMED INVARIANTS: admin-gate before
// feature-gate; feature-off returns 503 with a hint; empty entries →
// 400; ids w/o all → 400; all=true + truncate wipes existing rows;
// idempotent per-label enqueue (`existing=true`); ID must match wire
// `entries`.
package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/queue/imagejobs"
	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/worker/labelimages"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

func liDo(e http.Handler, method, target, token string, body any) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// buildLiWorker constructs a real labelimages.Worker via NewWorker with a
// fake but scheme-valid shim URL — enough that NewWorker returns non-nil.
// The Worker is only USED by AdminLabelImagesRegenerate to gate on nil;
// the queue enqueue path doesn't actually invoke the executor synchronously
// (workers are external), so no network I/O happens in these tests.
func buildLiWorker(cfg *config.Config, hz *testutil.Harness) *labelimages.Worker {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	w, err := labelimages.NewWorker(cfg, hz.DB, logger)
	Expect(err).NotTo(HaveOccurred())
	Expect(w).NotTo(BeNil(), "worker should be non-nil when feature is on + URL set")
	return w
}

var _ = Describe("AdminLabelImagesInfo (boom-myv): admin gate + shape", func() {
	It("returns 401/400 for unauth'd, 403 for non-admin, 200 with envelope for admin", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "alili"))
		e := boomtimeRouter(hz)

		// (1) no token.
		rec := liDo(e, http.MethodGet, "/api/v1/admin/label-images", "", nil)
		Expect(rec.Code).NotTo(Equal(http.StatusOK))

		// (2) non-admin. Also pin: 403 body must NOT leak the resolved
		// username or the admin allowlist (would confirm identity or
		// enumerate admins via a stolen token).
		nonAdmin, nonAdminToken := hz.MintUser("li_info_nonadmin")
		// Populate allowlist with a known-off name so we can assert it
		// isn't echoed.
		hz.Cfg.AdminUsers = map[string]struct{}{"secret-admin-alice": {}}
		rec = liDo(e, http.MethodGet, "/api/v1/admin/label-images", nonAdminToken, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
		Expect(rec.Body.String()).NotTo(ContainSubstring(nonAdmin),
			"403 body leaked resolved username: %s", rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("secret-admin-alice"),
			"403 body leaked admin allowlist member: %s", rec.Body.String())
		// Reset the allowlist for step (3).
		hz.Cfg.AdminUsers = nil

		// (3) admin → 200 with expected envelope keys.
		user, token := hz.MintUser("li_info_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		rec = liDo(e, http.MethodGet, "/api/v1/admin/label-images", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var env map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())
		for _, k := range []string{"enabled", "model", "shimUrl", "count", "items", "baseline"} {
			Expect(env).To(HaveKey(k), "AdminLabelImagesInfo missing key %s: %v", k, env)
		}
		// baseline sourced from labels table (isolated DB gets migrations
		// applied). At minimum it must not be nil or empty.
		bl, _ := env["baseline"].([]any)
		Expect(bl).NotTo(BeEmpty(),
			"baseline must be non-empty (migrations 00036/00039/00040/00043 should have populated labels)")
	})

	It("shimUrl response field NEVER carries credentials (?api_key=, user:pass@, ?token=)", func() {
		// LOCKS IN: even if an operator misconfigures ComfyUIShimURL to
		// include credentials, the /api/v1/admin/label-images response
		// must NOT propagate them verbatim. This is a defense-in-depth
		// invariant — the current code returns Cfg.ComfyUIShimURL as-is,
		// so this test will FAIL if credentials sneak in, and the fix is
		// to strip them from the field before serving.
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "aliinfoshim"))
		hz.Cfg.ComfyUIShimURL = "http://127.0.0.1:8080/generate"
		e := boomtimeRouter(hz)
		user, token := hz.MintUser("li_shim")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		rec := liDo(e, http.MethodGet, "/api/v1/admin/label-images", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var env map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())
		shim, _ := env["shimUrl"].(string)
		// Since we set the URL WITHOUT credentials, none must appear.
		for _, banned := range []string{"api_key", "apikey", "token=", "@127.0.0.1", "password"} {
			Expect(shim).NotTo(ContainSubstring(banned),
				"shimUrl leaked credential-shaped substring %q: %s", banned, shim)
		}
	})
})

var _ = Describe("AdminLabelImagesRegenerate (boom-myv/boom-8bz): feature gate + validation + idempotency", func() {
	It("returns 503 when either Worker or ImageJobQueue is nil (feature disabled)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "aliregen"))
		e := boomtimeRouter(hz)
		user, token := hz.MintUser("li_regen_off")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		// Neither wired: default state after NewHarness.
		rec := liDo(e, http.MethodPost, "/api/v1/admin/label-images/regenerate", token,
			map[string]any{"entries": []map[string]any{{"id": "polyglot", "prompt": "p"}}, "all": true})
		Expect(rec).To(testutil.HaveStatus(http.StatusServiceUnavailable))
		// Body should hint at what env vars to set — helps operators.
		Expect(rec.Body.String()).To(ContainSubstring("BOOM_FEATURE_LABEL_IMAGES"))
	})

	It("400s an empty entries list; 400s ids-w/o-all; 400s when ids don't match entries", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "aliregen"))
		hz.Cfg.FeatureLabelImages = true
		hz.Cfg.ComfyUIShimURL = "http://127.0.0.1:1"
		e, bh := boomtimeRouterH(hz)
		user, token := hz.MintUser("li_regen_validate")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		bh.SetLabelImagesWorker(buildLiWorker(hz.Cfg, hz))
		bh.SetImageJobQueue(imagejobs.NewRegistry(nil))

		// (1) empty entries.
		rec := liDo(e, http.MethodPost, "/api/v1/admin/label-images/regenerate", token,
			map[string]any{"entries": []any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("entries"))

		// (2) valid entries but neither all nor ids → 400.
		rec = liDo(e, http.MethodPost, "/api/v1/admin/label-images/regenerate", token,
			map[string]any{"entries": []map[string]any{{"id": "polyglot", "prompt": "p"}}})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("all"))

		// (3) ids that don't match any entry → 400 ("nothing to regenerate").
		rec = liDo(e, http.MethodPost, "/api/v1/admin/label-images/regenerate", token, map[string]any{
			"entries": []map[string]any{{"id": "polyglot", "prompt": "p"}},
			"ids":     []string{"does-not-exist"},
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("nothing to regenerate"))

		// (4) entry with blank id or blank prompt → filtered out silently. If all
		// entries filter → same 400 nothing-to-regen.
		rec = liDo(e, http.MethodPost, "/api/v1/admin/label-images/regenerate", token, map[string]any{
			"entries": []map[string]any{{"id": "", "prompt": "p"}, {"id": "x", "prompt": ""}},
			"all":     true,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("valid regen enqueues 202 with unique jobIds; re-enqueue same label → existing=true", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "aliregen"))
		hz.Cfg.FeatureLabelImages = true
		hz.Cfg.ComfyUIShimURL = "http://127.0.0.1:1"
		e, bh := boomtimeRouterH(hz)
		user, token := hz.MintUser("li_regen_ok")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		bh.SetLabelImagesWorker(buildLiWorker(hz.Cfg, hz))
		bh.SetImageJobQueue(imagejobs.NewRegistry(nil))

		body := map[string]any{
			"entries": []map[string]any{{"id": "polyglot", "prompt": "cyber oracle"}},
			"ids":     []string{"polyglot"},
		}
		rec := liDo(e, http.MethodPost, "/api/v1/admin/label-images/regenerate", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		jobs, _ := resp["jobs"].([]any)
		Expect(jobs).To(HaveLen(1))
		first, _ := jobs[0].(map[string]any)
		Expect(first["labelId"]).To(Equal("polyglot"))
		Expect(first["existing"]).To(BeFalse(), "first enqueue must be a new job")
		firstJobID := first["jobId"].(string)

		// Same label enqueue → existing=true, same jobId.
		rec = liDo(e, http.MethodPost, "/api/v1/admin/label-images/regenerate", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		jobs, _ = resp["jobs"].([]any)
		Expect(jobs).To(HaveLen(1))
		second, _ := jobs[0].(map[string]any)
		Expect(second["existing"]).To(BeTrue(), "re-enqueue of same label must return existing=true")
		Expect(second["jobId"]).To(Equal(firstJobID), "re-enqueue must return the same jobId")
	})

	It("all=true iterates every valid entry; truncate=true wipes existing rows before enqueue", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "aliregen"))
		hz.Cfg.FeatureLabelImages = true
		hz.Cfg.ComfyUIShimURL = "http://127.0.0.1:1"
		e, bh := boomtimeRouterH(hz)
		user, token := hz.MintUser("li_regen_all")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		bh.SetLabelImagesWorker(buildLiWorker(hz.Cfg, hz))
		bh.SetImageJobQueue(imagejobs.NewRegistry(nil))
		ctx := context.Background()

		// Seed a fake label_images row so we can prove `truncate=true` wiped it.
		_, err := hz.DB.Pool.Exec(ctx,
			`INSERT INTO label_images (label_id, image_bytes, mime_type, generated_at)
			 VALUES ('seed-old', '\x89504e47'::bytea, 'image/png', now())`)
		Expect(err).NotTo(HaveOccurred())

		body := map[string]any{
			"entries": []map[string]any{
				{"id": "a", "prompt": "aa"},
				{"id": "b", "prompt": "bb"},
			},
			"all":      true,
			"truncate": true,
		}
		rec := liDo(e, http.MethodPost, "/api/v1/admin/label-images/regenerate", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp["queued"]).To(BeEquivalentTo(2))

		// INVARIANT: TruncateLabelImages ran before the enqueue loop.
		var n int
		Expect(hz.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM label_images WHERE label_id='seed-old'`).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(0), "truncate=true must wipe existing label_images rows before enqueue")
	})

	It("per-entry Description is pulled from DB, not from wire body (Save+regen semantics)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "aliregen"))
		hz.Cfg.FeatureLabelImages = true
		hz.Cfg.ComfyUIShimURL = "http://127.0.0.1:1"
		e, bh := boomtimeRouterH(hz)
		user, token := hz.MintUser("li_regen_desc")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		bh.SetLabelImagesWorker(buildLiWorker(hz.Cfg, hz))
		reg := imagejobs.NewRegistry(nil)
		bh.SetImageJobQueue(reg)

		// Enqueue against a real label id from the migrated catalog. Any id in
		// the labels table works; the handler pulls the DB Description.
		rows, err := hz.DB.ListLabels(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).NotTo(BeEmpty(), "labels table empty in test DB")
		id := rows[0].ID
		wantDesc := rows[0].Description

		body := map[string]any{
			"entries": []map[string]any{{"id": id, "prompt": "prompt-from-wire"}},
			"ids":     []string{id},
		}
		rec := liDo(e, http.MethodPost, "/api/v1/admin/label-images/regenerate", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))

		// Inspect the enqueued job's Description via Snapshot.
		snap := reg.Snapshot()
		Expect(snap).To(HaveLen(1))
		Expect(snap[0].LabelID).To(Equal(id))
		Expect(snap[0].Description).To(Equal(wantDesc),
			"handler must pull description from DB (Save+regen contract), not wire body")
	})
})

var _ = Describe("AdminLabelImagesRegenerate: malformed body branch", func() {
	It("POST /regenerate with malformed JSON → 400 (BindJSONWithLimit branch)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "alimal"))
		hz.Cfg.FeatureLabelImages = true
		hz.Cfg.ComfyUIShimURL = "http://127.0.0.1:1"
		e, bh := boomtimeRouterH(hz)
		user, token := hz.MintUser("li_regen_mal")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		bh.SetLabelImagesWorker(buildLiWorker(hz.Cfg, hz))
		bh.SetImageJobQueue(imagejobs.NewRegistry(nil))

		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/label-images/regenerate",
			bytes.NewReader([]byte(`{not-json`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})
})

var _ = Describe("AdminLabelImagesWS (boom-8bz): cookie-auth + admin-gate + feature-gate", func() {
	It("rejects a request with no refresh_token cookie", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "aliws"))
		e := boomtimeRouter(hz)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/label-images/ws", nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Key", "dGVzdC1rZXktMS0yLTMtNC01LTY=")
		req.Header.Set("Sec-WebSocket-Version", "13")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).NotTo(Equal(http.StatusSwitchingProtocols))
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})

	It("rejects a non-admin with a valid cookie (403 pre-upgrade)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "aliws"))
		e := boomtimeRouter(hz)
		ctx := context.Background()
		user, _ := hz.MintUser("liws_nonadmin")
		refresh := fmt.Sprintf("refresh-li-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(user, refresh), 24)).To(Succeed())

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/label-images/ws", nil)
		req.Header.Set("Cookie", "refresh_token="+refresh)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Key", "dGVzdC1rZXktMS0yLTMtNC01LTY=")
		req.Header.Set("Sec-WebSocket-Version", "13")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusForbidden))
	})

	It("admin cookie + no ImageJobQueue → 503 (feature disabled after admin-gate)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "aliws"))
		e := boomtimeRouter(hz)
		ctx := context.Background()
		user, _ := hz.MintUser("liws_admin_noqueue")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		refresh := fmt.Sprintf("refresh-li-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(user, refresh), 24)).To(Succeed())

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/label-images/ws", nil)
		req.Header.Set("Cookie", "refresh_token="+refresh)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Key", "dGVzdC1rZXktMS0yLTMtNC01LTY=")
		req.Header.Set("Sec-WebSocket-Version", "13")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable))
	})
})
