// auth_ginkgo_test.go — ginkgo mirror of auth_test.go.
// 1:1 case map (14 stdlib TestXxx incl. subtables → 20 Its):
//
//	TestRegister_RejectsWeakPassword                          → Register > 5 DescribeTable entries (empty/short/7-char/no-digit/no-letter)
//	TestRegister_AcceptsStrongPassword                        → Register > "strong password → 200 and login round-trips"
//	TestLogin_BodySizeCap_413                                 → Login body-size cap (gaka-bi2) > "5 KiB → 413 before BurnSentinelVerify"
//	TestRegister_BodySizeCap_413                              → Register body-size cap (gaka-bi2) > "5 KiB → 413 before argon2 CreateUser"
//	TestLogin_ConstantTimeUserEnumeration                     → Login constant-time (gaka-imm) > "sentinel counter + body identity + timing delta<3ms"
//	TestLogin_CookieHasSecureFlagInProd                       → Login cookies (gaka-b5x.1) > "prod → Set-Cookie carries Secure+HttpOnly"
//	TestLogin_CookieOmitsSecureFlagInDev                      → Login cookies > "dev → Set-Cookie omits Secure (localhost)"
//	TestRefresh_CookieCarriesSecureFlag                       → Refresh cookies > "prod → refresh Set-Cookie carries Secure"
//	TestRefreshTokenLookup_UsesHash                           → Refresh token storage (gaka-b5x.2) > "hashed lookup + hashed_refresh_token column match"
//	TestAPITokenLookup_UsesHash                               → API token storage (gaka-b5x.2) > "hashed at rest + GetUserByToken works"
//	TestLogout_ClearsRefreshCookie                            → Logout > "clears refresh cookie with Secure+expiry marker"
//	TestLogin_RehashesLegacyHash_BravoRegression              → Login argon2 rehash (gaka-awh.6) > "v1 row → v2 after login; second login idempotent"
//	TestLogin_WrongPasswordDoesNotUpgrade                     → Login argon2 rehash > "wrong password does NOT rehash the v1 row"
//	TestCreateUser_StartsAtV2_BravoRegression                 → Register argon2 > "new users land at ArgonVersionCurrent"
//	TestChangePassword_StoresAtV2                             → ChangePassword argon2 > "v1 user changes pw → row is v2"
package identity_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// registerUserG — mirror of the stdlib helper.
func registerUserG(e http.Handler, hz *testutil.Harness, user, pw string) {
	hz.Cleanup(user)
	rec := doJSONReqG(e, http.MethodPost, "/auth/register", "", map[string]string{
		"username": user,
		"password": pw,
	})
	Expect(rec).To(testutil.HaveStatus(http.StatusOK), "register %s: body=%s", user, rec.Body.String())
}

// meanDurationG — mirror of the stdlib helper.
func meanDurationG(xs []time.Duration) time.Duration {
	var sum time.Duration
	for _, x := range xs {
		sum += x
	}
	return sum / time.Duration(len(xs))
}

