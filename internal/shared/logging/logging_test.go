// logging_test.go — coverage for slog init + tee/hub fan-out (gaka-d6x).
//
// Named invariants exercised (no bare roundtrips):
//
//   parseLevel
//     - "level parsing is case/whitespace insensitive"
//     - "unknown/garbage inputs fail SAFE to Info (never silently silence errors)"
//
//   Setup
//     - "dev env picks TextHandler; prod env picks JSONHandler (stdout format contract)"
//     - "installs returned logger as slog default (global side-effect is real)"
//     - "records at/above LogLevel reach stdout; below stay quiet"
//     - "hub ALWAYS receives records down to Debug even when stdout level is Info"
//       (this is the load-bearing 'DB tracer visible in Logs tab but quiet in terminal' contract)
//
//   teeHandler.Enabled
//     - "Enabled is true down to Debug regardless of stdoutLevel (so Handle runs and hub sees it)"
//
//   teeHandler.Handle
//     - "record without attrs publishes with nil Attrs (JSON emits no key vs empty {})"
//     - "record with attrs flattens each attr under its key"
//     - "hub=nil handler still delegates to base without panic (defensive: gaka-yzs)"
//
//   teeHandler.WithAttrs
//     - "WithAttrs accumulates across chained calls (attrs on the returned handler)"
//     - "attrs added via WithAttrs appear on subsequent hub records"
//
//   teeHandler.WithGroup
//     - "WithGroup prefixes record-time attribute keys with the group name"
//
//   NewLogHub
//     - "capacity <= 0 falls back to DefaultLogHubCapacity (guards accidental 0-cap
//        which would silently drop every log record)"

package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// captureStdout redirects os.Stdout during fn and returns whatever was written.
// Setup wires the base handler to os.Stdout unconditionally, so any assertion
// about stdout format has to redirect the FD, not swap in a *bytes.Buffer.
func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	_ = w.Close()
	os.Stdout = old
	return <-done
}

// drainHub returns everything currently buffered on the subscriber channel.
// Used to assert record delivery WITHOUT relying on subscribe ordering vs Publish.
func drainHub(ch chan LogEntry, deadline time.Duration) []LogEntry {
	end := time.Now().Add(deadline)
	var out []LogEntry
	for time.Now().Before(end) {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-time.After(5 * time.Millisecond):
			// nothing pending; if we've been idle a moment, bail
			if len(out) > 0 {
				return out
			}
		}
	}
	return out
}

var _ = Describe("parseLevel", func() {
	It("level parsing is case/whitespace insensitive", func() {
		// Named invariant: users type "DEBUG" or "  warn " in env vars; parser must
		// not silently downgrade to Info on cosmetic differences.
		Expect(parseLevel("debug")).To(Equal(slog.LevelDebug))
		Expect(parseLevel("DEBUG")).To(Equal(slog.LevelDebug))
		Expect(parseLevel(" Debug ")).To(Equal(slog.LevelDebug))
		Expect(parseLevel("warn")).To(Equal(slog.LevelWarn))
		Expect(parseLevel("WARNING")).To(Equal(slog.LevelWarn))
		Expect(parseLevel("Error")).To(Equal(slog.LevelError))
		Expect(parseLevel("info")).To(Equal(slog.LevelInfo))
		Expect(parseLevel("INFO")).To(Equal(slog.LevelInfo))
	})

	It("unknown/garbage inputs fail SAFE to Info (never silently silence errors)", func() {
		// Named invariant: a typo like BOOM_LOG_LEVEL=erorr must NOT accidentally
		// become LevelError+1 (which would silence real errors). Default = Info.
		Expect(parseLevel("")).To(Equal(slog.LevelInfo))
		Expect(parseLevel("banana")).To(Equal(slog.LevelInfo))
		Expect(parseLevel("erorr")).To(Equal(slog.LevelInfo))
		Expect(parseLevel("trace")).To(Equal(slog.LevelInfo)) // trace is not a slog level
		Expect(parseLevel("   ")).To(Equal(slog.LevelInfo))
	})
})

