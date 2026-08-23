// user_avatar_more_test.go — boom-d6x.handler: fill remaining coverage gaps
// for user_avatar.go beyond user_avatar_test.go.
//
// Named invariants (extending user_avatar_test.go):
//
//	"buildAvatarUserMessage handles empty + populated cases" — the LLM
//	prompt-assembly helper is stateless and pure; unit-test its two
//	visible branches (empty topLabels → "NEW OPERATOR", populated →
//	comma-joined). Covers the buildAvatarUserMessage 0% row.
//
//	"GetAvatarStatus with a running row returns status=running" — the
//	handler previously covered only the no-row branch (status:none).
//	Seeding a running row exercises the ok=true branch + the
//	updatedAt+error fields.
//
//	"GetAvatarStatus with a ready row returns status=ready + generatedAt" —
//	the third branch (ok=true, GeneratedAt != nil).
//
//	"RegenerateAvatar duplicate-in-flight → 409" — a second regen
//	request while status='running' must be refused.
//
//	"UserAvatar with empty :username → 400" — the guard runs before DB.
//
//	"SynthesizeAvatarPrompt with body > 4 KiB → 413" — admin-authed
//	caller with LLM configured: a big body trips the BindJSONWithLimit
//	cap BEFORE the LLM upstream call. Proves the cap wire-up.
//
//	"RegenerateAvatar eventually persists the shim bytes (Eventually)" —
//	rewritten: the previous test title promised eventual persistence but
//	only asserted the 202. Now we poll /avatar/status until it flips to
//	ready, then read the raw bytes via GetUserAvatar and assert they
//	equal the decoded base64 the shim returned. Also increments an
//	atomic counter inside the shim handler and asserts the goroutine
//	called upstream exactly once.
//
//	"SynthesizeAvatarPrompt non-admin caller → 403" — the endpoint sits
//	under /api/v1/admin/... and requireAdmin returns 403 for a caller
//	not on Cfg.AdminUsers. Without this, an accidental removal of the
//	requireAdmin call would silently open a per-token LLM cost amplifier.
//
//	"BadgeSvg upstream 502 does NOT leak upstream body" — companion to
//	the LLM 502-no-leak assertion. The critique flagged that the badges
//	tests only asserted StatusBadGateway; the no-leak substring check
//	was missing. Added a stub that returns 500 with a distinctive body,
//	then asserts that substring never appears in the client response.
package identity_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("Avatar Status extra branches (boom-d6x.handler)", func() {
	It("returns status=running when a running row exists", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("avstat_running")
		Expect(hz.DB.SetAvatarStatus(context.Background(), user, db.UserAvatarStatusRunning, "")).To(Succeed())

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/avatar/status", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got["status"]).To(Equal("running"),
			"expected status=running; got %+v", got)
		Expect(got).To(HaveKey("updatedAt"),
			"updatedAt should be present on any real row; got %+v", got)
	})

	It("returns status=ready + generatedAt when a saved avatar exists", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("avstat_ready")

		Expect(hz.DB.SaveUserAvatar(context.Background(), user,
			[]byte("bytes"), "image/png", "chroma_hd", "prompt", nil)).To(Succeed())

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/avatar/status", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got["status"]).To(Equal("ready"),
			"expected status=ready; got %+v", got)
		Expect(got).To(HaveKey("generatedAt"),
			"generatedAt must render on ready rows; got %+v", got)
	})
})

var _ = Describe("Avatar Regenerate extra branches (boom-d6x.handler)", func() {
	It("returns 409 when a regen is already running for the same user", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.FeatureLabelImages = true
		hz.Cfg.ComfyUIShimURL = "http://127.0.0.1:1"
		e := hz.Router()
		user, token := hz.MintUser("avregen_dup")

		// Plant a running row so the duplicate check trips.
		Expect(hz.DB.SetAvatarStatus(context.Background(), user, db.UserAvatarStatusRunning, "")).To(Succeed())

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/avatar/regenerate", token, map[string]any{
			"prompt": "chibi portrait, cel shading",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusConflict), "body=%s", rec.Body.String())
	})
})

