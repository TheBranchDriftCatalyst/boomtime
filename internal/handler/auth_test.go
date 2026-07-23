package handler_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// TestRegister_RejectsWeakPassword covers gaka-e5e: prior to the fix,
// POST /auth/register minted a working session for any password, including
// "" and "abc". Assert every rejection path returns 400 with a body that
// doesn't leak internal state (SQL fragments, stack frames), and that a
// policy-compliant password still succeeds end-to-end.
func TestRegister_RejectsWeakPassword(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()

	// Common uniqueness suffix so a re-run of the test doesn't collide with
	// a previously-inserted row that survived a partial cleanup.
	uniq := t.Name()
	_ = uniq // unused today but keeps a hook for future collision debugging

	weakCases := []struct {
		name     string
		username string
		password string
	}{
		{name: "empty password", username: "reg_weak_empty", password: ""},
		{name: "short password", username: "reg_weak_short", password: "abc"},
		{name: "seven-char boundary", username: "reg_weak_7", password: "abc1234"},
		{name: "no digit", username: "reg_weak_nodigit", password: "aaaaaaaa"},
		{name: "no letter", username: "reg_weak_noletter", password: "12345678"},
	}

	for _, tc := range weakCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Register cleanup even on 400 — the DB rows should NOT exist
			// (the reject happens before INSERT), but if the test ever
			// regresses and a row leaks through we want it removed so
			// re-runs stay reliable.
			hz.Cleanup(tc.username)

			rec := doJSONReq(t, e, http.MethodPost, "/auth/register", "", map[string]string{
				"username": tc.username,
				"password": tc.password,
			})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("weak password %q: status %d, want 400 body=%s", tc.password, rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			// The 400 body must NOT leak internal machinery. This is a
			// belt-and-suspenders check: the sentinel error strings are
			// hand-curated (no dynamic content), but if a future refactor
			// wraps the error in a way that pulls in DB drivers/stack
			// frames, we want that flagged here.
			for _, needle := range []string{
				"SELECT", "INSERT", "UPDATE", "DELETE", // SQL fragments
				"pgx", "pgconn", // driver names
				"goroutine", ".go:", // stack frames
			} {
				if strings.Contains(body, needle) {
					t.Errorf("weak password 400 body leaks internal string %q: body=%s", needle, body)
				}
			}
			// And the row must NOT exist in the DB — reject-before-insert.
			var n int
			if err := hz.DB.Pool.QueryRow(context.Background(),
				`SELECT count(*) FROM users WHERE username=$1`, tc.username).Scan(&n); err != nil {
				t.Fatalf("count users: %v", err)
			}
			if n != 0 {
				t.Errorf("weak-password register leaked a users row for %q (count=%d)", tc.username, n)
			}
		})
	}
}

// TestRegister_AcceptsStrongPassword: a policy-compliant password returns
// 200 (login response) and the user can subsequently log in with that
// password. Guards against a fix that overshoots and rejects valid inputs.
func TestRegister_AcceptsStrongPassword(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()

	user := "reg_strong_ok"
	pw := "abcdefg1" // 8 chars, letter + digit — minimum viable per policy
	hz.Cleanup(user)

	rec := doJSONReq(t, e, http.MethodPost, "/auth/register", "", map[string]string{
		"username": user,
		"password": pw,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("strong register: status %d, want 200 body=%s", rec.Code, rec.Body.String())
	}

	// And the returned session actually works — verify by logging in with
	// the same password, which is the round-trip the browser performs on
	// the next session.
	if code := verifyLogin(t, e, user, pw); code != http.StatusOK {
		t.Errorf("login with just-registered strong password: status %d, want 200", code)
	}
}

// TestLogin_BodySizeCap_413: gaka-bi2. Login MUST reject 5 KiB bodies with
// 413 before the argon2 verify runs. If the cap failed, we'd get 403 "Invalid
// credentials" — the constant-time verify path (BurnSentinelVerify or
// VerifyPassword) would have already spent ~10ms of CPU on the payload. 413
// is the ONLY signal that the DoS amplifier is closed.
//
// Non-tautological: deleting the http.MaxBytesReader line makes Login parse
// the 5 KiB body, call BurnSentinelVerify with the 5 KiB username, and return
// 403. This test would then fail with the exact regression signal.
func TestLogin_BodySizeCap_413(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()

	big := strings.Repeat("a", 5000)
	body := []byte(`{"username":"` + big + `","password":"test1234"}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize login: status %d (want 413). 403 would prove BurnSentinelVerify / argon2 ran on the payload — the DoS amplifier. body=%s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "payload too large") {
		t.Errorf("body missing sentinel: %s", rec.Body.String())
	}
}

// TestRegister_BodySizeCap_413: same as Login. Register runs
// auth.ValidatePassword + auth.CreateUser (which hashes with argon2) after
// Bind — a 413 here proves neither ran on the oversize payload.
func TestRegister_BodySizeCap_413(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()

	big := strings.Repeat("a", 5000)
	body := []byte(`{"username":"` + big + `","password":"abcdefg1"}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize register: status %d (want 413). Any other status would prove argon2 CreateUser ran on the payload. body=%s",
			rec.Code, rec.Body.String())
	}
}

