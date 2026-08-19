package server

import (
	"log/slog"
	"time"

	"github.com/labstack/echo/v5"
)

// requestLogger logs each HTTP request via slog (replaces katip HTTP logging).
func requestLogger(logger *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			// kubelet probes hit /healthz + /readyz every few seconds; logging
			// each one would evict useful lines from the LogHub ring buffer.
			// Skip them entirely (no request line) but leave every other path,
			// and its log level, untouched.
			switch c.Request().URL.Path {
			case "/healthz", "/readyz", "/livez", "/metrics":
				return next(c)
			}
			start := time.Now()
			err := next(c)
			status := 0
			if resp, ok := c.Response().(*echo.Response); ok {
				status = resp.Status
			}
			// InfoContext (not Info): slog only hands the context to the
			// handler on the *Context variants, and without it the traceHandler
			// sees context.Background() with no span — so trace_id/span_id never
			// get stamped and HyperDX can't pivot log -> trace (TALOS-kvg1).
			logger.InfoContext(c.Request().Context(), "http request",
				"method", c.Request().Method,
				"path", c.Request().URL.Path,
				"status", status,
				"dur_ms", time.Since(start).Milliseconds(),
			)
			return err
		}
	}
}
