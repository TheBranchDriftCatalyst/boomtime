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
			start := time.Now()
			err := next(c)
			status := 0
			if resp, ok := c.Response().(*echo.Response); ok {
				status = resp.Status
			}
			logger.Info("http request",
				"method", c.Request().Method,
				"path", c.Request().URL.Path,
				"status", status,
				"dur_ms", time.Since(start).Milliseconds(),
			)
			return err
		}
	}
}