// registerUser is a small helper for tests that need a live account whose
// plaintext password is known.
func registerUser(t *testing.T, e http.Handler, hz *testutil.Harness, user, pw string) {
	t.Helper()
	hz.Cleanup(user)
	rec := doJSONReq(t, e, http.MethodPost, "/auth/register", "", map[string]string{
		"username": user,
		"password": pw,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("register %s: status %d body=%s", user, rec.Code, rec.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// gaka-imm: constant-time login (Fix 1) — integration layer.
// Unit layer lives in internal/auth/auth_test.go
// (TestBurnSentinelVerify_Counter).
// ─────────────────────────────────────────────────────────────────────────────

// TestLogin_ConstantTimeUserEnumeration proves the "no such user" branch now
// spends the same ~10ms of argon2 CPU as the "wrong password" branch, closing
// gaka-imm. Two signals:
//  1. The sentinel-verify counter goes up on the invalid-user path (SPY
//     check — CANNOT pass if BurnSentinelVerify is deleted or bypassed).
//  2. The wall-clock mean delta over 20 iterations is < 3ms (TIMING check —
//     the direct regression signal).
//
// Bug caught: deleting `auth.BurnSentinelVerify(creds.Password)` from the
// Login handler makes signal (1) fail (counter delta = 0) AND signal (2)
// fail (invalid path ~1ms vs valid path ~10ms). Neither signal duplicates
// the other: the counter proves the code path, the timing proves the code
// path *did what it needed to*.
//
// Also asserts response-body identity so an attacker can't differentiate by
// error string either.
func TestLogin_ConstantTimeUserEnumeration(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()

	// Real user with a known password (so the valid-user-wrong-pw branch
	// hits VerifyPassword and burns ~10ms).
	user := "timing_valid"
	pw := "test1234"
	registerUser(t, e, hz, user, pw)

	// Prime the sentinel init so its ~10ms one-time hash+salt-derive cost
	// doesn't skew the first invalid-user timing sample.
	auth.BurnSentinelVerify("prime")

	const N = 20
	invalidUserTimes := make([]time.Duration, N)
	wrongPwTimes := make([]time.Duration, N)
	var invalidBody, wrongPwBody string

	// SIGNAL 1: sentinel-verify counter delta over N invalid-user probes.
	before := auth.SentinelVerifyCount()

	for i := 0; i < N; i++ {
		start := time.Now()
		rec := doJSONReq(t, e, http.MethodPost, "/auth/login", "", map[string]string{
			"username": "no_such_user_zzz",
			"password": "whatever-plaintext",
		})
		invalidUserTimes[i] = time.Since(start)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("invalid-user: status %d, want 403", rec.Code)
		}
		invalidBody = rec.Body.String()
	}
	for i := 0; i < N; i++ {
		start := time.Now()
		rec := doJSONReq(t, e, http.MethodPost, "/auth/login", "", map[string]string{
			"username": user,
			"password": "wrong-password-xyz",
		})
		wrongPwTimes[i] = time.Since(start)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("wrong-pw: status %d, want 403", rec.Code)
		}
		wrongPwBody = rec.Body.String()
	}

	after := auth.SentinelVerifyCount()
	if got := after - before; got < uint64(N) {
		t.Errorf("BurnSentinelVerify counter delta = %d, want >= %d — the sentinel code path did not run on the invalid-user branch",
			got, N)
	}

	// SIGNAL 2: response-body identity — an attacker MUST NOT be able to
	// differentiate on error text either.
	if invalidBody != wrongPwBody {
		t.Errorf("body divergence:\n  invalid-user: %s\n  wrong-pw    : %s", invalidBody, wrongPwBody)
	}

	// SIGNAL 3: wall-clock timing delta < 3ms.
	meanInvalid := mean(invalidUserTimes)
	meanWrong := mean(wrongPwTimes)
	delta := time.Duration(math.Abs(float64(meanInvalid - meanWrong)))
	t.Logf("gaka-imm timing: invalid-user mean=%s, wrong-pw mean=%s, delta=%s",
		meanInvalid, meanWrong, delta)
	if delta > 3*time.Millisecond {
		t.Errorf("gaka-imm regression: timing delta = %s > 3ms — user-enumeration oracle is back",
			delta)
	}
}

func mean(xs []time.Duration) time.Duration {
	var sum time.Duration
	for _, x := range xs {
		sum += x
	}
	return sum / time.Duration(len(xs))
}

// ─────────────────────────────────────────────────────────────────────────────
// gaka-b5x.1: Secure cookie flag (Fix 2) — integration layer.
// Unit layer lives in internal/config/config_test.go (TestCookieSecureDefaults).
// ─────────────────────────────────────────────────────────────────────────────

// TestLogin_CookieHasSecureFlagInProd flips h.Cfg.CookieSecure true (the
// production-mode result of Config.Load) and asserts the Set-Cookie header
// carries "; Secure". Deleting the `Secure: h.Cfg.CookieSecure` line makes
// this test fail — non-tautological with the unit layer's config derivation
// test.
func TestLogin_CookieHasSecureFlagInProd(t *testing.T) {
	hz := testutil.NewHarness(t)
	hz.Cfg.CookieSecure = true // simulate BOOM_ENV=production
	e := hz.Router()

	user := "cookie_prod"
	pw := "test1234"
	registerUser(t, e, hz, user, pw)

	rec := doJSONReq(t, e, http.MethodPost, "/auth/login", "", map[string]string{
		"username": user,
		"password": pw,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status %d body=%s", rec.Code, rec.Body.String())
	}
	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "refresh_token=") {
		t.Fatalf("no refresh_token in Set-Cookie: %q", setCookie)
	}
	if !strings.Contains(setCookie, "Secure") {
		t.Errorf("prod login Set-Cookie missing Secure flag: %q", setCookie)
	}
	if !strings.Contains(setCookie, "HttpOnly") {
		t.Errorf("prod login Set-Cookie missing HttpOnly: %q", setCookie)
	}
}

// TestLogin_CookieOmitsSecureFlagInDev is the flip side: with CookieSecure
// false, the Set-Cookie MUST NOT include Secure — else browsers reject the
// cookie on http://localhost during dev.
func TestLogin_CookieOmitsSecureFlagInDev(t *testing.T) {
	hz := testutil.NewHarness(t)
	hz.Cfg.CookieSecure = false // simulate BOOM_ENV=dev
	e := hz.Router()

	user := "cookie_dev"
	pw := "test1234"
	registerUser(t, e, hz, user, pw)

	rec := doJSONReq(t, e, http.MethodPost, "/auth/login", "", map[string]string{
		"username": user,
		"password": pw,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status %d body=%s", rec.Code, rec.Body.String())
	}
	setCookie := rec.Header().Get("Set-Cookie")
	// Case-sensitive check per RFC 6265 — the browser attribute is exactly "Secure".
	if strings.Contains(setCookie, "Secure") {
		t.Errorf("dev login Set-Cookie must NOT include Secure: %q", setCookie)
	}
}

// TestRefresh_CookieCarriesSecureFlag exercises the same cookie code path
// via /auth/refresh_token. Catches a future refactor that inlines a bare
// c.SetCookie somewhere and forgets the Secure flag.
func TestRefresh_CookieCarriesSecureFlag(t *testing.T) {
	hz := testutil.NewHarness(t)
	hz.Cfg.CookieSecure = true
	e := hz.Router()

	user := "cookie_refresh"
	pw := "test1234"
	registerUser(t, e, hz, user, pw)

	// Log in to obtain a refresh cookie.
	loginRec := doJSONReq(t, e, http.MethodPost, "/auth/login", "", map[string]string{
		"username": user,
		"password": pw,
	})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login: %d body=%s", loginRec.Code, loginRec.Body.String())
	}
	// Grab the refresh cookie value from the Set-Cookie header.
	setCookie := loginRec.Header().Get("Set-Cookie")
	refreshCookie := ""
	for _, part := range strings.Split(setCookie, ";") {
		p := strings.TrimSpace(part)
		if strings.HasPrefix(p, "refresh_token=") {
			refreshCookie = strings.TrimPrefix(p, "refresh_token=")
			break
		}
	}
	if refreshCookie == "" {
		t.Fatalf("could not extract refresh cookie from Set-Cookie: %q", setCookie)
	}

	// Now hit /auth/refresh_token.
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshCookie})
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("refresh_token: %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("Set-Cookie"), "Secure") {
		t.Errorf("refresh_token Set-Cookie missing Secure flag: %q", rr.Header().Get("Set-Cookie"))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// gaka-b5x.2: hash session tokens at rest (Fix 3) — integration layer.
// Unit layer lives in internal/auth/auth_test.go (TestHashToken_*).
// ─────────────────────────────────────────────────────────────────────────────

// TestRefreshTokenLookup_UsesHash covers both halves of the dual-path store:
//  1. NEW tokens minted via /auth/login populate hashed_refresh_token and
//     leave the raw refresh_token column NULL. A DB read no longer yields a
//     usable session token — the gaka-b5x.2 fix.
//  2. LEGACY tokens (raw column populated, hash NULL) still authenticate via
//     the fallback branch, so pre-migration sessions keep working.
//
// Bug caught: reverting the INSERT in db/auth.go:CreateAccessTokens to write
// the raw column makes (1) fail (raw column populated → test errors); a
// refactor that removes the `hashed_refresh_token IS NULL AND refresh_token
// = $2` fallback in GetUserByRefreshToken makes (2) fail (legacy 401).
func TestRefreshTokenLookup_UsesHash(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()

	user := "hash_lookup"
	pw := "test1234"
	registerUser(t, e, hz, user, pw)

	// (1) Login — assert the DB row for the freshly-minted refresh token is
	//     stored HASHED, with the raw column NULL.
	loginRec := doJSONReq(t, e, http.MethodPost, "/auth/login", "", map[string]string{
		"username": user,
		"password": pw,
	})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login: %d body=%s", loginRec.Code, loginRec.Body.String())
	}
	// Pull the raw refresh_token value the server sent to the client.
	// Use http.Response.Cookies() so URL-encoding on the wire (base64 "=" →
	// "%3D") is undone — otherwise the SHA-256 assertion below hashes the
	// encoded form and disagrees with the hash the server stored on the
	// decoded value.
	respForCookies := &http.Response{Header: loginRec.Header()}
	rawRefresh := ""
	for _, ck := range respForCookies.Cookies() {
		if ck.Name == "refresh_token" {
			rawRefresh = ck.Value
			break
		}
	}
	if rawRefresh == "" {
		t.Fatalf("could not extract refresh cookie: %q", loginRec.Header().Get("Set-Cookie"))
	}

	// Read the DB row directly. NOTE: /auth/register also mints a
	// (access, refresh) pair, so there are TWO refresh_tokens rows for this
	// user at this point. We want the one whose hash matches the cookie the
	// client currently holds — filter by that hash directly.
	wantHash := sha256.Sum256([]byte(rawRefresh))
	var rawCol *string
	var hashCol []byte
	if err := hz.DB.Pool.QueryRow(context.Background(),
		`SELECT refresh_token, hashed_refresh_token
		 FROM refresh_tokens
		 WHERE owner=$1 AND hashed_refresh_token=$2`,
		user, wantHash[:],
	).Scan(&rawCol, &hashCol); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if rawCol != nil {
		t.Errorf("new refresh row leaks raw token (regression): raw=%q — should be NULL post-gaka-b5x.2", *rawCol)
	}
	if len(hashCol) != 32 {
		t.Errorf("hashed_refresh_token len = %d, want 32 (SHA-256 bytes)", len(hashCol))
	}
	// Confirm the hash actually corresponds to the token the client holds.
	for i := range wantHash {
		if wantHash[i] != hashCol[i] {
			t.Errorf("hashed_refresh_token != SHA-256(client cookie) — write path is broken\n  client cookie=%q\n  got hash=%x\n  want hash=%x",
				rawRefresh, hashCol, wantHash[:])
			break
		}
	}

	// And the token STILL authenticates via /auth/refresh_token (proves the
	// hashed-path lookup works end-to-end).
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: rawRefresh})
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("refresh_token lookup on hashed row failed: %d body=%s", rr.Code, rr.Body.String())
	}

	// (2) Plant a LEGACY row (raw populated, hash NULL) and assert lookup
	//     still works via the fallback. This catches a refactor that drops
	//     the legacy branch mid-cutover, silently breaking every user
	//     holding a pre-migration cookie.
	legacyUser := "hash_legacy"
	registerUser(t, e, hz, legacyUser, pw)
	legacyRaw := auth.ToBase64(auth.NewRawToken())
	if _, err := hz.DB.Pool.Exec(context.Background(),
		`INSERT INTO refresh_tokens (owner, refresh_token, token_expiry)
		 VALUES ($1, $2, NOW() + interval '1 hour')`,
		legacyUser, legacyRaw); err != nil {
		t.Fatalf("plant legacy row: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: legacyRaw})
	rr = httptest.NewRecorder()
	e.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("legacy raw-column refresh_token lookup failed: %d body=%s — dual-path fallback is broken",
			rr.Code, rr.Body.String())
	}
}

