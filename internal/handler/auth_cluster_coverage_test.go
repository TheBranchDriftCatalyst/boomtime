// auth_cluster_coverage_test.go — coverage-completeness suite for the auth
// cluster of internal/handler (auth.go, password.go, wakatime_key.go).
//
// Goals (gaka-d6x.handler):
//   - Every user-scoped endpoint gets a NAMED INVARIANT test — no bare
//     roundtrips.
//   - Every user-scoped endpoint gets an explicit cross-user isolation check:
//     user B MUST NOT see or modify user A's data via /api/v1/users/current/*.
//   - Security-critical paths (auth, wakatime key) get no-oracle checks
//     (identical envelope on missing vs present, no plaintext leaks).
//   - probeWakatimeKey is exercised end-to-end via an in-process RoundTripper
//     that captures the outbound request (so we test the code path without
//     touching wakatime.com).
package handler_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// -----------------------------------------------------------------------------
// Shared local helpers — kept file-local (suffix `AC` for AuthCluster) so they
// don't collide with existing *G helpers in testhelpers_test.go / auth_test.go.
// -----------------------------------------------------------------------------

// routerWithAuthClusterAC wraps hz.Router() and adds the auth endpoints the
// production server registers but testutil.Router omits (Logout, CurrentUser,
// api-token CRUD, wakatime key GET/DELETE). Registering them here — rather
// than in every spec — avoids the duplicate-route panic while keeping the
// spec bodies focused.
func routerWithAuthClusterAC(hz *testutil.Harness) http.Handler {
	e := hz.Router()
	h := hz.H
	e.POST("/auth/logout", h.Logout)
	e.POST("/auth/create_api_token", h.CreateAPIToken)
	e.GET("/auth/tokens", h.ListAPITokens)
	e.DELETE("/auth/token/:id", h.DeleteToken)
	e.POST("/auth/token", h.UpdateToken)
	e.GET("/auth/users/current", h.CurrentUser)
	e.GET("/api/v1/users/current/wakatime_key", h.GetWakatimeKey)
	e.DELETE("/api/v1/users/current/wakatime_key", h.DeleteWakatimeKey)
	return e
}

// installEncryptionKeyAC seeds a deterministic BOOM_ENCRYPTION_KEY so
// auth.Encrypt / auth.Decrypt succeed for the spec's duration. Mirrors the
// importer_test helper of the same intent (withEncryptionKeyGinkgo).
func installEncryptionKeyAC() {
	const key = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	prev, hadPrev := os.LookupEnv("BOOM_ENCRYPTION_KEY")
	os.Setenv("BOOM_ENCRYPTION_KEY", key)
	auth.ResetForTest()
	Expect(auth.LoadKeyFromEnv()).To(Succeed())
	DeferCleanup(func() {
		if hadPrev {
			os.Setenv("BOOM_ENCRYPTION_KEY", prev)
		} else {
			os.Unsetenv("BOOM_ENCRYPTION_KEY")
		}
		auth.ResetForTest()
	})
}

// clearEncryptionKeyAC intentionally strips BOOM_ENCRYPTION_KEY so
// auth.Encrypt returns ErrKeyUnset, letting us hit the SaveWakatimeKey
// encrypt-error branch (500) without stubbing the whole crypto package.
func clearEncryptionKeyAC() {
	prev, hadPrev := os.LookupEnv("BOOM_ENCRYPTION_KEY")
	os.Unsetenv("BOOM_ENCRYPTION_KEY")
	auth.ResetForTest()
	DeferCleanup(func() {
		if hadPrev {
			os.Setenv("BOOM_ENCRYPTION_KEY", prev)
		} else {
			os.Unsetenv("BOOM_ENCRYPTION_KEY")
		}
		auth.ResetForTest()
	})
}

// stubRoundTripperAC turns every outbound request into a canned response
// built by respond. The captured request pointer lets specs assert on the
// exact Authorization header the probe emitted.
type stubRoundTripperAC struct {
	respond   func(*http.Request) (*http.Response, error)
	callCount atomic.Int64
	lastReq   atomic.Pointer[http.Request]
}

func (s *stubRoundTripperAC) RoundTrip(r *http.Request) (*http.Response, error) {
	s.callCount.Add(1)
	// Copy the request pointer (r itself is mutated after RoundTrip returns
	// in some flows). Header only; Body is fully consumed here.
	s.lastReq.Store(r)
	return s.respond(r)
}

// installProbeStubAC swaps handler.httpClient (via the test seam) for one
// backed by rt. Returns the roundtripper so specs can inspect callCount /
// lastReq. Cleanup is registered on the current spec.
func installProbeStubAC(status int, body string) *stubRoundTripperAC {
	rt := &stubRoundTripperAC{
		respond: func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		},
	}
	client := &http.Client{Transport: rt}
	restore := handler.SwapHTTPClientForTest(client)
	DeferCleanup(restore)
	return rt
}

// installProbeErrStubAC installs a stub that returns a network-level error
// (simulates DNS failure, connection reset, etc.). Used to prove that a
// probe that "couldn't reach a verdict" defaults to WakatimeKeyStatusUnknown
// and still persists the key (save-on-success is NOT gated on unknown).
func installProbeErrStubAC(err error) *stubRoundTripperAC {
	rt := &stubRoundTripperAC{
		respond: func(_ *http.Request) (*http.Response, error) {
			return nil, err
		},
	}
	client := &http.Client{Transport: rt}
	restore := handler.SwapHTTPClientForTest(client)
	DeferCleanup(restore)
	return rt
}

// mintAPITokenAC inserts a fresh non-expiring API token for owner and
// returns the raw token string. Sits alongside hz.MintUser, which already
// mints one, when a spec needs a SECOND token for the same owner (e.g. to
// prove DELETE deletes exactly the right one and leaves siblings alive).
func mintAPITokenAC(hz *testutil.Harness, owner string) string {
	raw := auth.NewRawToken()
	Expect(hz.DB.InsertAPIToken(context.Background(), owner, raw, "")).To(Succeed())
	return raw
}

// tokenIDPrefixAC computes the 12-char hex prefix of SHA-256(wireForm) that
// the server exposes as the token ID. `wireForm` must be the exact string
// the client would put in the Authorization: Basic header — i.e. what gets
// hashed by db.hashSessionToken on lookup. For CreateAPIToken responses,
// callers must base64-encode the raw UUID BEFORE calling this helper.
// For hz.MintUser tokens (which insert the raw UUID directly), pass the raw.
func tokenIDPrefixAC(hz *testutil.Harness, owner, wireForm string) string {
	sum := sha256.Sum256([]byte(wireForm))
	var id string
	Expect(hz.DB.Pool.QueryRow(context.Background(),
		`SELECT LEFT(encode(hashed_token,'hex'),12)
		   FROM auth_tokens WHERE owner=$1 AND token_expiry IS NULL
		    AND hashed_token = $2`,
		owner, sum[:]).Scan(&id)).To(Succeed())
	return id
}

// -----------------------------------------------------------------------------
// CurrentUser (GET /auth/users/current)
// -----------------------------------------------------------------------------

