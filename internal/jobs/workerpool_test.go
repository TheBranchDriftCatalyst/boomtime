package jobs

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// The whole point of boom-jokv. A pool that silently ran serially would pass
// every "did the jobs finish" assertion, so this measures OVERLAP directly:
// peak simultaneous handlers must exceed 1.
func TestDrainRunsJobsConcurrently(t *testing.T) {
	s, ctx := newTestStore(t)

	const jobCount = 8
	var (
		mu       sync.Mutex
		inFlight int
		peak     int
	)
	reg := NewRegistry()
	reg.Register("slow", HandlerFunc(func(context.Context, Job) error {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()
		// Long enough that serial execution could not overlap by accident.
		time.Sleep(120 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	}))

	for i := 0; i < jobCount; i++ {
		if _, err := s.Enqueue(ctx, "slow", "", nil, 1, time.Time{}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	p := NewLocalProvider(s, quietLogger(), "pool-test")
	p.SetWorkers(4)
	if err := p.Drain(ctx, reg); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	mu.Lock()
	gotPeak := peak
	mu.Unlock()
	if gotPeak < 2 {
		t.Errorf("peak concurrent handlers = %d, want >1 — the pool is running serially, "+
			"which is the exact bug boom-jokv fixes", gotPeak)
	}
	if gotPeak > 4 {
		t.Errorf("peak concurrent handlers = %d, exceeds the 4 workers configured", gotPeak)
	}
}

// Every job must run exactly once. N in-process claimers rely on
// FOR UPDATE SKIP LOCKED for this; if that assumption were wrong the pool would
// double-execute, which for liberation means downloading the same book twice.
func TestDrainPoolRunsEachJobExactlyOnce(t *testing.T) {
	s, ctx := newTestStore(t)

	const jobCount = 25
	var runs sync.Map
	var total atomic.Int64

	reg := NewRegistry()
	reg.Register("once", HandlerFunc(func(_ context.Context, j Job) error {
		total.Add(1)
		if _, dup := runs.LoadOrStore(j.ID, true); dup {
			t.Errorf("job %d ran twice — SKIP LOCKED did not hold across in-process workers", j.ID)
		}
		return nil
	}))

	for i := 0; i < jobCount; i++ {
		if _, err := s.Enqueue(ctx, "once", "", nil, 1, time.Time{}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	p := NewLocalProvider(s, quietLogger(), "once-test")
	p.SetWorkers(6)
	if err := p.Drain(ctx, reg); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if got := total.Load(); got != jobCount {
		t.Errorf("handler ran %d times, want exactly %d", got, jobCount)
	}
}

// EXIT SEMANTICS. A worker that finds the queue empty stops, but Drain must not
// return while a sibling is still mid-handler — a ScaledJob pod terminating then
// would kill a running job, reintroducing the mid-job-kill drain mode exists to
// prevent.
func TestDrainWaitsForSiblingsBeforeReturning(t *testing.T) {
	s, ctx := newTestStore(t)

	var running atomic.Bool
	var finished atomic.Bool

	reg := NewRegistry()
	reg.Register("lonely", HandlerFunc(func(context.Context, Job) error {
		running.Store(true)
		time.Sleep(250 * time.Millisecond)
		finished.Store(true)
		running.Store(false)
		return nil
	}))

	// ONE job, SIX workers: five find the queue empty immediately and stop while
	// the sixth is still working. That is the race this guards.
	if _, err := s.Enqueue(ctx, "lonely", "", nil, 1, time.Time{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	p := NewLocalProvider(s, quietLogger(), "sibling-test")
	p.SetWorkers(6)
	if err := p.Drain(ctx, reg); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	if !finished.Load() {
		t.Error("Drain returned before the in-flight job finished — a ScaledJob pod would have killed it")
	}
	if running.Load() {
		t.Error("Drain returned with a handler still running")
	}
}

// SetWorkers must clamp rather than deadlock or spin on a nonsense value.
func TestSetWorkersClamps(t *testing.T) {
	p := NewLocalProvider(nil, quietLogger(), "clamp")
	for _, n := range []int{0, -1, -100} {
		p.SetWorkers(n)
		if p.workers != 1 {
			t.Errorf("SetWorkers(%d) = %d, want clamped to 1", n, p.workers)
		}
	}
	p.SetWorkers(8)
	if p.workers != 8 {
		t.Errorf("SetWorkers(8) = %d", p.workers)
	}
}

// locked_by must still identify ONE claimant once a process has several.
func TestWorkerIDDisambiguatesSlots(t *testing.T) {
	p := NewLocalProvider(nil, quietLogger(), "pod-a")

	p.SetWorkers(1)
	if got := p.workerID(0); got != "pod-a" {
		t.Errorf("single-worker id = %q, want the bare pod id so existing rows read unchanged", got)
	}

	p.SetWorkers(3)
	seen := map[string]bool{}
	for slot := 0; slot < 3; slot++ {
		id := p.workerID(slot)
		if seen[id] {
			t.Errorf("slot %d reused worker id %q — locked_by would stop identifying one claimant", slot, id)
		}
		seen[id] = true
	}
}