// TestAPITokenLookup_UsesHash mirrors the refresh-token test for API tokens
// (auth_tokens table). Same dual-path guarantee: new rows hashed, legacy
// rows fallback-authenticate.
func TestAPITokenLookup_UsesHash(t *testing.T) {
	hz := testutil.NewHarness(t)

	// MintUser() calls InsertAPIToken() which now stores only the hash.
	user, token := hz.MintUser("apitok")

	// (1) The auth_tokens row should have raw=NULL, hash=SHA-256(token).
	var rawCol *string
	var hashCol []byte
	if err := hz.DB.Pool.QueryRow(context.Background(),
		`SELECT token, hashed_token FROM auth_tokens WHERE owner=$1 AND token_expiry IS NULL`, user,
	).Scan(&rawCol, &hashCol); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if rawCol != nil {
		t.Errorf("new API token row leaks raw token: %q — should be NULL", *rawCol)
	}
	wantHash := sha256.Sum256([]byte(token))
	for i := range wantHash {
		if wantHash[i] != hashCol[i] {
			t.Errorf("stored hashed_token != SHA-256(minted token) — write path is broken")
			break
		}
	}

	// (2) End-to-end auth still works via the hashed-path lookup.
	// Use GetUserByToken directly (integration layer, no HTTP path needed
	// for API-token lookup — it's the same lookup exercised by every authed
	// endpoint).
	owner, ok, err := hz.DB.GetUserByToken(context.Background(), token)
	if err != nil {
		t.Fatalf("GetUserByToken: %v", err)
	}
	if !ok || owner != user {
		t.Errorf("GetUserByToken on hashed row: ok=%v owner=%q, want ok=true owner=%q", ok, owner, user)
	}

	// (3) Plant a LEGACY api token row (raw populated, hash NULL) and
	//     assert lookup still works via the fallback.
	legacyUser, _ := hz.MintUser("apitok_legacy_owner")
	legacyRaw := auth.ToBase64(auth.NewRawToken())
	if _, err := hz.DB.Pool.Exec(context.Background(),
		`INSERT INTO auth_tokens (owner, token, token_expiry)
		 VALUES ($1, $2, NOW() + interval '30 minutes')`,
		legacyUser, legacyRaw); err != nil {
		t.Fatalf("plant legacy api token: %v", err)
	}
	owner, ok, err = hz.DB.GetUserByToken(context.Background(), legacyRaw)
	if err != nil {
		t.Fatalf("GetUserByToken (legacy): %v", err)
	}
	if !ok || owner != legacyUser {
		t.Errorf("legacy raw-column api token lookup failed: ok=%v owner=%q, want ok=true owner=%q — dual-path fallback broken",
			ok, owner, legacyUser)
	}
}

