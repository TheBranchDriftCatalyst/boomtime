// Package server: rate-limit middleware tests, three layers.
//
// Non-tautological test layering (per orchestrator directive):
//
//  1. UNIT tests exercise the real *rate.Limiter and the real sync.Map bucket
//     store — bucket math (11th request denied under a 10-burst), TTL
//     eviction (real time.Time cutoffs against real lastSeen stamps),
//     endpoint classification, and the BOOM_DISABLE_RATE_LIMIT bypass. These
//     do NOT construct an echo Context — they call limiterFor / classify /
//     evictOlderThan directly. Catches: limiter arithmetic regressions,
//     classifier regressions, TTL sweeper regressions, env-toggle
//     regressions. Does NOT catch: middleware wiring bugs, response shape
//     bugs, cross-IP bucketing at the request level.
//
//  2. INTEGRATION tests spin up a real echo.Echo, install the real
//     middleware via the real store, and drive it with net/http/httptest
//     requests that flow through the router. Sends 12 requests, asserts
//     10×200 then 2×429, asserts Retry-After header + JSON body shape.
//     Then sends from a second RemoteAddr and asserts a fresh bucket.
//     Then hits /auth/login until throttled and hits POST wakatime_key
//     independently to prove group isolation. Catches: middleware install
//     order (would fail if we ever put it AFTER the handler), 429 envelope
//     regressions, RealIP() plumbing, cross-IP bucket isolation. Does NOT
//     catch: real-network timing behavior, real echo-server startup errors,
//     coexistence with CORS preflight in a browser.
//
//  3. E2E is run manually via the curl loop in the beads report — the
//     backend must be live on :8080. We do NOT commit a flaky wall-clock
//     test to CI; the report captures the raw curl output as evidence.
//     Catches: real HTTP client behavior, real reverse-proxy path (kubelet
//     probe, OPTIONS preflight), timing under real syscall latency.
package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"golang.org/x/time/rate"
)

// ─────────────────────────────── Layer 1: UNIT ────────────────────────────────

// TestClassifyEndpoint locks in the bucket routing decisions. If a caller
// renames an auth route without touching the classifier, this fires.
func TestClassifyEndpoint(t *testing.T) {
	cases := []struct {
		method, path string
		want         endpointGroup
	}{
		{"POST", "/auth/login", groupAuthWrite},
		{"POST", "/auth/register", groupAuthWrite},
		{"POST", "/auth/refresh_token", groupAuthWrite},
		{"POST", "/api/v1/users/current/password", groupAuthWrite},
		{"POST", "/api/v1/users/current/wakatime_key", groupWakatimeProbe},
		// GET wakatime_key is not a probe — reads shouldn't share the probe budget.
		{"GET", "/api/v1/users/current/wakatime_key", groupDefault},
		// change-password only when POST — a GET would just fall to default.
		{"GET", "/api/v1/users/current/password", groupDefault},
		// Everything else lands in default.
		{"GET", "/api/v1/users/current/stats", groupDefault},
		{"POST", "/api/v1/users/current/heartbeats", groupDefault},
	}
	for _, c := range cases {
		if got := classifyEndpoint(c.method, c.path); got != c.want {
			t.Fatalf("%s %s: got %q want %q", c.method, c.path, got, c.want)
		}
	}
}

// TestLimiterForAllowThenDeny exercises the REAL rate.Limiter through the
// store: 10-burst, refill at once per 6s. First 10 Allow() calls return true;
// the 11th returns false. This is NOT a mock — it actually invokes the
// tokens/burst arithmetic in golang.org/x/time/rate.
func TestLimiterForAllowThenDeny(t *testing.T) {
	s := &rateLimitStore{
		buckets: map[endpointGroup]*sync.Map{
			groupAuthWrite: {},
			groupDefault:   {},
		},
		configs: map[endpointGroup]bucketConfig{
			groupAuthWrite: {Rate: rate.Every(6 * time.Second), Burst: 10},
			groupDefault:   {Rate: 60, Burst: 60},
		},
		logger:     silentLogger(),
		userLookup: func(*echo.Context) string { return "" },
		stop:       make(chan struct{}),
	}
	// Note: we intentionally do NOT start cleanupLoop — the test drives
	// eviction directly in TestEvictOlderThan.
	lim := s.limiterFor(groupAuthWrite, "ip:1.2.3.4")
	for i := 0; i < 10; i++ {
		if !lim.Allow() {
			t.Fatalf("request %d unexpectedly denied under burst=10", i+1)
		}
	}
	if lim.Allow() {
		t.Fatalf("11th request must be denied (burst exhausted)")
	}
	// Sanity: a DIFFERENT key gets a fresh limiter.
	other := s.limiterFor(groupAuthWrite, "ip:9.9.9.9")
	if !other.Allow() {
		t.Fatalf("fresh key must have full burst available")
	}
	if lim == other {
		t.Fatalf("distinct keys must produce distinct limiters")
	}
}

