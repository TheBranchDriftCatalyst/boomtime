// oidc_link_mode_test.go — regression for the "lost link intent silently
// becomes a login" finding in the 2026-09-06 audit (internal/identity/oidc.go).
//
// /auth/link/oidc records state → initiating-username in the IN-PROCESS
// linkIntents map. If that entry is gone by the time the provider redirects
// back — boomtime restarted (a deploy), or the callback landed on a second
// replica — the state COOKIE still validates, takeLinkIntent misses, and
// CallbackOIDC fell straight through to LOGIN mode: it provisioned/resolved the
// OIDC identity and OVERWROTE the caller's refresh_token cookie. Under
// provider=local that cookie no longer resolves (local ResolveCookie reads
// refresh_tokens), so the user was silently signed out and no link was made;
// under provider=oidc they ended up signed in as whatever account the OIDC sub
// maps to. A mode switch is a far worse outcome than "link expired, try again".
package identity_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/go-jose/go-jose/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

const linkModeKID = "link-mode-key-1"
const linkModeNonce = "link-mode-nonce-abc123"

// mockIdPForLink is a minimal Authentik stand-in: discovery, JWKS, and a /token
// endpoint that returns a locally-signed id_token. Mirrors
// internal/shared/auth/oidc_callback_test.go's mockAuthentik; copied rather
// than shared because that helper is private to its own _test package.
func mockIdPForLink(key *rsa.PrivateKey, clientID, sub, preferred string) (*httptest.Server, string) {
	var server *httptest.Server
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: key.Public(), KeyID: linkModeKID, Algorithm: "RS256", Use: "sig",
	}}}
	mux := http.NewServeMux()
	jsonHdr := func(w http.ResponseWriter) { w.Header().Set("Content-Type", "application/json") }
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		jsonHdr(w)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                server.URL + "/",
			"authorization_endpoint":                server.URL + "/authorize",
			"token_endpoint":                        server.URL + "/token",
			"jwks_uri":                              server.URL + "/jwks",
			"userinfo_endpoint":                     server.URL + "/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		jsonHdr(w)
		_ = json.NewEncoder(w).Encode(jwks)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		jsonHdr(w)
		claims := map[string]any{
			"iss":                server.URL + "/",
			"aud":                clientID,
			"sub":                sub,
			"exp":                time.Now().Add(time.Hour).Unix(),
			"iat":                time.Now().Unix(),
			"email":              preferred + "@example.com",
			"preferred_username": preferred,
			"groups":             []string{"boomtime-full"},
			"nonce":              linkModeNonce,
		}
		signer, err := jose.NewSigner(
			jose.SigningKey{Algorithm: jose.RS256, Key: key},
			(&jose.SignerOptions{}).WithHeader("kid", linkModeKID).WithType("JWT"))
		Expect(err).NotTo(HaveOccurred())
		payload, merr := json.Marshal(claims)
		Expect(merr).NotTo(HaveOccurred())
		obj, serr := signer.Sign(payload)
		Expect(serr).NotTo(HaveOccurred())
		raw, cerr := obj.CompactSerialize()
		Expect(cerr).NotTo(HaveOccurred())
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     raw,
		})
	})
	server = httptest.NewServer(mux)
	return server, server.URL
}

var _ = Describe("OIDC callback with a LOST link intent", func() {
	It("fails the link instead of silently logging the caller in as the OIDC identity", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		apiroute.Redirect(e, http.MethodGet, "/auth/callback/oidc", http.StatusFound,
			hz.H.Identity.CallbackOIDC)

		ctx := context.Background()
		const clientID = "boomtime"
		stamp := time.Now().Format("150405.000000000")
		preferred := "linkmode_" + stamp
		sub := "sub-linkmode-" + stamp

		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())
		idp, issuer := mockIdPForLink(key, clientID, sub, preferred)
		DeferCleanup(idp.Close)

		resolver, err := auth.NewOIDCResolver(ctx, issuer, "", clientID, "secret",
			"http://127.0.0.1/auth/callback/oidc",
			map[string]string{"boomtime-full": "full"}, true)
		Expect(err).NotTo(HaveOccurred())
		auth.SetOIDCResolver(resolver)
		DeferCleanup(func() { auth.SetOIDCResolver(nil) })

		hz.Cleanup(preferred)
		DeferCleanup(func() {
			_, _ = hz.DB.Pool.Exec(ctx, `DELETE FROM user_external_identities WHERE sub=$1`, sub)
			_, _ = hz.DB.Pool.Exec(ctx, `DELETE FROM oidc_sessions WHERE username=$1`, preferred)
			_, _ = hz.DB.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, preferred)
		})

		// A link flow whose in-process intent is GONE (the deploy/second-replica
		// case): the state + nonce + mode cookies survive in the browser, the
		// server-side map does not.
		const state = "link-mode-state-token"
		req := httptest.NewRequest(http.MethodGet, "/auth/callback/oidc?code=mock-code&state="+state, nil)
		req.AddCookie(&http.Cookie{Name: "oidc_state", Value: state})
		req.AddCookie(&http.Cookie{Name: "oidc_nonce", Value: linkModeNonce})
		req.AddCookie(&http.Cookie{Name: "oidc_mode", Value: "link"})
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusFound), "body=%s", rec.Body.String())
		Expect(rec.Header().Get("Location")).To(Equal("/app/settings?tab=profile&link=error"),
			"a link flow whose intent was lost fell through to LOGIN mode (Location=%q) instead of "+
				"failing the link — under provider=local this silently replaces the caller's session "+
				"cookie with one that no longer resolves, and under provider=oidc it signs them in as "+
				"the OIDC identity", rec.Header().Get("Location"))

		for _, set := range rec.Header().Values("Set-Cookie") {
			Expect(set).NotTo(ContainSubstring("refresh_token="),
				"the failed link overwrote the caller's session cookie: %q", set)
		}

		exists, err := hz.DB.UserExists(ctx, preferred)
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse(),
			"the lost-intent callback autoprovisioned an account (%s) — it ran LOGIN mode", preferred)
	})
})
