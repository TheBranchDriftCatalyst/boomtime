// github_oauth_test.go — end-to-end coverage of the GitHub connect callback
// (gaka-2ip Phase 1). A mock-GitHub httptest server stands in for github.com /
// api.github.com (mirrors oidc_callback_test.go's approach); the resolver is
// installed via the auth test seam and the handler is driven through the real
// Echo router. Asserts the token is stored ENCRYPTED (decrypts back to the
// mock token), the login is captured, and the token NEVER appears in any HTTP
// response or the status payload.
package identity_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity/oauth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

const ghTestSigningKey = "test-oauth-state-signing-key-abc"
const ghMockToken = "gho_MOCK_TOKEN_should_never_leak"

// mockGithubServerGH serves the token + user endpoints. Returns the server.
func mockGithubServerGH(login string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + ghMockToken + `","token_type":"bearer","scope":"read:user"}`))
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"` + login + `","id":42}`))
	})
	return httptest.NewServer(mux)
}

// routerWithGithubGH wires the router + the github routes the production server
// registers, and installs a resolver pointed at srv. Sets the signing key so
// CallbackGithub can verify state.
func routerWithGithubGH(hz *testutil.Harness, srv *httptest.Server) http.Handler {
	hz.Cfg.OAuthStateSigningKey = ghTestSigningKey
	hz.Cfg.FeatureGithubStats = true
	hz.Cfg.GithubOAuthClientID = "cid"
	hz.Cfg.GithubOAuthClientSecret = "csecret"

	resolver := auth.NewGithubOAuthResolverForTest("cid", "csecret", "https://boom/cb",
		srv.URL+"/login/oauth/authorize", srv.URL+"/login/oauth/access_token", srv.URL+"/user")
	auth.SetGithubOAuthResolver(resolver)
	DeferCleanup(func() { auth.SetGithubOAuthResolver(nil) })

	e := hz.Router()
	h := hz.H
	e.GET("/auth/github/callback", h.Identity.CallbackGithub)
	e.GET("/api/v1/users/current/github", h.Identity.GetGithubConnection)
	e.DELETE("/api/v1/users/current/github", h.Identity.DisconnectGithub)
	return e
}

var _ = Describe("GitHub connect callback (gaka-2ip)", func() {
	It("exchanges the code, captures the login, and stores the token ENCRYPTED — never leaking it", func() {
		installEncryptionKeyAC()
		hz := testutil.NewHarness(GinkgoT())
		srv := mockGithubServerGH("octocat")
		DeferCleanup(srv.Close)
		e := routerWithGithubGH(hz, srv)

		user, token := hz.MintUser("gh_cb_ok")
		state, err := oauth.Sign([]byte(ghTestSigningKey), user, time.Now())
		Expect(err).NotTo(HaveOccurred())

		// Drive the callback.
		req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=good-code&state="+state, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusFound))
		Expect(rec.Header().Get("Location")).To(ContainSubstring("github=connected"))
		// The redirect Location MUST NOT carry the token anywhere.
		Expect(rec.Header().Get("Location")).NotTo(ContainSubstring(ghMockToken))
		Expect(rec.Body.String()).NotTo(ContainSubstring(ghMockToken))

		// The token is stored ENCRYPTED — raw column bytes are not the plaintext,
		// but decrypt back to the mock token.
		blob, ok, err := hz.DB.GetEncryptedGithubToken(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(string(blob)).NotTo(ContainSubstring(ghMockToken), "token stored in plaintext — encryption did not happen")
		dec, err := auth.Decrypt(blob)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(dec)).To(Equal(ghMockToken))

		// The status API reports connected + login, and NEVER the token.
		sreq := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/github", nil)
		sreq.Header.Set("Authorization", "Basic "+token)
		srec := httptest.NewRecorder()
		e.ServeHTTP(srec, sreq)
		Expect(srec.Code).To(Equal(http.StatusOK))
		Expect(srec.Body.String()).NotTo(ContainSubstring(ghMockToken))

		var payload struct {
			Connected bool    `json:"connected"`
			Login     string  `json:"login"`
			Status    string  `json:"status"`
			CheckedAt *string `json:"checkedAt"`
		}
		Expect(json.Unmarshal(srec.Body.Bytes(), &payload)).To(Succeed())
		Expect(payload.Connected).To(BeTrue())
		Expect(payload.Login).To(Equal("octocat"))
		Expect(payload.Status).To(Equal("valid"))
	})

	It("rejects a tampered/forged state and stores nothing", func() {
		installEncryptionKeyAC()
		hz := testutil.NewHarness(GinkgoT())
		srv := mockGithubServerGH("octocat")
		DeferCleanup(srv.Close)
		e := routerWithGithubGH(hz, srv)

		user, _ := hz.MintUser("gh_cb_forged")

		// A state signed with the WRONG key (an attacker without the signing key).
		forged, err := oauth.Sign([]byte("attacker-key"), user, time.Now())
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=good-code&state="+forged, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusFound))
		Expect(rec.Header().Get("Location")).To(ContainSubstring("github=state"))
		// Nothing stored.
		_, ok, err := hz.DB.GetEncryptedGithubToken(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	It("rejects an expired state", func() {
		installEncryptionKeyAC()
		hz := testutil.NewHarness(GinkgoT())
		srv := mockGithubServerGH("octocat")
		DeferCleanup(srv.Close)
		e := routerWithGithubGH(hz, srv)

		user, _ := hz.MintUser("gh_cb_expired")
		// Issued 30 minutes ago — well past the 10-minute max-age.
		stale, err := oauth.Sign([]byte(ghTestSigningKey), user, time.Now().Add(-30*time.Minute))
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=good-code&state="+stale, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusFound))
		Expect(rec.Header().Get("Location")).To(ContainSubstring("github=state"))
	})

	It("disconnect clears the stored token (idempotent)", func() {
		installEncryptionKeyAC()
		hz := testutil.NewHarness(GinkgoT())
		srv := mockGithubServerGH("octocat")
		DeferCleanup(srv.Close)
		e := routerWithGithubGH(hz, srv)

		user, token := hz.MintUser("gh_cb_disc")
		state, _ := oauth.Sign([]byte(ghTestSigningKey), user, time.Now())
		req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=good-code&state="+state, nil)
		e.ServeHTTP(httptest.NewRecorder(), req)

		// Confirm connected, then DELETE.
		del := httptest.NewRequest(http.MethodDelete, "/api/v1/users/current/github", nil)
		del.Header.Set("Authorization", "Basic "+token)
		drec := httptest.NewRecorder()
		e.ServeHTTP(drec, del)
		Expect(drec.Code).To(Equal(http.StatusNoContent))

		_, ok, err := hz.DB.GetEncryptedGithubToken(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())

		// Idempotent second delete.
		del2 := httptest.NewRequest(http.MethodDelete, "/api/v1/users/current/github", nil)
		del2.Header.Set("Authorization", "Basic "+token)
		drec2 := httptest.NewRecorder()
		e.ServeHTTP(drec2, del2)
		Expect(drec2.Code).To(Equal(http.StatusNoContent))
	})

	It("cross-user isolation: user B cannot see user A's connection", func() {
		installEncryptionKeyAC()
		hz := testutil.NewHarness(GinkgoT())
		srv := mockGithubServerGH("alice-gh")
		DeferCleanup(srv.Close)
		e := routerWithGithubGH(hz, srv)

		userA, _ := hz.MintUser("gh_iso_a")
		_, tokenB := hz.MintUser("gh_iso_b")
		state, _ := oauth.Sign([]byte(ghTestSigningKey), userA, time.Now())
		e.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=c&state="+state, nil))

		// B queries their own connection — must be disconnected (A's connect
		// must not bleed across).
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/github", nil)
		req.Header.Set("Authorization", "Basic "+tokenB)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(strings.Contains(rec.Body.String(), `"connected":false`)).To(BeTrue())
	})
})
