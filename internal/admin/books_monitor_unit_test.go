// books_monitor_unit_test.go — pure, no-DB/no-network coverage of the reading
// monitor's streaming core (streamReadingMonitor) + its clamps + sort. A FAKE
// amazon.KindleSidecar returns a SCRIPTED, advancing position so we assert the
// exact frame protocol the FE parses: first-seen + advance emit `sample`
// frames (with every field), an unchanged position deduces to heartbeat-only,
// a per-book fetch error emits an `error` frame and the stream continues, and
// every cycle emits exactly one `heartbeat`.
package admin

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// fakeSidecar is a scripted amazon.KindleSidecar. Each call to
// FetchLastPagePosition returns the next scripted (position, ok, err); the last
// entry repeats once the script is exhausted so the loop can run extra cycles
// deterministically. eventTime is fixed so CreationTime is assertable.
type fakeSidecar struct {
	positions []int64 // scripted furthest-page-read per call, in call order
	ok        []bool  // parallel ok flags (default true when shorter)
	err       []error // parallel errors (default nil when shorter)
	eventTime time.Time
	calls     int64
}

func (f *fakeSidecar) FetchLastPagePosition(_ context.Context, _ *amazon.DeviceCredential, _ string) (int64, time.Time, bool, error) {
	i := int(atomic.AddInt64(&f.calls, 1)) - 1
	idx := i
	if idx >= len(f.positions) {
		idx = len(f.positions) - 1 // repeat the last scripted value
	}
	ok := true
	if idx < len(f.ok) {
		ok = f.ok[idx]
	}
	var err error
	if idx < len(f.err) {
		err = f.err[idx]
	}
	return f.positions[idx], f.eventTime, ok, err
}

// collectFrames runs streamReadingMonitor against a fixed one-book lister and a
// tiny interval, cancelling the loop after `targetHeartbeats` heartbeat frames
// so the run is deterministic regardless of wall-clock timing. It returns every
// frame emitted, in order.
func collectFrames(t *testing.T, sc amazon.KindleSidecar, books []db.ReadingItem, targetHeartbeats int) []monitorFrame {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		mu     sync.Mutex
		frames []monitorFrame
		beats  int
	)
	lister := func(context.Context) ([]db.ReadingItem, error) { return books, nil }
	emit := func(f monitorFrame) error {
		mu.Lock()
		defer mu.Unlock()
		frames = append(frames, f)
		if f.Type == "heartbeat" {
			beats++
			if beats >= targetHeartbeats {
				cancel() // next select hits ctx.Done → clean stop
			}
		}
		return nil
	}

	done := make(chan struct{})
	go func() {
		_ = streamReadingMonitor(ctx, sc, &amazon.DeviceCredential{}, lister, 5*time.Millisecond, emit)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streamReadingMonitor did not stop within 5s")
	}
	mu.Lock()
	defer mu.Unlock()
	out := make([]monitorFrame, len(frames))
	copy(out, frames)
	return out
}

func framesOfType(frames []monitorFrame, typ string) []monitorFrame {
	var out []monitorFrame
	for _, f := range frames {
		if f.Type == typ {
			out = append(out, f)
		}
	}
	return out
}