// TestLogout_ClearsRefreshCookie: /auth/logout now emits a Set-Cookie with
// MaxAge=-1 to evict the client-side cookie. Without the matching Secure
// attribute the browser wouldn't actually clear a Secure-flagged cookie in
// prod — this test catches a regression where clearRefreshCookie loses
// the Secure flag.
func TestLogout_ClearsRefreshCookie(t *testing.T) {
	hz := testutil.NewHarness(t)
	hz.Cfg.CookieSecure = true
	e := hz.Router()
	// Logout isn't on the default router; register it inline.
	e.POST("/auth/logout", hz.H.Logout)

	user := "logout_clears"
	pw := "test1234"
	registerUser(t, e, hz, user, pw)

	loginRec := doJSONReq(t, e, http.MethodPost, "/auth/login", "", map[string]string{
		"username": user,
		"password": pw,
	})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login: %d body=%s", loginRec.Code, loginRec.Body.String())
	}
	// Extract access token and refresh cookie.
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &lr); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	refreshCookie := ""
	for _, part := range strings.Split(loginRec.Header().Get("Set-Cookie"), ";") {
		p := strings.TrimSpace(part)
		if strings.HasPrefix(p, "refresh_token=") {
			refreshCookie = strings.TrimPrefix(p, "refresh_token=")
			break
		}
	}

	// Logout.
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.Header.Set("Authorization", "Basic "+lr.Token)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshCookie})
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("logout: %d body=%s", rr.Code, rr.Body.String())
	}
	set := rr.Header().Get("Set-Cookie")
	if !strings.Contains(set, "refresh_token=") {
		t.Errorf("logout missing clearing Set-Cookie: %q", set)
	}
	if !strings.Contains(set, "Max-Age=0") && !strings.Contains(set, "Expires=") {
		t.Errorf("logout clearing cookie missing expiry marker: %q", set)
	}
	if !strings.Contains(set, "Secure") {
		t.Errorf("prod-mode logout clearing cookie missing Secure — browser will not evict Secure-flagged cookie: %q", set)
	}
}

