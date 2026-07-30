// user_avatar_ginkgo_test.go — ginkgo mirror of user_avatar_test.go (gaka-9v4).
// 1:1 case map (6 stdlib TestXxx):
//
//	TestAvatar_SynthesizePrompt_401WithoutToken   → SynthesizePrompt > "no token → auth failure (not 2xx)"
//	TestAvatar_SynthesizePrompt_403ForNonAdmin    → SynthesizePrompt > "authed non-admin → 403"
//	TestAvatar_SynthesizePrompt_503WhenLLMUnconfigured → SynthesizePrompt > "admin, no LLM key → 503 w/ LLM sentinel"
//	TestAvatar_Regenerate_503WhenShimDisabled     → Regenerate > "no shim → 503"
//	TestAvatar_Regenerate_400OnEmptyPrompt        → Regenerate > "empty prompt → 400 with shim on"
//	TestAvatar_PublicGet_404WhenNotReady          → PublicGet > "no row / running row → 404; ready row → 200 with bytes"
//	TestAvatar_Status_None                        → Status > "no row → {status:none}"
package identity_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("Avatar SynthesizePrompt (gaka-9v4)", func() {
	It("rejects unauth'd callers before any LLM check leaks config posture", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		body := strings.NewReader(`{"topLabels":["PYTHON MASTER"],"synopsis":"night owl"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/avatar/synthesize-prompt", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec.Code).NotTo(Or(Equal(http.StatusOK), Equal(http.StatusAccepted)),
			"synthesize-prompt with no token: got %d (want auth failure). body=%s",
			rec.Code, rec.Body.String())
	})

	It("returns 403 for a non-admin API caller (before the LLM gate)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("avatar_synth_nonadmin_g")

		body := strings.NewReader(`{"topLabels":["PYTHON MASTER"],"synopsis":"night owl"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/avatar/synthesize-prompt", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden),
			"synthesize-prompt as non-admin: body=%s", rec.Body.String())
	})

	It("returns 503 with an LLM sentinel when the admin caller has no LLM key configured", func() {
		hz := testutil.NewHarness(GinkgoT())
		username, token := hz.MintUser("avatar_synth_off_g")
		hz.Cfg.AdminUsers = map[string]struct{}{username: {}}
		e := hz.Router()

		body := strings.NewReader(`{"topLabels":["PYTHON MASTER"],"synopsis":"night owl"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/avatar/synthesize-prompt", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusServiceUnavailable),
			"synthesize-prompt with no LLM key: body=%s", rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("LLM"))
	})
})

var _ = Describe("Avatar Regenerate (gaka-9v4)", func() {
	It("returns 503 when the comfyui shim is not configured", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("avatar_regen_off_g")

		body := strings.NewReader(`{"prompt":"chibi portrait, cel shading"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/avatar/regenerate", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusServiceUnavailable),
			"regenerate with no shim: body=%s", rec.Body.String())
	})

	It("returns 400 for an empty prompt (with shim on)", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.FeatureLabelImages = true
		hz.Cfg.ComfyUIShimURL = "http://127.0.0.1:1"
		e := hz.Router()
		_, token := hz.MintUser("avatar_regen_empty_g")

		body := strings.NewReader(`{"prompt":"   "}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/avatar/regenerate", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"regenerate with empty prompt: body=%s", rec.Body.String())
	})
})

var _ = Describe("Avatar PublicGet (gaka-9v4)", func() {
	It("404s no-row + running rows; serves 200+bytes only when status=ready", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		username, _ := hz.MintUser("avatar_public_get_g")
		ctx := context.Background()

		// (1) No row → 404.
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+username+"/avatar", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound), "no row: body=%s", rec.Body.String())

		// (2) status=running → still 404.
		Expect(hz.DB.SetAvatarStatus(ctx, username, db.UserAvatarStatusRunning, "")).To(Succeed())
		req = httptest.NewRequest(http.MethodGet, "/api/v1/users/"+username+"/avatar", nil)
		rec = httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound), "running: body=%s", rec.Body.String())

		// (3) status=ready with bytes → 200, correct content-type, bytes match.
		img := []byte("\x89PNG\r\n\x1a\nchibi-bytes-g")
		Expect(hz.DB.SaveUserAvatar(ctx, username, img, "image/png", "chroma_hd", "prompt", nil)).To(Succeed())
		req = httptest.NewRequest(http.MethodGet, "/api/v1/users/"+username+"/avatar", nil)
		rec = httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "ready: body=%s", rec.Body.String())
		Expect(rec.Header().Get("Content-Type")).To(Equal("image/png"))
		Expect(rec.Body.String()).To(Equal(string(img)))
		Expect(rec.Header().Get("Cache-Control")).To(ContainSubstring("max-age="))
	})
})

var _ = Describe("Avatar Status (gaka-9v4)", func() {
	It("returns {status:none} for a user with no row", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("avatar_status_none_g")

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/avatar/status", nil)
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "status/no-row: body=%s", rec.Body.String())
		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed(), "body=%s", rec.Body.String())
		Expect(got["status"]).To(Equal("none"), "full=%v", got)
	})
})