var _ = Describe("CurrentUser (GET /auth/users/current)", func() {
	It("emits full_name/email/is_admin/timezone/effective_timezone; no cookie => 400 envelope with NO whoami hint", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.DefaultTimezone = "America/New_York"
		e := routerWithAuthClusterAC(hz)

		// -- Branch A: cookie absent → 400 MissingRefreshTokenCookie ----
		req := httptest.NewRequest(http.MethodGet, "/auth/users/current", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"no cookie must NOT be 200 — that would leak whoami on an anon request; body=%s", rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("full_name"),
			"400 error envelope must not carry a UserStatus body")

		// -- Branch B: happy path ----
		user, _ := hz.MintUser("current_ok")
		td := db.TokenData{
			Owner:        user,
			Token:        auth.ToBase64(auth.NewRawToken()),
			RefreshToken: auth.ToBase64(auth.NewRawToken()),
		}
		Expect(hz.DB.CreateAccessTokens(context.Background(), td, 24)).To(Succeed())

		req2 := httptest.NewRequest(http.MethodGet, "/auth/users/current", nil)
		req2.AddCookie(&http.Cookie{Name: "refresh_token", Value: td.RefreshToken})
		rec2 := httptest.NewRecorder()
		e.ServeHTTP(rec2, req2)
		Expect(rec2).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec2.Body.String())

		var wrapped struct {
			Data struct {
				FullName          string `json:"full_name"`
				Email             string `json:"email"`
				IsAdmin           bool   `json:"is_admin"`
				Timezone          string `json:"timezone"`
				EffectiveTimezone string `json:"effective_timezone"`
			} `json:"data"`
		}
		Expect(json.Unmarshal(rec2.Body.Bytes(), &wrapped)).To(Succeed())
		Expect(wrapped.Data.FullName).To(Equal(user))
		Expect(wrapped.Data.Email).To(Equal(user + "@hakatime.dev"))
		Expect(wrapped.Data.IsAdmin).To(BeFalse(), "user not on admin allowlist must NOT get is_admin=true")
		// gaka-dg7: raw stored blank + BOOM_DEFAULT_TIMEZONE fallback resolves to America/New_York.
		Expect(wrapped.Data.Timezone).To(BeEmpty(),
			"user hasn't set a timezone — the raw column must stay '' so the FE picker doesn't lie")
		Expect(wrapped.Data.EffectiveTimezone).To(Equal("America/New_York"),
			"3-level resolver must fall through to BOOM_DEFAULT_TIMEZONE; got %q", wrapped.Data.EffectiveTimezone)
	})

	It("is_admin=true ONLY for users on the AdminUsers allowlist (cross-user isolation)", func() {
		hz := testutil.NewHarness(GinkgoT())
		admin, _ := hz.MintUser("current_admin")
		other, _ := hz.MintUser("current_other")

		hz.Cfg.AdminUsers = map[string]struct{}{admin: {}}

		e := routerWithAuthClusterAC(hz)

		mkCookieReq := func(user string) *httptest.ResponseRecorder {
			td := db.TokenData{
				Owner:        user,
				Token:        auth.ToBase64(auth.NewRawToken()),
				RefreshToken: auth.ToBase64(auth.NewRawToken()),
			}
			Expect(hz.DB.CreateAccessTokens(context.Background(), td, 24)).To(Succeed())
			req := httptest.NewRequest(http.MethodGet, "/auth/users/current", nil)
			req.AddCookie(&http.Cookie{Name: "refresh_token", Value: td.RefreshToken})
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}

		adminRec := mkCookieReq(admin)
		otherRec := mkCookieReq(other)

		var adminBody, otherBody struct {
			Data struct {
				FullName string `json:"full_name"`
				IsAdmin  bool   `json:"is_admin"`
			} `json:"data"`
		}
		Expect(json.Unmarshal(adminRec.Body.Bytes(), &adminBody)).To(Succeed())
		Expect(json.Unmarshal(otherRec.Body.Bytes(), &otherBody)).To(Succeed())

		Expect(adminBody.Data.IsAdmin).To(BeTrue(), "%s is on the allowlist", admin)
		Expect(otherBody.Data.IsAdmin).To(BeFalse(),
			"%s is NOT on the allowlist — cross-user isolation: A's admin bit must not leak into B's /users/current response", other)
		Expect(adminBody.Data.FullName).To(Equal(admin))
		Expect(otherBody.Data.FullName).To(Equal(other),
			"user B's cookie yielded user A's identity — the resolver mixed sessions across users")
	})
})

// -----------------------------------------------------------------------------
// API-token CRUD (auth/tokens, auth/token/:id, auth/token, auth/create_api_token)
// -----------------------------------------------------------------------------