// Silence unused-import warnings when only some tests reference these.
var _ = db.TokenData{}

// ─────────────────────────────────────────────────────────────────────────────
// gaka-awh.6 (Bravo MEDIUM): argon2id params bumped to OWASP ASVS L1 2025
// floor + transparent rehash-on-login. Integration layer.
// Unit layer lives in internal/auth/auth_test.go
// (TestArgon2Params_LockedToOWASPFloor_BravoRegression,
//  TestHashPassword_UsesCurrentParams, TestVerifyPassword_v1AndV2_BothWork).
// ─────────────────────────────────────────────────────────────────────────────

// plantLegacyUser inserts a users row directly at ArgonVersionLegacy (v1)
// using the ACTUAL v1 params via HashPasswordWithVersion — simulates a user
// whose account pre-dates the Bravo params bump.
func plantLegacyUser(t *testing.T, hz *testutil.Harness, username, password string) {
	t.Helper()
	ctx := context.Background()
	hz.Cleanup(username)
	hash, salt, err := auth.HashPasswordWithVersion(password, auth.ArgonVersionLegacy)
	if err != nil {
		t.Fatalf("hash v1: %v", err)
	}
	created, err := hz.DB.InsertUser(ctx, db.StoredUser{
		Username: username, HashedPassword: hash, SaltUsed: salt,
		ArgonVersion: auth.ArgonVersionLegacy,
	})
	if err != nil || !created {
		t.Fatalf("plant legacy user %s: created=%v err=%v", username, created, err)
	}
}

