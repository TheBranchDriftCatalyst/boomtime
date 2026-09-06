// oidc_logout_test.go — boom-haz1 regression.
//
// Under BOOM_AUTH_PROVIDER=oidc, Logout takes the OIDC branch and revokes the
// bearers the FE minted through /auth/refresh_token. It used to do that with
// DB.DeleteUserAccessTokens, whose SQL is an unqualified
// `DELETE FROM auth_tokens WHERE owner = $1` — which also destroyed the
// NEVER-EXPIRING API tokens minted via /auth/create_api_token. Those stay a
// supported credential under OIDC (OIDCResolver.ResolveBearer delegates to the
// local resolver), so clicking "Log out" in the web UI silently killed every
// editor / wakatime-plugin token and heartbeat ingestion 401'd until the user
// noticed and re-created them.
//
// The spec below pins BOTH halves of the contract, because a fix that simply
// stopped revoking anything would also "pass" a one-sided assertion:
//   - the 30-minute session bearer MUST die with the session (boom-93f.14), and
//   - the NULL-expiry API token MUST survive (boom-haz1).
//
// The same `token_expiry IS NOT NULL` predicate has always guarded
// ChangePasswordAndRevoke for exactly this reason.
package identity_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// oidcProviderResolver reports ProviderName() == "oidc" so Logout takes the
// OIDC branch, while embedding LocalPasswordResolver for every other method —
// the handler under test only reads ProviderName() plus the DB directly, so a
// real IdP round-trip is not needed to exercise the revoke scoping.
type oidcProviderResolver struct{ auth.LocalPasswordResolver }

func (oidcProviderResolver) ProviderName() string { return "oidc" }

var _ = Describe("OIDC logout revoke scoping (boom-haz1)", func() {
	It("kills the session bearer but PRESERVES the never-expiring API token", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		apiroute.NoContent(e, http.MethodPost, "/auth/logout", hz.H.Identity.Logout)

		ctx := context.Background()
		user := "haz1_oidc_logout_g"
		hz.Cleanup(user)

		hash, salt, err := auth.HashPassword("pw-" + user)
		Expect(err).NotTo(HaveOccurred())
		created, err := hz.DB.InsertUser(ctx, db.StoredUser{
			Username: user, HashedPassword: hash, SaltUsed: salt,
			ArgonVersion: auth.ArgonVersionCurrent,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())

		// The editor/plugin credential: auth_tokens row with a NULL token_expiry.
		// InsertAPIToken stores exactly the value the client later presents in
		// the Authorization header (auth.CreateAPIToken's ToBase64(raw)).
		apiToken := auth.ToBase64(auth.NewRawToken())
		Expect(hz.DB.InsertAPIToken(ctx, user, apiToken, "editor-plugin")).To(Succeed())

		// The web-session credential: the 30-minute bearer /auth/refresh_token
		// mints under OIDC (non-NULL token_expiry).
		sessionBearer := auth.ToBase64(auth.NewRawToken())
		Expect(hz.DB.CreateOIDCAccessToken(ctx, user, sessionBearer)).To(Succeed())

		// The browser session itself: an oidc_sessions row keyed by the opaque
		// value carried in the `refresh_token` cookie.
		sessionID, err := auth.RandToken()
		Expect(err).NotTo(HaveOccurred())
		Expect(hz.DB.CreateOIDCSession(ctx, sessionID, user, time.Now().UTC().Add(time.Hour), nil)).To(Succeed())
		DeferCleanup(func() {
			_, _ = hz.DB.Pool.Exec(ctx, `DELETE FROM oidc_sessions WHERE username=$1`, user)
		})

		prev := auth.CurrentResolver()
		auth.SetResolver(oidcProviderResolver{})
		DeferCleanup(func() { auth.SetResolver(prev) })

		// Preconditions: both credentials authenticate before logout.
		owner, ok, err := hz.DB.GetUserByToken(ctx, apiToken)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "precondition: API token must resolve before logout")
		Expect(owner).To(Equal(user))
		_, ok, err = hz.DB.GetUserByToken(ctx, sessionBearer)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "precondition: session bearer must resolve before logout")

		req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: sessionID})
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		Expect(rr.Code).To(Equal(http.StatusNoContent), "body=%s", rr.Body.String())

		// The session itself is gone (unchanged boom-93f.13 behaviour).
		_, sessionAlive, err := hz.DB.GetOIDCSessionUser(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(sessionAlive).To(BeFalse(), "oidc_sessions row survived logout")

		// The session-minted bearer dies WITH the session (boom-93f.14).
		_, bearerAlive, err := hz.DB.GetUserByToken(ctx, sessionBearer)
		Expect(err).NotTo(HaveOccurred())
		Expect(bearerAlive).To(BeFalse(),
			"session bearer survived logout — the OIDC session revoke regressed")

		// boom-haz1: the NULL-expiry API token MUST still authenticate.
		apiOwner, apiAlive, err := hz.DB.GetUserByToken(ctx, apiToken)
		Expect(err).NotTo(HaveOccurred())
		Expect(apiAlive).To(BeTrue(),
			"boom-haz1 REGRESSION: web logout destroyed the never-expiring API token — "+
				"every editor/wakatime plugin now 401s on heartbeat ingest until the user re-creates it")
		Expect(apiOwner).To(Equal(user))

		// And it is still the ONLY auth_tokens row left for this owner, so the
		// fix scoped the delete rather than skipping it.
		var remaining int
		Expect(hz.DB.Pool.QueryRow(ctx,
			`SELECT count(*) FROM auth_tokens WHERE owner=$1`, user).Scan(&remaining)).To(Succeed())
		Expect(remaining).To(Equal(1),
			"expected exactly the API token to remain in auth_tokens; got %d rows", remaining)
	})
})
