// Mock-Authentik integration test for OIDCResolver.HandleCallback (boom-0oe.11,
// design §9.1). A local httptest.Server plays the OIDC provider — serving the
// discovery doc, a JWKS, and a /token endpoint that returns a locally-signed
// id_token — so the whole callback path (code exchange → JWKS verify → §6.5
// provisioning → session create) is exercised deterministically, with no live
// Authentik and no issuer-split-horizon.
package auth

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
)

const mockKID = "mock-key-1"

// mockNonce is baked into the id_token AND passed to HandleCallback so the
// boom-93f.16 nonce check passes deterministically in tests.
const mockNonce = "mock-nonce-abc123"

// mockAuthentik spins an httptest OIDC provider signing id_tokens with `key`.
// `groups` is baked into the issued id_token. Returns the server (caller sets
// DeferCleanup) + the issuer URL (trailing slash, as NewOIDCResolver expects).
func mockAuthentik(key *rsa.PrivateKey, clientID, sub, preferred string, groups []string) (*httptest.Server, string) {
	var server *httptest.Server
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: key.Public(), KeyID: mockKID, Algorithm: "RS256", Use: "sig",
	}}}

	mux := http.NewServeMux()
	jsonHdr := func(w http.ResponseWriter) { w.Header().Set("Content-Type", "application/json") }
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		jsonHdr(w)
		iss := server.URL + "/"
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                iss,
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
			"groups":             groups,
			"nonce":              mockNonce,
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     signIDToken(claims, key),
		})
	})
	server = httptest.NewServer(mux)
	return server, server.URL
}

func signIDToken(claims map[string]any, key *rsa.PrivateKey) string {
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithHeader("kid", mockKID).WithType("JWT"),
	)
	Expect(err).NotTo(HaveOccurred())
	payload, err := json.Marshal(claims)
	Expect(err).NotTo(HaveOccurred())
	obj, err := signer.Sign(payload)
	Expect(err).NotTo(HaveOccurred())
	s, err := obj.CompactSerialize()
	Expect(err).NotTo(HaveOccurred())
	return s
}

