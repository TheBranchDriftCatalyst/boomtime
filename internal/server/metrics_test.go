package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
)

// metricsTestEcho builds a bare echo with the real metricsMiddleware plus the
// same /metrics scrape handler NewWithHandler wires, so tests exercise the
// production shapes.
func metricsTestEcho() *echo.Echo {
	e := echo.New()
	e.Use(metricsMiddleware())
	e.GET("/metrics", func(c *echo.Context) error {
		metrics.Handler().ServeHTTP(c.Response(), c.Request())
		return nil
	})
	return e
}

// TestMetricsMiddlewareLabelsByRouteTemplate is the cardinality-bound invariant:
// requests to /p/abc and /p/xyz must collapse onto ONE series keyed by the
// route TEMPLATE (/p/:slug), never the raw path.
func TestMetricsMiddlewareLabelsByRouteTemplate(t *testing.T) {
	e := metricsTestEcho()
	e.GET("/p/:slug", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })
	e.GET("/boom", func(c *echo.Context) error { return c.String(http.StatusInternalServerError, "no") })

	ok := metrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "/p/:slug", "2xx")
	err5xx := metrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "/boom", "5xx")
	beforeOK := testutil.ToFloat64(ok)
	beforeErr := testutil.ToFloat64(err5xx)

	for _, slug := range []string{"abc", "xyz", "abc"} {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p/"+slug, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /p/%s = %d, want 200", slug, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET /boom = %d, want 500", rec.Code)
	}

	if got := testutil.ToFloat64(ok) - beforeOK; got != 3 {
		t.Errorf("http_requests_total{route=/p/:slug,2xx} delta = %v, want 3 (all slugs collapse)", got)
	}
	if got := testutil.ToFloat64(err5xx) - beforeErr; got != 1 {
		t.Errorf("http_requests_total{route=/boom,5xx} delta = %v, want 1", got)
	}
	// The bound itself: no raw-path series exists for /p/abc (reading it here
	// creates it at 0, which is exactly the proof — it was never incremented).
	if raw := testutil.ToFloat64(metrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "/p/abc", "2xx")); raw != 0 {
		t.Errorf("raw path /p/abc leaked a series (value=%v) — cardinality not bounded to route template", raw)
	}
}

// TestMetricsEndpointReturnsPromText hits a real route then GET /metrics and
// asserts a 200 with Prometheus text carrying http_requests_total.
func TestMetricsEndpointReturnsPromText(t *testing.T) {
	e := metricsTestEcho()
	e.GET("/ok", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ok = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "http_requests_total") {
		t.Errorf("/metrics body missing http_requests_total:\n%s", body)
	}
	if !strings.Contains(body, `route="/ok"`) {
		t.Errorf("/metrics body missing the /ok route series:\n%s", body)
	}
}

// TestMetricsMiddlewareSkipsProbes verifies kubelet + scrape paths are excluded
// (they would dominate the graph).
func TestMetricsMiddlewareSkipsProbes(t *testing.T) {
	e := metricsTestEcho()
	e.GET("/healthz", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })

	probe := metrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "/healthz", "2xx")
	before := testutil.ToFloat64(probe)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if got := testutil.ToFloat64(probe) - before; got != 0 {
		t.Errorf("/healthz advanced the router series by %v, want 0 (probe must be skipped)", got)
	}
}

// TestRatelimitDecisionCounter drives the real rate-limit middleware past the
// auth-write burst and asserts http_ratelimit_decisions_total advances with the
// right decision (allowed vs throttled) under a bounded scope label.
func TestRatelimitDecisionCounter(t *testing.T) {
	store := newRateLimitStore(silentLogger(), func(*echo.Context) string { return "" })
	e := echo.New()
	e.Use(store.middleware())
	e.POST("/auth/login", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })

	allowed := metrics.HTTPRatelimitDecisionsTotal.WithLabelValues("allowed", "ip")
	throttled := metrics.HTTPRatelimitDecisionsTotal.WithLabelValues("throttled", "ip")
	beforeA := testutil.ToFloat64(allowed)
	beforeT := testutil.ToFloat64(throttled)

	// auth-write bucket burst is 10; the 11th/12th same-IP request 429s.
	var got200, got429 int
	for i := 0; i < 12; i++ {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))
		switch rec.Code {
		case http.StatusOK:
			got200++
		case http.StatusTooManyRequests:
			got429++
		}
	}
	if got200 != 10 || got429 != 2 {
		t.Fatalf("status split = %d ok / %d throttled, want 10/2", got200, got429)
	}
	if d := testutil.ToFloat64(allowed) - beforeA; d != 10 {
		t.Errorf("allowed decisions delta = %v, want 10", d)
	}
	if d := testutil.ToFloat64(throttled) - beforeT; d != 2 {
		t.Errorf("throttled decisions delta = %v, want 2", d)
	}
}
