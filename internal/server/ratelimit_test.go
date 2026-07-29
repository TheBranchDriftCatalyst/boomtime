// ratelimit_ginkgo_test.go — ginkgo mirror of ratelimit_test.go (gaka-0vp).
//
// 1:1 case map (11 stdlib TestXxx):
//
//	Layer 1 — UNIT
//	  TestClassifyEndpoint              → classifyEndpoint DescribeTable (9 named Entries — one per stdlib table row)
//	  TestLimiterForAllowThenDeny       → limiterFor > "10 allow then 11th denied; distinct keys → distinct limiters"
//	  TestLimiterForGroupIsolation      → limiterFor > "draining auth-write leaves wakatime-probe full"
//	  TestEvictOlderThan                → evictOlderThan > "sweeps stale bucket, keeps fresh one"
//	  TestBucketFromEnv                 → bucketFromEnv > "valid overrides / malformed values keep default"
//	  TestInstallRateLimitDisabled      → installRateLimit > "BOOM_DISABLE_RATE_LIMIT=1 bypasses limiter for 1000 reqs"
//
//	Layer 2 — INTEGRATION (each spins up an httptest server; assertions live
//	in the It so goroutine panics bubble as spec failures)
//	  TestIntegration_BucketKicksInAt429            → real echo integration > "10 pass, 2 x 429 with Retry-After + envelope"
//	  TestIntegration_DifferentIPsSeparateBuckets   → real echo integration > "distinct client IPs get distinct buckets"
//	  TestIntegration_GroupIsolation                → real echo integration > "draining /auth/login leaves wakatime_key intact"
//	  TestIntegration_HealthzBypass                 → real echo integration > "/healthz never rate-limited even with burst=0"
//	  TestIntegration_OptionsBypass                 → real echo integration > "OPTIONS preflight bypasses limiter"
//
// Uses os.Setenv + DeferCleanup save/restore to mirror the stdlib t.Setenv
// pattern (see docs/testing/ginkgo.md).
package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/time/rate"
)

// setenvG mirrors t.Setenv for ginkgo: save current value, set new, and
// restore on cleanup. Handles the "was-unset" case.
func setenvG(key, value string) {
	prev, ok := os.LookupEnv(key)
	os.Setenv(key, value)
	DeferCleanup(func() {
		if ok {
			os.Setenv(key, prev)
		} else {
			os.Unsetenv(key)
		}
	})
}

// ───────────────────────────── Layer 1: UNIT ─────────────────────────────────

var _ = Describe("classifyEndpoint (bucket routing decisions)", func() {
	DescribeTable("method+path → endpointGroup",
		func(method, path string, want endpointGroup) {
			Expect(classifyEndpoint(method, path)).To(Equal(want))
		},
		Entry("POST /auth/login → auth-write", "POST", "/auth/login", groupAuthWrite),
		Entry("POST /auth/register → auth-write", "POST", "/auth/register", groupAuthWrite),
		Entry("POST /auth/refresh_token → auth-write", "POST", "/auth/refresh_token", groupAuthWrite),
		Entry("POST /users/current/password → auth-write", "POST", "/api/v1/users/current/password", groupAuthWrite),
		Entry("POST /users/current/wakatime_key → wakatime-probe", "POST", "/api/v1/users/current/wakatime_key", groupWakatimeProbe),
		Entry("GET /users/current/wakatime_key → default (reads don't share probe budget)", "GET", "/api/v1/users/current/wakatime_key", groupDefault),
		Entry("GET /users/current/password → default (not POST)", "GET", "/api/v1/users/current/password", groupDefault),
		Entry("GET /users/current/stats → default", "GET", "/api/v1/users/current/stats", groupDefault),
		Entry("POST /users/current/heartbeats → default", "POST", "/api/v1/users/current/heartbeats", groupDefault),
	)
})