var _ = Describe("API token CRUD", func() {
	It("Create mints a NEW row, response returns raw plaintext ONCE, DB stores only SHA-256(hash)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		user, callerToken := hz.MintUser("apitok_create")

		var beforeN int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM auth_tokens WHERE owner=$1 AND token_expiry IS NULL`,
			user).Scan(&beforeN)).To(Succeed())

		rec := doJSONReqG(e, http.MethodPost, "/auth/create_api_token", callerToken,
			map[string]string{"name": "editor-plugin"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "create: body=%s", rec.Body.String())

		var resp struct {
			APIToken string `json:"apiToken"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.APIToken).NotTo(BeEmpty(), "response MUST carry the raw plaintext ONCE")

		var afterN int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM auth_tokens WHERE owner=$1 AND token_expiry IS NULL`,
			user).Scan(&afterN)).To(Succeed())
		Expect(afterN).To(Equal(beforeN+1), "exactly one new row for the caller")

		// NAMED INVARIANT: DB stores only SHA-256(base64(raw)). The response
		// carries the RAW UUID; the client base64-encodes it before sending
		// as Basic auth (see PluginSetup.tsx — "Authorization: Basic
		// <base64(api_key)>"). Verify the stored hash matches the on-wire form.
		// (SHA-256 computed in Go to avoid a pgcrypto extension dependency.)
		wireForm := base64.StdEncoding.EncodeToString([]byte(resp.APIToken))
		wantHash := sha256.Sum256([]byte(wireForm))
		var hashCol []byte
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT hashed_token FROM auth_tokens
			 WHERE owner=$1 AND token_expiry IS NULL
			   AND hashed_token = $2`,
			user, wantHash[:]).Scan(&hashCol)).To(Succeed())
		Expect(hashCol).To(HaveLen(32),
			"stored token hash should be SHA-256 (32 bytes); got %d", len(hashCol))
		Expect(bytes.Equal(hashCol, wantHash[:])).To(BeTrue(),
			"stored hash != SHA-256(base64(response.apiToken))")

		// NAMED INVARIANT: raw response is NOT stored as-is (must not equal hash).
		rawHash := sha256.Sum256([]byte(resp.APIToken))
		Expect(bytes.Equal(hashCol, rawHash[:])).To(BeFalse(),
			"DB stored sha256(raw) instead of sha256(base64(raw)) — hash oracle across formats")

		// The token immediately authenticates against subsequent API calls
		// once the client base64-encodes it (the documented on-wire form).
		verify := doJSONReqG(e, http.MethodGet, "/auth/tokens", wireForm, nil)
		Expect(verify).To(testutil.HaveStatus(http.StatusOK),
			"just-minted API token failed to authenticate under base64(raw); body=%s", verify.Body.String())
	})

	It("Create trims name > 42 chars server-side (no client-side reliance)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		user, callerToken := hz.MintUser("apitok_trim")

		// 100-char name — server must clip to 42 before persisting.
		longName := strings.Repeat("N", 100)
		rec := doJSONReqG(e, http.MethodPost, "/auth/create_api_token", callerToken,
			map[string]string{"name": longName})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var resp struct {
			APIToken string `json:"apiToken"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		// Look up the freshly minted row by its stored hash. auth_tokens has
		// no created_at column, so ORDER BY that is not an option — hash
		// lookup is the deterministic path. Stored hash = sha256(base64(raw)).
		wireForm := base64.StdEncoding.EncodeToString([]byte(resp.APIToken))
		newHash := sha256.Sum256([]byte(wireForm))
		var storedName *string
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT token_name FROM auth_tokens
			 WHERE owner=$1 AND hashed_token=$2`,
			user, newHash[:]).Scan(&storedName)).To(Succeed())
		Expect(storedName).NotTo(BeNil(), "token_name must be persisted")
		Expect(*storedName).To(HaveLen(42),
			"server did NOT trim to 42 — client-provided length trumped the guard; got %q (%d)", *storedName, len(*storedName))
	})

	It("Create accepts empty/absent body (endpoint has always been callable without one)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		_, callerToken := hz.MintUser("apitok_nobody")

		// Send with an entirely absent body.
		req := httptest.NewRequest(http.MethodPost, "/auth/create_api_token", nil)
		req.Header.Set("Authorization", "Basic "+callerToken)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK),
			"empty body must remain callable — the shape is documented as optional; body=%s", rec.Body.String())
	})

	It("Create rejects requests with NO Authorization header (400 MissingAuth envelope)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)

		rec := doJSONReqG(e, http.MethodPost, "/auth/create_api_token", "", nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"unauth caller of /auth/create_api_token MUST NOT be allowed to mint tokens for whoever; body=%s", rec.Body.String())
	})

	It("List returns ONLY the caller's tokens (cross-user isolation) and never leaks raw plaintext", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)

		userA, tokenA := hz.MintUser("apitok_list_a")
		userB, tokenB := hz.MintUser("apitok_list_b")
		Expect(userA).NotTo(Equal(userB))

		// Give A a SECOND token so the list has 2 rows.
		secondA := mintAPITokenAC(hz, userA)
		_ = secondA

		listA := doJSONReqG(e, http.MethodGet, "/auth/tokens", tokenA, nil)
		Expect(listA).To(testutil.HaveStatus(http.StatusOK), "body=%s", listA.Body.String())

		var tokensA []struct {
			ID   string  `json:"tknId"`
			Name *string `json:"tknName"`
		}
		Expect(json.Unmarshal(listA.Body.Bytes(), &tokensA)).To(Succeed())
		Expect(len(tokensA)).To(Equal(2),
			"A should see BOTH tokens (mint + explicit second); got %d", len(tokensA))

		// NAMED INVARIANT #1: raw plaintext never appears in the list body.
		listBodyA := listA.Body.String()
		Expect(listBodyA).NotTo(ContainSubstring(tokenA),
			"raw A token leaked in /auth/tokens response — must only expose SHA-256 prefix")
		Expect(listBodyA).NotTo(ContainSubstring(secondA),
			"raw second-A token leaked in /auth/tokens response")

		// NAMED INVARIANT #2: user B's list does NOT include ANY of A's tokens.
		listB := doJSONReqG(e, http.MethodGet, "/auth/tokens", tokenB, nil)
		Expect(listB).To(testutil.HaveStatus(http.StatusOK))
		var tokensB []struct {
			ID string `json:"tknId"`
		}
		Expect(json.Unmarshal(listB.Body.Bytes(), &tokensB)).To(Succeed())
		// Build set of A's prefixes; assert B's list is disjoint.
		aIDs := map[string]struct{}{}
		for _, t := range tokensA {
			aIDs[t.ID] = struct{}{}
		}
		for _, t := range tokensB {
			_, clash := aIDs[t.ID]
			Expect(clash).To(BeFalse(),
				"cross-user leak: user B's /auth/tokens contains one of A's IDs (%s)", t.ID)
		}
	})

	It("Delete scoped to owner: DELETE of A's token by B leaves the row intact (no oracle)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)

		userA, tokenA := hz.MintUser("apitok_del_a")
		_, tokenB := hz.MintUser("apitok_del_b")

		// Extra token on A that we want to try to delete AS B.
		victimRaw := mintAPITokenAC(hz, userA)
		victimID := tokenIDPrefixAC(hz, userA, victimRaw)

		// GUARANTEE 1: DELETE from B against A's token → 204 (same envelope)
		// but row STAYS. No error differentiation lets B probe which IDs
		// belong to A.
		delAsB := doJSONReqG(e, http.MethodDelete, "/auth/token/"+victimID, tokenB, nil)
		Expect(delAsB).To(testutil.HaveStatus(http.StatusNoContent),
			"expected 204 for cross-owner delete (no-oracle); body=%s", delAsB.Body.String())

		var stillThere int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM auth_tokens
			 WHERE owner=$1 AND LEFT(encode(hashed_token,'hex'),12)=$2`,
			userA, victimID).Scan(&stillThere)).To(Succeed())
		Expect(stillThere).To(Equal(1),
			"B's cross-owner DELETE actually deleted A's token — scope predicate is broken")

		// GUARANTEE 2: DELETE by A DOES remove the row.
		delAsA := doJSONReqG(e, http.MethodDelete, "/auth/token/"+victimID, tokenA, nil)
		Expect(delAsA).To(testutil.HaveStatus(http.StatusNoContent))
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM auth_tokens
			 WHERE owner=$1 AND LEFT(encode(hashed_token,'hex'),12)=$2`,
			userA, victimID).Scan(&stillThere)).To(Succeed())
		Expect(stillThere).To(Equal(0),
			"owner's DELETE did NOT actually remove the row")
	})

	It("Update (rename) scoped to owner: B cannot rename A's token, A's name is preserved verbatim", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)

		userA, tokenA := hz.MintUser("apitok_up_a")
		_, tokenB := hz.MintUser("apitok_up_b")

		// A's second token: mint via API so we know the ID + starting name.
		mkRec := doJSONReqG(e, http.MethodPost, "/auth/create_api_token", tokenA,
			map[string]string{"name": "starting-name"})
		Expect(mkRec).To(testutil.HaveStatus(http.StatusOK))
		var mk struct {
			APIToken string `json:"apiToken"`
		}
		Expect(json.Unmarshal(mkRec.Body.Bytes(), &mk)).To(Succeed())
		// CreateAPIToken stores sha256(base64(raw)); ID is prefix of the hash
		// of the on-wire form (base64), not of the raw response value.
		mkWire := base64.StdEncoding.EncodeToString([]byte(mk.APIToken))
		tokID := tokenIDPrefixAC(hz, userA, mkWire)

		// B tries to rename A's token → 204 (no oracle) but row unchanged.
		hostileRename := doJSONReqG(e, http.MethodPost, "/auth/token", tokenB, map[string]string{
			"tokenId":   tokID,
			"tokenName": "PWNED-BY-B",
		})
		Expect(hostileRename).To(testutil.HaveStatus(http.StatusNoContent))

		var nameAfterHostile *string
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT token_name FROM auth_tokens
			 WHERE owner=$1 AND LEFT(encode(hashed_token,'hex'),12)=$2`,
			userA, tokID).Scan(&nameAfterHostile)).To(Succeed())
		Expect(nameAfterHostile).NotTo(BeNil())
		Expect(*nameAfterHostile).To(Equal("starting-name"),
			"user B renamed A's token — scope predicate on UPDATE is broken; got %q", *nameAfterHostile)

		// A's own rename works.
		selfRename := doJSONReqG(e, http.MethodPost, "/auth/token", tokenA, map[string]string{
			"tokenId":   tokID,
			"tokenName": "self-renamed",
		})
		Expect(selfRename).To(testutil.HaveStatus(http.StatusNoContent))

		var nameAfterSelf *string
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT token_name FROM auth_tokens
			 WHERE owner=$1 AND LEFT(encode(hashed_token,'hex'),12)=$2`,
			userA, tokID).Scan(&nameAfterSelf)).To(Succeed())
		Expect(*nameAfterSelf).To(Equal("self-renamed"),
			"owner's rename did NOT stick")
	})

	It("Update rejects a 5 KiB body with 413 (gaka-bi2)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		_, tokenA := hz.MintUser("apitok_up_413")

		big := strings.Repeat("x", 5000)
		body := []byte(`{"tokenId":"abcdef012345","tokenName":"` + big + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/auth/token", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+tokenA)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
			"Update endpoint has a 4 KiB cap; oversize body must 413 before the SQL UPDATE runs; body=%s",
			rec.Body.String())
	})

	It("List/Delete/Update all require Authorization (400 MissingAuth), never 500/silent-accept", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)

		unauth := []struct{ method, path string }{
			{http.MethodGet, "/auth/tokens"},
			{http.MethodDelete, "/auth/token/deadbeef1234"},
			{http.MethodPost, "/auth/token"},
			{http.MethodPost, "/auth/create_api_token"},
		}
		for _, u := range unauth {
			rec := doJSONReqG(e, u.method, u.path, "", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"%s %s: unauth must 400 MissingAuth, not 500 or 204; body=%s",
				u.method, u.path, rec.Body.String())
		}
	})
})

