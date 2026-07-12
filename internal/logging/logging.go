// Package logging configures the stdlib slog logger (text for dev, JSON for prod)
// replacing hakatime's katip setup. It also tees every log record into a LogHub
// (an in-process ring buffer + WS fan-out) so the dashboard's Logs tab can view
// the running server's own logs live and durably across reloads.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
)

// The package used to expose a process-wide LogHub via a package-global +
// Hub() accessor (audit gaka-yzs). That's gone — Setup returns the hub
// explicitly and callers thread it through server.New / handler.New. Same
// pattern importer.Hub already follows: dependency-injected, testable,
// no hidden order dependency between Setup and handler.New.

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

// Setup builds a slog.Logger and installs it as the default, PLUS the
// LogHub the tee handler publishes to. Callers thread the returned hub
// through server.New → handler.New so the Logs endpoint reads the same
// instance. The base handler (text in dev, JSON in prod) still writes to
// os.Stdout unchanged; the teeHandler wraps it so every record is ALSO
// published to the hub.
func Setup(c *config.Config) (*slog.Logger, *LogHub) {
	stdoutLevel := parseLevel(c.LogLevel)
	// The base handler accepts everything down to Debug; the teeHandler decides
	// what reaches stdout (>= stdoutLevel) vs the LogHub (everything down to
	// Debug). This keeps the terminal quiet at INFO while the Logs tab can still
	// show + filter DEBUG records like the DB query tracer.
	opts := &slog.HandlerOptions{Level: slog.LevelDebug}
	var base slog.Handler
	if c.IsDev() {
		base = slog.NewTextHandler(os.Stdout, opts)
	} else {
		base = slog.NewJSONHandler(os.Stdout, opts)
	}
	hub := NewLogHub(DefaultLogHubCapacity)
	l := slog.New(&teeHandler{base: base, hub: hub, stdoutLevel: stdoutLevel})
	slog.SetDefault(l)
	return l, hub
}

// teeHandler wraps a base slog.Handler: it delegates to base.Handle (keeping
// stdout output identical) and then best-effort publishes the record to the hub.
// Publishing is non-blocking (see LogHub.Publish), so the logging path is never
// slowed by a WS subscriber. Safe when hub is nil.
type teeHandler struct {
	base        slog.Handler
	hub         *LogHub
	stdoutLevel slog.Level  // records below this are kept out of stdout (still hubbed)
	attrs       []slog.Attr // accumulated via WithAttrs
	group       string      // last group name (for prefixing, best-effort)
}

func (t *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	// Enabled down to Debug so DEBUG records (e.g. the DB query tracer) reach
	// Handle and get published to the LogHub even when stdout is at INFO.
	return level >= slog.LevelDebug
}

func (t *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	// stdout only at/above the configured level; the LogHub gets everything.
	var err error
	if r.Level >= t.stdoutLevel {
		err = t.base.Handle(ctx, r)
	}

	if t.hub != nil {
		attrs := make(map[string]string)
		for _, a := range t.attrs {
			attrs[a.Key] = a.Value.String()
		}
		r.Attrs(func(a slog.Attr) bool {
			key := a.Key
			if t.group != "" {
				key = t.group + "." + key
			}
			attrs[key] = a.Value.String()
			return true
		})
		if len(attrs) == 0 {
			attrs = nil
		}
		t.hub.Publish(LogEntry{
			Time:  r.Time,
			Level: r.Level.String(),
			Msg:   r.Message,
			Attrs: attrs,
		})
	}
	return err
}

func (t *teeHandler) WithAttrs(as []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(t.attrs)+len(as))
	merged = append(merged, t.attrs...)
	merged = append(merged, as...)
	return &teeHandler{base: t.base.WithAttrs(as), hub: t.hub, stdoutLevel: t.stdoutLevel, attrs: merged, group: t.group}
}

func (t *teeHandler) WithGroup(name string) slog.Handler {
	return &teeHandler{base: t.base.WithGroup(name), hub: t.hub, stdoutLevel: t.stdoutLevel, attrs: t.attrs, group: name}
}
