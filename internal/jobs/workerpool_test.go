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

// The SQL behind the outcome gauges is the part that can be silently wrong —
// percentile_cont syntax, the interval cast, the NULL handling. Exercise it
// against a real database rather than trusting it compiles.
func TestRecentOutcomesReadsRealRows(t *testing.T) {
	s, ctx := newTestStore(t)

	reg := NewRegistry()
	reg.Register("fast", HandlerFunc(func(context.Context, Job) error { return nil }))
	reg.Register("broken", HandlerFunc(func(context.Context, Job) error {
		return context.DeadlineExceeded // any error → terminal failure at 1 attempt
	}))

	for i := 0; i < 3; i++ {
		if _, err := s.Enqueue(ctx, "fast", "", nil, 1, time.Time{}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	if _, err := s.Enqueue(ctx, "broken", "", nil, 1, time.Time{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	p := NewLocalProvider(s, quietLogger(), "outcomes-test")
	p.SetWorkers(2)
	if err := p.Drain(ctx, reg); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	out, ok := s.RecentOutcomes(ctx)
	if !ok {
		t.Fatal("RecentOutcomes reported not-ok against a live DB")
	}

	fast, hasFast := out["fast"]
	if !hasFast {
		t.Fatalf("no outcomes for kind 'fast'; got %v", out)
	}
	if fast.ByStatus["done"] != 3 {
		t.Errorf("fast done = %d, want 3 (ByStatus = %v)", fast.ByStatus["done"], fast.ByStatus)
	}
	// Percentiles must be real numbers, not NULL-collapsed to something absurd.
	if fast.P50 < 0 || fast.P95 < 0 {
		t.Errorf("negative percentiles: p50=%v p95=%v", fast.P50, fast.P95)
	}

	if broken, hasBroken := out["broken"]; !hasBroken {
		t.Error("no outcomes for the failing kind — failures must be visible, they are the point")
	} else if broken.ByStatus["failed"] != 1 {
		t.Errorf("broken failed = %d, want 1 (ByStatus = %v)", broken.ByStatus["failed"], broken.ByStatus)
	}
}

// THIS TEST EXISTS BECAUSE THE SQL SHIPPED BROKEN. QueueDepth's original query
// put FILTER outside the EXTRACT rather than inside min(), which Postgres
// rejects as a syntax error. QueueDepth then returned ok=false, the collector
// emitted nothing, and jobs_queue_depth was silently absent in production while
// 604 jobs sat queued.
//
// The unit test at the metrics layer stubbed the provider, so it never ran a
// query — it asserted the collector's plumbing and proved nothing about the SQL.
// Anything whose failure mode is "returns ok=false and emits no series" needs to
// touch a real database, because that failure is indistinguishable from an idle
// queue.
func TestQueueDepthReadsRealRows(t *testing.T) {
	s, ctx := newTestStore(t)

	// 3 due now, 2 scheduled for the future.
	for i := 0; i < 3; i++ {
		if _, err := s.Enqueue(ctx, "depth-due", "", nil, 1, time.Time{}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Enqueue(ctx, "depth-later", "", nil, 1, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("enqueue future: %v", err)
		}
	}

	out, ok := s.QueueDepth(ctx, 2*time.Minute)
	if !ok {
		t.Fatal("QueueDepth reported not-ok against a live DB — the query is broken")
	}

	due, hasDue := out["depth-due"]
	if !hasDue {
		t.Fatalf("no depth for 'depth-due'; got %v", out)
	}
	if due.Queued != 3 {
		t.Errorf("queued = %d, want 3", due.Queued)
	}
	if due.Scheduled != 0 {
		t.Errorf("scheduled = %d, want 0 — due work must not be counted as scheduled", due.Scheduled)
	}
	// The staleness signal must be a real, non-negative number.
	if due.OldestQueuedAge < 0 {
		t.Errorf("oldest age = %v, want >= 0", due.OldestQueuedAge)
	}

	// Future-dated work is 'scheduled', NOT 'queued'. Conflating them would fire
	// a backlog alert on every periodic kind's normal steady state.
	later, hasLater := out["depth-later"]
	if !hasLater {
		t.Fatalf("no depth for 'depth-later'; got %v", out)
	}
	if later.Scheduled != 2 {
		t.Errorf("scheduled = %d, want 2", later.Scheduled)
	}
	if later.Queued != 0 {
		t.Errorf("queued = %d, want 0 — future run_at is scheduled, not due", later.Queued)
	}
	if later.OldestQueuedAge != 0 {
		t.Errorf("oldest age = %v, want 0 when nothing is due", later.OldestQueuedAge)
	}
}

// A row still marked running whose heartbeat has lapsed is STALE, not running.
// Counting it as running makes the gauge overcount live execution after any
// deploy that restarts pods mid-job — observed at 10 while the fleet semaphore
// correctly held 5.
func TestQueueDepthSeparatesStaleFromRunning(t *testing.T) {
	s, ctx := newTestStore(t)

	id, err := s.Enqueue(ctx, "stale-kind", "", nil, 1, time.Time{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, _, err := s.ClaimNext(ctx, "dead-pod", nil, nil); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Simulate the pod dying: the row stays running, the heartbeat stops.
	if _, err := s.pool.Exec(ctx,
		`UPDATE jobs SET heartbeat_at = now() - interval '10 minutes',
		                 locked_at    = now() - interval '10 minutes',
		                 started_at   = now() - interval '10 minutes'
		  WHERE id = $1`, id); err != nil {
		t.Fatalf("age the row: %v", err)
	}

	out, ok := s.QueueDepth(ctx, 2*time.Minute)
	if !ok {
		t.Fatal("QueueDepth not-ok")
	}
	got := out["stale-kind"]
	if got.Stale != 1 {
		t.Errorf("stale = %d, want 1 — a dead pod's row must not be counted as live", got.Stale)
	}
	if got.Running != 0 {
		t.Errorf("running = %d, want 0 — the heartbeat lapsed well past the lease", got.Running)
	}
}
