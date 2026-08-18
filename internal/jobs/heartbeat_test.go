package jobs

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// heartbeatAt reads the raw heartbeat_at column (not exposed on Job) for asserts.
func heartbeatAt(t *testing.T, s *Store, ctx context.Context, id int64) *time.Time {
	t.Helper()
	var hb *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT heartbeat_at FROM jobs WHERE id=$1`, id).Scan(&hb); err != nil {
		t.Fatalf("read heartbeat_at: %v", err)
	}
	return hb
}

// claimStamps returns started_at/finished_at/status for a row (asserting resets).
func rowState(t *testing.T, s *Store, ctx context.Context, id int64) (status string, started, finished, hb *time.Time, runAt time.Time, errMsg string) {
	t.Helper()
	if err := s.pool.QueryRow(ctx,
		`SELECT status, started_at, finished_at, heartbeat_at, run_at, error FROM jobs WHERE id=$1`, id).
		Scan(&status, &started, &finished, &hb, &runAt, &errMsg); err != nil {
		t.Fatalf("rowState: %v", err)
	}
	return
}

// TestHeartbeatBumps: claiming stamps heartbeat_at, and Heartbeat refreshes it.
func TestHeartbeatBumps(t *testing.T) {
	s, ctx := newTestStore(t)
	id, _ := s.Enqueue(ctx, "demo", "", nil, 1, time.Now().Add(-time.Second))
	if _, ok, _ := s.ClaimNext(ctx, "w1", nil, nil); !ok {
		t.Fatal("expected to claim")
	}
	if hb := heartbeatAt(t, s, ctx, id); hb == nil {
		t.Fatal("claim should have stamped heartbeat_at")
	}

	// Backdate the stamp, then Heartbeat should push it forward to ~now.
	if _, err := s.pool.Exec(ctx,
		`UPDATE jobs SET heartbeat_at = now() - interval '10 minutes' WHERE id=$1`, id); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	before := heartbeatAt(t, s, ctx, id)
	if err := s.Heartbeat(ctx, id); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	after := heartbeatAt(t, s, ctx, id)
	if !after.After(*before) {
		t.Fatalf("Heartbeat did not advance heartbeat_at: before=%v after=%v", before, after)
	}
	if time.Since(*after) > time.Minute {
		t.Fatalf("heartbeat_at not ~now after Heartbeat: %v", after)
	}

	// Heartbeat on a non-running (done) job is a guarded no-op (no error, no bump).
	if err := s.Complete(ctx, id); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	doneHB := heartbeatAt(t, s, ctx, id)
	if err := s.Heartbeat(ctx, id); err != nil {
		t.Fatalf("Heartbeat(done): %v", err)
	}
	if got := heartbeatAt(t, s, ctx, id); !got.Equal(*doneHB) {
		t.Fatalf("Heartbeat mutated a non-running row: %v -> %v", doneHB, got)
	}
}

// TestReapStaleRunningReclaimsToQueued: a stale running job with attempts left is
// reset to queued (re-runnable now) and becomes claimable again.
func TestReapStaleRunningReclaimsToQueued(t *testing.T) {
	s, ctx := newTestStore(t)
	id, _ := s.Enqueue(ctx, "demo", "", nil, 3, time.Now().Add(-time.Second))
	if _, ok, _ := s.ClaimNext(ctx, "w1", nil, nil); !ok {
		t.Fatal("expected to claim")
	}
	// Make it stale: heartbeat/locked/started all well past the lease.
	if _, err := s.pool.Exec(ctx,
		`UPDATE jobs SET heartbeat_at = now() - interval '10 minutes',
		        locked_at = now() - interval '10 minutes',
		        started_at = now() - interval '10 minutes' WHERE id=$1`, id); err != nil {
		t.Fatalf("stale: %v", err)
	}

	n, err := s.ReapStaleRunning(ctx, 120*time.Second)
	if err != nil {
		t.Fatalf("ReapStaleRunning: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed = %d, want 1", n)
	}
	status, started, _, hb, runAt, errMsg := rowState(t, s, ctx, id)
	if status != "queued" {
		t.Fatalf("status = %q, want queued", status)
	}
	if started != nil || hb != nil {
		t.Fatalf("started_at/heartbeat_at not cleared: started=%v hb=%v", started, hb)
	}
	if time.Since(runAt) > time.Minute {
		t.Fatalf("run_at not reset to now: %v", runAt)
	}
	if errMsg == "" {
		t.Fatal("expected a reclaim note in error")
	}
	// Attempt count is preserved (was 1) and it's claimable again.
	j, ok, _ := s.ClaimNext(ctx, "w2", nil, nil)
	if !ok || j.ID != id || j.Attempts != 2 {
		t.Fatalf("reclaimed job not re-claimable as attempt 2: ok=%v %+v", ok, j)
	}
}

// TestReapStaleRunningExhaustedToFailed: a stale running job with no attempts left
// becomes terminal 'failed'.
func TestReapStaleRunningExhaustedToFailed(t *testing.T) {
	s, ctx := newTestStore(t)
	id, _ := s.Enqueue(ctx, "demo", "", nil, 1, time.Now().Add(-time.Second))
	if _, ok, _ := s.ClaimNext(ctx, "w1", nil, nil); !ok { // attempts now 1 == max
		t.Fatal("expected to claim")
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE jobs SET heartbeat_at = now() - interval '10 minutes' WHERE id=$1`, id); err != nil {
		t.Fatalf("stale: %v", err)
	}
	n, err := s.ReapStaleRunning(ctx, 120*time.Second)
	if err != nil {
		t.Fatalf("ReapStaleRunning: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed = %d, want 1", n)
	}
	status, _, finished, _, _, errMsg := rowState(t, s, ctx, id)
	if status != "failed" {
		t.Fatalf("status = %q, want failed", status)
	}
	if finished == nil {
		t.Fatal("failed row should have finished_at")
	}
	if errMsg == "" {
		t.Fatal("expected an error note")
	}
	if _, ok, _ := s.ClaimNext(ctx, "w2", nil, nil); ok {
		t.Fatal("a terminally-failed reaped job must not be claimable")
	}
}