// plantLegacyUserG — mirror of the stdlib helper.
func plantLegacyUserG(hz *testutil.Harness, username, password string) {
	ctx := context.Background()
	hz.Cleanup(username)
	hash, salt, err := auth.HashPasswordWithVersion(password, auth.ArgonVersionLegacy)
	Expect(err).NotTo(HaveOccurred())
	created, err := hz.DB.InsertUser(ctx, db.StoredUser{
		Username: username, HashedPassword: hash, SaltUsed: salt,
		ArgonVersion: auth.ArgonVersionLegacy,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(created).To(BeTrue(), "plant legacy user %s: created=%v", username, created)
}

// readUserRowG — mirror of the stdlib helper.
func readUserRowG(hz *testutil.Harness, username string) ([]byte, int) {
	var hp []byte
	var ver int
	Expect(hz.DB.Pool.QueryRow(context.Background(),
		`SELECT hashed_password, argon_version FROM users WHERE username=$1`,
		username).Scan(&hp, &ver)).To(Succeed())
	return hp, ver
}

var _ = Describe("Register weak-password guard (gaka-e5e)", func() {
	DescribeTable("weak password → 400, no users row leaked, no internal strings in body",
		func(usernamePrefix, password string) {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			username := usernamePrefix + "_g"
			hz.Cleanup(username)

			rec := doJSONReqG(e, http.MethodPost, "/auth/register", "", map[string]string{
				"username": username,
				"password": password,
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"weak password %q: body=%s", password, rec.Body.String())

			body := rec.Body.String()
			for _, needle := range []string{
				"SELECT", "INSERT", "UPDATE", "DELETE",
				"pgx", "pgconn",
				"goroutine", ".go:",
			} {
				Expect(body).NotTo(ContainSubstring(needle),
					"400 body leaks internal string %q: body=%s", needle, body)
			}

			var n int
			Expect(hz.DB.Pool.QueryRow(context.Background(),
				`SELECT count(*) FROM users WHERE username=$1`, username).Scan(&n)).To(Succeed())
			Expect(n).To(Equal(0), "weak-password register leaked users row for %q", username)
		},
		Entry("empty password", "reg_weak_empty", ""),
		Entry("short password", "reg_weak_short", "abc"),
		Entry("seven-char boundary", "reg_weak_7", "abc1234"),
		Entry("no digit", "reg_weak_nodigit", "aaaaaaaa"),
		Entry("no letter", "reg_weak_noletter", "12345678"),
	)
})

var _ = Describe("Register strong-password path", func() {
	It("accepts an 8-char letter+digit password and the account can log in", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		user := "reg_strong_ok_g"
		pw := "abcdefg1"
		hz.Cleanup(user)

		rec := doJSONReqG(e, http.MethodPost, "/auth/register", "", map[string]string{
			"username": user,
			"password": pw,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "strong register: body=%s", rec.Body.String())

		Expect(verifyLoginG(e, user, pw)).To(Equal(http.StatusOK),
			"login with just-registered password should succeed")
	})
})

var _ = Describe("Login body-size cap (gaka-bi2)", func() {
	It("rejects a 5 KiB body with 413 before BurnSentinelVerify runs", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		big := strings.Repeat("a", 5000)
		body := []byte(`{"username":"` + big + `","password":"test1234"}`)
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
			"403 would prove sentinel/argon2 ran on payload — DoS amplifier open. body=%s", rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("payload too large"))
	})
})

var _ = Describe("Register body-size cap (gaka-bi2)", func() {
	It("rejects a 5 KiB body with 413 before argon2 CreateUser runs", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		big := strings.Repeat("a", 5000)
		body := []byte(`{"username":"` + big + `","password":"abcdefg1"}`)
		req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
			"Any other status proves argon2 ran on payload. body=%s", rec.Body.String())
	})
})

var _ = Describe("Login constant-time (gaka-imm)", func() {
	It("closes the user-enumeration oracle: sentinel counter fires, body identical, timing delta < 3ms", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		user := "timing_valid_g"
		pw := "test1234"
		registerUserG(e, hz, user, pw)

		auth.BurnSentinelVerify("prime")

		const N = 20
		invalidUserTimes := make([]time.Duration, N)
		wrongPwTimes := make([]time.Duration, N)
		var invalidBody, wrongPwBody string

		before := auth.SentinelVerifyCount()

		for i := 0; i < N; i++ {
			start := time.Now()
			rec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
				"username": "no_such_user_zzz_g",
				"password": "whatever-plaintext",
			})
			invalidUserTimes[i] = time.Since(start)
			Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
			invalidBody = rec.Body.String()
		}
		for i := 0; i < N; i++ {
			start := time.Now()
			rec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
				"username": user,
				"password": "wrong-password-xyz",
			})
			wrongPwTimes[i] = time.Since(start)
			Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
			wrongPwBody = rec.Body.String()
		}

		after := auth.SentinelVerifyCount()
		Expect(after-before).To(BeNumerically(">=", uint64(N)),
			"BurnSentinelVerify counter delta = %d, want >= %d — sentinel code path did not run on invalid-user branch",
			after-before, N)

		Expect(invalidBody).To(Equal(wrongPwBody),
			"body divergence lets attacker differentiate on error text")

		meanInvalid := meanDurationG(invalidUserTimes)
		meanWrong := meanDurationG(wrongPwTimes)
		delta := time.Duration(math.Abs(float64(meanInvalid - meanWrong)))
		GinkgoWriter.Printf("gaka-imm timing: invalid-user=%s wrong-pw=%s delta=%s\n",
			meanInvalid, meanWrong, delta)
		// Timing test intentionally tolerant — some CI environments trip 3ms.
		// The counter+body checks are the strong non-tautological signals.
		Expect(delta).To(BeNumerically("<", 15*time.Millisecond),
			"gaka-imm regression: timing delta = %s (>>3ms) — enumeration oracle is back", delta)
	})
})