var _ = Describe("OIDCResolver.HandleCallback (mock Authentik)", func() {
	const clientID = "boomtime"
	g2r := map[string]string{"boomtime-admin": "admin", "boomtime-full": "full", "boomtime-light": "light"}

	It("autoprovisions a new user with the role from its groups + persists link + session", func() {
		database := openServiceTestDB()
		ctx := context.Background()

		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())
		sub := "sub-new-" + time.Now().Format("150405.000000000")
		preferred := uniqueUsername("oidcnew")

		server, issuer := mockAuthentik(key, clientID, sub, preferred, []string{"boomtime-full"})
		DeferCleanup(server.Close)

		resolver, err := NewOIDCResolver(ctx, issuer, "", clientID, "secret", server.URL+"/cb", g2r, true /*autoprovision*/)
		Expect(err).NotTo(HaveOccurred())

		res, aerr := resolver.HandleCallback(ctx, database, "any-code", mockNonce)
		Expect(aerr).To(BeNil())
		Expect(res.Identity.Username).To(Equal(preferred))
		Expect(res.Identity.Role).To(Equal(RoleFull))

		// External-identity link persisted on (authentik, sub).
		u, ok, e := database.GetUserByExternalIdentity(ctx, OIDCProviderName, sub)
		Expect(e).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(u).To(Equal(preferred))

		// Server-side session created + resolvable.
		su, sok, se := database.GetOIDCSessionUser(ctx, res.SessionID)
		Expect(se).NotTo(HaveOccurred())
		Expect(sok).To(BeTrue())
		Expect(su).To(Equal(preferred))
	})

	It("rejects an unknown identity with 403 when autoprovision is off", func() {
		database := openServiceTestDB()
		ctx := context.Background()

		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())
		sub := "sub-reject-" + time.Now().Format("150405.000000000")
		preferred := uniqueUsername("oidcreject")

		server, issuer := mockAuthentik(key, clientID, sub, preferred, []string{"boomtime-light"})
		DeferCleanup(server.Close)

		resolver, err := NewOIDCResolver(ctx, issuer, "", clientID, "secret", server.URL+"/cb", g2r, false /*autoprovision OFF*/)
		Expect(err).NotTo(HaveOccurred())

		res, aerr := resolver.HandleCallback(ctx, database, "any-code", mockNonce)
		Expect(res).To(BeNil())
		Expect(aerr).NotTo(BeNil())
		Expect(aerr.Status).To(Equal(http.StatusForbidden))
	})

	It("resolves a returning user on the existing (authentik, sub) link", func() {
		database := openServiceTestDB()
		ctx := context.Background()

		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())
		sub := "sub-return-" + time.Now().Format("150405.000000000")
		preferred := uniqueUsername("oidcret")

		server, issuer := mockAuthentik(key, clientID, sub, preferred, []string{"boomtime-full"})
		DeferCleanup(server.Close)
		resolver, err := NewOIDCResolver(ctx, issuer, "", clientID, "secret", server.URL+"/cb", g2r, true)
		Expect(err).NotTo(HaveOccurred())

		// First login provisions.
		_, aerr := resolver.HandleCallback(ctx, database, "code-1", mockNonce)
		Expect(aerr).To(BeNil())
		// Second login must resolve to the SAME user (not a collision-suffixed new one).
		res2, aerr2 := resolver.HandleCallback(ctx, database, "code-2", mockNonce)
		Expect(aerr2).To(BeNil())
		Expect(res2.Identity.Username).To(Equal(preferred))
	})

	It("updates a returning user's role when the provider groups now map to a different role (boom-93f.19 write-on-change)", func() {
		database := openServiceTestDB()
		ctx := context.Background()

		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())
		sub := "sub-rolechg-" + time.Now().Format("150405.000000000")
		preferred := uniqueUsername("oidcrole")

		// The returned Identity.Role is NOT a useful probe here: with
		// BOOM_FEATURE_USER_MODEL off (the test default) resolveIdentity returns
		// AllCapsIdentity → Role=full for everyone. Assert on the stored
		// users.role row instead, which is what the write-on-change touches.

		// First login: groups → light. Row provisioned at role=light.
		srv1, iss1 := mockAuthentik(key, clientID, sub, preferred, []string{"boomtime-light"})
		DeferCleanup(srv1.Close)
		r1, err := NewOIDCResolver(ctx, iss1, "", clientID, "secret", srv1.URL+"/cb", g2r, true)
		Expect(err).NotTo(HaveOccurred())
		res1, aerr := r1.HandleCallback(ctx, database, "code-1", mockNonce)
		Expect(aerr).To(BeNil())
		Expect(res1.Identity.Username).To(Equal(preferred))
		full1, ferr1 := database.GetUserFullByName(ctx, preferred)
		Expect(ferr1).NotTo(HaveOccurred())
		Expect(full1.Role).To(Equal(string(RoleLight)))

		// Second login, SAME sub, groups now → admin. The existing-link branch
		// must see derived(admin) != stored(light) and rewrite the row's role.
		srv2, iss2 := mockAuthentik(key, clientID, sub, preferred, []string{"boomtime-admin"})
		DeferCleanup(srv2.Close)
		r2, err := NewOIDCResolver(ctx, iss2, "", clientID, "secret", srv2.URL+"/cb", g2r, true)
		Expect(err).NotTo(HaveOccurred())
		res2, aerr2 := r2.HandleCallback(ctx, database, "code-2", mockNonce)
		Expect(aerr2).To(BeNil())
		Expect(res2.Identity.Username).To(Equal(preferred)) // same user, not suffixed
		full2, ferr2 := database.GetUserFullByName(ctx, preferred)
		Expect(ferr2).NotTo(HaveOccurred())
		Expect(full2.Role).To(Equal(string(RoleAdmin)))

		// Third login with the SAME (admin) groups exercises the equal-role
		// branch: no clobber, no error, role stays admin.
		res3, aerr3 := r2.HandleCallback(ctx, database, "code-3", mockNonce)
		Expect(aerr3).To(BeNil())
		Expect(res3.Identity.Username).To(Equal(preferred))
		full3, ferr3 := database.GetUserFullByName(ctx, preferred)
		Expect(ferr3).NotTo(HaveOccurred())
		Expect(full3.Role).To(Equal(string(RoleAdmin)))
	})

	It("rejects an id_token whose nonce does not match the expected nonce (boom-93f.16)", func() {
		database := openServiceTestDB()
		ctx := context.Background()

		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())
		sub := "sub-nonce-" + time.Now().Format("150405.000000000")
		preferred := uniqueUsername("oidcnonce")

		server, issuer := mockAuthentik(key, clientID, sub, preferred, []string{"boomtime-full"})
		DeferCleanup(server.Close)
		resolver, err := NewOIDCResolver(ctx, issuer, "", clientID, "secret", server.URL+"/cb", g2r, true)
		Expect(err).NotTo(HaveOccurred())

		// id_token carries mockNonce, but we present a DIFFERENT expected nonce
		// (as a replayed/injected token would) → 401, no provisioning.
		res, aerr := resolver.HandleCallback(ctx, database, "any-code", "wrong-nonce")
		Expect(res).To(BeNil())
		Expect(aerr).NotTo(BeNil())
		Expect(aerr.Status).To(Equal(http.StatusUnauthorized))

		// An empty expected nonce (missing cookie) must also fail closed.
		res2, aerr2 := resolver.HandleCallback(ctx, database, "any-code", "")
		Expect(res2).To(BeNil())
		Expect(aerr2).NotTo(BeNil())
		Expect(aerr2.Status).To(Equal(http.StatusUnauthorized))
	})
})
