// middleware_extra_test.go — coverage boost for the middleware trio
// (requestLogger, n1Middleware, userCtxMiddleware) and the two low-covered
// helpers (limiterFor unknown-group defense, bucketKey wakatime-probe with
// no lookup, userLookupFromDB nil-db fast path). gaka-d6x.
//
// These are UNIT tests that don't need Postgres for the middleware wiring
// pieces — real DB integration lives in server_integration_test.go.
package server

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
	"golang.org/x/time/rate"
)

// --- requestLogger --------------------------------------------------------

// TestRequestLogger_EmitsMethodPathStatusAndDurationOnEveryRequest pins the
// INVARIANT that every completed request produces exactly one INFO record
// carrying method, path, status, and dur_ms attributes — no matter whether
// the downstream handler wrote a status explicitly or accepted the default.
// This is the contract HTTP-log consumers (grep, ELK, LogHub) depend on.
func TestRequestLogger_EmitsMethodPathStatusAndDurationOnEveryRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	e := echo.New()
	e.Use(requestLogger(logger))
	e.GET("/explicit", func(c *echo.Context) error { return c.String(http.StatusTeapot, "brew") })
	e.GET("/implicit", func(c *echo.Context) error { return nil })

	srv := httptest.NewServer(e)
	defer srv.Close()

	// One request that sets an explicit status.
	res, err := srv.Client().Get(srv.URL + "/explicit")
	if err != nil {
		t.Fatalf("GET /explicit: %v", err)
	}
	res.Body.Close()
	// One request that leaves the default.
	res2, err := srv.Client().Get(srv.URL + "/implicit")
	if err != nil {
		t.Fatalf("GET /implicit: %v", err)
	}
	res2.Body.Close()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 log records, got %d: %s", len(lines), buf.String())
	}

	// Every record must contain every mandatory attr key. Missing keys
	// would break dashboards that pivot on `path` or `status`.
	for i, line := range lines {
		for _, key := range []string{`"msg":"http request"`, `"method":"GET"`, `"path":"/`, `"status":`, `"dur_ms":`} {
			if !strings.Contains(line, key) {
				t.Errorf("record %d missing %q; got: %s", i, key, line)
			}
		}
	}
	// The explicit one must carry status=418 (teapot); the implicit one
	// falls back to whatever Echo defaulted to (typically 200).
	if !strings.Contains(lines[0], `"status":418`) {
		t.Errorf("explicit record should carry status=418, got: %s", lines[0])
	}
}

// TestRequestLogger_PropagatesHandlerErrorWithoutSwallowing pins the
// INVARIANT that the middleware is transparent to handler errors — an error
// from the wrapped handler MUST be returned unchanged so Echo's error mapper
// still sees it. Silent swallowing here would turn 500s into fake 200s in
// prod.
func TestRequestLogger_PropagatesHandlerErrorWithoutSwallowing(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	e := echo.New()
	e.Use(requestLogger(logger))
	boom := echo.NewHTTPError(http.StatusForbidden, "nope")
	e.GET("/x", func(c *echo.Context) error { return boom })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (echo mapped from returned HTTPError), got %d", rec.Code)
	}
	if !strings.Contains(buf.String(), "http request") {
		t.Errorf("logger did not record the request; got: %s", buf.String())
	}
}

// --- n1Middleware ---------------------------------------------------------

// TestN1Middleware_NoOpWhenStatsSentinelMissing pins the INVARIANT that
// when a downstream middleware forks the request onto a fresh context and
// drops the req-stats sentinel installed by n1Middleware, the middleware
// silently no-ops rather than emitting a bogus "0 queries suspected" warn.
// Prevents spam when a future middleware misbehaves.
func TestN1Middleware_NoOpWhenStatsSentinelMissing(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	// Install BOTH n1Middleware and a middleware that strips the req-stats
	// context — simulating a bug where a downstream middleware forks the
	// request onto a fresh context.
	e := echo.New()
	e.Use(n1Middleware(logger, 1, 1))
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			// Replace request with a bare context — WithReqStats sentinel is gone.
			c.SetRequest(c.Request().WithContext(context.Background()))
			return next(c)
		}
	})
	e.GET("/x", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	e.ServeHTTP(rec, req)

	if strings.Contains(buf.String(), "db N+1 suspected") {
		t.Errorf("missing req-stats sentinel must NOT trigger phantom warn; got: %s", buf.String())
	}
}

