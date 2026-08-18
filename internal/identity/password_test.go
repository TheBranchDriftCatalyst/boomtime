// password_ginkgo_test.go — ginkgo mirror of password_test.go.
// 1:1 case map (7 stdlib TestXxx incl. subtests → 9 Its):
//
//	TestChangePasswordBodySizeCap_413                 → ChangePassword body-size cap > "5 KiB body → 413 (argon2 never runs)"
//	TestChangePasswordUnderCapStillWorks_204          → ChangePassword > "under-cap body still works (204)"
//	TestChangePasswordWrongCurrentPassword            → ChangePassword > "wrong current password → 401"
//	TestChangePasswordWeakNewPassword                 → ChangePassword > "weak new password → 400 (short/letters-only/digits-only)"
//	TestChangePassword_UsesSharedValidator_Gaka0guRegression
//	  → ChangePassword shared validator (gaka-0gu) > "multibyte reject (marquee)" + "sentinel text surfaces (no-digit)"
//	TestChangePasswordHappyPath                       → ChangePassword > "happy path: old dead, new works, refresh tokens revoked"
//	TestChangePassword_RevokesOtherAccessTokens       → ChangePassword revocation (gaka-abo) > "other access token revoked, own survives, other refresh dead"
//	TestChangePassword_AtomicOnDBError                → ChangePassword atomicity > "fault-injected tx rolls back password AND refresh-token revoke"
package identity_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// routerWithChangePasswordG — mirror of the stdlib helper.
// mintAccessTokenPairG — mirror of the stdlib helper.
func mintAccessTokenPairG(hz *testutil.Harness, user string) (accessToken, refreshToken string) {
	accessToken = auth.ToBase64(auth.NewRawToken())
	refreshToken = auth.ToBase64(auth.NewRawToken())
	Expect(hz.DB.CreateAccessTokens(context.Background(), db.TokenData{
		Owner: user, Token: accessToken, RefreshToken: refreshToken,
	}, 24)).To(Succeed())
	return accessToken, refreshToken
}

var _ = Describe("ChangePassword body-size cap (gaka-bi2)", func() {
	It("returns 413 on a 5 KiB body — argon2 never runs on the payload", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, _, token := mintUserWithPasswordG(hz, "chpwd_413_g", "test1234")

		big := strings.Repeat("a", 5000)
		body := []byte(`{"currentPassword":"` + big + `","newPassword":"test5678"}`)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/password", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
			"401 would prove argon2 ran on the payload — DoS amplifier not closed. body=%s",
			rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("payload too large"))
		Expect(rec.Body.String()).To(ContainSubstring("limit="))
	})
})

var _ = Describe("ChangePassword happy paths + validation", func() {
	It("an under-cap body works (204)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, _, token := mintUserWithPasswordG(hz, "chpwd_under_g", "test1234")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
			"currentPassword": "test1234",
			"newPassword":     "test5678",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent))
	})

	It("wrong current password → 401", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, _, token := mintUserWithPasswordG(hz, "chpwd_wrong_g", "test1234")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
			"currentPassword": "not-my-password",
			"newPassword":     "test5678",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusUnauthorized))
	})

	It("weak new password → 400 for short / letters-only / digits-only", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, _, token := mintUserWithPasswordG(hz, "chpwd_weak_g", "test1234")

		for _, bad := range []string{"ab1", "abcdefghij", "1234567890"} {
			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
				"currentPassword": "test1234",
				"newPassword":     bad,
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "weak %q: body=%s", bad, rec.Body.String())
		}
	})
})

var _ = Describe("ChangePassword shared validator (gaka-0gu regression)", func() {
	It("marquee multibyte reject: 日本1a (4 runes, 8 bytes) → 400 with shared sentinel text", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, _, token := mintUserWithPasswordG(hz, "chpwd_marquee_g", "test1234")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
			"currentPassword": "test1234",
			"newPassword":     "日本1a",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"A 204 here means ChangePassword regressed to inline byte-based check; body=%s", rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring(auth.ErrPasswordTooShort.Error()),
			"missing shared sentinel — delegation may be broken")
	})

	It("no-digit password surfaces the shared ErrPasswordNoDigit text", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, _, token := mintUserWithPasswordG(hz, "chpwd_sentinel_g", "test1234")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
			"currentPassword": "test1234",
			"newPassword":     "abcdefgh",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring(auth.ErrPasswordNoDigit.Error()))
	})
})

