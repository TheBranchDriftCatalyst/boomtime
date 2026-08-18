// monitor_test.go — DB-backed tests of the PERSISTENT two-level reading-monitor
// engine (monitor.go). They drive the exported RunMonitorOnce with an INJECTED
// `now` and the swappable fake sidecar (no network, no real Amazon), advancing
// `now` across passes to exercise the L1→L2→idle state machine deterministically.
//
// Covered:
//   - L1 detects an advance and the book enters L2 (active).
//   - an active book leaves L2 after the idle gap G.
//   - an advance records the pinned metrics + increments reading_activity_seconds
//     AND lands a reading_activity(source='kindle') bucket via the shared
//     composition.
//   - debounced (one toast per session) vs verbose (one per advance) toast counts.
//   - a disabled user is a no-op.
package reading_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"testing"
	"time"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/reading"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/notify"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// monitorCfg is the tuning the engine tests run under: T1=120s, T2=30s, G=300s.
// withDefaults() (applied inside the engine) folds in every other MonitorConfig
// field, so only the three the L1/L2/idle tests exercise are set here.
var monitorCfg = reading.MonitorConfig{
	DetectInterval:  120 * time.Second,
	CaptureInterval: 30 * time.Second,
	IdleGap:         300 * time.Second,
}

// newMonitorService wires a books Service on the harness DB with a seeded Amazon
// credential, the given fake sidecar, and a notify hub. It mints an owner, ENABLES
// the persistent monitor for them in `mode`, and returns everything the tests need.
func newMonitorService(t *testing.T, hz *testutil.Harness, sc *fakeSidecar, mode string) (*reading.Service, string, *notify.Hub) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("gen key: %v", err)
	}
	t.Setenv("BOOM_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	auth.ResetForTest()
	t.Cleanup(auth.ResetForTest)

	owner, _ := hz.MintUser("rm")
	az := amazon.NewStore(hz.DB)
	if err := az.Save(context.Background(), owner, amazon.DeviceCredential{CustomerID: "1", DeviceSerial: "dev"}); err != nil {
		t.Fatalf("seed amazon credential: %v", err)
	}

	enabled := true
	if err := hz.DB.SetReadingMonitorSettings(context.Background(), owner, &enabled, &mode); err != nil {
		t.Fatalf("enable monitor: %v", err)
	}

	hub := notify.NewHub()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := reading.New(hz.DB, az, logger).SetNotify(hub)
	svc.SetSidecar(sc)

	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = hz.DB.DeleteKindleReadingPositions(ctx, owner, "")
		_, _ = hz.DB.DeleteReadingActivity(ctx, owner, "")
		_, _ = hz.DB.DeleteReadingItems(ctx, owner, "")
	})
	return svc, owner, hub
}

// drainToasts counts (and empties) the events waiting on a hub subscription.
func drainToasts(ch <-chan notify.Event) int {
	n := 0
	for {
		select {
		case <-ch:
			n++
		default:
			return n
		}
	}
}

func TestReadingMonitor_L1DetectsAdvanceEntersL2(t *testing.T) {
	hz := testutil.NewHarness(t)
	sc := &fakeSidecar{ok: false}
	svc, owner, _ := newMonitorService(t, hz, sc, db.ReadingMonitorModeDebounced)
	seedInProgressBook(t, hz.DB, owner, "B0MON01")

	ctx := context.Background()
	t0 := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

	// First pass: the book reports a fresh last-page-read position → detected as an
	// advance, the book becomes active (enters L2).
	sc.pos, sc.at, sc.ok = 100, t0, true
	adv, err := svc.RunMonitorOnce(ctx, monitorCfg, t0)
	if err != nil {
		t.Fatalf("pass1: %v", err)
	}
	if adv != 1 {
		t.Fatalf("advances = %d, want 1 (L1 detected the advance)", adv)
	}
	active, err := hz.DB.CountActiveKindleMonitorBooks(ctx, owner)
	if err != nil {
		t.Fatalf("count active: %v", err)
	}
	if active != 1 {
		t.Fatalf("active books = %d, want 1 (book entered L2)", active)
	}
}

func TestReadingMonitor_LeavesL2AfterIdleGap(t *testing.T) {
	hz := testutil.NewHarness(t)
	sc := &fakeSidecar{ok: false}
	svc, owner, _ := newMonitorService(t, hz, sc, db.ReadingMonitorModeDebounced)
	seedInProgressBook(t, hz.DB, owner, "B0MON02")

	ctx := context.Background()
	t0 := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

	// Advance #1 → active.
	sc.pos, sc.at, sc.ok = 100, t0, true
	if _, err := svc.RunMonitorOnce(ctx, monitorCfg, t0); err != nil {
		t.Fatalf("pass1: %v", err)
	}
	// Advance #2 at +30s (the T2 capture cadence) → still active.
	t1 := t0.Add(30 * time.Second)
	sc.pos, sc.at = 250, t1
	if _, err := svc.RunMonitorOnce(ctx, monitorCfg, t1); err != nil {
		t.Fatalf("pass2: %v", err)
	}
	if active, _ := hz.DB.CountActiveKindleMonitorBooks(ctx, owner); active != 1 {
		t.Fatalf("active after 2 advances = %d, want 1", active)
	}

	// A pass past the idle gap with NO further advance: the book falls back to L1.
	t2 := t1.Add(monitorCfg.IdleGap + time.Second)
	// position unchanged (250) → no advance this poll
	if _, err := svc.RunMonitorOnce(ctx, monitorCfg, t2); err != nil {
		t.Fatalf("pass3: %v", err)
	}
	if active, _ := hz.DB.CountActiveKindleMonitorBooks(ctx, owner); active != 0 {
		t.Fatalf("active after idle gap = %d, want 0 (left L2)", active)
	}
}

