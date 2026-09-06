// password_initial_test.go — regression for the "OIDC-provisioned users are
// permanently wedged" finding in the 2026-09-06 audit
// (internal/identity/password.go).
//
// ProvisionOIDCUser writes ”::bytea password material, so HasUsablePassword is
// false forever. UnlinkIdentity refuses to remove the last sign-in method with
// 409 "cannot unlink your only sign-in method — set a password first" — but
// there was no way to set one: POST /api/v1/users/current/password 400s on an
// empty currentPassword and 401s on any supplied value (VerifyPasswordWithVersion
// rejects an empty stored hash by design, boom-93f.19), and no CLI
// set-password command exists. The account could neither unlink nor gain local
// login, and if the IdP were decommissioned it was unrecoverable without manual
// SQL.
//
// The fix keeps ChangePassword's contract for every account that HAS a
// password and only relaxes the current-password requirement for accounts that
// provably have none — the caller is already holding a valid session for that
// exact account, so no new authority is granted.
package identity_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("Initial password for a passwordless (OIDC-provisioned) account", func() {
	postPassword := func(e http.Handler, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/password",
			bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("lets the account set one, so 'set a password first' stops being a dead end", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		apiroute.NoContent(e, http.MethodDelete,
			"/api/v1/users/current/identities/:provider", hz.H.Identity.UnlinkIdentity)

		ctx := context.Background()
		user, err := hz.DB.ProvisionOIDCUser(ctx, "oidc_nopw_g", auth.OIDCProviderName,
			"sub-oidc-nopw-g", "nopw@example.test", "full", []byte(`{}`))
		Expect(err).NotTo(HaveOccurred())
		hz.Cleanup(user)
		DeferCleanup(func() {
			_, _ = hz.DB.Pool.Exec(ctx, `DELETE FROM user_external_identities WHERE username=$1`, user)
		})

		token := auth.ToBase64(auth.NewRawToken())
		Expect(hz.DB.InsertAPIToken(ctx, user, token, "")).To(Succeed())

		// Preconditions: no usable password, and the unlink guard points at a
		// remedy — that remedy must exist.
		hasPw, err := hz.DB.HasUsablePassword(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(hasPw).To(BeFalse(), "precondition: OIDC-provisioned row must have no password")

		unlink := httptest.NewRequest(http.MethodDelete,
			"/api/v1/users/current/identities/"+auth.OIDCProviderName, nil)
		unlink.Header.Set("Authorization", "Basic "+token)
		unlinkRec := httptest.NewRecorder()
		e.ServeHTTP(unlinkRec, unlink)
		Expect(unlinkRec).To(testutil.HaveStatus(http.StatusConflict))
		Expect(unlinkRec.Body.String()).To(ContainSubstring("set a password first"))

		// A supplied current password can never work — there is nothing to
		// verify against — so an empty one has to be accepted or the account is
		// wedged.
		Expect(postPassword(e, token, `{"currentPassword":"anything-at-all","newPassword":"brandnew123"}`)).
			To(testutil.HaveStatus(http.StatusUnauthorized),
				"a passwordless row must never authenticate a supplied current password")

		rec := postPassword(e, token, `{"newPassword":"brandnew123"}`)
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent),
			"passwordless account still cannot set an initial password (%d) — UnlinkIdentity's "+
				"'set a password first' remedy does not exist, so the account can neither unlink "+
				"nor obtain local login: body=%s", rec.Code, rec.Body.String())

		// It really is a working local credential now.
		hasPw, err = hz.DB.HasUsablePassword(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(hasPw).To(BeTrue(), "password column still empty after a 204")
		Expect(verifyLoginG(e, user, "brandnew123")).To(Equal(http.StatusOK),
			"the newly set password does not authenticate at /auth/login")

		// And the guard it was blocked on now lets go.
		unlink2 := httptest.NewRequest(http.MethodDelete,
			"/api/v1/users/current/identities/"+auth.OIDCProviderName, nil)
		unlink2.Header.Set("Authorization", "Basic "+token)
		unlinkRec2 := httptest.NewRecorder()
		e.ServeHTTP(unlinkRec2, unlink2)
		Expect(unlinkRec2).To(testutil.HaveStatus(http.StatusNoContent),
			"unlink still refused after a password was set: body=%s", unlinkRec2.Body.String())
	})

	It("still requires the current password for an account that HAS one", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		pwUser, _, token := mintUserWithPasswordG(hz, "chpwd_initial_guard_g", "test1234")

		Expect(postPassword(e, token, `{"newPassword":"brandnew123"}`)).
			To(testutil.HaveStatus(http.StatusBadRequest),
				"omitting currentPassword must NOT become a password-reset bypass for accounts "+
					"that have a password")
		Expect(postPassword(e, token, `{"currentPassword":"","newPassword":"brandnew123"}`)).
			To(testutil.HaveStatus(http.StatusBadRequest))
		Expect(postPassword(e, token, `{"currentPassword":"wrong-one","newPassword":"brandnew123"}`)).
			To(testutil.HaveStatus(http.StatusUnauthorized))
		Expect(verifyLoginG(e, pwUser, "test1234")).To(Equal(http.StatusOK),
			"the original password stopped working after refused change attempts")
	})

	It("still enforces the password policy on the initial password", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		ctx := context.Background()

		user, err := hz.DB.ProvisionOIDCUser(ctx, "oidc_nopw_weak_g", auth.OIDCProviderName,
			"sub-oidc-nopw-weak-g", "weak@example.test", "full", []byte(`{}`))
		Expect(err).NotTo(HaveOccurred())
		hz.Cleanup(user)
		DeferCleanup(func() {
			_, _ = hz.DB.Pool.Exec(ctx, `DELETE FROM user_external_identities WHERE username=$1`, user)
		})
		token := auth.ToBase64(auth.NewRawToken())
		Expect(hz.DB.InsertAPIToken(ctx, user, token, "")).To(Succeed())

		Expect(postPassword(e, token, `{"newPassword":"abc"}`)).
			To(testutil.HaveStatus(http.StatusBadRequest))
		Expect(postPassword(e, token, `{"newPassword":""}`)).
			To(testutil.HaveStatus(http.StatusBadRequest))

		hasPw, err := hz.DB.HasUsablePassword(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(hasPw).To(BeFalse(), "a rejected weak password was persisted anyway")
	})
})
