// oidc_rotate_cookie_test.go — regression for the boom-93f.11.6 silent-rotation
// medium found in the 2026-09-06 audit.
//
// CallbackOIDC pins the browser cookie's `Expires` to the FIRST id_token's
// expiry. tryRotateOIDCSession then refresh-grants against the IdP and pushes
// oidc_sessions.id_token_expiry forward — but it emitted no Set-Cookie, so the
// browser still evicted the cookie at the ORIGINAL Expires. The next
// /auth/refresh_token arrived with no cookie and the user was logged out while
// a valid, freshly-rotated session row sat in the DB: the whole rotation
// machinery (decrypt → IdP round-trip → DB update) could never extend anything.
//
// The spec asserts the server-side rotation AND the client-side cookie move
// together — either half alone is satisfiable without the session actually
// living longer.
package identity_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// rotatingResolverStub stands in for *auth.OIDCResolver: it reports the "oidc"
// provider, resolves the session cookie to a fixed owner, and returns a
// successful refresh-grant. Using a stub (rather than a fake Authentik with
// discovery + JWKS + signed id_tokens) keeps the spec about the ONE thing under
// test — what the handler does with a successful rotation.
type rotatingResolverStub struct {
	auth.LocalPasswordResolver
	owner     string
	newExpiry time.Time
	calls     int
}

func (r *rotatingResolverStub) ProviderName() string { return "oidc" }

func (r *rotatingResolverStub) ResolveCookie(_ context.Context, _ *db.DB, _ string) (*auth.Identity, *apierr.Error) {
	return auth.AllCapsIdentity(r.owner), nil
}

func (r *rotatingResolverStub) RefreshSession(_ context.Context, _ string) (time.Time, string, *apierr.Error) {
	r.calls++
	return r.newExpiry, "rotated-provider-refresh", nil
}

var _ = Describe("OIDC silent session rotation (boom-93f.11.6)", func() {
	It("re-emits the session cookie with the EXTENDED expiry, not just the DB row", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.CookieSecure = true
		e := hz.Router() // registers POST /auth/refresh_token
		installEncryptionKeyAC()

		ctx := context.Background()
		user := "oidc_rotate_cookie_g"
		hz.Cleanup(user)

		hash, salt, err := auth.HashPassword("pw-" + user)
		Expect(err).NotTo(HaveOccurred())
		created, err := hz.DB.InsertUser(ctx, db.StoredUser{
			Username: user, HashedPassword: hash, SaltUsed: salt,
			ArgonVersion: auth.ArgonVersionCurrent,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())

		// A session whose id_token expires SOON — this is the instant the
		// browser would drop the cookie if we never move it.
		originalExpiry := time.Now().UTC().Add(15 * time.Minute).Truncate(time.Second)
		encRefresh, err := auth.Encrypt([]byte("provider-refresh-token"))
		Expect(err).NotTo(HaveOccurred())
		sessionID, err := auth.RandToken()
		Expect(err).NotTo(HaveOccurred())
		Expect(hz.DB.CreateOIDCSession(ctx, sessionID, user, originalExpiry, encRefresh)).To(Succeed())
		DeferCleanup(func() {
			_, _ = hz.DB.Pool.Exec(ctx, `DELETE FROM oidc_sessions WHERE username=$1`, user)
		})

		// The IdP hands back a much later expiry.
		rotatedExpiry := time.Now().UTC().Add(8 * time.Hour).Truncate(time.Second)
		stub := &rotatingResolverStub{owner: user, newExpiry: rotatedExpiry}
		prev := auth.CurrentResolver()
		auth.SetResolver(stub)
		DeferCleanup(func() { auth.SetResolver(prev) })

		req := httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: sessionID})
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		Expect(rr.Code).To(Equal(http.StatusOK), "body=%s", rr.Body.String())

		// Server side: the rotation actually ran and moved the row forward.
		Expect(stub.calls).To(Equal(1), "RefreshSession was not called — rotation path did not run")
		_, dbExpiry, found, err := hz.DB.GetOIDCSessionRefresh(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(dbExpiry.UTC()).To(BeTemporally("~", rotatedExpiry, 2*time.Second),
			"server-side id_token_expiry was not rotated")

		// Client side: the browser must be told about it. Without a Set-Cookie
		// carrying the NEW Expires the browser evicts the cookie at
		// originalExpiry and the rotation is a no-op.
		var session *http.Cookie
		for _, ck := range (&http.Response{Header: rr.Header()}).Cookies() {
			if ck.Name == "refresh_token" {
				session = ck
			}
		}
		Expect(session).NotTo(BeNil(),
			"no refresh_token Set-Cookie after a successful rotation — the browser will still "+
				"discard the session cookie at the ORIGINAL id_token expiry, so silent rotation "+
				"cannot extend the session")
		Expect(session.Value).To(Equal(sessionID),
			"rotation must keep the SAME opaque session id — a new value would orphan the row")
		Expect(session.Expires.UTC()).To(BeTemporally(">", originalExpiry),
			"re-emitted cookie still expires at the original id_token expiry (%s)", originalExpiry)
		Expect(session.Expires.UTC()).To(BeTemporally("~", rotatedExpiry, 2*time.Second))

		// The attribute tuple must match the login cookie exactly, or the
		// browser stores a second cookie instead of replacing the first.
		Expect(session.HttpOnly).To(BeTrue())
		Expect(session.Secure).To(BeTrue(), "prod-mode rotation cookie lost Secure")
		Expect(session.SameSite).To(Equal(http.SameSiteStrictMode))
		Expect(session.Path).To(Equal("/"))
	})
})