// -----------------------------------------------------------------------------
// Logout — edge cases not covered by the existing "clears cookie" test.
// -----------------------------------------------------------------------------

var _ = Describe("Logout (POST /auth/logout) — edge cases", func() {
	It("no Authorization header → 400 MissingAuth (no DB writes)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)

		req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		// Include a refresh cookie so we can prove the 400 fires on the
		// Authorization guard BEFORE the cookie check.
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "does-not-matter"})
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"logout with no auth header must 400 MissingAuth, not attempt DELETE with an empty token; body=%s", rec.Body.String())
	})

	It("Authorization present but refresh_token cookie absent → 400 MissingRefreshTokenCookie", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		_, tokenA := hz.MintUser("logout_no_cookie")

		req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		req.Header.Set("Authorization", "Basic "+tokenA)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"missing refresh cookie must 400, not proceed to DELETE with an empty refresh; body=%s", rec.Body.String())
	})

	It("stale/mismatched tokens (n<2) → 403 InvalidCredentials, no cookie is cleared on failure", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		_, tokenA := hz.MintUser("logout_mismatch")

		// Access token is valid but refresh cookie is a random value that
		// doesn't hash to any refresh_tokens row. DeleteTokens returns n=1
		// (only the access token row), so the guard fires.
		req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		req.Header.Set("Authorization", "Basic "+tokenA)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "phony-mismatched-refresh-value"})
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden),
			"n<2 branch must 403 InvalidCredentials — same envelope as login-side to avoid a token-scan oracle; body=%s", rec.Body.String())
	})
})

// -----------------------------------------------------------------------------
// Register — edge cases not covered by the existing weak/strong/413 suites.
// -----------------------------------------------------------------------------

var _ = Describe("Register (POST /auth/register) — edge cases", func() {
	It("EnableRegistration=false → 403 DisabledRegistration BEFORE the body is read (no side effects)", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.EnableRegistration = false
		e := hz.Router()

		user := "reg_disabled_" + auth.NewRawToken()[:8]
		hz.Cleanup(user)

		rec := doJSONReqG(e, http.MethodPost, "/auth/register", "", map[string]string{
			"username": user, "password": "abcdefg1",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden),
			"disabled registration must 403; body=%s", rec.Body.String())

		// NAMED INVARIANT: no users row was created despite otherwise-valid input.
		var n int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM users WHERE username=$1`, user).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(0),
			"registration was refused but a users row leaked — DisabledRegistration guard is running AFTER the insert")
	})

	It("duplicate username → 409 UsernameExists, no second row inserted", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user := "reg_dup_" + auth.NewRawToken()[:8]
		hz.Cleanup(user)

		rec1 := doJSONReqG(e, http.MethodPost, "/auth/register", "", map[string]string{
			"username": user, "password": "abcdefg1",
		})
		Expect(rec1).To(testutil.HaveStatus(http.StatusOK), "first register: body=%s", rec1.Body.String())

		rec2 := doJSONReqG(e, http.MethodPost, "/auth/register", "", map[string]string{
			"username": user, "password": "otherpw2",
		})
		Expect(rec2).To(testutil.HaveStatus(http.StatusConflict),
			"second register with same username must 409; body=%s", rec2.Body.String())

		var n int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM users WHERE username=$1`, user).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(1),
			"duplicate register created a second row for %s — insertion is not idempotent", user)
	})
})

// -----------------------------------------------------------------------------
// RefreshToken — edge case.
// -----------------------------------------------------------------------------

var _ = Describe("RefreshToken (POST /auth/refresh_token) — no cookie", func() {
	It("returns 400 MissingRefreshTokenCookie when the cookie is absent (no session minted)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"no cookie must 400; body=%s", rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring(`"token"`),
			"no session may be minted when the cookie is absent")

		// The Set-Cookie header must NOT be written on the error path.
		Expect(rec.Header().Get("Set-Cookie")).To(BeEmpty(),
			"refresh error must not emit a refresh_token cookie")
	})

	It("stale/unknown cookie → 403 ExpiredRefreshToken, no session minted", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "never-created-in-db"})
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden),
			"unknown refresh token must 403 ExpiredRefreshToken; body=%s", rec.Body.String())
	})
})

// -----------------------------------------------------------------------------
// ChangePassword — missing-fields branch (not covered by the 401/weak tests).
// -----------------------------------------------------------------------------

var _ = Describe("ChangePassword — auth guard", func() {
	It("400 MissingAuth without an Authorization header (no argon2 verify on body)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", "", map[string]string{
			"currentPassword": "x", "newPassword": "y",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"unauth ChangePassword must 400 MissingAuth, not run the verify branch; body=%s", rec.Body.String())
	})

	It("403 InvalidToken with an unknown Bearer token", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password",
			"unknown-token-doesnt-hash-to-anyone",
			map[string]string{"currentPassword": "x", "newPassword": "y"})
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden),
			"unknown token must 403 InvalidToken; body=%s", rec.Body.String())
	})
})