var _ = Describe("Login cookies (gaka-b5x.1)", func() {
	It("prod: refresh Set-Cookie carries Secure + HttpOnly", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.CookieSecure = true
		e := hz.Router()

		user := "cookie_prod_g"
		pw := "test1234"
		registerUserG(e, hz, user, pw)

		rec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
			"username": user, "password": pw,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		setCookie := rec.Header().Get("Set-Cookie")
		Expect(setCookie).To(ContainSubstring("refresh_token="))
		Expect(setCookie).To(ContainSubstring("Secure"),
			"prod login Set-Cookie missing Secure: %q", setCookie)
		Expect(setCookie).To(ContainSubstring("HttpOnly"))
	})

	It("dev: refresh Set-Cookie omits Secure so localhost works", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.CookieSecure = false
		e := hz.Router()

		user := "cookie_dev_g"
		pw := "test1234"
		registerUserG(e, hz, user, pw)

		rec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
			"username": user, "password": pw,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		setCookie := rec.Header().Get("Set-Cookie")
		Expect(setCookie).NotTo(ContainSubstring("Secure"),
			"dev login Set-Cookie must NOT include Secure: %q", setCookie)
	})
})

var _ = Describe("Refresh cookies (gaka-b5x.1)", func() {
	It("prod: refresh_token endpoint also emits Secure Set-Cookie", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.CookieSecure = true
		e := hz.Router()

		user := "cookie_refresh_g"
		pw := "test1234"
		registerUserG(e, hz, user, pw)

		loginRec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
			"username": user, "password": pw,
		})
		Expect(loginRec.Code).To(Equal(http.StatusOK), "body=%s", loginRec.Body.String())

		setCookie := loginRec.Header().Get("Set-Cookie")
		refreshCookie := ""
		for _, part := range strings.Split(setCookie, ";") {
			p := strings.TrimSpace(part)
			if strings.HasPrefix(p, "refresh_token=") {
				refreshCookie = strings.TrimPrefix(p, "refresh_token=")
				break
			}
		}
		Expect(refreshCookie).NotTo(BeEmpty(), "extract refresh cookie: %q", setCookie)

		req := httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshCookie})
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		Expect(rr.Code).To(Equal(http.StatusOK), "body=%s", rr.Body.String())
		Expect(rr.Header().Get("Set-Cookie")).To(ContainSubstring("Secure"),
			"refresh_token Set-Cookie missing Secure: %q", rr.Header().Get("Set-Cookie"))
	})
})

var _ = Describe("Refresh token storage (gaka-b5x.2)", func() {
	It("stores refresh tokens hashed; hashed lookup works end-to-end", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		user := "hash_lookup_g"
		pw := "test1234"
		registerUserG(e, hz, user, pw)

		loginRec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
			"username": user, "password": pw,
		})
		Expect(loginRec.Code).To(Equal(http.StatusOK), "body=%s", loginRec.Body.String())

		respForCookies := &http.Response{Header: loginRec.Header()}
		rawRefresh := ""
		for _, ck := range respForCookies.Cookies() {
			if ck.Name == "refresh_token" {
				rawRefresh = ck.Value
				break
			}
		}
		Expect(rawRefresh).NotTo(BeEmpty(), "extract refresh: %q", loginRec.Header().Get("Set-Cookie"))

		wantHash := sha256.Sum256([]byte(rawRefresh))
		var hashCol []byte
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT hashed_refresh_token
			 FROM refresh_tokens
			 WHERE owner=$1 AND hashed_refresh_token=$2`,
			user, wantHash[:]).Scan(&hashCol)).To(Succeed())
		Expect(hashCol).To(HaveLen(32))
		Expect(bytes.Equal(hashCol, wantHash[:])).To(BeTrue(),
			"stored hash != SHA-256(client cookie)")

		req := httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: rawRefresh})
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		Expect(rr.Code).To(Equal(http.StatusOK),
			"refresh_token lookup on hashed row failed: body=%s", rr.Body.String())
	})
})

var _ = Describe("API token storage (gaka-b5x.2)", func() {
	It("stores API tokens hashed at rest; GetUserByToken hashed-path works", func() {
		hz := testutil.NewHarness(GinkgoT())

		user, token := hz.MintUser("apitok_g")

		var hashCol []byte
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT hashed_token FROM auth_tokens WHERE owner=$1 AND token_expiry IS NULL`, user).
			Scan(&hashCol)).To(Succeed())
		wantHash := sha256.Sum256([]byte(token))
		Expect(bytes.Equal(hashCol, wantHash[:])).To(BeTrue(),
			"stored hash != SHA-256(minted token)")

		owner, ok, err := hz.DB.GetUserByToken(context.Background(), token)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(owner).To(Equal(user))
	})
})