var _ = Describe("limiterFor (real rate.Limiter arithmetic)", func() {
	It("first 10 Allow() calls succeed, the 11th is denied; distinct keys produce distinct limiters", func() {
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
		lim := s.limiterFor(groupAuthWrite, "ip:1.2.3.4")
		for i := 0; i < 10; i++ {
			Expect(lim.Allow()).To(BeTrue(), "request %d unexpectedly denied under burst=10", i+1)
		}
		Expect(lim.Allow()).To(BeFalse(), "11th request must be denied (burst exhausted)")

		other := s.limiterFor(groupAuthWrite, "ip:9.9.9.9")
		Expect(other.Allow()).To(BeTrue(), "fresh key must have full burst available")
		Expect(lim == other).To(BeFalse(), "distinct keys must produce distinct limiters")
	})

	It("draining the auth-write bucket does not deplete the wakatime-probe bucket for the same key", func() {
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
		Expect(aw.Allow()).To(BeFalse(), "auth-write must be exhausted")
		// Wakatime-probe must still have its full burst of 5.
		for i := 0; i < 5; i++ {
			Expect(wk.Allow()).To(BeTrue(), "wakatime-probe req %d denied — group isolation broken", i+1)
		}
		Expect(wk.Allow()).To(BeFalse(), "wakatime-probe burst should now be exhausted at 5")
	})
})

var _ = Describe("evictOlderThan (sweeper)", func() {
	It("removes stale buckets past the cutoff and keeps fresh ones", func() {
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

		_, staleStillThere := s.buckets[groupDefault].Load("ip:stale")
		Expect(staleStillThere).To(BeFalse(), "stale entry should have been evicted")

		_, freshStillThere := s.buckets[groupDefault].Load("ip:fresh")
		Expect(freshStillThere).To(BeTrue(), "fresh entry should have survived")
	})
})

var _ = Describe("bucketFromEnv (env-driven overrides)", func() {
	It("applies valid overrides and falls back to defaults on malformed values", func() {
		def := bucketConfig{Rate: rate.Every(6 * time.Second), Burst: 10}

		// Valid overrides.
		setenvG("BOOM_RATELIMIT_AUTH_WRITE_RATE", "0.5")
		setenvG("BOOM_RATELIMIT_AUTH_WRITE_BURST", "20")
		got := bucketFromEnv(groupAuthWrite, def, silentLogger())
		Expect(float64(got.Rate)).To(Equal(0.5))
		Expect(got.Burst).To(Equal(20))

		// Malformed rate → default kept.
		setenvG("BOOM_RATELIMIT_AUTH_WRITE_RATE", "abc")
		setenvG("BOOM_RATELIMIT_AUTH_WRITE_BURST", "-5")
		got = bucketFromEnv(groupAuthWrite, def, silentLogger())
		Expect(got.Rate).To(Equal(def.Rate), "malformed rate should not override default")
		Expect(got.Burst).To(Equal(def.Burst), "invalid burst should not override default")
	})
})

var _ = Describe("installRateLimit (BOOM_DISABLE_RATE_LIMIT bypass)", func() {
	It("returns nil store and never 429s over 1000 requests when disabled", func() {
		setenvG(rateLimitDisableEnv, "1")
		e := echo.New()
		store := installRateLimit(e, silentLogger(), nil)
		Expect(store).To(BeNil(), "installRateLimit must return nil store when disabled")

		e.POST("/auth/login", func(c *echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})
		srv := httptest.NewServer(e)
		DeferCleanup(srv.Close)

		client := srv.Client()
		for i := 0; i < 1000; i++ {
			res, err := client.Post(srv.URL+"/auth/login", "application/json", strings.NewReader(`{}`))
			Expect(err).NotTo(HaveOccurred(), "request %d errored", i)
			res.Body.Close()
			Expect(res.StatusCode).NotTo(Equal(http.StatusTooManyRequests),
				"request %d hit 429 despite disable env — bypass broken", i+1)
		}
	})
})

// ────────────────────────── Layer 2: INTEGRATION ─────────────────────────────