// TestReapStaleRunningSkipsFresh: a running job with a fresh heartbeat survives.
func TestReapStaleRunningSkipsFresh(t *testing.T) {
	s, ctx := newTestStore(t)
	id, _ := s.Enqueue(ctx, "demo", "", nil, 3, time.Now().Add(-time.Second))
	if _, ok, _ := s.ClaimNext(ctx, "w1", nil, nil); !ok { // heartbeat_at = now()
		t.Fatal("expected to claim")
	}
	n, err := s.ReapStaleRunning(ctx, 120*time.Second)
	if err != nil {
		t.Fatalf("ReapStaleRunning: %v", err)
	}
	if n != 0 {
		t.Fatalf("reclaimed = %d, want 0 (fresh heartbeat)", n)
	}
	if status, _, _, _, _, _ := rowState(t, s, ctx, id); status != "running" {
		t.Fatalf("fresh job status = %q, want running (untouched)", status)
	}
}

// TestReapStaleRunningBacklogNullHeartbeat: the CURRENT backlog case — a row
// created before the heartbeat column existed has heartbeat_at NULL; the reaper's
// COALESCE fallback onto an old locked_at still marks it stale and reclaims it.
func TestReapStaleRunningBacklogNullHeartbeat(t *testing.T) {
	s, ctx := newTestStore(t)
	id, _ := s.Enqueue(ctx, "demo", "", nil, 3, time.Now().Add(-time.Second))
	if _, ok, _ := s.ClaimNext(ctx, "w1", nil, nil); !ok {
		t.Fatal("expected to claim")
	}
	// Simulate a pre-heartbeat zombie: NULL heartbeat, old locked_at/started_at.
	if _, err := s.pool.Exec(ctx,
		`UPDATE jobs SET heartbeat_at = NULL,
		        locked_at = now() - interval '10 minutes',
		        started_at = now() - interval '10 minutes' WHERE id=$1`, id); err != nil {
		t.Fatalf("backlog setup: %v", err)
	}
	n, err := s.ReapStaleRunning(ctx, 120*time.Second)
	if err != nil {
		t.Fatalf("ReapStaleRunning: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed = %d, want 1 (NULL-heartbeat backlog row)", n)
	}
	if status, _, _, _, _, _ := rowState(t, s, ctx, id); status != "queued" {
		t.Fatalf("backlog job status = %q, want queued", status)
	}
}

// fakeEnq counts Enqueue calls for the coalesce test without touching the DB.
type fakeEnq struct{ calls int }

func (f *fakeEnq) Enqueue(ctx context.Context, kind string, payload []byte, opts ...EnqueueOption) (int64, error) {
	f.calls++
	return int64(f.calls), nil
}

// TestSchedulerCoalesce: fire() skips enqueue for a due kind that already has a
// queued (or running) row, but enqueues when the kind is idle.
func TestSchedulerCoalesce(t *testing.T) {
	s, ctx := newTestStore(t)
	enq := &fakeEnq{}
	sched := NewScheduler(s, enq, slog.New(slog.NewTextHandler(nopWriter{}, nil)))

	if err := sched.Register(ctx, "cron", time.Hour); err != nil {
		t.Fatalf("Register: %v", err)
	}
	forceDue := func() {
		if _, err := s.pool.Exec(ctx,
			`UPDATE job_schedules SET next_run_at = now() - interval '1 minute' WHERE kind='cron'`); err != nil {
			t.Fatalf("force due: %v", err)
		}
	}

	// A queued job of that kind already exists → the due tick must NOT enqueue.
	if _, err := s.Enqueue(ctx, "cron", "", nil, 1, time.Time{}); err != nil {
		t.Fatalf("seed queued: %v", err)
	}
	forceDue()
	sched.fire(ctx)
	if enq.calls != 0 {
		t.Fatalf("coalesce failed: enqueued %d times while one queued", enq.calls)
	}

	// Drain the pending row (claim + complete) → the kind is idle → fire enqueues.
	j, ok, _ := s.ClaimNext(ctx, "w1", nil, nil)
	if !ok {
		t.Fatal("expected to claim the seeded cron job")
	}
	if err := s.Complete(ctx, j.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	forceDue()
	sched.fire(ctx)
	if enq.calls != 1 {
		t.Fatalf("idle kind should enqueue once, got %d", enq.calls)
	}
}

// nopWriter drops scheduler log output in tests.
type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
