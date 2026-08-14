package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
)

// seriesSum returns the summed value across all points of the named series in a
// fresh snapshot, or 0 when absent.
func seriesSum(name string) float64 {
	for _, s := range metrics.Snapshot(time.Time{}) {
		if s.Name == name {
			var total float64
			for _, p := range s.Points {
				total += p.Value
			}
			return total
		}
	}
	return 0
}

// TestMetricsMiddlewareBumpsRouterSeries drives requests through the real
// metricsMiddleware and asserts the http.requests / http.errors series advance
// by exactly the number served (delta-based so it is robust to any series the
// process already accumulated).
func TestMetricsMiddlewareBumpsRouterSeries(t *testing.T) {
	e := echo.New()
	e.Use(metricsMiddleware())
	e.GET("/ok", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })
	e.GET("/boom", func(c *echo.Context) error { return c.String(http.StatusInternalServerError, "no") })

	before := seriesSum("http.requests")
	beforeGet := seriesSum(metrics.Name("http.requests", "method", http.MethodGet))
	beforeErr := seriesSum("http.errors")
	before5xx := seriesSum(metrics.Name("http.errors", "class", "5xx"))

	const okCalls = 3
	for i := 0; i < okCalls; i++ {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /ok = %d, want 200", rec.Code)
		}
	}
	// One error request.
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET /boom = %d, want 500", rec.Code)
	}

	if got := seriesSum("http.requests") - before; got != okCalls+1 {
		t.Errorf("http.requests delta = %v, want %d", got, okCalls+1)
	}
	if got := seriesSum(metrics.Name("http.requests", "method", http.MethodGet)) - beforeGet; got != okCalls+1 {
		t.Errorf("http.requests{method=GET} delta = %v, want %d", got, okCalls+1)
	}
	if got := seriesSum("http.errors") - beforeErr; got != 1 {
		t.Errorf("http.errors delta = %v, want 1", got)
	}
	if got := seriesSum(metrics.Name("http.errors", "class", "5xx")) - before5xx; got != 1 {
		t.Errorf("http.errors{class=5xx} delta = %v, want 1", got)
	}
}

// TestMetricsMiddlewareSkipsHealthProbes verifies kubelet probes don't pollute
// the router series (they would dominate the graph).
func TestMetricsMiddlewareSkipsHealthProbes(t *testing.T) {
	e := echo.New()
	e.Use(metricsMiddleware())
	e.GET("/healthz", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })

	before := seriesSum("http.requests")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if got := seriesSum("http.requests") - before; got != 0 {
		t.Errorf("http.requests advanced by %v on a health probe, want 0", got)
	}
}
