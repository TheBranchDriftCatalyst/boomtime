package handler_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// mintUserWithPassword provisions a user whose plaintext password is known,
// then mints an API token. Returns (username, plaintextPassword, apiToken).
func mintUserWithPassword(t *testing.T, hz *testutil.Harness, prefix, password string) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	// Use MintUser's username generator implicitly: we need a unique name.
	// Simplest path is to piggyback on MintUser and then rewrite the hash.
	user, token := hz.MintUser(prefix)
	hash, salt, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := hz.DB.UpdatePassword(ctx, user, hash, salt); err != nil {
		t.Fatalf("update password: %v", err)
	}
	return user, password, token
}

// doJSON issues a JSON request against the harness router.
func doJSONReq(t *testing.T, e http.Handler, method, target, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// verifyLogin exercises /auth/login for a user+password pair.
func verifyLogin(t *testing.T, e http.Handler, user, password string) int {
	t.Helper()
	rec := doJSONReq(t, e, http.MethodPost, "/auth/login", "", map[string]string{
		"username": user, "password": password,
	})
	return rec.Code
}

// routerWithChangePassword returns a router with /auth/login + the
// change-password route registered. The Harness.Router() intentionally omits
// misc routes to stay minimal, so we register what we need here.
func routerWithChangePassword(hz *testutil.Harness) http.Handler {
	e := hz.Router()
	e.POST("/api/v1/users/current/password", hz.H.ChangePassword)
	return e
}

// TestChangePasswordBodySizeCap_413: gaka-bi2. With a real authed user and a
// 5 KiB body (> BodyLimitSmall = 4 KiB), the response MUST be 413 Payload Too
// Large — NOT 401 (wrong password), 400 (invalid body), or 500.
//
// This is the marquee non-tautological integration assertion: the ONLY code
// path that produces a 401 here is the auth.VerifyPassword call that runs on
// the parsed body. If we get 401 instead of 413, the cap failed and argon2
// ran on the oversize input (the exact DoS amplifier this fix closes).
//
// Deleting the http.MaxBytesReader line in BindJSONWithLimit fails this test:
// the handler would bind successfully (5 KiB is well within Echo defaults),
// call VerifyPassword("aaa...", stored_hash, salt), the compare would fail,
// and the response would be 401 — a clean regression signal.
func TestChangePasswordBodySizeCap_413(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithChangePassword(hz)
	_, _, token := mintUserWithPassword(t, hz, "chpwd_413", "test1234")

	// 5 KiB currentPassword string — well over the 4 KiB Small cap.
	big := strings.Repeat("a", 5000)
	body := []byte(`{"currentPassword":"` + big + `","newPassword":"test5678"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize body: status %d (want 413). 401 would prove argon2 ran on the payload — the exact DoS this fix closes. body=%s",
			rec.Code, rec.Body.String())
	}
	// Envelope check — FE distinguishes 413 from generic 400 via this shape.
	if !strings.Contains(rec.Body.String(), "payload too large") {
		t.Errorf("body missing sentinel 'payload too large': %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "limit=") {
		t.Errorf("body missing limit hint: %s", rec.Body.String())
	}
}

// TestChangePasswordUnderCapStillWorks_204: the cap must not break normal
// requests. A tiny valid body still succeeds.
func TestChangePasswordUnderCapStillWorks_204(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithChangePassword(hz)
	_, _, token := mintUserWithPassword(t, hz, "chpwd_under", "test1234")

	rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
		"currentPassword": "test1234",
		"newPassword":     "test5678",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("under-cap body: status %d, want 204 body=%s", rec.Code, rec.Body.String())
	}
}

// TestChangePasswordWrongCurrentPassword: wrong current-password → 401.
func TestChangePasswordWrongCurrentPassword(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithChangePassword(hz)
	_, _, token := mintUserWithPassword(t, hz, "chpwd_wrong", "test1234")

	rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
		"currentPassword": "not-my-password",
		"newPassword":     "test5678",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password: status %d, want 401 body=%s", rec.Code, rec.Body.String())
	}
}

// TestChangePasswordWeakNewPassword: weak new password → 400.
func TestChangePasswordWeakNewPassword(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithChangePassword(hz)
	_, _, token := mintUserWithPassword(t, hz, "chpwd_weak", "test1234")

	// Short (< 8) — 400.
	rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
		"currentPassword": "test1234",
		"newPassword":     "ab1",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("short password: status %d, want 400", rec.Code)
	}

	// Long but letters-only — 400.
	rec = doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
		"currentPassword": "test1234",
		"newPassword":     "abcdefghij",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("letters-only password: status %d, want 400", rec.Code)
	}

	// Long but digits-only — 400.
	rec = doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
		"currentPassword": "test1234",
		"newPassword":     "1234567890",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("digits-only password: status %d, want 400", rec.Code)
	}
}

// TestChangePassword_UsesSharedValidator_Gaka0guRegression is the pinning
// test for gaka-0gu — ChangePassword MUST delegate its new-password policy
// to auth.ValidatePassword instead of running an inline check.
//
// If this fails, ChangePassword regressed and is no longer delegating to
// auth.ValidatePassword — the byte-vs-rune fix from gaka-e5e is being
// bypassed.
//
// The marquee assertion is `日本1a`: 4 runes, 8 bytes. The old inline check
// used len() (byte count) and would have ACCEPTED it as "long enough"; the
// shared rune-counted validator REJECTS it with ErrPasswordTooShort. This
// single assertion proves BOTH (a) delegation happened, and (b) the byte-vs-
// rune fix is intact at ChangePassword.
//
// Policy edge cases (empty / no digit / no letter) are unit-tested at the
// auth layer already (see internal/auth/password_policy_test.go); those are
// intentionally NOT duplicated here. The one non-marquee case retained is
// a `no-digit` acceptance-path negative — it pins that the sentinel's
// Error() text is what surfaces in the 400 response body, so the FE sees
// the same string the shared validator names.
//
// Anti-tautology proof captured at implementation time (gaka-0gu):
//
//	// with the shared call restored:
//	--- PASS: TestChangePassword_UsesSharedValidator_Gaka0guRegression
//	    marquee_multibyte_reject: PASS
//
//	// with `if len(req.NewPassword) < 8` re-added inline (buggy byte check):
//	--- FAIL: TestChangePassword_UsesSharedValidator_Gaka0guRegression
//	    marquee_multibyte_reject: status 204, want 400 — 日本1a accepted at 8 bytes
func TestChangePassword_UsesSharedValidator_Gaka0guRegression(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithChangePassword(hz)

	t.Run("marquee_multibyte_reject", func(t *testing.T) {
		// 日本1a — 4 runes, 8 bytes. Byte-based len() accepts; rune-based
		// utf8.RuneCountInString rejects. If this passes AS 400, delegation
		// is intact. If it flips to 204, ChangePassword re-added an inline
		// byte-based check and the gaka-e5e fix is bypassed at this route.
		_, _, token := mintUserWithPassword(t, hz, "chpwd_marquee", "test1234")
		rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
			"currentPassword": "test1234",
			"newPassword":     "日本1a",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("multibyte 日本1a (4 runes / 8 bytes): status %d, want 400. "+
				"A 204 here means ChangePassword regressed to an inline byte-based "+
				"check and the gaka-e5e rune-count fix is being bypassed. body=%s",
				rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), auth.ErrPasswordTooShort.Error()) {
			t.Errorf("body missing shared sentinel text %q; got %s. "+
				"Delegation may still be broken — a locally-crafted 400 message "+
				"would not carry the shared validator's exact string.",
				auth.ErrPasswordTooShort.Error(), rec.Body.String())
		}
	})

	t.Run("sentinel_text_surfaces_no_digit", func(t *testing.T) {
		// Not re-testing the algorithm (that's unit-tested at the auth layer);
		// this pins that the sentinel's Error() TEXT is what the 400 body
		// carries — i.e., the handler emits err.Error() from the shared
		// package rather than a locally-worded string.
		_, _, token := mintUserWithPassword(t, hz, "chpwd_sentinel", "test1234")
		rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
			"currentPassword": "test1234",
			"newPassword":     "abcdefgh",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("no-digit password: status %d, want 400 body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), auth.ErrPasswordNoDigit.Error()) {
			t.Errorf("body missing shared ErrPasswordNoDigit text %q; got %s",
				auth.ErrPasswordNoDigit.Error(), rec.Body.String())
		}
	})
}

// TestChangePasswordHappyPath: 200/204, old password no longer works, new
// password does, refresh tokens for the owner are revoked.
func TestChangePasswordHappyPath(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithChangePassword(hz)
	user, oldPassword, token := mintUserWithPassword(t, hz, "chpwd_happy", "test1234")

	// Plant a refresh token for the owner so we can prove revocation happened.
	if err := hz.DB.CreateAccessTokens(context.Background(), db.TokenData{
		Owner: user, Token: "rev-tok-" + user, RefreshToken: "rev-refresh-" + user,
	}, 24); err != nil {
		t.Fatalf("plant refresh token: %v", err)
	}

	// Confirm old password logs in.
	if code := verifyLogin(t, e, user, oldPassword); code != http.StatusOK {
		t.Fatalf("old password login: status %d, want 200", code)
	}

	newPassword := "test5678"
	rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
		"currentPassword": oldPassword,
		"newPassword":     newPassword,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("happy path: status %d, want 204 body=%s", rec.Code, rec.Body.String())
	}

	// Refresh tokens for the owner should be gone (revoked) IMMEDIATELY after
	// the change — before any subsequent login that would mint fresh ones.
	var n int
	if err := hz.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM refresh_tokens WHERE owner=$1`, user).Scan(&n); err != nil {
		t.Fatalf("count refresh tokens: %v", err)
	}
	if n != 0 {
		t.Errorf("refresh tokens after change: got %d, want 0", n)
	}

	// Old password no longer works.
	if code := verifyLogin(t, e, user, oldPassword); code == http.StatusOK {
		t.Errorf("old password still works after change: got %d", code)
	}
	// New password does work.
	if code := verifyLogin(t, e, user, newPassword); code != http.StatusOK {
		t.Errorf("new password login: status %d, want 200", code)
	}
}

// mintAccessTokenPair provisions a fresh (access, refresh) token pair for
// an existing user by directly inserting into the DB — mirrors what
// /auth/login does but skips the password-verify roundtrip so tests can
// simulate "user already logged in from another browser".
func mintAccessTokenPair(t *testing.T, hz *testutil.Harness, user string) (accessToken, refreshToken string) {
	t.Helper()
	accessToken = auth.ToBase64(auth.NewRawToken())
	refreshToken = auth.ToBase64(auth.NewRawToken())
	if err := hz.DB.CreateAccessTokens(context.Background(), db.TokenData{
		Owner: user, Token: accessToken, RefreshToken: refreshToken,
	}, 24); err != nil {
		t.Fatalf("mint access token pair: %v", err)
	}
	return accessToken, refreshToken
}

// TestChangePassword_RevokesOtherAccessTokens is the regression test for
// gaka-abo: after a password rotation, OTHER browsers' still-live 30-minute
// access tokens MUST be dead immediately (not merely expiring naturally over
// the next half hour), while the caller's OWN access token — the one that
// authenticated the change-password request — MUST survive.
func TestChangePassword_RevokesOtherAccessTokens(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithChangePassword(hz)
	// Also register an authed endpoint we can probe with the old token — Stats
	// runs h.resolveUser, so a revoked access token produces the same 403
	// InvalidToken response the browser would see on any authed request.
	// (The task copy says "401"; the system's actual invalid-token response
	// is 403 — the security guarantee is the same either way: !=200.)
	user, password, browser1Token := mintUserWithPassword(t, hz, "chpwd_revoke", "test1234")

	// Simulate a second browser logged in as the same user.
	browser2Token, browser2Refresh := mintAccessTokenPair(t, hz, user)

	// Sanity: BEFORE the password change, browser-2's access token authenticates.
	pre := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/stats", browser2Token, nil)
	if pre.Code == http.StatusForbidden || pre.Code == http.StatusUnauthorized {
		t.Fatalf("browser-2 access token was not accepted BEFORE change: %d body=%s", pre.Code, pre.Body.String())
	}

	// Browser-1 changes the password. Its own access token authenticates the request.
	rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/password", browser1Token, map[string]string{
		"currentPassword": password,
		"newPassword":     "test5678",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change password: status %d body=%s", rec.Code, rec.Body.String())
	}

	// GUARANTEE 1: browser-2's OLD access token is dead — the whole point of the fix.
	post := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/stats", browser2Token, nil)
	if post.Code == http.StatusOK {
		t.Errorf("browser-2 access token still works AFTER password change (revoke gap): got %d", post.Code)
	}

	// GUARANTEE 2: browser-1's OWN access token still works — the caller must
	// not be force-logged-out by the very request they made.
	self := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/stats", browser1Token, nil)
	if self.Code == http.StatusForbidden || self.Code == http.StatusUnauthorized {
		t.Errorf("browser-1 own access token was revoked mid-request: got %d body=%s", self.Code, self.Body.String())
	}

	// GUARANTEE 3: browser-2's refresh token cannot mint a fresh access token.
	// /auth/refresh_token reads the refresh_token cookie via resolveOwnerFromCookie.
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: browser2Refresh})
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, req)
	if rr.Code == http.StatusOK {
		t.Errorf("browser-2 refresh token still mints access tokens after password change: got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestChangePassword_AtomicOnDBError proves the UPDATE+revoke pair is a single
// transaction. Uses db.SetChangePasswordFaultInjector to force the revoke step
// to fail AFTER the users row UPDATE has run inside the tx. On rollback the
// password MUST remain the old value (else the "process dies mid-way" gap
// Charlie flagged as LOW still exists).
func TestChangePassword_AtomicOnDBError(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithChangePassword(hz)
	user, oldPassword, token := mintUserWithPassword(t, hz, "chpwd_atomic", "test1234")

	// Also plant a refresh token so we can check it survives the rollback.
	_, refreshBefore := mintAccessTokenPair(t, hz, user)

	// Install the fault-injection hook that fires INSIDE the tx after the
	// users UPDATE runs but before the DELETEs; clear it before returning so
	// no later test is affected.
	forced := errors.New("forced-mid-tx-failure")
	db.SetChangePasswordFaultInjector(func() error { return forced })
	t.Cleanup(func() { db.SetChangePasswordFaultInjector(nil) })

	rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
		"currentPassword": oldPassword,
		"newPassword":     "shouldnt-persist-9",
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("forced-fault expected 500, got %d body=%s", rec.Code, rec.Body.String())
	}

	// GUARANTEE 1: password unchanged (UPDATE inside tx rolled back).
	if code := verifyLogin(t, e, user, oldPassword); code != http.StatusOK {
		t.Errorf("old password should still work after rolled-back change: got %d", code)
	}
	if code := verifyLogin(t, e, user, "shouldnt-persist-9"); code == http.StatusOK {
		t.Errorf("new password should NOT work after rolled-back change")
	}

	// GUARANTEE 2: the planted refresh token still exists (revoke rolled back too).
	// Post-v31 hashed-only lookup — the raw refresh_token column is dropped.
	refreshHashBefore := sha256.Sum256([]byte(refreshBefore))
	var n int
	if err := hz.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM refresh_tokens
		 WHERE owner=$1 AND hashed_refresh_token=$2`,
		user, refreshHashBefore[:]).Scan(&n); err != nil {
		t.Fatalf("count refresh tokens: %v", err)
	}
	if n != 1 {
		t.Errorf("planted refresh token: got count=%d, want 1 (rollback should have preserved it)", n)
	}
}