var _ = Describe("real-echo integration: bucket kicks in at 429", func() {
	It("sends 12 requests: 10 succeed, 2 x 429 with Retry-After header + JSON envelope", func() {
		e := echo.New()
		store := newRateLimitStore(silentLogger(), func(*echo.Context) string { return "" })
		e.Use(store.middleware())
		e.POST("/auth/login", func(c *echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})

		srv := httptest.NewServer(e)
		DeferCleanup(srv.Close)

		successes, throttled := 0, 0
		var lastRetryAfter string
		var lastBody map[string]any
		for i := 0; i < 12; i++ {
			req, _ := http.NewRequest("POST", srv.URL+"/auth/login", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "203.0.113.7:12345"
			res, err := srv.Client().Do(req)
			Expect(err).NotTo(HaveOccurred(), "req %d errored", i)
			switch res.StatusCode {
			case http.StatusTooManyRequests:
				throttled++
				lastRetryAfter = res.Header.Get("Retry-After")
				body, _ := io.ReadAll(res.Body)
				_ = json.Unmarshal(body, &lastBody)
			case http.StatusOK:
				successes++
			default:
				res.Body.Close()
				Fail("req " + strconv.Itoa(i) + " unexpected status " + strconv.Itoa(res.StatusCode))
			}
			res.Body.Close()
		}
		Expect(successes).To(Equal(10), "expected 10 successes")
		Expect(throttled).To(Equal(2), "expected 2 throttled")
		Expect(lastRetryAfter).NotTo(BeEmpty(), "Retry-After header missing on 429")

		n, err := strconv.Atoi(lastRetryAfter)
		Expect(err).NotTo(HaveOccurred(), "Retry-After should be an integer, got %q", lastRetryAfter)
		Expect(n).To(BeNumerically(">=", 1), "Retry-After should be >= 1")

		Expect(lastBody["error"]).To(Equal("rate limited"), "body.error mismatch")
		_, hasRetryAfter := lastBody["retryAfter"]
		Expect(hasRetryAfter).To(BeTrue(), "body.retryAfter missing")
	})
})

var _ = Describe("real-echo integration: different IPs get separate buckets", func() {
	It("two distinct client IPs each get their own 10-then-2×429 window", func() {
		e := echo.New()
		// Trust the X-Test-Client-IP header so tests can spoof the client IP.
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
		DeferCleanup(srv.Close)

		drainIP := func(ip string) (success, throttled int) {
			for i := 0; i < 12; i++ {
				req, _ := http.NewRequest("POST", srv.URL+"/auth/login", strings.NewReader(`{}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Test-Client-IP", ip)
				res, err := srv.Client().Do(req)
				Expect(err).NotTo(HaveOccurred(), "ip %s req %d errored", ip, i)
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
		Expect(s1).To(Equal(10), "ip1 successes")
		Expect(t1).To(Equal(2), "ip1 throttled")
		Expect(s2).To(Equal(10), "ip2 successes — separate IPs SHARED a bucket")
		Expect(t2).To(Equal(2), "ip2 throttled — separate IPs SHARED a bucket")
	})
})

var _ = Describe("real-echo integration: group isolation across endpoints", func() {
	It("draining /auth/login leaves POST /wakatime_key with a full burst", func() {
		e := echo.New()
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
		DeferCleanup(srv.Close)

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
		Expect(throttled).To(Equal(2), "auth-write: expected 2 throttled")

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
		Expect(waSuccess).To(Equal(5), "wakatime-probe should have full burst; groups leaked")
	})
})

var _ = Describe("real-echo integration: /healthz bypasses the limiter", func() {
	It("100 consecutive GETs to /healthz survive even under burst=0; other routes 429 immediately", func() {
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
		DeferCleanup(srv.Close)

		for i := 0; i < 100; i++ {
			res, _ := srv.Client().Get(srv.URL + "/healthz")
			Expect(res.StatusCode).To(Equal(http.StatusOK),
				"/healthz req %d got %d — kubelet probe blocked", i, res.StatusCode)
			res.Body.Close()
		}
		res, _ := srv.Client().Get(srv.URL + "/other")
		Expect(res.StatusCode).To(Equal(http.StatusTooManyRequests),
			"/other should 429 with burst=0")
		res.Body.Close()
	})
})

var _ = Describe("real-echo integration: OPTIONS preflight bypasses the limiter", func() {
	It("50 consecutive OPTIONS requests never 429 under burst=0", func() {
		e := echo.New()
		store := &rateLimitStore{
			buckets:    map[endpointGroup]*sync.Map{groupDefault: {}},
			configs:    map[endpointGroup]bucketConfig{groupDefault: {Rate: 0, Burst: 0}},
			logger:     silentLogger(),
			userLookup: func(*echo.Context) string { return "" },
			stop:       make(chan struct{}),
		}
		e.Use(store.middleware())
		e.OPTIONS("/anything", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
		srv := httptest.NewServer(e)
		DeferCleanup(srv.Close)

		for i := 0; i < 50; i++ {
			req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/anything", nil)
			res, _ := srv.Client().Do(req)
			Expect(res.StatusCode).NotTo(Equal(http.StatusTooManyRequests),
				"OPTIONS req %d hit 429 — preflight should bypass", i)
			res.Body.Close()
		}
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
func silentLoggerRL() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
