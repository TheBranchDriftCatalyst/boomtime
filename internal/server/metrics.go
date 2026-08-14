package server

import (
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
)

// metricsMiddleware decorates the router with request-rate metrics (the
// "router metric decorations"): every served request bumps a small, bounded
// set of counter series that the admin Metrics dashboard renders as
// rate-over-time.
//
// Series emitted (see internal/metrics):
//
//   - http.requests               — overall request rate
//   - http.requests{method=…}     — rate split by HTTP method (bounded: ~5)
//   - http.errors                 — rate of >=400 responses (overall)
//   - http.errors{class=4xx|5xx}  — client vs server error rate
//
// Cardinality is deliberately bounded: we key by METHOD and error CLASS, never
// by raw path (which would explode one series per id). Health/readiness probes
// are skipped so kubelet's every-few-seconds polling doesn't drown the graph —
// same paths requestLogger already ignores.
//
// It runs AFTER next() so the response status is final, and it never returns an
// error of its own — instrumentation must not alter request outcomes.
func metricsMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			switch c.Request().URL.Path {
			case "/healthz", "/readyz", "/livez":
				return next(c)
			}

			err := next(c)

			status := 0
			if resp, ok := c.Response().(*echo.Response); ok {
				status = resp.Status
			}

			metrics.Inc("http.requests", 1)
			metrics.Inc(metrics.Name("http.requests", "method", c.Request().Method), 1)

			if status >= 400 {
				metrics.Inc("http.errors", 1)
				class := "4xx"
				if status >= 500 {
					class = "5xx"
				}
				metrics.Inc(metrics.Name("http.errors", "class", class), 1)
			}
			return err
		}
	}
}