func TestReadingMonitor_AdvanceEmitsMetricsAndReadingActivity(t *testing.T) {
	hz := testutil.NewHarness(t)
	sc := &fakeSidecar{ok: false}
	svc, owner, _ := newMonitorService(t, hz, sc, db.ReadingMonitorModeDebounced)
	seedInProgressBook(t, hz.DB, owner, "B0MON03")

	ctx := context.Background()
	t0 := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)

	advBefore := promtestutil.ToFloat64(metrics.ReadingMonitorAdvancesTotal.WithLabelValues("kindle"))
	secsBefore := promtestutil.ToFloat64(metrics.ReadingActivitySecondsTotal.WithLabelValues("kindle"))

	// Two advances 30s apart → the second has a prior advance, so its 30s in-session
	// interval is reading-time that lands in reading_activity + the seconds counter.
	sc.pos, sc.at, sc.ok = 100, t0, true
	if _, err := svc.RunMonitorOnce(ctx, monitorCfg, t0); err != nil {
		t.Fatalf("pass1: %v", err)
	}
	t1 := t0.Add(30 * time.Second)
	sc.pos, sc.at = 250, t1
	if _, err := svc.RunMonitorOnce(ctx, monitorCfg, t1); err != nil {
		t.Fatalf("pass2: %v", err)
	}

	if got := promtestutil.ToFloat64(metrics.ReadingMonitorAdvancesTotal.WithLabelValues("kindle")) - advBefore; got != 2 {
		t.Errorf("advances_total delta = %v, want 2", got)
	}
	if got := promtestutil.ToFloat64(metrics.ReadingActivitySecondsTotal.WithLabelValues("kindle")) - secsBefore; got != 30 {
		t.Errorf("reading_activity_seconds_total delta = %v, want 30", got)
	}

	// The shared composition landed the 30s onto the reading_activity day bucket.
	day := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	rows, err := hz.DB.ListReadingActivity(ctx, owner, "kindle", day, day)
	if err != nil {
		t.Fatalf("list activity: %v", err)
	}
	if len(rows) != 1 || rows[0].ListeningSeconds != 30 {
		t.Fatalf("reading_activity = %+v, want one kindle bucket @ 30s", rows)
	}

	// The active-books gauge reflects the one book in L2.
	if got := promtestutil.ToFloat64(metrics.ReadingMonitorActiveBooks.WithLabelValues("kindle")); got != 1 {
		t.Errorf("active_books gauge = %v, want 1", got)
	}
}

func TestReadingMonitor_DebouncedVsVerboseToastCounts(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)

	// Same 2-advance script under each mode. debounced = one toast (the session
	// start); verbose = one toast per advance (two).
	run := func(mode string) int {
		hz := testutil.NewHarness(t)
		sc := &fakeSidecar{ok: false}
		svc, owner, hub := newMonitorService(t, hz, sc, mode)
		seedInProgressBook(t, hz.DB, owner, "B0MON04")
		ch, unsub := hub.Subscribe(owner)
		defer unsub()

		sc.pos, sc.at, sc.ok = 100, t0, true
		if _, err := svc.RunMonitorOnce(ctx, monitorCfg, t0); err != nil {
			t.Fatalf("%s pass1: %v", mode, err)
		}
		t1 := t0.Add(30 * time.Second)
		sc.pos, sc.at = 250, t1
		if _, err := svc.RunMonitorOnce(ctx, monitorCfg, t1); err != nil {
			t.Fatalf("%s pass2: %v", mode, err)
		}
		return drainToasts(ch)
	}

	if n := run(db.ReadingMonitorModeDebounced); n != 1 {
		t.Errorf("debounced toasts = %d, want 1 (one per session)", n)
	}
	if n := run(db.ReadingMonitorModeVerbose); n != 2 {
		t.Errorf("verbose toasts = %d, want 2 (one per advance)", n)
	}
}

func TestReadingMonitor_DisabledUserIsNoOp(t *testing.T) {
	hz := testutil.NewHarness(t)
	sc := &fakeSidecar{pos: 100, at: time.Now().UTC(), ok: true}
	svc, owner, _ := newMonitorService(t, hz, sc, db.ReadingMonitorModeDebounced)
	seedInProgressBook(t, hz.DB, owner, "B0MON05")

	ctx := context.Background()
	// Disable the monitor for this user — RunMonitorOnce must skip them entirely.
	disabled := false
	if err := hz.DB.SetReadingMonitorSettings(ctx, owner, &disabled, nil); err != nil {
		t.Fatalf("disable: %v", err)
	}

	adv, err := svc.RunMonitorOnce(ctx, monitorCfg, time.Now().UTC())
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if adv != 0 {
		t.Errorf("advances = %d, want 0 (disabled user is a no-op)", adv)
	}
	if sc.calls != 0 {
		t.Errorf("sidecar calls = %d, want 0 (disabled → never polled)", sc.calls)
	}
	states, err := hz.DB.ListKindleMonitorStates(ctx, owner)
	if err != nil {
		t.Fatalf("list states: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("monitor states = %d, want 0 (disabled writes nothing)", len(states))
	}
}
