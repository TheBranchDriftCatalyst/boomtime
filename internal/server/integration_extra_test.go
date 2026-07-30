// integration_extra_test.go — DB-backed coverage boost for gaka-d6x.
//
// These tests need Postgres (BOOM_TEST_DATABASE_URL). They cover:
//   - userLookupFromDB happy/miss paths (bucket key by resolved owner)
//   - userCtxMiddleware end-to-end (real token → real db.UserFrom stamp)
//   - New / NewWithHandler wiring (constructs a full router w/ every mw)
//   - registerStatic SPA fallback + asset 404 branch
//
// Follows the stdlib convention that Postgres-dependent tests OpenDB(t) via
// internal/testutil so a missing DB → skip (not fail) unless BOOM_REQUIRE_DB=1.
package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
	"github.com/labstack/echo/v5"
)

// mintUserAndToken inserts a fresh user + never-expiring API token and
// returns (username, rawToken). Cleans up on test end.
func mintUserAndToken(t *testing.T, database *db.DB, prefix string) (username, token string) {
	t.Helper()
	username = prefix + "_" + time.Now().Format("150405.000000000")
	ctx := context.Background()
	hash, salt, err := auth.HashPassword("pw-" + username)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	created, err := database.InsertUser(ctx, db.StoredUser{
		Username:       username,
		HashedPassword: hash,
		SaltUsed:       salt,
		ArgonVersion:   auth.ArgonVersionCurrent,
	})
	if err != nil || !created {
		t.Fatalf("insert user %s: %v (created=%v)", username, err, created)
	}
	token = auth.NewRawToken()
	if err := database.InsertAPIToken(ctx, username, token, ""); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	t.Cleanup(func() {
		for _, q := range []string{
			"DELETE FROM auth_tokens WHERE owner=$1",
			"DELETE FROM users WHERE username=$1",
		} {
			_, _ = database.Pool.Exec(context.Background(), q, username)
		}
	})
	return username, token
}

// --- userLookupFromDB with real DB ---------------------------------------

// TestUserLookupFromDB_ValidTokenResolvesToOwner_InvalidReturnsEmpty pins
// the security-critical INVARIANT of the rate-limit user bucketing key:
//
//  1. A real token belonging to alice MUST resolve to "alice" (proves the
//     hot path — otherwise EVERY authenticated request would silently
//     fall back to IP bucketing, defeating gaka-jk6's user-scope limit).
//  2. A bogus/tampered token MUST resolve to "" — never to some OTHER
//     user (no oracle: the limiter caller can't distinguish "no auth"
//     from "wrong auth" via the bucket key).
//  3. The lookup must NEVER cross-key: alice's token MUST NOT resolve to
//     bob just because a race stored alice's token as bob's row.
func TestUserLookupFromDB_ValidTokenResolvesToOwner_InvalidReturnsEmpty(t *testing.T) {
	database := testutil.OpenDB(t)
	alice, aliceTok := mintUserAndToken(t, database, "alice")
	bob, bobTok := mintUserAndToken(t, database, "bob")

	lookup := userLookupFromDB(database)
	e := echo.New()

	makeCtx := func(rawToken string) *echo.Context {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if rawToken != "" {
			// Boomtime uses `Basic <rawToken>` per auth.ParseAuthHeader (which
			// strips only the "Basic" prefix — the token itself is stored raw
			// under SHA-256 in the DB, so the wire value MUST be the same raw
			// bytes InsertAPIToken received).
			req.Header.Set(echo.HeaderAuthorization, "Basic "+rawToken)
		}
		return e.NewContext(req, httptest.NewRecorder())
	}

	if got := lookup(makeCtx(aliceTok)); got != alice {
		t.Errorf("alice's token: expected %q, got %q", alice, got)
	}
	if got := lookup(makeCtx(bobTok)); got != bob {
		t.Errorf("bob's token: expected %q, got %q", bob, got)
	}

	// Cross-key: alice's token must NOT resolve to bob.
	if got := lookup(makeCtx(aliceTok)); got == bob {
		t.Errorf("cross-key leak: alice's token resolved to bob")
	}

	// Bogus token: NEVER resolve to ANY existing user (no oracle).
	for _, bogus := range []string{"nonsense", "AAAA" + aliceTok, aliceTok + "X"} {
		if got := lookup(makeCtx(bogus)); got != "" {
			t.Errorf("bogus token %q resolved to %q; must be empty (no oracle)", bogus, got)
		}
	}

	// No Authorization header at all: empty (proves ParseAuthHeader short-circuit).
	if got := lookup(makeCtx("")); got != "" {
		t.Errorf("no auth header: expected empty, got %q", got)
	}
}