// readUserRow returns (hashed_password, argon_version) for username.
func readUserRow(t *testing.T, hz *testutil.Harness, username string) ([]byte, int) {
	t.Helper()
	var hp []byte
	var ver int
	if err := hz.DB.Pool.QueryRow(context.Background(),
		`SELECT hashed_password, argon_version FROM users WHERE username=$1`,
		username).Scan(&hp, &ver); err != nil {
		t.Fatalf("read user row %s: %v", username, err)
	}
	return hp, ver
}

// TestLogin_RehashesLegacyHash_BravoRegression proves the transparent
// rehash-on-login upgrade works end-to-end and is IDEMPOTENT (a second login
// on the same account is a no-op — no unnecessary rehash work).
//
// Non-tautological proof: deleting the `if user.ArgonVersion < ...` block in
// handler.Login makes the third assertion below fail (post-login version stays
// at 1). Captured at implementation time.
//
// Regression signals:
//  1. Row inserted at v1 (legacy hash bytes).
//  2. Login with correct password → 200.
//  3. Row is NOW at v2 AND hashed_password bytes changed (proves the rehash
//     actually ran on the wire — not just the version flip).
//  4. Second login → still 200, row STILL at v2, hashed_password UNCHANGED
//     from post-first-login value (idempotent — no wasted work).
func TestLogin_RehashesLegacyHash_BravoRegression(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()

	user := "bravo_rehash_legacy"
	pw := "bravoMedium1!"
	plantLegacyUser(t, hz, user, pw)

	// (1) confirm we planted a legacy row.
	preHash, preVer := readUserRow(t, hz, user)
	if preVer != auth.ArgonVersionLegacy {
		t.Fatalf("planted user version = %d, want %d — plant helper broken", preVer, auth.ArgonVersionLegacy)
	}

	// (2) login with correct password.
	rec := doJSONReq(t, e, http.MethodPost, "/auth/login", "", map[string]string{
		"username": user, "password": pw,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy user login: status %d body=%s", rec.Code, rec.Body.String())
	}

	// (3) row was silently upgraded to v2 AND hashed_password bytes changed.
	postHash, postVer := readUserRow(t, hz, user)
	if postVer != auth.ArgonVersionCurrent {
		t.Errorf("post-login version = %d, want %d — transparent rehash did NOT bump the row",
			postVer, auth.ArgonVersionCurrent)
	}
	if bytes.Equal(preHash, postHash) {
		t.Error("post-login hashed_password bytes UNCHANGED — version flag was bumped but the actual hash wasn't rewritten (rehash faked)")
	}

	// (4) second login — no rehash on an already-current row. The bytes
	// MUST stay identical (a v2 → v2 upgrade would produce different bytes
	// due to a fresh salt).
	rec = doJSONReq(t, e, http.MethodPost, "/auth/login", "", map[string]string{
		"username": user, "password": pw,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("second login: status %d body=%s", rec.Code, rec.Body.String())
	}
	post2Hash, post2Ver := readUserRow(t, hz, user)
	if post2Ver != auth.ArgonVersionCurrent {
		t.Errorf("second-login version = %d, want %d", post2Ver, auth.ArgonVersionCurrent)
	}
	if !bytes.Equal(postHash, post2Hash) {
		t.Error("second login re-hashed an already-v2 row — the guard is missing")
	}
}

// TestLogin_WrongPasswordDoesNotUpgrade is the negative sibling of the
// rehash regression: a v1 row that survives a WRONG-password attempt must
// stay at v1 with the same hash bytes. Bug caught: a refactor that runs
// the upgrade before verifying credentials.
func TestLogin_WrongPasswordDoesNotUpgrade(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()

	user := "bravo_wrongpw_v1"
	pw := "bravoMedium1!"
	plantLegacyUser(t, hz, user, pw)
	preHash, preVer := readUserRow(t, hz, user)

	rec := doJSONReq(t, e, http.MethodPost, "/auth/login", "", map[string]string{
		"username": user, "password": "not-the-password",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong password: status %d, want 403", rec.Code)
	}
	postHash, postVer := readUserRow(t, hz, user)
	if postVer != preVer {
		t.Errorf("wrong-password login changed argon_version: pre=%d post=%d", preVer, postVer)
	}
	if !bytes.Equal(preHash, postHash) {
		t.Error("wrong-password login changed hashed_password — the upgrade ran on an unauthenticated request")
	}
}

// TestCreateUser_StartsAtV2_BravoRegression proves NEW users land at v2
// immediately (never v1). This catches a refactor that forgets to pass
// ArgonVersion in the InsertUser call — the row would land at 0 (unknown),
// or at 1 if the schema default were flipped.
//
// Non-tautological proof: reverting service.go's CreateUser to omit
// ArgonVersion from the StoredUser literal makes the row land at 0, and this
// test fails with "argon_version = 0, want 2". Captured at implementation
// time.
func TestCreateUser_StartsAtV2_BravoRegression(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()

	user := "bravo_newuser_v2"
	hz.Cleanup(user)
	pw := "bravoMedium1!"

	rec := doJSONReq(t, e, http.MethodPost, "/auth/register", "", map[string]string{
		"username": user, "password": pw,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("register: status %d body=%s", rec.Code, rec.Body.String())
	}
	_, ver := readUserRow(t, hz, user)
	if ver != auth.ArgonVersionCurrent {
		t.Errorf("new user argon_version = %d, want %d — new registrations MUST land at the OWASP-floor generation, never v1",
			ver, auth.ArgonVersionCurrent)
	}
}

// TestChangePassword_StoresAtV2 proves the change-password path always
// writes a v2 hash — even for a user whose account started at v1 and never
// logged in to trigger the transparent rehash. Belt-and-suspenders: even if
// a user reset their password via the change-password endpoint (which
// requires their current password), the resulting row is at v2.
func TestChangePassword_StoresAtV2(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	e.POST("/api/v1/users/current/password", hz.H.ChangePassword)

	user := "bravo_chpwd_v1_to_v2"
	pw := "bravoMedium1!"
	plantLegacyUser(t, hz, user, pw)

	// The API-token path in MintUser hashes an unrelated user; we need an
	// API token for our v1 user to hit /api/v1/users/current/password.
	// Mint one directly.
	token := auth.NewRawToken()
	if err := hz.DB.InsertAPIToken(context.Background(), user, token); err != nil {
		t.Fatalf("insert api token: %v", err)
	}

	newPw := "bravoMedium2!"
	rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/password", token,
		map[string]string{"currentPassword": pw, "newPassword": newPw})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change password: status %d body=%s", rec.Code, rec.Body.String())
	}

	_, ver := readUserRow(t, hz, user)
	if ver != auth.ArgonVersionCurrent {
		t.Errorf("post-change argon_version = %d, want %d — change-password MUST bump to current generation",
			ver, auth.ArgonVersionCurrent)
	}
	// And the new password actually authenticates.
	if code := verifyLogin(t, e, user, newPw); code != http.StatusOK {
		t.Errorf("login with new password after change: status %d, want 200", code)
	}
}