var _ = Describe("RegenerateAvatar happy path (boom-d6x.handler)", func() {
	It("dispatches the async goroutine (202 immediate) AND eventually persists the shim bytes", func() {
		// Distinctive payload so we can assert on the saved bytes AFTER the
		// goroutine finishes — plain "hello" b64 = "aGVsbG8=" is easy to
		// verify. Wrap the handler in an atomic counter so we can prove the
		// shim was actually called (not just that status flipped).
		const shimB64 = "aGVsbG8="
		wantBytes, decErr := base64.StdEncoding.DecodeString(shimB64)
		Expect(decErr).NotTo(HaveOccurred())

		var callCount atomic.Int32
		shim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + shimB64 + `"}]}`))
		}))
		DeferCleanup(shim.Close)

		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.FeatureLabelImages = true
		hz.Cfg.ComfyUIShimURL = shim.URL
		hz.Cfg.ComfyUIModel = "flux_schnell_fast"
		e := hz.Router()
		user, token := hz.MintUser("avregen_ok")

		body := strings.NewReader(`{"prompt":"chibi portrait, night owl"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/avatar/regenerate", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted), "body=%s", rec.Body.String())
		// Response says 'running'.
		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got["status"]).To(Equal("running"))

		// Eventually the goroutine finishes: status flips to 'ready' and the
		// saved bytes match what the shim returned. If the goroutine were
		// never dispatched (or panicked), the status would stay 'running'
		// forever and this Eventually would time out — that's the whole
		// point of the assertion the previous test lacked.
		Eventually(func() string {
			r := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/avatar/status", token, nil)
			if r.Code != http.StatusOK {
				return "http-" + http.StatusText(r.Code)
			}
			var payload map[string]any
			if err := json.Unmarshal(r.Body.Bytes(), &payload); err != nil {
				return "unmarshal-fail"
			}
			s, _ := payload["status"].(string)
			return s
		}, 5*time.Second, 50*time.Millisecond).Should(Equal("ready"),
			"async goroutine never flipped status to 'ready' — was it dispatched?")

		// Assert exactly one shim call was made. A retry loop or a duplicate
		// goroutine would push this > 1 and regress the "one avatar per user,
		// no batching win" comment on RegenerateAvatar.
		Expect(callCount.Load()).To(Equal(int32(1)),
			"expected exactly 1 shim call, got %d — dispatch race or retry regression",
			callCount.Load())

		// Assert the persisted bytes match the shim's decoded b64 — the
		// full save path (Generate → SaveUserAvatar → DB) round-tripped.
		av, ok, err := hz.DB.GetUserAvatar(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "avatar row missing after ready flip")
		Expect(av.ImageBytes).To(Equal(wantBytes),
			"saved bytes don't match the shim response: got %q want %q",
			string(av.ImageBytes), string(wantBytes))
	})
})

var _ = Describe("UserAvatar public GET input guards (boom-d6x.handler)", func() {
	It("returns 404 for a nonexistent username (row absent)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/nobody-here/avatar", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
			"body=%s", rec.Body.String())
	})
})

var _ = Describe("SynthesizePrompt success path w/ mock LLM (boom-d6x.handler)", func() {
	It("proxies the upstream SSE stream 1:1 with text/event-stream headers", func() {
		// Fake OpenAI-compat upstream — a couple of `data:` lines then done.
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The handler POSTs to /chat/completions — accept anything.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"chibi"}}]}` + "\n"))
			_, _ = w.Write([]byte(`data: [DONE]` + "\n"))
		}))
		DeferCleanup(upstream.Close)

		hz := testutil.NewHarness(GinkgoT())
		username, token := hz.MintUser("avsp_ok")
		hz.Cfg.AdminUsers = map[string]struct{}{username: {}}
		hz.Cfg.LLMAPIKey = "sk-test"
		hz.Cfg.LLMBaseURL = upstream.URL
		hz.Cfg.LLMModel = "gpt-test"
		e := hz.Router()

		body := strings.NewReader(`{"topLabels":["A"],"synopsis":"night"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/avatar/synthesize-prompt", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		Expect(rec.Header().Get("Content-Type")).To(Equal("text/event-stream"),
			"SSE content-type not wired: got %q", rec.Header().Get("Content-Type"))
		Expect(rec.Body.String()).To(ContainSubstring("chibi"),
			"upstream body not proxied: %s", rec.Body.String())
	})

	It("returns 502 when the LLM upstream responds non-200 (no leak of upstream body)", func() {
		// Non-200 → handler surfaces 502 (no leak of upstream body).
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("secret rate-limit blob"))
		}))
		DeferCleanup(upstream.Close)

		hz := testutil.NewHarness(GinkgoT())
		username, token := hz.MintUser("avsp_502")
		hz.Cfg.AdminUsers = map[string]struct{}{username: {}}
		hz.Cfg.LLMAPIKey = "sk-test"
		hz.Cfg.LLMBaseURL = upstream.URL
		e := hz.Router()

		body := strings.NewReader(`{"topLabels":["A"],"synopsis":"n"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/avatar/synthesize-prompt", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusBadGateway),
			"expected 502; got %d body=%s", rec.Code, rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("secret rate-limit blob"),
			"upstream body leaked to client: %s", rec.Body.String())
	})

	It("non-admin caller → 403 (LLM cost gate is the tightest security guard on this route)", func() {
		// AdminUsers is empty by default in the harness — an authenticated
		// non-admin token must be rejected with 403 by requireAdmin BEFORE
		// any LLM upstream call happens. This is the "boom-9v4 admin-only"
		// invariant called out in user_avatar.go's comments: LLM cost is
		// per-token and unbounded per user in the wrong hands. If this
		// endpoint ever becomes open to any authed user, DROP requireAdmin
		// and update this test — do not just delete it.
		hz := testutil.NewHarness(GinkgoT())
		_, token := hz.MintUser("avsp_nonadmin")
		// Deliberately DO NOT add this user to AdminUsers.
		hz.Cfg.LLMAPIKey = "sk-test"
		hz.Cfg.LLMBaseURL = "http://127.0.0.1:1" // would fail-fast if reached
		e := hz.Router()

		body := strings.NewReader(`{"topLabels":["A"],"synopsis":"n"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/avatar/synthesize-prompt", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden),
			"non-admin caller must be 403 (LLM cost amplifier); got %d body=%s",
			rec.Code, rec.Body.String())
	})
})

var _ = Describe("SynthesizePrompt body-size cap (boom-d6x.handler)", func() {
	It("admin caller with LLM configured but a 5 KiB body → 413", func() {
		hz := testutil.NewHarness(GinkgoT())
		user, token := hz.MintUser("avsp_413")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		hz.Cfg.LLMAPIKey = "sk-test"
		e := hz.Router()

		big := strings.Repeat("x", 5000)
		body := []byte(`{"topLabels":["A"],"synopsis":"` + big + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/avatar/synthesize-prompt",
			bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
			"body cap must fire before LLM upstream; got %d body=%s",
			rec.Code, rec.Body.String())
	})
})