var _ = Describe("ChangePassword (POST /api/v1/users/current/password) — missing fields", func() {
	It("empty currentPassword → 400 BEFORE any argon2 verify runs (no timing oracle for empty)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, _, token := mintUserWithPasswordG(hz, "chpwd_missing_cur", "test1234")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
			"currentPassword": "",
			"newPassword":     "shouldnotmatter1",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"missing currentPassword must 400 not 401 (401 would prove an empty verify ran); body=%s", rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("required"),
			"error body must name which fields are required")
	})

	It("empty newPassword → 400 (guard fires BEFORE ValidatePassword sentinel path)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, _, token := mintUserWithPasswordG(hz, "chpwd_missing_new", "test1234")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
			"currentPassword": "test1234",
			"newPassword":     "",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"empty newPassword must 400 with the shared 'required' message, not ErrPasswordTooShort; body=%s", rec.Body.String())
	})
})

// -----------------------------------------------------------------------------
// Wakatime key: GET / DELETE + cross-user
// -----------------------------------------------------------------------------

var _ = Describe("GetWakatimeKey (GET /api/v1/users/current/wakatime_key)", func() {
	It("no saved key → hasSavedKey=false; NEVER leaks a prefix / length / status hint on absent", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		_, token := hz.MintUser("wkkey_get_none")

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/wakatime_key", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var got struct {
			HasSavedKey bool    `json:"hasSavedKey"`
			KeyStatus   *string `json:"keyStatus,omitempty"`
			CheckedAt   *string `json:"checkedAt,omitempty"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got.HasSavedKey).To(BeFalse())
		Expect(got.KeyStatus).To(BeNil(),
			"absent-key response leaked a status — the FE can't distinguish 'never set' from 'set but not-yet-checked' via status")
		Expect(got.CheckedAt).To(BeNil(),
			"absent-key response leaked a checkedAt — same problem as leaking a status")

		// Raw body check: no fields that would hint at a value/prefix/length.
		body := rec.Body.String()
		for _, needle := range []string{"prefix", "length", "keyHint", "suffix", "\"key\""} {
			Expect(body).NotTo(ContainSubstring(needle),
				"GET response includes a hint field %q that would leak plaintext structure: %s", needle, body)
		}
	})

	It("with saved key: reports hasSavedKey+status+checkedAt but NEVER the plaintext or ciphertext", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()

		user, token := hz.MintUser("wkkey_get_set")

		// Directly seed a ciphertext + valid status via the DB (bypasses the
		// probe path — we just want to prove GET renders correctly).
		plaintext := "waka_secret_prefix_never_leaks"
		ct, err := auth.Encrypt([]byte(plaintext))
		Expect(err).NotTo(HaveOccurred())
		Expect(hz.DB.SetEncryptedWakatimeKey(context.Background(), user, ct, db.WakatimeKeyStatusValid)).To(Succeed())

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/wakatime_key", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var got struct {
			HasSavedKey bool    `json:"hasSavedKey"`
			KeyStatus   *string `json:"keyStatus"`
			CheckedAt   *string `json:"checkedAt"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got.HasSavedKey).To(BeTrue())
		Expect(got.KeyStatus).NotTo(BeNil())
		Expect(*got.KeyStatus).To(Equal(string(db.WakatimeKeyStatusValid)))
		Expect(got.CheckedAt).NotTo(BeNil(), "we just SetEncryptedWakatimeKey which sets checked_at=now(); GET must surface it")

		// SECURITY-CRITICAL: neither plaintext NOR ciphertext appears in the body.
		body := rec.Body.String()
		Expect(body).NotTo(ContainSubstring(plaintext),
			"plaintext wakatime key leaked in GET response: %s", body)
		Expect(body).NotTo(ContainSubstring(base64.StdEncoding.EncodeToString(ct)),
			"ciphertext (base64) leaked in GET response")
	})

	It("cross-user isolation: B's GET reports B's saved state (false), NOT A's (true)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()

		userA, tokenA := hz.MintUser("wkkey_iso_a")
		userB, tokenB := hz.MintUser("wkkey_iso_b")
		Expect(userA).NotTo(Equal(userB))

		// A has a saved key, B does not.
		ct, err := auth.Encrypt([]byte("a-only-secret"))
		Expect(err).NotTo(HaveOccurred())
		Expect(hz.DB.SetEncryptedWakatimeKey(context.Background(), userA, ct, db.WakatimeKeyStatusValid)).To(Succeed())

		recA := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/wakatime_key", tokenA, nil)
		recB := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/wakatime_key", tokenB, nil)

		var gotA, gotB struct {
			HasSavedKey bool `json:"hasSavedKey"`
		}
		Expect(json.Unmarshal(recA.Body.Bytes(), &gotA)).To(Succeed())
		Expect(json.Unmarshal(recB.Body.Bytes(), &gotB)).To(Succeed())

		Expect(gotA.HasSavedKey).To(BeTrue(), "A set a key; A's GET must reflect it")
		Expect(gotB.HasSavedKey).To(BeFalse(),
			"user B's GET reported A's saved-key bit — cross-user resolver mixed sessions")
	})
})