var _ = Describe("ChangePassword happy-path invariants", func() {
	It("204s, revokes ALL refresh tokens, and the old password stops working", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, oldPassword, token := mintUserWithPasswordG(hz, "chpwd_happy_g", "test1234")

		// Plant a refresh token so we can prove revocation happened.
		Expect(hz.DB.CreateAccessTokens(context.Background(), db.TokenData{
			Owner: user, Token: "rev-tok-" + user, RefreshToken: "rev-refresh-" + user,
		}, 24)).To(Succeed())

		Expect(verifyLoginG(e, user, oldPassword)).To(Equal(http.StatusOK), "old password baseline")

		newPassword := "test5678"
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
			"currentPassword": oldPassword,
			"newPassword":     newPassword,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent), "happy path: body=%s", rec.Body.String())

		var n int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM refresh_tokens WHERE owner=$1`, user).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(0), "refresh tokens after change")

		Expect(verifyLoginG(e, user, oldPassword)).NotTo(Equal(http.StatusOK),
			"old password still works after change")
		Expect(verifyLoginG(e, user, newPassword)).To(Equal(http.StatusOK),
			"new password should login")
	})
})

var _ = Describe("ChangePassword access-token revocation (gaka-abo)", func() {
	It("kills OTHER browsers' access + refresh tokens, keeps caller's OWN access token alive", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, password, browser1Token := mintUserWithPasswordG(hz, "chpwd_revoke_g", "test1234")

		browser2Token, browser2Refresh := mintAccessTokenPairG(hz, user)

		// BEFORE the change: browser-2's access token authenticates.
		pre := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/stats", browser2Token, nil)
		Expect(pre.Code).NotTo(Or(Equal(http.StatusForbidden), Equal(http.StatusUnauthorized)),
			"browser-2 access not accepted BEFORE change: %d body=%s", pre.Code, pre.Body.String())

		// Browser-1 changes the password.
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", browser1Token, map[string]string{
			"currentPassword": password,
			"newPassword":     "test5678",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent), "change password: body=%s", rec.Body.String())

		// GUARANTEE 1: browser-2's OLD access token is dead.
		post := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/stats", browser2Token, nil)
		Expect(post.Code).NotTo(Equal(http.StatusOK),
			"browser-2 access token still works AFTER password change (revoke gap): %d", post.Code)

		// GUARANTEE 2: browser-1's OWN access token still works.
		self := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/stats", browser1Token, nil)
		Expect(self.Code).NotTo(Or(Equal(http.StatusForbidden), Equal(http.StatusUnauthorized)),
			"browser-1 own access token revoked mid-request: %d body=%s", self.Code, self.Body.String())

		// GUARANTEE 3: browser-2's refresh token cannot mint a fresh access token.
		req := httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: browser2Refresh})
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		Expect(rr.Code).NotTo(Equal(http.StatusOK),
			"browser-2 refresh token still mints access tokens: %d body=%s", rr.Code, rr.Body.String())
	})
})

var _ = Describe("ChangePassword atomicity on DB error", func() {
	It("rolls back BOTH the password UPDATE and refresh-token revoke on mid-tx fault", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, oldPassword, token := mintUserWithPasswordG(hz, "chpwd_atomic_g", "test1234")

		_, refreshBefore := mintAccessTokenPairG(hz, user)

		forced := errors.New("forced-mid-tx-failure")
		db.SetChangePasswordFaultInjector(func() error { return forced })
		DeferCleanup(func() { db.SetChangePasswordFaultInjector(nil) })

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
			"currentPassword": oldPassword,
			"newPassword":     "shouldnt-persist-9",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError),
			"forced-fault expected 500: body=%s", rec.Body.String())

		// GUARANTEE 1: password unchanged.
		Expect(verifyLoginG(e, user, oldPassword)).To(Equal(http.StatusOK),
			"old password should still work after rolled-back change")
		Expect(verifyLoginG(e, user, "shouldnt-persist-9")).NotTo(Equal(http.StatusOK),
			"new password should NOT work after rolled-back change")

		// GUARANTEE 2: the planted refresh token still exists.
		refreshHashBefore := sha256.Sum256([]byte(refreshBefore))
		var n int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM refresh_tokens
			 WHERE owner=$1 AND hashed_refresh_token=$2`,
			user, refreshHashBefore[:]).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(1), "rollback should have preserved planted refresh token")
	})
})