// TestLimiterForGroupIsolation proves that draining the auth-write budget
// does NOT affect the wakatime-probe budget for the same key.
func TestLimiterForGroupIsolation(t *testing.T) {
	s := &rateLimitStore{
		buckets: map[endpointGroup]*sync.Map{
			groupAuthWrite:     {},
			groupWakatimeProbe: {},
			groupDefault:       {},
		},
		configs: map[endpointGroup]bucketConfig{
			groupAuthWrite:     {Rate: rate.Every(6 * time.Second), Burst: 10},
			groupWakatimeProbe: {Rate: rate.Every(12 * time.Second), Burst: 5},
			groupDefault:       {Rate: 60, Burst: 60},
		},
		logger:     silentLogger(),
		userLookup: func(*echo.Context) string { return "" },
		stop:       make(chan struct{}),
	}
	aw := s.limiterFor(groupAuthWrite, "user:panda")
	wk := s.limiterFor(groupWakatimeProbe, "user:panda")
	// Drain auth-write completely.
	for i := 0; i < 10; i++ {
		_ = aw.Allow()
	}
	if aw.Allow() {
		t.Fatalf("auth-write must be exhausted")
	}
	// Wakatime-probe must still have its full burst of 5.
	for i := 0; i < 5; i++ {
		if !wk.Allow() {
			t.Fatalf("wakatime-probe req %d denied — group isolation broken", i+1)
		}
	}
	if wk.Allow() {
		t.Fatalf("wakatime-probe burst should now be exhausted at 5")
	}
}

// TestEvictOlderThan drives the sweeper directly. Two buckets, one is aged
// past the cutoff, one is fresh. After evictOlderThan, only the fresh one
// survives. This exercises the real sync.Map.Range + Delete path.
func TestEvictOlderThan(t *testing.T) {
	s := &rateLimitStore{
		buckets: map[endpointGroup]*sync.Map{
			groupDefault: {},
		},
		configs: map[endpointGroup]bucketConfig{
			groupDefault: {Rate: 60, Burst: 60},
		},
		logger:     silentLogger(),
		userLookup: func(*echo.Context) string { return "" },
		stop:       make(chan struct{}),
	}
	_ = s.limiterFor(groupDefault, "ip:stale")
	_ = s.limiterFor(groupDefault, "ip:fresh")
	// Age the stale entry to well before the cutoff.
	if v, ok := s.buckets[groupDefault].Load("ip:stale"); ok {
		v.(*rateLimiterEntry).lastSeen.Store(time.Now().Add(-1 * time.Hour).UnixNano())
	}
	s.evictOlderThan(time.Now().Add(-10 * time.Minute))
	if _, ok := s.buckets[groupDefault].Load("ip:stale"); ok {
		t.Fatalf("stale entry should have been evicted")
	}
	if _, ok := s.buckets[groupDefault].Load("ip:fresh"); !ok {
		t.Fatalf("fresh entry should have survived")
	}
}

// TestBucketFromEnv covers env override parsing. Malformed values must not
// clobber the default (silent-drop + WARN policy).
func TestBucketFromEnv(t *testing.T) {
	def := bucketConfig{Rate: rate.Every(6 * time.Second), Burst: 10}
	// Valid overrides.
	t.Setenv("BOOM_RATELIMIT_AUTH_WRITE_RATE", "0.5")
	t.Setenv("BOOM_RATELIMIT_AUTH_WRITE_BURST", "20")
	got := bucketFromEnv(groupAuthWrite, def, silentLogger())
	if float64(got.Rate) != 0.5 || got.Burst != 20 {
		t.Fatalf("env overrides ignored: got %+v", got)
	}
	// Malformed rate → default kept.
	t.Setenv("BOOM_RATELIMIT_AUTH_WRITE_RATE", "abc")
	t.Setenv("BOOM_RATELIMIT_AUTH_WRITE_BURST", "-5")
	got = bucketFromEnv(groupAuthWrite, def, silentLogger())
	if got.Rate != def.Rate || got.Burst != def.Burst {
		t.Fatalf("malformed env should not override default: got %+v want %+v", got, def)
	}
}