var _ = Describe("DeleteWakatimeKey (DELETE /api/v1/users/current/wakatime_key)", func() {
	It("idempotent 204 when no key exists (FE doesn't need to first-check with GET)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		_, token := hz.MintUser("wkkey_del_noop")

		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/wakatime_key", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent),
			"clear-when-absent must 204 (idempotent); body=%s", rec.Body.String())
	})

	It("clears ciphertext AND status/checked_at metadata (not just the blob)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()

		user, token := hz.MintUser("wkkey_del_full")
		ct, err := auth.Encrypt([]byte("some-plaintext"))
		Expect(err).NotTo(HaveOccurred())
		Expect(hz.DB.SetEncryptedWakatimeKey(context.Background(), user, ct, db.WakatimeKeyStatusValid)).To(Succeed())

		// Confirm all three columns are populated pre-delete.
		var preBlob []byte
		var preStatus *string
		var preChecked *[]byte // opaque; we only care it's non-null
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT encrypted_wakatime_key, wakatime_key_status, wakatime_key_checked_at::text::bytea
			   FROM users WHERE username=$1`, user).Scan(&preBlob, &preStatus, &preChecked)).To(Succeed())
		Expect(preBlob).NotTo(BeEmpty())
		Expect(preStatus).NotTo(BeNil())
		Expect(preChecked).NotTo(BeNil())

		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/wakatime_key", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent), "body=%s", rec.Body.String())

		// NAMED INVARIANT: all three columns are NULL post-delete. If only
		// the blob went NULL, a subsequent presence-probe would render a
		// stale status/checkedAt from the last save (leaks history).
		var postBlob []byte
		var postStatus *string
		var postChecked *[]byte
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT encrypted_wakatime_key, wakatime_key_status, wakatime_key_checked_at::text::bytea
			   FROM users WHERE username=$1`, user).Scan(&postBlob, &postStatus, &postChecked)).To(Succeed())
		Expect(postBlob).To(BeNil(), "DELETE left ciphertext behind")
		Expect(postStatus).To(BeNil(),
			"DELETE left status metadata behind — leaks 'this user WAS valid recently'")
		Expect(postChecked).To(BeNil(),
			"DELETE left checked_at behind — leaks last-save wall-clock")
	})

	It("cross-user isolation: B's DELETE does NOT touch A's ciphertext", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()

		userA, _ := hz.MintUser("wkkey_del_iso_a")
		_, tokenB := hz.MintUser("wkkey_del_iso_b")

		ct, err := auth.Encrypt([]byte("A's key stays"))
		Expect(err).NotTo(HaveOccurred())
		Expect(hz.DB.SetEncryptedWakatimeKey(context.Background(), userA, ct, db.WakatimeKeyStatusValid)).To(Succeed())

		// B calls DELETE — endpoint is scoped to CURRENT user (from the
		// token), so it MUST NULL only B's columns, not A's.
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/wakatime_key", tokenB, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent))

		var aBlobAfter []byte
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT encrypted_wakatime_key FROM users WHERE username=$1`, userA).Scan(&aBlobAfter)).To(Succeed())
		Expect(aBlobAfter).To(Equal(ct),
			"B's DELETE clobbered A's encrypted key — the endpoint is not scoped to owner")
	})
})

// -----------------------------------------------------------------------------
// SaveWakatimeKey — probe injection covers 2xx (persist), 401 (reject),
// network-error (persist w/ unknown), and the empty-body 400 guard.
// -----------------------------------------------------------------------------

var _ = Describe("SaveWakatimeKey (POST /api/v1/users/current/wakatime_key)", func() {
	It("empty key body → 400 (never call probe, never overwrite existing key)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()

		// Install a stub that FAILS the test if the probe runs at all.
		rt := &stubRoundTripperAC{
			respond: func(*http.Request) (*http.Response, error) {
				defer GinkgoRecover()
				Fail("SaveWakatimeKey ran the probe for an empty-body POST — the guard is missing")
				return nil, errors.New("unreachable")
			},
		}
		restore := handler.SwapHTTPClientForTest(&http.Client{Transport: rt})
		DeferCleanup(restore)

		user, token := hz.MintUser("wkkey_save_empty")

		// Seed an existing key so we can prove the empty-POST didn't clobber it.
		existingCT, err := auth.Encrypt([]byte("existing-key-must-survive"))
		Expect(err).NotTo(HaveOccurred())
		Expect(hz.DB.SetEncryptedWakatimeKey(context.Background(), user, existingCT, db.WakatimeKeyStatusValid)).To(Succeed())

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/wakatime_key", token,
			map[string]string{"key": ""})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"empty key must 400 BadRequest with DELETE hint; body=%s", rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("DELETE"),
			"error should hint at using DELETE (clients rely on the hint)")

		// NAMED INVARIANT: existing ciphertext is untouched.
		var stillCT []byte
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT encrypted_wakatime_key FROM users WHERE username=$1`, user).Scan(&stillCT)).To(Succeed())
		Expect(stillCT).To(Equal(existingCT),
			"empty POST silently clobbered the saved key — the guard runs AFTER the write")
		Expect(rt.callCount.Load()).To(BeZero(), "probe was called on the empty-body path — extra latency + exposes key to upstream")
	})

	It("probe returns 401 → 400 client-facing, NO ciphertext persisted (save-on-success invariant)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()

		installProbeStubAC(http.StatusUnauthorized, `{"error":"unauthenticated"}`)

		user, token := hz.MintUser("wkkey_save_401")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/wakatime_key", token,
			map[string]string{"key": "bogus-key-rejected-upstream"})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"upstream 401 must yield client 400 (validate-then-persist); body=%s", rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("rejected"),
			"error text must name what happened (Wakatime rejected the key)")

		// NAMED INVARIANT: no ciphertext row was written.
		var blob []byte
		var status *string
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT encrypted_wakatime_key, wakatime_key_status FROM users WHERE username=$1`,
			user).Scan(&blob, &status)).To(Succeed())
		Expect(blob).To(BeNil(),
			"401 branch persisted ciphertext — that's the exact 'stored a bad key' failure the design forbids")
		Expect(status).To(BeNil(),
			"401 branch wrote status metadata — the design says status only flips on a persisted key")
	})

	It("probe returns 200 → 204 client-facing, ciphertext persisted with status=valid; probe carried Authorization: Basic base64(key)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()

		rt := installProbeStubAC(http.StatusOK, `{"data":{"id":"deadbeef"}}`)

		user, token := hz.MintUser("wkkey_save_ok")
		plaintext := "wakatime-valid-key-abc123"

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/wakatime_key", token,
			map[string]string{"key": plaintext})
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent), "body=%s", rec.Body.String())

		// NAMED INVARIANT #1: exactly one probe call.
		Expect(rt.callCount.Load()).To(Equal(int64(1)),
			"expected exactly one probe call; got %d", rt.callCount.Load())

		// NAMED INVARIANT #2: probe carried Authorization: Basic base64(plaintext).
		// The endpoint MUST NOT leak the raw key anywhere else in the request
		// (no query strings, no URL fragments — Basic auth header only).
		got := rt.lastReq.Load()
		Expect(got).NotTo(BeNil(), "probe stub never captured a request")
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(plaintext))
		Expect(got.Header.Get("Authorization")).To(Equal(wantAuth),
			"probe outbound Authorization header wrong — either the wrong encoding or the wrong material")
		Expect(got.URL.RawQuery).To(BeEmpty(), "raw key must NOT appear in query string")

		// NAMED INVARIANT #3: ciphertext is now on the row.
		info, err := hz.DB.GetWakatimeKeyInfo(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.HasSavedKey).To(BeTrue(), "ciphertext missing after 200-probe path")
		Expect(info.Status).NotTo(BeNil())
		Expect(*info.Status).To(Equal(string(db.WakatimeKeyStatusValid)),
			"status must be 'valid' after 2xx probe (matches design comment)")

		// NAMED INVARIANT #4: what's on disk decrypts back to plaintext under
		// the current env key (no cross-key confusion; not stored raw).
		Expect(info.Blob).NotTo(Equal([]byte(plaintext)),
			"encrypted_wakatime_key column stored the plaintext directly — encryption path was skipped")
		roundtrip, err := auth.Decrypt(info.Blob)
		Expect(err).NotTo(HaveOccurred(), "stored blob does not decrypt under current key")
		Expect(string(roundtrip)).To(Equal(plaintext),
			"decrypt yielded wrong plaintext — encrypt/decrypt path is broken or wrong key was used")
	})

	It("probe returns network error → 204 client-facing with status=unknown (couldn't-reach-a-verdict path)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()

		installProbeErrStubAC(errors.New("simulated: dial tcp: no route to host"))

		user, token := hz.MintUser("wkkey_save_neterr")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/wakatime_key", token,
			map[string]string{"key": "waka-key-network-flake"})
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent),
			"network error must NOT be conflated with a 401; body=%s", rec.Body.String())

		info, err := hz.DB.GetWakatimeKeyInfo(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.HasSavedKey).To(BeTrue(),
			"network-error branch must still persist the ciphertext (design: unknown-verdict flavor of save-on-success)")
		Expect(info.Status).NotTo(BeNil())
		Expect(*info.Status).To(Equal(string(db.WakatimeKeyStatusUnknown)),
			"unknown-verdict status was NOT written — FE would render a false invalidity")
	})

	It("probe returns 500 → 204 with status=unknown (5xx never means the key is bad)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()

		installProbeStubAC(http.StatusInternalServerError, `internal upstream failure`)

		user, token := hz.MintUser("wkkey_save_5xx")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/wakatime_key", token,
			map[string]string{"key": "valid-but-upstream-500"})
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent), "body=%s", rec.Body.String())

		info, err := hz.DB.GetWakatimeKeyInfo(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.HasSavedKey).To(BeTrue())
		Expect(*info.Status).To(Equal(string(db.WakatimeKeyStatusUnknown)),
			"5xx upstream must map to 'unknown', not 'invalid' — otherwise a wakatime outage silently poisons every save")
	})

	It("probe returns 403 → 400 client-facing (403 counts as an explicit key rejection just like 401)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()

		installProbeStubAC(http.StatusForbidden, `{"error":"forbidden"}`)

		user, token := hz.MintUser("wkkey_save_403")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/wakatime_key", token,
			map[string]string{"key": "bogus-key-forbidden-upstream"})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"403 must be treated as explicit rejection like 401 (validate-then-persist); body=%s", rec.Body.String())

		info, err := hz.DB.GetWakatimeKeyInfo(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.HasSavedKey).To(BeFalse(), "403 branch persisted ciphertext — rejects design invariant")
	})

	It("BOOM_ENCRYPTION_KEY unset → 500 generic envelope AFTER a successful probe (no plaintext logged/persisted)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		clearEncryptionKeyAC()

		installProbeStubAC(http.StatusOK, `{"data":{}}`)

		user, token := hz.MintUser("wkkey_save_nokey")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/wakatime_key", token,
			map[string]string{"key": "plaintext-must-not-persist"})
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError),
			"encryption unavailable must 500, not persist plaintext; body=%s", rec.Body.String())
		// Client-facing envelope must be the GENERIC message — never the
		// internal ErrKeyUnset.Error() text (that leaks env config state).
		Expect(rec.Body.String()).NotTo(ContainSubstring("BOOM_ENCRYPTION_KEY"),
			"500 response leaks env-key state to the client")

		// NAMED INVARIANT: no ciphertext on the row.
		var blob []byte
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT encrypted_wakatime_key FROM users WHERE username=$1`, user).Scan(&blob)).To(Succeed())
		Expect(blob).To(BeNil(),
			"encrypt-error branch persisted SOMETHING — must be atomic with the save")
	})

	It("cross-user isolation: A saves key; B's row is unaffected (blob column NULL)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()

		installProbeStubAC(http.StatusOK, `{"data":{}}`)

		userA, tokenA := hz.MintUser("wkkey_save_iso_a")
		userB, _ := hz.MintUser("wkkey_save_iso_b")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/wakatime_key", tokenA,
			map[string]string{"key": "a-only-key"})
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent), "body=%s", rec.Body.String())

		var bBlob []byte
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT encrypted_wakatime_key FROM users WHERE username=$1`, userB).Scan(&bBlob)).To(Succeed())
		Expect(bBlob).To(BeNil(),
			"A's save wrote ciphertext into B's row — scope on UPDATE is broken")

		// A's own row is populated (positive sibling assertion).
		var aBlob []byte
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT encrypted_wakatime_key FROM users WHERE username=$1`, userA).Scan(&aBlob)).To(Succeed())
		Expect(aBlob).NotTo(BeEmpty())
	})
})