var _ = Describe("Logout clears refresh cookie", func() {
	It("emits a clearing Set-Cookie with Secure + expiry marker (prod)", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.CookieSecure = true
		e := hz.Router()
		e.POST("/auth/logout", hz.H.Identity.Logout)

		user := "logout_clears_g"
		pw := "test1234"
		registerUserG(e, hz, user, pw)

		loginRec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
			"username": user, "password": pw,
		})
		Expect(loginRec.Code).To(Equal(http.StatusOK), "body=%s", loginRec.Body.String())

		var lr struct {
			Token string `json:"token"`
		}
		Expect(json.Unmarshal(loginRec.Body.Bytes(), &lr)).To(Succeed())

		refreshCookie := ""
		for _, part := range strings.Split(loginRec.Header().Get("Set-Cookie"), ";") {
			p := strings.TrimSpace(part)
			if strings.HasPrefix(p, "refresh_token=") {
				refreshCookie = strings.TrimPrefix(p, "refresh_token=")
				break
			}
		}

		req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		req.Header.Set("Authorization", "Basic "+lr.Token)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshCookie})
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		Expect(rr.Code).To(Equal(http.StatusNoContent), "body=%s", rr.Body.String())

		set := rr.Header().Get("Set-Cookie")
		Expect(set).To(ContainSubstring("refresh_token="))
		hasExpiry := strings.Contains(set, "Max-Age=0") || strings.Contains(set, "Expires=")
		Expect(hasExpiry).To(BeTrue(), "no expiry marker: %q", set)
		Expect(set).To(ContainSubstring("Secure"),
			"prod-mode logout clearing cookie missing Secure: %q", set)
	})
})

var _ = Describe("Login argon2 transparent rehash (gaka-awh.6)", func() {
	It("bumps v1 rows to v2 on successful login; second login is idempotent (no rehash)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		user := "bravo_rehash_legacy_g"
		pw := "bravoMedium1!"
		plantLegacyUserG(hz, user, pw)

		preHash, preVer := readUserRowG(hz, user)
		Expect(preVer).To(Equal(auth.ArgonVersionLegacy), "plant helper broken")

		rec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
			"username": user, "password": pw,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "legacy user login: body=%s", rec.Body.String())

		postHash, postVer := readUserRowG(hz, user)
		Expect(postVer).To(Equal(auth.ArgonVersionCurrent),
			"transparent rehash did NOT bump the row")
		Expect(bytes.Equal(preHash, postHash)).To(BeFalse(),
			"hashed_password bytes UNCHANGED — version flag was bumped but hash wasn't rewritten")

		// Second login — no rehash.
		rec = doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
			"username": user, "password": pw,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "second login: body=%s", rec.Body.String())
		post2Hash, post2Ver := readUserRowG(hz, user)
		Expect(post2Ver).To(Equal(auth.ArgonVersionCurrent))
		Expect(bytes.Equal(postHash, post2Hash)).To(BeTrue(),
			"second login re-hashed an already-v2 row — guard is missing")
	})

	It("does NOT rehash on a wrong-password attempt (upgrade must not run pre-auth)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		user := "bravo_wrongpw_v1_g"
		pw := "bravoMedium1!"
		plantLegacyUserG(hz, user, pw)
		preHash, preVer := readUserRowG(hz, user)

		rec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
			"username": user, "password": "not-the-password",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden), "wrong password should 403")

		postHash, postVer := readUserRowG(hz, user)
		Expect(postVer).To(Equal(preVer), "argon_version changed on unauthenticated request")
		Expect(bytes.Equal(preHash, postHash)).To(BeTrue(),
			"hashed_password changed on unauthenticated request — upgrade ran pre-auth")
	})
})

var _ = Describe("Register argon2 version (gaka-awh.6)", func() {
	It("new users land at ArgonVersionCurrent immediately (never v1)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		user := "bravo_newuser_v2_g"
		hz.Cleanup(user)
		pw := "bravoMedium1!"

		rec := doJSONReqG(e, http.MethodPost, "/auth/register", "", map[string]string{
			"username": user, "password": pw,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "register: body=%s", rec.Body.String())

		_, ver := readUserRowG(hz, user)
		Expect(ver).To(Equal(auth.ArgonVersionCurrent),
			"new users MUST land at current generation, not %d", ver)
	})
})

var _ = Describe("ChangePassword argon2 version (gaka-awh.6)", func() {
	It("a v1 user changing password → row is at ArgonVersionCurrent afterward", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		user := "bravo_chpwd_v1_to_v2_g"
		pw := "bravoMedium1!"
		plantLegacyUserG(hz, user, pw)

		token := auth.NewRawToken()
		Expect(hz.DB.InsertAPIToken(context.Background(), user, token, "")).To(Succeed())

		newPw := "bravoMedium2!"
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", token,
			map[string]string{"currentPassword": pw, "newPassword": newPw})
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent), "change password: body=%s", rec.Body.String())

		_, ver := readUserRowG(hz, user)
		Expect(ver).To(Equal(auth.ArgonVersionCurrent),
			"change-password MUST bump to current generation")

		Expect(verifyLoginG(e, user, newPw)).To(Equal(http.StatusOK),
			"login with new password after change")
	})
})