// TestN1Middleware_InstallsStatsSentinelAndSilentUnderThreshold pins two
// coupled INVARIANTS:
//  1. The middleware MUST install a req-stats sentinel into ctx (proved by
//     the handler reading it back and confirming ok=true) — otherwise the
//     pgx tracer downstream can't record queries against the request.
//  2. With BOTH thresholds set high (or a real request that ran zero
//     queries), no WARN fires. Prevents log spam on healthz / static-only
//     requests that never touched the DB.
func TestN1Middleware_InstallsStatsSentinelAndSilentUnderThreshold(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	e := echo.New()
	e.Use(n1Middleware(logger, 100, 100))
	var sawSentinel bool
	e.GET("/x", func(c *echo.Context) error {
		_, _, _, ok := db.ReqStatsSummary(c.Request().Context())
		sawSentinel = ok
		return c.String(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if !sawSentinel {
		t.Fatalf("n1Middleware MUST install req-stats sentinel so pgx tracer can record queries")
	}
	if strings.Contains(buf.String(), "db N+1 suspected") {
		t.Errorf("zero-query request must never warn; got: %s", buf.String())
	}
}

// --- userCtxMiddleware (no-DB paths) --------------------------------------

// TestUserCtxMiddleware_MissingAuthHeaderIsFailOpen pins the security
// INVARIANT that a request with no Authorization header MUST NOT be denied
// (fail-open contract) AND MUST NOT stamp a user into the context (which
// would leak DEBUG SQL to whoever winds up "owner=" downstream).
func TestUserCtxMiddleware_MissingAuthHeaderIsFailOpen(t *testing.T) {
	e := echo.New()
	// Use a nil DB — the code path we're covering short-circuits before
	// touching the DB when the header is missing/empty.
	e.Use(userCtxMiddleware(nil))
	var stampedUser string
	e.GET("/x", func(c *echo.Context) error {
		stampedUser = db.UserFrom(c.Request().Context())
		return c.String(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("missing Authorization must not block request, got status %d", rec.Code)
	}
	if stampedUser != "" {
		t.Errorf("no auth header should mean no user in ctx, got %q", stampedUser)
	}
}

// TestUserCtxMiddleware_MalformedAuthHeaderIsFailOpen pins the INVARIANT
// that a garbage Authorization header (not "Bearer <tok>") is treated the
// same as "no header" — fail-open, no ctx user stamp. Prevents a downstream
// handler that reads UserFrom() from mis-attributing SQL to a fake owner.
func TestUserCtxMiddleware_MalformedAuthHeaderIsFailOpen(t *testing.T) {
	e := echo.New()
	e.Use(userCtxMiddleware(nil))
	var stampedUser string
	e.GET("/x", func(c *echo.Context) error {
		stampedUser = db.UserFrom(c.Request().Context())
		return c.String(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(echo.HeaderAuthorization, "Not-Bearer garbage")
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("malformed Authorization must not block request, got status %d", rec.Code)
	}
	if stampedUser != "" {
		t.Errorf("malformed auth header should mean no user in ctx, got %q", stampedUser)
	}
}

// --- limiterFor unknown-group defense -------------------------------------

// TestLimiterFor_UnknownGroupFallsBackToDefault pins the INVARIANT that
// asking the store for a bucket whose group is NOT registered (a code bug
// that should never happen but must not panic) yields the DEFAULT bucket
// instead of nil or a crash. This is the defensive branch at ratelimit.go
// L184-188 — the "shouldn't happen but be safe" fallback.
func TestLimiterFor_UnknownGroupFallsBackToDefault(t *testing.T) {
	s := &rateLimitStore{
		buckets: map[endpointGroup]*sync.Map{
			groupDefault: {},
		},
		configs: map[endpointGroup]bucketConfig{
			groupDefault: {Rate: 42, Burst: 7},
		},
		logger:     silentLogger(),
		userLookup: func(*echo.Context) string { return "" },
		stop:       make(chan struct{}),
	}

	// Unknown group must not panic and must return a working limiter.
	lim := s.limiterFor(endpointGroup("mystery-group"), "key")
	if lim == nil {
		t.Fatalf("unknown group must return a non-nil limiter (default fallback)")
	}
	// The returned limiter must obey the DEFAULT bucket's config — burst 7.
	// Drain 7, then the 8th must be denied. Anything else means we
	// silently swapped to a fake limiter with different limits.
	for i := 0; i < 7; i++ {
		if !lim.Allow() {
			t.Fatalf("default-fallback burst should be 7, denied at %d", i+1)
		}
	}
	if lim.Allow() {
		t.Errorf("8th call must be denied — default burst is 7")
	}
	// The stored entry must live under groupDefault, NOT under the bogus
	// group name — otherwise a repeat call to the bogus group would create
	// a fresh limiter every time, defeating the fallback.
	if _, ok := s.buckets[groupDefault].Load("key"); !ok {
		t.Errorf("fallback should have stored the entry under groupDefault, not the bogus group")
	}
}

// --- bucketKey no-lookup fallback -----------------------------------------

// TestBucketKey_WakatimeProbeFallsBackToIPWhenUserLookupFails pins the
// INVARIANT: even for the wakatime-probe group (which PREFERS a user key),
// an unauthenticated request MUST NOT skip the limiter — the middleware
// falls back to an IP key so anonymous abuse still hits a bucket. Silent
// disable here would let attackers spray wakatime.com through an unauth'd
// path if one ever existed.
func TestBucketKey_WakatimeProbeFallsBackToIPWhenUserLookupFails(t *testing.T) {
	s := &rateLimitStore{
		buckets:    map[endpointGroup]*sync.Map{groupWakatimeProbe: {}, groupDefault: {}},
		configs:    map[endpointGroup]bucketConfig{groupWakatimeProbe: {Rate: rate.Every(12), Burst: 5}, groupDefault: {Rate: 60, Burst: 60}},
		logger:     silentLogger(),
		userLookup: func(*echo.Context) string { return "" }, // always miss
		stop:       make(chan struct{}),
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/wakatime_key", nil)
	req.RemoteAddr = "198.51.100.9:12345"
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	key := s.bucketKey(c, groupWakatimeProbe)
	if !strings.HasPrefix(key, "ip:") {
		t.Errorf("no user resolved → key should start with ip:, got %q", key)
	}
}

// TestBucketKey_AuthenticatedRequestBucketsPerUserNotIP pins the security
// INVARIANT: once the caller has a token that resolves to a user, the
// bucket key MUST be user-scoped so multi-IP abuse from ONE account still
// hits the same bucket. If we bucketed by IP after auth, an attacker with
// a stolen token could rotate proxies to reset the counter.
func TestBucketKey_AuthenticatedRequestBucketsPerUserNotIP(t *testing.T) {
	s := &rateLimitStore{
		buckets:    map[endpointGroup]*sync.Map{groupDefault: {}},
		configs:    map[endpointGroup]bucketConfig{groupDefault: {Rate: 60, Burst: 60}},
		logger:     silentLogger(),
		userLookup: func(*echo.Context) string { return "panda" },
		stop:       make(chan struct{}),
	}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/stats", nil)
	req.RemoteAddr = "203.0.113.99:12345"
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	key := s.bucketKey(c, groupDefault)
	if key != "user:panda" {
		t.Errorf("authenticated request should bucket by user, got %q", key)
	}
}

// TestBucketKey_WakatimeProbeUsesUserKeyWhenAuthenticated pins the
// INVARIANT: the wakatime-probe group specifically checks for a user
// FIRST (multi-IP abuse of one account still throttles). This exercises
// the "if group == groupWakatimeProbe && owner != ''" branch that's
// otherwise unreachable in the fallback tests.
func TestBucketKey_WakatimeProbeUsesUserKeyWhenAuthenticated(t *testing.T) {
	s := &rateLimitStore{
		buckets:    map[endpointGroup]*sync.Map{groupWakatimeProbe: {}, groupDefault: {}},
		configs:    map[endpointGroup]bucketConfig{groupWakatimeProbe: {Rate: rate.Every(12), Burst: 5}, groupDefault: {Rate: 60, Burst: 60}},
		logger:     silentLogger(),
		userLookup: func(*echo.Context) string { return "alice" },
		stop:       make(chan struct{}),
	}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/wakatime_key", nil)
	req.RemoteAddr = "203.0.113.9:12345"
	c := e.NewContext(req, httptest.NewRecorder())

	key := s.bucketKey(c, groupWakatimeProbe)
	if key != "user:alice" {
		t.Errorf("wakatime-probe with auth should bucket by user, got %q", key)
	}
}

// --- userLookupFromDB nil-db fast path ------------------------------------

// TestUserLookupFromDB_NilDBReturnsEmptyString pins the INVARIANT that a
// nil DB reference (production defense; also useful for tests that install
// the middleware without a live DB) yields a lookup that always returns "" —
// causing the middleware to bucket by IP. Anything else (panic, DB call
// against nil pool) would crash the whole request pipeline at startup.
func TestUserLookupFromDB_NilDBReturnsEmptyString(t *testing.T) {
	lookup := userLookupFromDB(nil)
	if lookup == nil {
		t.Fatalf("userLookupFromDB(nil) must return a non-nil callback")
	}
	// Call it with a fake context — must return "" without touching a DB.
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderAuthorization, "Basic somebody-somewhere")
	c := e.NewContext(req, httptest.NewRecorder())
	if got := lookup(c); got != "" {
		t.Errorf("nil-DB lookup must return \"\", got %q", got)
	}
}

// --- installRateLimit production path -------------------------------------

// TestInstallRateLimit_EnabledPathWiresStoreAndBuckets pins the INVARIANT
// that when the disable env is NOT set, installRateLimit returns a
// non-nil store, configures every default group, and installs middleware
// that ACTUALLY 429s on burst exhaustion. This is the production-mode
// smoke test — the previous ginkgo tests only prove the DISABLED path.
func TestInstallRateLimit_EnabledPathWiresStoreAndBuckets(t *testing.T) {
	// Make sure the disable env is unset for the duration of this test.
	if v, ok := os.LookupEnv(rateLimitDisableEnv); ok {
		os.Unsetenv(rateLimitDisableEnv)
		defer os.Setenv(rateLimitDisableEnv, v)
	}
	// Speed up: shrink the auth-write burst via env so we don't have to
	// send hundreds of requests to trip it.
	t.Setenv("BOOM_RATELIMIT_AUTH_WRITE_BURST", "3")
	t.Setenv("BOOM_RATELIMIT_AUTH_WRITE_RATE", "0.001") // essentially "no refill for the test window"

	e := echo.New()
	store := installRateLimit(e, silentLogger(), nil)
	if store == nil {
		t.Fatal("installRateLimit must return non-nil store when NOT disabled")
	}
	// The three default groups MUST be present.
	for _, g := range []endpointGroup{groupAuthWrite, groupWakatimeProbe, groupDefault} {
		if _, ok := store.configs[g]; !ok {
			t.Errorf("store should have config for group %q", g)
		}
		if _, ok := store.buckets[g]; !ok {
			t.Errorf("store should have bucket map for group %q", g)
		}
	}
	// Ensure our env override actually took effect.
	if store.configs[groupAuthWrite].Burst != 3 {
		t.Errorf("BOOM_RATELIMIT_AUTH_WRITE_BURST=3 not applied, got burst=%d", store.configs[groupAuthWrite].Burst)
	}

	e.POST("/auth/login", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })
	srv := httptest.NewServer(e)
	defer srv.Close()

	// Send 5 requests: 3 should pass (burst=3), then at least 1 must 429.
	successes, throttled := 0, 0
	for i := 0; i < 5; i++ {
		res, err := srv.Client().Post(srv.URL+"/auth/login", "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("req %d: %v", i, err)
		}
		if res.StatusCode == http.StatusTooManyRequests {
			throttled++
		} else if res.StatusCode == http.StatusOK {
			successes++
		}
		res.Body.Close()
	}
	if successes < 1 || successes > 3 {
		t.Errorf("expected 1..3 successes (burst=3), got %d", successes)
	}
	if throttled < 1 {
		t.Errorf("expected at least one 429 on 5 requests with burst=3, got 0")
	}
}