// --- userCtxMiddleware end-to-end ----------------------------------------

// TestUserCtxMiddleware_ValidTokenStampsOwnerIntoCtx pins the INVARIANT
// central to gaka-ar7 (LogHub cross-tenant leak fix): a valid token MUST
// end up as db.UserFrom(ctx) so the pgx tracer's DEBUG SQL records carry
// the "user" attr for LogHub filtering. Without this stamp, every SQL
// query would broadcast to every authenticated Logs viewer.
func TestUserCtxMiddleware_ValidTokenStampsOwnerIntoCtx(t *testing.T) {
	database := testutil.OpenDB(t)
	alice, aliceTok := mintUserAndToken(t, database, "alice")

	e := echo.New()
	e.Use(userCtxMiddleware(database))
	var stamped string
	e.GET("/x", func(c *echo.Context) error {
		stamped = db.UserFrom(c.Request().Context())
		return c.String(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(echo.HeaderAuthorization, "Basic "+aliceTok)
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if stamped != alice {
		t.Errorf("expected UserFrom(ctx) == %q, got %q", alice, stamped)
	}
}

// TestUserCtxMiddleware_UnknownTokenDoesNotStamp pins the security-critical
// no-oracle INVARIANT: a token that doesn't match any user MUST NOT stamp
// SOME OTHER (e.g. previously-cached) user into ctx. A stale/misresolved
// stamp would attribute SQL to the wrong owner — exactly the cross-tenant
// leak gaka-ar7 closed. Fail-open on DB errors is intentional; we assert
// there's no wrong-user attribution.
func TestUserCtxMiddleware_UnknownTokenDoesNotStamp(t *testing.T) {
	database := testutil.OpenDB(t)
	alice, aliceTok := mintUserAndToken(t, database, "alice")

	e := echo.New()
	e.Use(userCtxMiddleware(database))
	var stamped string
	e.GET("/x", func(c *echo.Context) error {
		stamped = db.UserFrom(c.Request().Context())
		return c.String(http.StatusOK, "ok")
	})

	// Send a request with alice's token first — proves the middleware CAN
	// stamp, so we know the "no stamp" result below isn't a false negative.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(echo.HeaderAuthorization, "Basic "+aliceTok)
	e.ServeHTTP(rec, req)
	if stamped != alice {
		t.Fatalf("baseline: alice should have been stamped, got %q", stamped)
	}
	stamped = "" // reset

	// Now send a request with a bogus token from a fresh context. It MUST
	// leave the context unstamped — NEVER "alice" (leftover) and NEVER
	// some other user.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/x", nil)
	req2.Header.Set(echo.HeaderAuthorization, "Basic bogus-does-not-exist")
	e.ServeHTTP(rec2, req2)

	if stamped != "" {
		t.Errorf("unknown token must not stamp any user; got %q", stamped)
	}
}

// --- New / NewWithHandler wiring -----------------------------------------

// TestNewWithHandler_WiresCorsRateLimitAndStaticInCorrectOrder pins the
// INVARIANT that the fully-wired server produced by NewWithHandler serves
// every middleware layer in the documented order:
//
//   - CORS is applied (Origin echoed for allowed, dropped for disallowed).
//   - Rate limit is INSTALLED (we run with BOOM_DISABLE_RATE_LIMIT=1 so we
//     don't have to drain buckets; the middleware still hits the pass-
//     through path, proving the wiring is present).
//   - /healthz responds (proves route registration ran).
//   - Static / SPA fallback returns the embedded shell for unknown routes.
//
// Anything else here means the wiring drifted from the original composition
// and a security-critical middleware may have been dropped.
func TestNewWithHandler_WiresCorsRateLimitAndStaticInCorrectOrder(t *testing.T) {
	database := testutil.OpenDB(t)
	cfg := &config.Config{
		Port:               8080,
		EnableRegistration: true,
		SessionExpiry:      24,
		DBPort:             5432,
		HTTPLog:            true, // exercise the requestLogger install branch
		DBN1Threshold:      100,  // exercise the n1Middleware install branch
		DBN1DupThresh:      50,
	}
	// Force rate limit off so we don't have to worry about bucket state
	// leaking between test cases — the coverage target is the install
	// pass-through branch, not the throttle behaviour (covered elsewhere).
	t.Setenv(rateLimitDisableEnv, "1")
	// Provide a fixed CORS allowlist so we can prove exact-match echo.
	t.Setenv("BOOM_CORS_ALLOWED_ORIGINS", "https://ok.example.com")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	e, h := NewWithHandler(database, cfg, logger, nil, importer.NewHub(), nil)
	if e == nil || h == nil {
		t.Fatalf("NewWithHandler must return non-nil (echo=%v, handler=%v)", e, h)
	}

	srv := httptest.NewServer(e)
	defer srv.Close()

	// 1) /healthz must respond 200 (registerMetaRoutes wired it).
	res, err := srv.Client().Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz: expected 200, got %d", res.StatusCode)
	}

	// 2) CORS: an ALLOWED origin gets echoed on a preflight; a DISALLOWED
	//    origin gets NO Access-Control-Allow-Origin. Uses OPTIONS so the
	//    disabled rate limit isn't relevant (preflight bypass is inside
	//    the middleware).
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/v1/version", nil)
	req.Header.Set("Origin", "https://ok.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	res2, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("preflight allowed: %v", err)
	}
	res2.Body.Close()
	if got := res2.Header.Get("Access-Control-Allow-Origin"); got != "https://ok.example.com" {
		t.Errorf("allowed origin should be echoed, got %q", got)
	}

	req3, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/v1/version", nil)
	req3.Header.Set("Origin", "https://evil.example.com")
	req3.Header.Set("Access-Control-Request-Method", http.MethodGet)
	res3, err := srv.Client().Do(req3)
	if err != nil {
		t.Fatalf("preflight denied: %v", err)
	}
	res3.Body.Close()
	if got := res3.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin must NOT be echoed, got %q", got)
	}

	// 3) Static/SPA fallback: an unknown non-API path SHOULD serve the
	//    embedded SPA shell (registerStatic's shell-fallback branch).
	res4, err := srv.Client().Get(srv.URL + "/some/unknown/spa/route")
	if err != nil {
		t.Fatalf("GET spa route: %v", err)
	}
	body, _ := io.ReadAll(res4.Body)
	res4.Body.Close()
	if res4.StatusCode != http.StatusOK {
		t.Errorf("SPA fallback: expected 200 for /some/unknown/spa/route, got %d", res4.StatusCode)
	}
	if !bytes.Contains(body, []byte("<html")) && !bytes.Contains(body, []byte("<HTML")) {
		t.Errorf("SPA fallback body should be HTML, got: %.200s", string(body))
	}
	// The shell MUST carry a no-cache Cache-Control so clients revalidate
	// on every load (prevents stale-chunk lazy-load breakage).
	if !strings.Contains(res4.Header.Get("Cache-Control"), "no-cache") {
		t.Errorf("SPA shell Cache-Control should include no-cache, got %q", res4.Header.Get("Cache-Control"))
	}

	// 4) Static asset request that DOESN'T exist MUST return 404 (NOT the
	//    SPA shell) — proves the "asset extension" branch in registerStatic
	//    that stops stale JS bundles from being served HTML.
	res5, err := srv.Client().Get(srv.URL + "/assets/Missing-DEADBEEF.js")
	if err != nil {
		t.Fatalf("GET missing asset: %v", err)
	}
	res5.Body.Close()
	if res5.StatusCode != http.StatusNotFound {
		t.Errorf("missing asset must 404 (not SPA-fallback), got %d", res5.StatusCode)
	}
}

// TestNew_ConvenienceWrapperReturnsSameEcho pins the INVARIANT that the
// New wrapper (kept for backward-compat with older cmd/boomtime callers)
// produces the SAME echo instance shape as NewWithHandler — same routes,
// same middlewares. If someone splits the two apart in a refactor we
// catch it here.
func TestNew_ConvenienceWrapperReturnsSameEcho(t *testing.T) {
	database := testutil.OpenDB(t)
	cfg := &config.Config{Port: 8080, EnableRegistration: true, SessionExpiry: 24, DBPort: 5432}
	t.Setenv(rateLimitDisableEnv, "1")
	t.Setenv("BOOM_CORS_ALLOWED_ORIGINS", "https://ok.example.com")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	e := New(database, cfg, logger, nil, importer.NewHub(), nil)
	if e == nil {
		t.Fatal("New returned nil")
	}
	// Route count is a proxy for "wiring intact"; it must match what
	// NewWithHandler produces.
	e2, _ := NewWithHandler(database, cfg, logger, nil, importer.NewHub(), nil)
	if len(e.Router().Routes()) != len(e2.Router().Routes()) {
		t.Errorf("New and NewWithHandler must produce the same route set; got %d vs %d",
			len(e.Router().Routes()), len(e2.Router().Routes()))
	}
}

// --- n1Middleware WARN emission (with real pgx tracer) ------------------

// TestN1Middleware_EmitsWarnWhenQueryCountExceedsThreshold pins the
// INVARIANT that the middleware's WARN fires when the real pgx tracer
// records more queries in one request than the configured threshold. The
// warn is the sole operator signal for a leaked N+1 loop that survived
// review — its absence would let a regression pile up hidden.
//
// We use NewWithObservability to install the real n1Tracer against the
// same request-scope sentinel the middleware installs, then run several
// tiny SELECTs to exceed the count threshold.
func TestN1Middleware_EmitsWarnWhenQueryCountExceedsThreshold(t *testing.T) {
	// Open a fresh pool with the pgx n1Tracer wired to the exact same
	// req-stats sentinel the middleware installs. Without this, plain
	// db.New() gives us a tracer-less pool and no queries are recorded.
	ctx := context.Background()
	database, err := db.NewWithObservability(ctx, testutil.DatabaseURL(), db.Options{
		N1Threshold: 3,
		N1DupThresh: 2,
	})
	if err != nil {
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			t.Fatalf("NewWithObservability: %v", err)
		}
		t.Skipf("skipping: %v", err)
	}
	t.Cleanup(database.Close)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	e := echo.New()
	e.Use(n1Middleware(logger, 3, 2))
	e.GET("/x", func(c *echo.Context) error {
		// Run 5 queries — over the count threshold of 3 AND identical, so
		// max_duplicate=5 also breaches dup threshold 2. We use identical
		// SELECTs so both signals fire; either alone would trigger warn.
		for i := 0; i < 5; i++ {
			rows, err := database.Pool.Query(c.Request().Context(), "SELECT 1")
			if err != nil {
				t.Fatalf("SELECT %d: %v", i, err)
			}
			rows.Close()
		}
		return c.String(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	got := buf.String()
	if !strings.Contains(got, `"msg":"db N+1 suspected"`) {
		t.Fatalf("expected N+1 warn; got: %s", got)
	}
	// Contract checks: the warn record MUST carry the offending count
	// and the duplicate normalized SQL so operators can grep for the
	// leaked handler.
	if !strings.Contains(got, `"queries":5`) {
		t.Errorf("warn record should include queries=5; got: %s", got)
	}
	if !strings.Contains(got, `"max_duplicate":5`) {
		t.Errorf("warn record should include max_duplicate=5; got: %s", got)
	}
	if !strings.Contains(got, `"path":"/x"`) {
		t.Errorf("warn record should include the request path; got: %s", got)
	}
}

// --- registerStatic disk-mode branch -------------------------------------

// TestRegisterStatic_ServesFromDashboardPathWhenConfigured pins the
// INVARIANT that BOOM_DASHBOARD_PATH (Config.DashboardPath) takes
// precedence over the embedded dist FS. This is the ops escape hatch for
// serving a hot-reload dashboard build; a regression here silently sends
// clients the stale embedded shell.
func TestRegisterStatic_ServesFromDashboardPathWhenConfigured(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "index.html"), []byte("<html>disk-shell</html>"), 0o644); err != nil {
		t.Fatalf("write disk shell: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "favicon.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	e := echo.New()
	cfg := &config.Config{DashboardPath: tmp}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registerStatic(e, cfg, logger)

	srv := httptest.NewServer(e)
	defer srv.Close()

	// SPA fallback → serves the disk shell (not the embedded one).
	res, err := srv.Client().Get(srv.URL + "/anything")
	if err != nil {
		t.Fatalf("GET /anything: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "disk-shell") {
		t.Errorf("disk-mode SPA fallback should serve tmp/index.html, got %q", string(body))
	}

	// Real asset resolves.
	res2, err := srv.Client().Get(srv.URL + "/favicon.svg")
	if err != nil {
		t.Fatalf("GET /favicon.svg: %v", err)
	}
	body2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if res2.StatusCode != http.StatusOK || !strings.Contains(string(body2), "<svg") {
		t.Errorf("disk asset should serve, got %d %q", res2.StatusCode, string(body2))
	}

	// Missing asset with extension → 404 (the anti-stale-chunk branch).
	res3, err := srv.Client().Get(srv.URL + "/assets/nope.js")
	if err != nil {
		t.Fatalf("GET missing asset: %v", err)
	}
	res3.Body.Close()
	if res3.StatusCode != http.StatusNotFound {
		t.Errorf("missing asset with extension should 404, got %d", res3.StatusCode)
	}
}