// TestInstallRateLimitDisabled verifies the BOOM_DISABLE_RATE_LIMIT bypass
// installs a genuine pass-through: 1000 requests to /auth/login never 429.
func TestInstallRateLimitDisabled(t *testing.T) {
	t.Setenv(rateLimitDisableEnv, "1")
	e := echo.New()
	if store := installRateLimit(e, silentLogger(), nil); store != nil {
		t.Fatalf("installRateLimit must return nil store when disabled")
	}
	e.POST("/auth/login", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	srv := httptest.NewServer(e)
	defer srv.Close()
	client := srv.Client()
	for i := 0; i < 1000; i++ {
		res, err := client.Post(srv.URL+"/auth/login", "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("request %d hit 429 despite disable env — bypass broken", i+1)
		}
	}
}

// ─────────────────────────── Layer 2: INTEGRATION ─────────────────────────────

// TestIntegration_BucketKicksInAt429 spins up a real echo server with the
// real middleware. 10 requests succeed, the 11th and 12th are 429, and the
// Retry-After header is present and > 0, and the body has the {error,
// retryAfter} shape.
func TestIntegration_BucketKicksInAt429(t *testing.T) {
	e := echo.New()
	store := newRateLimitStore(silentLogger(), func(*echo.Context) string { return "" })
	e.Use(store.middleware())
	e.POST("/auth/login", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	srv := httptest.NewServer(e)
	defer srv.Close()

	successes, throttled := 0, 0
	var lastRetryAfter string
	var lastBody map[string]any
	for i := 0; i < 12; i++ {
		req, _ := http.NewRequest("POST", srv.URL+"/auth/login", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		// Force the same client IP so all 12 hit the same bucket.
		req.RemoteAddr = "203.0.113.7:12345"
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("req %d: %v", i, err)
		}
		if res.StatusCode == http.StatusTooManyRequests {
			throttled++
			lastRetryAfter = res.Header.Get("Retry-After")
			body, _ := io.ReadAll(res.Body)
			_ = json.Unmarshal(body, &lastBody)
		} else if res.StatusCode == http.StatusOK {
			successes++
		} else {
			t.Fatalf("req %d unexpected status %d", i, res.StatusCode)
		}
		res.Body.Close()
	}
	// Note: because httptest sets RemoteAddr on the *server* side and the
	// echo.RealIP() extractor is nil by default, all requests land in the
	// same "ip:" bucket (the server-side conn RemoteAddr). So exactly 10
	// succeed, 2 throttle.
	if successes != 10 {
		t.Fatalf("expected 10 successes, got %d", successes)
	}
	if throttled != 2 {
		t.Fatalf("expected 2 throttled, got %d", throttled)
	}
	if lastRetryAfter == "" {
		t.Fatalf("Retry-After header missing on 429")
	}
	if n, err := strconv.Atoi(lastRetryAfter); err != nil || n < 1 {
		t.Fatalf("Retry-After should be positive integer, got %q", lastRetryAfter)
	}
	if lastBody["error"] != "rate limited" {
		t.Fatalf("body.error = %v, want %q", lastBody["error"], "rate limited")
	}
	if _, ok := lastBody["retryAfter"]; !ok {
		t.Fatalf("body.retryAfter missing: %v", lastBody)
	}
}

// TestIntegration_DifferentIPsSeparateBuckets uses a custom net.Listener
// injected via echo.Listener so we can drive two DIFFERENT client IPs and
// prove they don't share a bucket. Since httptest gives us a real TCP
// server, we key both by RemoteAddr with a trick: we bypass RealIP() with a
// custom IPExtractor that respects a test header. This exercises the
// bucketing path end-to-end.
func TestIntegration_DifferentIPsSeparateBuckets(t *testing.T) {
	e := echo.New()
	// Trust the X-Test-Client-IP header so tests can spoof the client IP
	// without needing to open multiple sockets. Production stays on the
	// default RealIP() (RemoteAddr) — this extractor is scoped to this
	// echo instance only.
	e.IPExtractor = func(r *http.Request) string {
		if v := r.Header.Get("X-Test-Client-IP"); v != "" {
			return v
		}
		return r.RemoteAddr
	}
	store := newRateLimitStore(silentLogger(), func(*echo.Context) string { return "" })
	e.Use(store.middleware())
	e.POST("/auth/login", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	srv := httptest.NewServer(e)
	defer srv.Close()

	drainIP := func(ip string) (success, throttled int) {
		for i := 0; i < 12; i++ {
			req, _ := http.NewRequest("POST", srv.URL+"/auth/login", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-Client-IP", ip)
			res, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("ip %s req %d: %v", ip, i, err)
			}
			switch res.StatusCode {
			case http.StatusOK:
				success++
			case http.StatusTooManyRequests:
				throttled++
			}
			res.Body.Close()
		}
		return
	}

	s1, t1 := drainIP("10.0.0.1")
	s2, t2 := drainIP("10.0.0.2")
	if s1 != 10 || t1 != 2 {
		t.Fatalf("ip1: expected 10/2, got %d/%d", s1, t1)
	}
	if s2 != 10 || t2 != 2 {
		t.Fatalf("ip2: expected 10/2, got %d/%d — separate IPs SHARED a bucket", s2, t2)
	}
}

// TestIntegration_GroupIsolation confirms that draining /auth/login does
// not affect POST /wakatime_key. Both go through the same middleware
// instance, so if group routing broke, the second endpoint would 429 after
// zero requests.
func TestIntegration_GroupIsolation(t *testing.T) {
	e := echo.New()
	// Simulate an authenticated user for the wakatime probe by injecting a
	// static owner. The auth-write group falls back to IP (no lookup match
	// on the same headerless request).
	store := newRateLimitStore(silentLogger(), func(c *echo.Context) string {
		if c.Request().Header.Get("X-Test-Auth") == "yes" {
			return "panda"
		}
		return ""
	})
	e.Use(store.middleware())
	e.POST("/auth/login", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })
	e.POST("/api/v1/users/current/wakatime_key", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	srv := httptest.NewServer(e)
	defer srv.Close()

	// Drain /auth/login (auth-write, burst 10).
	throttled := 0
	for i := 0; i < 12; i++ {
		req, _ := http.NewRequest("POST", srv.URL+"/auth/login", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		res, _ := srv.Client().Do(req)
		if res.StatusCode == http.StatusTooManyRequests {
			throttled++
		}
		res.Body.Close()
	}
	if throttled != 2 {
		t.Fatalf("auth-write: expected 2 throttled, got %d", throttled)
	}

	// wakatime_key should still have its full burst of 5 for user panda.
	waSuccess := 0
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/users/current/wakatime_key", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Auth", "yes")
		res, _ := srv.Client().Do(req)
		if res.StatusCode == http.StatusOK {
			waSuccess++
		}
		res.Body.Close()
	}
	if waSuccess != 5 {
		t.Fatalf("wakatime-probe should have full burst; got %d/5 — groups leaked", waSuccess)
	}
}

// TestIntegration_HealthzBypass proves GET /healthz is never rate-limited,
// even when the default bucket would deny.
func TestIntegration_HealthzBypass(t *testing.T) {
	e := echo.New()
	store := &rateLimitStore{
		buckets: map[endpointGroup]*sync.Map{
			groupDefault: {},
		},
		configs: map[endpointGroup]bucketConfig{
			// Impossible bucket: rate=0, burst=0. Everything else must 429.
			groupDefault: {Rate: 0, Burst: 0},
		},
		logger:     silentLogger(),
		userLookup: func(*echo.Context) string { return "" },
		stop:       make(chan struct{}),
	}
	e.Use(store.middleware())
	e.GET("/healthz", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })
	e.GET("/other", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })
	srv := httptest.NewServer(e)
	defer srv.Close()

	// /healthz survives 100 consecutive requests.
	for i := 0; i < 100; i++ {
		res, _ := srv.Client().Get(srv.URL + "/healthz")
		if res.StatusCode != http.StatusOK {
			t.Fatalf("/healthz req %d got %d — kubelet probe blocked", i, res.StatusCode)
		}
		res.Body.Close()
	}
	// /other is throttled on the FIRST request (burst=0).
	res, _ := srv.Client().Get(srv.URL + "/other")
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("/other should 429 with burst=0, got %d", res.StatusCode)
	}
	res.Body.Close()
}

// TestIntegration_OptionsBypass proves OPTIONS preflight bypasses the
// limiter — the CORS middleware owns those.
func TestIntegration_OptionsBypass(t *testing.T) {
	e := echo.New()
	store := &rateLimitStore{
		buckets: map[endpointGroup]*sync.Map{groupDefault: {}},
		configs: map[endpointGroup]bucketConfig{groupDefault: {Rate: 0, Burst: 0}},
		logger:  silentLogger(),
		userLookup: func(*echo.Context) string { return "" },
		stop:       make(chan struct{}),
	}
	e.Use(store.middleware())
	e.OPTIONS("/anything", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	srv := httptest.NewServer(e)
	defer srv.Close()
	for i := 0; i < 50; i++ {
		req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/anything", nil)
		res, _ := srv.Client().Do(req)
		if res.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("OPTIONS req %d hit 429 — preflight should bypass", i)
		}
		res.Body.Close()
	}
}

// slog helper mirrored from cors_test — silent logger for quiet runs.
func silentLoggerRL() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// override cors_test.silentLogger if needed — but the cors_test already
// defines silentLogger() in the same package, so we reuse that. This shim
// exists only to make the intent explicit if someone extracts the file.
var _ = silentLoggerRL