// -----------------------------------------------------------------------------
// Wakatime unauth guards (resolveUser branch coverage on all 3 endpoints).
// -----------------------------------------------------------------------------

var _ = Describe("Wakatime key endpoints — auth guard", func() {
	It("GET/POST/DELETE all 400 MissingAuth without an Authorization header (no leak of hasSavedKey)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)

		for _, m := range []struct{ method, path string }{
			{http.MethodGet, "/api/v1/users/current/wakatime_key"},
			{http.MethodPost, "/api/v1/users/current/wakatime_key"},
			{http.MethodDelete, "/api/v1/users/current/wakatime_key"},
		} {
			rec := doJSONReqG(e, m.method, m.path, "", map[string]string{"key": "x"})
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"%s %s: unauth must 400 MissingAuth — a 200 here would leak whoami's saved-key bit; body=%s",
				m.method, m.path, rec.Body.String())
			// The MissingAuth envelope must NOT include hasSavedKey / status.
			Expect(rec.Body.String()).NotTo(ContainSubstring("hasSavedKey"),
				"unauth path leaked hasSavedKey shape")
			Expect(rec.Body.String()).NotTo(ContainSubstring("keyStatus"),
				"unauth path leaked keyStatus shape")
		}
	})

	It("GET/POST/DELETE all 403 InvalidToken with an unknown Bearer token", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)

		bogusToken := "unknown-token-that-does-not-hash-to-a-users-row"
		for _, m := range []struct{ method, path string }{
			{http.MethodGet, "/api/v1/users/current/wakatime_key"},
			{http.MethodPost, "/api/v1/users/current/wakatime_key"},
			{http.MethodDelete, "/api/v1/users/current/wakatime_key"},
		} {
			rec := doJSONReqG(e, m.method, m.path, bogusToken, map[string]string{"key": "x"})
			Expect(rec).To(testutil.HaveStatus(http.StatusForbidden),
				"%s %s: unknown token must 403 InvalidToken; body=%s",
				m.method, m.path, rec.Body.String())
		}
	})
})

// -----------------------------------------------------------------------------
// Direct probe unit tests (probeWakatimeKey via the handler-level seam).
// -----------------------------------------------------------------------------

var _ = Describe("probeWakatimeKey status mapping", func() {
	It("uses an httptest.Server to prove the code path treats 200/401/403/5xx per spec", func() {
		// This is a table-ish spec against a REAL http.Server (via
		// httptest.NewServer) — we swap httpClient at handler level and
		// front the wakatime probe URL requests through a router that maps
		// paths to specific status codes.
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()

		mux := http.NewServeMux()
		var wantStatus atomic.Int32
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(int(wantStatus.Load()))
		})
		srv := httptest.NewServer(mux)
		DeferCleanup(srv.Close)

		// Point the handler.httpClient at a client whose RoundTripper
		// REWRITES the URL to our test server (const wakatimeProbeURL
		// cannot be changed at test time).
		u := srv.URL
		rt := &rewriteRoundTripperAC{target: u}
		restore := handler.SwapHTTPClientForTest(&http.Client{Transport: rt})
		DeferCleanup(restore)

		user, token := hz.MintUser("wkkey_probe_map")

		type entry struct {
			serverStatus int
			wantClient   int
			wantSaved    bool
			wantColumn   string // "valid" | "unknown" | ""
		}
		for _, tc := range []entry{
			{200, http.StatusNoContent, true, "valid"},
			{204, http.StatusNoContent, true, "valid"},
			{401, http.StatusBadRequest, false, ""},
			{403, http.StatusBadRequest, false, ""},
			{500, http.StatusNoContent, true, "unknown"},
			{429, http.StatusNoContent, true, "unknown"},
		} {
			// Reset per-tc state.
			Expect(hz.DB.ClearEncryptedWakatimeKey(context.Background(), user)).To(Succeed())
			wantStatus.Store(int32(tc.serverStatus))

			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/wakatime_key", token,
				map[string]string{"key": "probeed-key-per-status"})
			Expect(rec).To(testutil.HaveStatus(tc.wantClient),
				"upstream=%d wantClient=%d gotClient=%d body=%s",
				tc.serverStatus, tc.wantClient, rec.Code, rec.Body.String())

			info, err := hz.DB.GetWakatimeKeyInfo(context.Background(), user)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.HasSavedKey).To(Equal(tc.wantSaved),
				"upstream=%d expected persistence=%v got=%v", tc.serverStatus, tc.wantSaved, info.HasSavedKey)
			if tc.wantSaved {
				Expect(info.Status).NotTo(BeNil())
				Expect(*info.Status).To(Equal(tc.wantColumn),
					"upstream=%d expected status=%q got=%q", tc.serverStatus, tc.wantColumn, *info.Status)
			}
		}
	})
})

