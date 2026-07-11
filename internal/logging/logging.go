// Package logging configures the stdlib slog logger (text for dev, JSON for prod)
// replacing hakatime's katip setup.
package logging

import (
	"log/slog"
	"os"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
)

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Setup builds a slog.Logger and installs it as the default.
func Setup(c *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(c.LogLevel)}
	var h slog.Handler
	if c.IsDev() {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	l := slog.New(h)
	slog.SetDefault(l)
	return l
}
