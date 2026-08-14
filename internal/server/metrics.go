package server

import (
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
)

// metricsMiddleware is the GENERIC incoming RED decorator around the router:
// every served request records the two Prometheus series that back both the
// /metrics scrape and the admin Metrics tab —
//
//   - http_requests_total{method,route,status_class} — the RATE + ERRORS
//   - http_request_duration_seconds{method,route}     — the DURATION (histogram)
//
// Cardinality is bounded on purpose: we label by the matched echo route
// TEMPLATE (c.Path(), e.g. /p/:slug) — NEVER the raw URL path — so a public
// profile at /p/abc and /p/xyz collapse onto one series instead of exploding
// one per slug. `method` is the small HTTP-verb set; `status_class` is one of
// 1xx..5xx (see metrics.StatusClass).
//
// Probe + scrape paths are skipped: kubelet hits /healthz|/readyz|/livez every
// few seconds and Prometheus hits /metrics itself — none belong on the graph.
// Those are the same paths requestLogger ignores (plus /metrics).
//
// It times + observes AFTER next() so the status + latency are final, and it
// never returns an error of its own — instrumentation must not alter outcomes.
func metricsMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			switch c.Request().URL.Path {
			case "/healthz", "/readyz", "/livez", "/metrics":
				return next(c)
			}

			start := time.Now()
			err := next(c)
			dur := time.Since(start).Seconds()

			status := 0
			if resp, ok := c.Response().(*echo.Response); ok {
				status = resp.Status
			}

			// c.Path() is the registered route template ("/p/:slug"), the whole
			// point of the cardinality bound. A request that matched no route
			// (echo's 404 path) leaves it empty — bucket those as "unmatched"
			// rather than emitting an empty-label series.
			route := c.Path()
			if route == "" {
				route = "unmatched"
			}
			method := c.Request().Method

			metrics.HTTPRequestDuration.WithLabelValues(method, route).Observe(dur)
			metrics.HTTPRequestsTotal.WithLabelValues(method, route, metrics.StatusClass(status)).Inc()
			return err
		}
	}
}