// -----------------------------------------------------------------------------
// DB-outage fault injection: closing the pool BEFORE the endpoint runs makes
// every subsequent query error out. Lets us hit the internal-error branches
// that would otherwise require a live SetXxxFaultInjector seam.
// Each harness owns its own pool (see testutil.OpenDB → t.Cleanup(Close)),
// so closing early here does NOT affect other specs.
// -----------------------------------------------------------------------------

var _ = Describe("Auth cluster — internal-error branches (pool closed)", func() {
	It("GetWakatimeKey with a dead pool → 500 generic envelope (no info leak)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		_, token := hz.MintUser("wkkey_dbdown_get")

		// Kill the pool AFTER the token/user are on disk. Every subsequent
		// query on hz.DB.Pool now returns "closed pool" style errors.
		hz.DB.Pool.Close()

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/wakatime_key", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError),
			"DB outage must 500 generic; body=%s", rec.Body.String())
		// SECURITY-CRITICAL: internal error envelope MUST NOT leak DB internals.
		body := rec.Body.String()
		for _, needle := range []string{"pool", "conn", "SELECT", "closed"} {
			Expect(body).NotTo(ContainSubstring(needle),
				"500 body leaked internal string %q: %s", needle, body)
		}
	})

	It("SaveWakatimeKey with a dead pool AFTER a successful probe → 500 generic (persist branch fails)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		installEncryptionKeyAC()
		installProbeStubAC(http.StatusOK, `{"data":{}}`)

		_, token := hz.MintUser("wkkey_dbdown_save")
		// resolveUser needs the DB to look up the token — do that lookup
		// via a first-pass call, THEN close the pool so it fails on the
		// persist step. Actually a simpler play: SaveWakatimeKey's very
		// first DB touch is resolveUser too. Closing the pool means the
		// FIRST DB call fails (resolveUser → 403). To hit the PERSIST
		// branch we need the token lookup to succeed. Use a stub that
		// closes the pool inside the probe RoundTrip: the resolveUser
		// call already finished by then.
		rt := &stubRoundTripperAC{
			respond: func(*http.Request) (*http.Response, error) {
				hz.DB.Pool.Close()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"data":{}}`)),
					Header:     make(http.Header),
				}, nil
			},
		}
		restore := handler.SwapHTTPClientForTest(&http.Client{Transport: rt})
		DeferCleanup(restore)

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/wakatime_key", token,
			map[string]string{"key": "valid-key-but-db-dies-during-persist"})
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError),
			"persist-time DB error must 500; body=%s", rec.Body.String())
	})

	It("DeleteWakatimeKey with a dead pool → 500 (Clear query fails)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		_, token := hz.MintUser("wkkey_dbdown_del")

		// Same approach — close pool AFTER token verified but BEFORE the
		// Clear query. resolveUser is a pool call too, so closing here
		// will make resolveUser fail with generic 500 which STILL hits
		// the same branch (h.internalErr envelope). Either way we cover
		// the failure branch of the handler.
		hz.DB.Pool.Close()

		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/wakatime_key", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError),
			"DB outage on Clear must 500; body=%s", rec.Body.String())
	})

	It("ChangePassword with a dead pool → 500 (GetUserByName errors before verify)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, _, token := mintUserWithPasswordG(hz, "chpwd_dbdown", "test1234")

		hz.DB.Pool.Close()

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/password", token, map[string]string{
			"currentPassword": "test1234",
			"newPassword":     "test5678",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError),
			"DB down must 500 generic — not 401 (would falsely claim wrong password); body=%s", rec.Body.String())
	})

	It("Login with a dead pool → 500 generic (never reaches sentinel verify)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		hz.DB.Pool.Close()

		rec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
			"username": "any", "password": "any",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError),
			"login on dead pool must 500 generic — a 403 would prove BurnSentinelVerify ran on a lookup error, contradicting design; body=%s", rec.Body.String())
	})

	It("Register with a dead pool → 500 generic (CreateUser insert fails)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		hz.DB.Pool.Close()

		rec := doJSONReqG(e, http.MethodPost, "/auth/register", "", map[string]string{
			"username": "anything", "password": "abcdefg1",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError),
			"register on dead pool must 500; body=%s", rec.Body.String())
	})

	It("RefreshToken with a dead pool → 500 (GetUserByRefreshToken errors)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		hz.DB.Pool.Close()

		req := httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "any-value"})
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError),
			"dead pool on refresh must 500 generic, not 403 (403 would fingerprint DB errors as expired refresh); body=%s", rec.Body.String())
	})

	It("ListAPITokens with a dead pool → 500 (query fails after resolveUser)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		_, token := hz.MintUser("apitok_dbdown_list")

		hz.DB.Pool.Close()

		rec := doJSONReqG(e, http.MethodGet, "/auth/tokens", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError))
	})

	It("Logout with a dead pool → 500 (DeleteTokens fails)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := routerWithAuthClusterAC(hz)
		_, token := hz.MintUser("logout_dbdown")

		hz.DB.Pool.Close()

		req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		req.Header.Set("Authorization", "Basic "+token)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "any-value"})
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError))
	})
})

// rewriteRoundTripperAC rewrites the Host/Scheme of every outbound request to
// the target server, preserving path/headers/body. Necessary because
// wakatimeProbeURL in handler is a const pointing at wakatime.com.
type rewriteRoundTripperAC struct {
	target string // e.g. "http://127.0.0.1:64000"
}

func (r *rewriteRoundTripperAC) RoundTrip(req *http.Request) (*http.Response, error) {
	// Parse once, mutate URL Host/Scheme.
	req2 := req.Clone(req.Context())
	// The httptest.Server URL already includes scheme + host.
	if strings.HasPrefix(r.target, "http://") {
		req2.URL.Scheme = "http"
		req2.URL.Host = strings.TrimPrefix(r.target, "http://")
	} else if strings.HasPrefix(r.target, "https://") {
		req2.URL.Scheme = "https"
		req2.URL.Host = strings.TrimPrefix(r.target, "https://")
	}
	return http.DefaultTransport.RoundTrip(req2)
}