var _ = Describe("Setup", func() {
	var savedDefault *slog.Logger
	BeforeEach(func() { savedDefault = slog.Default() })
	AfterEach(func() { slog.SetDefault(savedDefault) })

	It("dev env picks TextHandler; prod env picks JSONHandler (stdout format contract)", func() {
		// Named invariant: prod deploys parse logs with jq/loki — dev shells
		// want colored text. Base handler choice must follow c.IsDev() exactly.
		devOut := captureStdout(func() {
			cfg := &config.Config{LogLevel: "info", Env: "dev"}
			l, _ := Setup(cfg)
			l.Info("hello-dev")
		})
		// Text handler format: `time=... level=INFO msg=hello-dev`
		Expect(devOut).To(ContainSubstring("msg=hello-dev"))
		Expect(devOut).To(ContainSubstring("level=INFO"))
		// If it were the JSON handler we'd see `"msg":"hello-dev"`
		Expect(devOut).NotTo(ContainSubstring(`"msg":"hello-dev"`))

		prodOut := captureStdout(func() {
			cfg := &config.Config{LogLevel: "info", Env: "prod"}
			l, _ := Setup(cfg)
			l.Info("hello-prod")
		})
		// JSON format is a JSON object per line; must parse as such.
		var line map[string]any
		firstLine := strings.SplitN(strings.TrimSpace(prodOut), "\n", 2)[0]
		Expect(json.Unmarshal([]byte(firstLine), &line)).To(Succeed(), "prod output must be valid JSON: %q", firstLine)
		Expect(line["msg"]).To(Equal("hello-prod"))
		Expect(line["level"]).To(Equal("INFO"))
	})

	It("installs returned logger as slog default (global side-effect is real)", func() {
		// Named invariant: callers depend on `slog.Info(...)` (package-global)
		// producing the same output as the returned logger — Setup must have
		// called slog.SetDefault so imported libs that use log/slog get the hub too.
		out := captureStdout(func() {
			cfg := &config.Config{LogLevel: "info", Env: "dev"}
			_, hub := Setup(cfg)
			// Use the package-global slog, not the returned logger:
			slog.Info("via-package-default")
			// The hub is the one Setup returned — if SetDefault didn't run,
			// the record would go to some other hub and never appear here.
			entries := hub.Backfill(0)
			Expect(entries).To(HaveLen(1))
			Expect(entries[0].Msg).To(Equal("via-package-default"))
		})
		Expect(out).To(ContainSubstring("via-package-default"))
	})

	It("hub ALWAYS receives records down to Debug even when stdout level is Info", func() {
		// Named invariant: the DB query tracer emits at DEBUG. It must appear in the
		// Logs tab (hub) even when a prod operator sets LOG_LEVEL=info to keep the
		// terminal quiet. This is the whole reason teeHandler splits stdout vs hub.
		out := captureStdout(func() {
			cfg := &config.Config{LogLevel: "info", Env: "dev"}
			l, hub := Setup(cfg)
			l.Debug("db-query-trace")
			l.Info("http-request")
			l.Warn("degraded-cache")

			entries := hub.Backfill(0)
			msgs := make([]string, len(entries))
			levels := make([]string, len(entries))
			for i, e := range entries {
				msgs[i] = e.Msg
				levels[i] = e.Level
			}
			Expect(msgs).To(ContainElements("db-query-trace", "http-request", "degraded-cache"))
			Expect(levels).To(ContainElement("DEBUG"))
		})
		// Stdout at Info suppresses DEBUG:
		Expect(out).NotTo(ContainSubstring("db-query-trace"))
		// but INFO/WARN reach stdout:
		Expect(out).To(ContainSubstring("http-request"))
		Expect(out).To(ContainSubstring("degraded-cache"))
	})

	It("records below stdoutLevel do not produce a stdout line", func() {
		// Named invariant tightened: if you set LOG_LEVEL=error, an INFO log is
		// completely silent on the terminal. (The hub still sees it — verified
		// in the previous test.)
		out := captureStdout(func() {
			cfg := &config.Config{LogLevel: "error", Env: "dev"}
			l, hub := Setup(cfg)
			l.Info("should-not-appear-on-stdout")
			l.Error("should-appear-on-stdout")

			// Hub still gets everything:
			entries := hub.Backfill(0)
			Expect(entries).To(HaveLen(2))
		})
		Expect(out).NotTo(ContainSubstring("should-not-appear-on-stdout"))
		Expect(out).To(ContainSubstring("should-appear-on-stdout"))
	})
})