func TestStreamReadingMonitor_ScriptedAdvance_EmitsSampleAndHeartbeatFrames(t *testing.T) {
	evt := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	// Cycle 1: 100 (first-seen → sample). Cycle 2: 150 (advanced → sample).
	// Cycle 3: 150 (unchanged → NO sample; heartbeat only).
	sc := &fakeSidecar{positions: []int64{100, 150, 150}, eventTime: evt}
	book := db.ReadingItem{Source: "kindle", ExternalID: "ASIN1", Title: "Book One", Status: "reading"}

	frames := collectFrames(t, sc, []db.ReadingItem{book}, 3)

	samples := framesOfType(frames, "sample")
	beats := framesOfType(frames, "heartbeat")

	if len(beats) != 3 {
		t.Fatalf("want 3 heartbeat frames (one per cycle), got %d (frames=%+v)", len(beats), frames)
	}
	if len(samples) != 2 {
		t.Fatalf("want 2 sample frames (first-seen + one advance; the unchanged 3rd cycle must dedup), got %d (frames=%+v)", len(samples), frames)
	}

	// First sample: every field the FE reads must be populated correctly.
	s0 := samples[0]
	if s0.ASIN != "ASIN1" || s0.Title != "Book One" {
		t.Errorf("sample[0] identity: got asin=%q title=%q", s0.ASIN, s0.Title)
	}
	if s0.Location != 100 {
		t.Errorf("sample[0] location: want 100, got %d", s0.Location)
	}
	if s0.CreationTime != evt.Format(time.RFC3339) {
		t.Errorf("sample[0] creationTime: want %q, got %q", evt.Format(time.RFC3339), s0.CreationTime)
	}
	if s0.SampledAt == "" {
		t.Errorf("sample[0] sampledAt must be a server RFC3339 timestamp, got empty")
	}
	if _, err := time.Parse(time.RFC3339, s0.SampledAt); err != nil {
		t.Errorf("sample[0] sampledAt not RFC3339: %q (%v)", s0.SampledAt, err)
	}

	// Second sample is the advance.
	if samples[1].Location != 150 {
		t.Errorf("sample[1] location: want 150 (the advance), got %d", samples[1].Location)
	}

	// Heartbeats report the polled book count.
	for i, b := range beats {
		if b.Books != 1 || b.Polled != 1 {
			t.Errorf("heartbeat[%d]: want books=1 polled=1, got books=%d polled=%d", i, b.Books, b.Polled)
		}
	}
}

func TestStreamReadingMonitor_PerBookFetchError_EmitsErrorFrameAndContinues(t *testing.T) {
	evt := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	// Cycle 1: a fetch error. Cycle 2+: a real position (recovered).
	sc := &fakeSidecar{
		positions: []int64{0, 220},
		ok:        []bool{false, true},
		err:       []error{context.DeadlineExceeded, nil},
		eventTime: evt,
	}
	book := db.ReadingItem{Source: "kindle", ExternalID: "ASIN9", Title: "Flaky Book", Status: "reading"}

	frames := collectFrames(t, sc, []db.ReadingItem{book}, 2)

	errs := framesOfType(frames, "error")
	if len(errs) != 1 {
		t.Fatalf("want exactly 1 error frame from the cycle-1 fetch failure, got %d (frames=%+v)", len(errs), frames)
	}
	if errs[0].ASIN != "ASIN9" || errs[0].Error == "" {
		t.Errorf("error frame should carry the book asin + a message, got asin=%q err=%q", errs[0].ASIN, errs[0].Error)
	}
	// The stream kept going: cycle 2 recovered and emitted a real sample.
	samples := framesOfType(frames, "sample")
	if len(samples) != 1 || samples[0].Location != 220 {
		t.Fatalf("want 1 sample (loc 220) after recovery, got %+v", samples)
	}
	if len(framesOfType(frames, "heartbeat")) != 2 {
		t.Errorf("want a heartbeat every cycle even when a fetch errored")
	}
}

func TestClampInterval(t *testing.T) {
	cases := []struct{ in, want time.Duration }{
		{0, monitorMinInterval},
		{1 * time.Second, monitorMinInterval},
		{monitorMinInterval, monitorMinInterval},
		{6 * time.Second, 6 * time.Second},
		{monitorMaxInterval, monitorMaxInterval},
		{5 * time.Minute, monitorMaxInterval},
	}
	for _, c := range cases {
		if got := clampInterval(c.in); got != c.want {
			t.Errorf("clampInterval(%v): want %v, got %v", c.in, c.want, got)
		}
	}
}

func TestClampLimit(t *testing.T) {
	cases := []struct{ in, want int }{
		{-5, 1}, {0, 1}, {1, 1}, {12, 12}, {monitorMaxLimit, monitorMaxLimit}, {1000, monitorMaxLimit},
	}
	for _, c := range cases {
		if got := clampLimit(c.in); got != c.want {
			t.Errorf("clampLimit(%d): want %d, got %d", c.in, c.want, got)
		}
	}
}

func TestSortBySyncedDesc(t *testing.T) {
	t0 := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	items := []db.ReadingItem{
		{Title: "old", SyncedAt: t0},
		{Title: "newest", SyncedAt: t0.Add(2 * time.Hour)},
		{Title: "mid", SyncedAt: t0.Add(1 * time.Hour)},
	}
	sortBySyncedDesc(items)
	got := []string{items[0].Title, items[1].Title, items[2].Title}
	want := []string{"newest", "mid", "old"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortBySyncedDesc order: want %v, got %v", want, got)
		}
	}
}