var _ = Describe("teeHandler", func() {
	// build constructs a teeHandler with a JSON base writing to buf, so we can
	// distinguish "did the base handler run?" from "was the record hubbed?"
	build := func(stdoutLevel slog.Level, hub *LogHub) (*teeHandler, *bytes.Buffer) {
		buf := &bytes.Buffer{}
		base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		return &teeHandler{base: base, hub: hub, stdoutLevel: stdoutLevel}, buf
	}

	It("Enabled is true down to Debug regardless of stdoutLevel", func() {
		// Named invariant: Enabled MUST return true so slog dispatches Handle,
		// which is where the tee decides stdout-vs-hub. If Enabled short-circuited
		// at stdoutLevel, DEBUG records would never reach the hub.
		h, _ := build(slog.LevelError, NewLogHub(10))
		Expect(h.Enabled(context.Background(), slog.LevelDebug)).To(BeTrue())
		Expect(h.Enabled(context.Background(), slog.LevelInfo)).To(BeTrue())
		Expect(h.Enabled(context.Background(), slog.LevelWarn)).To(BeTrue())
		Expect(h.Enabled(context.Background(), slog.LevelError)).To(BeTrue())
		// Below Debug (LevelDebug-1) is the only rejection:
		Expect(h.Enabled(context.Background(), slog.LevelDebug-1)).To(BeFalse())
	})

	It("record without attrs publishes with nil Attrs (JSON emits no key vs empty {})", func() {
		// Named invariant: LogEntry.Attrs uses `omitempty`. When there are no
		// slog attributes, Attrs must be nil (not an empty map) so JSON output
		// omits the field entirely — the Logs tab expects that shape.
		hub := NewLogHub(10)
		h, _ := build(slog.LevelDebug, hub)

		rec := slog.NewRecord(time.Now(), slog.LevelInfo, "no-attrs", 0)
		Expect(h.Handle(context.Background(), rec)).To(Succeed())

		entries := hub.Backfill(0)
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].Attrs).To(BeNil(),
			"omitempty contract: attr-less record must serialize without an attrs key")
	})

	It("record with attrs flattens each attr under its key", func() {
		hub := NewLogHub(10)
		h, _ := build(slog.LevelDebug, hub)

		rec := slog.NewRecord(time.Now(), slog.LevelInfo, "with-attrs", 0)
		rec.AddAttrs(slog.String("user", "alice"), slog.Int("count", 3))
		Expect(h.Handle(context.Background(), rec)).To(Succeed())

		entries := hub.Backfill(0)
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].Attrs).To(HaveKeyWithValue("user", "alice"))
		Expect(entries[0].Attrs).To(HaveKey("count"))
	})

	It("hub=nil handler still delegates to base without panic (defensive: gaka-yzs)", func() {
		// Named invariant: callers that don't wire a hub (tests, migrations,
		// early boot) must still be able to log. teeHandler.Handle guards `t.hub
		// != nil` — this test would panic on a regression that dropped it.
		h, buf := build(slog.LevelDebug, nil)
		rec := slog.NewRecord(time.Now(), slog.LevelInfo, "no-hub", 0)
		Expect(h.Handle(context.Background(), rec)).To(Succeed())
		Expect(buf.String()).To(ContainSubstring("no-hub"))
	})

	It("stdout suppression path: rec below stdoutLevel skips base but still hubs", func() {
		// Named invariant re-checked at the handler level (independent of Setup):
		// Handle must not call base.Handle for a below-threshold record, but the
		// hub Publish is unconditional. This is what keeps the terminal quiet
		// while the Logs tab stays complete.
		hub := NewLogHub(10)
		h, buf := build(slog.LevelWarn, hub)

		rec := slog.NewRecord(time.Now(), slog.LevelDebug, "quiet-on-stdout", 0)
		Expect(h.Handle(context.Background(), rec)).To(Succeed())

		Expect(buf.String()).To(BeEmpty(), "base handler must not fire for records below stdoutLevel")
		entries := hub.Backfill(0)
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].Msg).To(Equal("quiet-on-stdout"))
	})
})

var _ = Describe("teeHandler.WithAttrs", func() {
	build := func() (*teeHandler, *bytes.Buffer, *LogHub) {
		buf := &bytes.Buffer{}
		base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		hub := NewLogHub(10)
		return &teeHandler{base: base, hub: hub, stdoutLevel: slog.LevelDebug}, buf, hub
	}

	It("WithAttrs accumulates across chained calls (attrs on the returned handler)", func() {
		// Named invariant: slog contracts require WithAttrs to APPEND, not
		// replace. A regression that reassigned `merged` = `as` would drop
		// context attrs added by intermediate loggers.
		h, _, _ := build()
		h2 := h.WithAttrs([]slog.Attr{slog.String("first", "1")}).(*teeHandler)
		h3 := h2.WithAttrs([]slog.Attr{slog.String("second", "2")}).(*teeHandler)

		Expect(h3.attrs).To(HaveLen(2))
		keys := []string{h3.attrs[0].Key, h3.attrs[1].Key}
		Expect(keys).To(Equal([]string{"first", "second"}))
	})

	It("attrs added via WithAttrs appear on subsequent hub records", func() {
		// Named invariant: attrs attached via WithAttrs must be flattened into
		// LogEntry.Attrs at Handle time — the Logs tab groups by user via that
		// key, so a regression here means logs "for user alice" wouldn't
		// filter under FilterForUser.
		h, _, hub := build()
		h2 := h.WithAttrs([]slog.Attr{slog.String("user", "alice")}).(*teeHandler)

		rec := slog.NewRecord(time.Now(), slog.LevelInfo, "scoped", 0)
		Expect(h2.Handle(context.Background(), rec)).To(Succeed())

		entries := hub.Backfill(0)
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].Attrs).To(HaveKeyWithValue("user", "alice"))
	})
})

var _ = Describe("teeHandler.WithGroup", func() {
	It("WithGroup prefixes record-time attribute keys with the group name", func() {
		// Named invariant: slog's WithGroup contract nests subsequent attrs.
		// teeHandler's best-effort flattening prefixes record.Attrs keys with
		// "<group>.". Regression here would collide unrelated 'id' keys from
		// different subsystems in the Logs tab UI.
		buf := &bytes.Buffer{}
		base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		hub := NewLogHub(10)
		h := &teeHandler{base: base, hub: hub, stdoutLevel: slog.LevelDebug}

		g := h.WithGroup("importer").(*teeHandler)
		Expect(g.group).To(Equal("importer"))

		rec := slog.NewRecord(time.Now(), slog.LevelInfo, "job-started", 0)
		rec.AddAttrs(slog.String("id", "j-42"))
		Expect(g.Handle(context.Background(), rec)).To(Succeed())

		entries := hub.Backfill(0)
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].Attrs).To(HaveKeyWithValue("importer.id", "j-42"),
			"group prefix must be applied so subsystem attrs don't collide")
		Expect(entries[0].Attrs).NotTo(HaveKey("id"))
	})
})

var _ = Describe("NewLogHub default capacity", func() {
	It("capacity <= 0 falls back to DefaultLogHubCapacity (guards accidental drop-all)", func() {
		// Named invariant: a caller passing 0 or -1 must NOT get a zero-capacity
		// hub (which would silently drop every Publish). Documented behavior:
		// fall back to DefaultLogHubCapacity (1000).
		h := NewLogHub(0)
		Expect(h.capacity).To(Equal(DefaultLogHubCapacity))

		h2 := NewLogHub(-42)
		Expect(h2.capacity).To(Equal(DefaultLogHubCapacity))

		// Positive value is honored verbatim:
		h3 := NewLogHub(17)
		Expect(h3.capacity).To(Equal(17))
	})
})

// ensure drainHub is used somewhere so `go vet` doesn't flag it as unused
// if we later remove the concurrency test — currently unused (kept as a
// helper for future concurrent-fanout regression tests).
var _ = drainHub
