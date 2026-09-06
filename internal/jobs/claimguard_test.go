// claimguard_test.go — the compare-and-set discipline on executor-side writes.
//
// Store.Complete / Fail / Requeue / Heartbeat used to UPDATE by id alone. That
// made a stale worker's (or a post-cancel) terminal write authoritative over the
// reaper's and the admin's, which is how a single job ends up running twice or a
// cancelled job comes back to life. These tests drive both concrete races
// end-to-end against Postgres — the guard is a WHERE clause, so an in-memory
// fake would prove nothing.
package jobs

import (
	"context"
	"testing"
	"time"
)

// lapseLease backdates every liveness signal on a row so ReapStaleRunning
// considers its worker dead. This is the durable trace of "worker A blocked
// >lease inside its handler (a wedged pool, a hung external call) while its
// heartbeat writes stalled" — exec.go only logs heartbeat failures and lets the
// run continue, so A is still executing when the row is taken away from it.
func lapseLease(t *testing.T, s *Store, ctx context.Context, id int64) {
	t.Helper()
	if _, err := s.pool.Exec(ctx,
		`UPDATE jobs SET heartbeat_at = now() - interval '10 minutes',
		        locked_at    = now() - interval '10 minutes',
		        started_at   = now() - interval '10 minutes'
		  WHERE id = $1`, id); err != nil {
		t.Fatalf("lapse lease: %v", err)
	}
}

// TestStaleWorkerTerminalWriteCannotClobberReclaimedRow is the double-execution
// window. Worker A claims job 7, loses its lease mid-handler, the reaper requeues
// the row, worker B claims it and is genuinely running it — and THEN A's handler
// returns an error and A writes its result. Unguarded, that Fail-with-retry sets
// the row A no longer owns back to 'queued' while B is still inside the handler,
// so a third worker claims it: two concurrent runs of one job plus a corrupted
// attempt history. The guard must make A's write a no-op.
func TestStaleWorkerTerminalWriteCannotClobberReclaimedRow(t *testing.T) {
	s, ctx := newTestStore(t)
	id, err := s.Enqueue(ctx, "demo", "", nil, 3, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	a, ok, err := s.ClaimNext(ctx, "wA", nil, nil)
	if err != nil || !ok {
		t.Fatalf("ClaimNext(wA): ok=%v err=%v", ok, err)
	}
	if a.LockedBy != "wA" {
		t.Fatalf("claim did not carry its lock owner: LockedBy=%q, want wA", a.LockedBy)
	}

	lapseLease(t, s, ctx, id)
	if n, err := s.ReapStaleRunning(ctx, time.Minute); err != nil || n != 1 {
		t.Fatalf("ReapStaleRunning: n=%d err=%v, want 1", n, err)
	}

	b, ok, err := s.ClaimNext(ctx, "wB", nil, nil)
	if err != nil || !ok {
		t.Fatalf("ClaimNext(wB) after reap: ok=%v err=%v", ok, err)
	}
	if b.ID != id || b.LockedBy != "wB" || b.Attempts != 2 {
		t.Fatalf("re-claim wrong: id=%d locked_by=%q attempts=%d", b.ID, b.LockedBy, b.Attempts)
	}

	// --- A finally returns an error and records a retry. It must not land. ---
	retryAt := time.Now().Add(-time.Minute) // immediately due, maximising the damage
	changed, err := s.Fail(ctx, id, a.LockedBy, "A's late error", &retryAt)
	if err != nil {
		t.Fatalf("Fail from stale worker: %v", err)
	}
	if changed {
		t.Error("stale worker's Fail reported a row change — it rewrote a claim it no longer holds")
	}

	status, _, _, _, _, errMsg := rowState(t, s, ctx, id)
	if status != string(StatusRunning) {
		t.Fatalf("row status = %q, want running: the stale worker's retry-write flipped a LIVE run back to queued", status)
	}
	if errMsg == "A's late error" {
		t.Error("stale worker overwrote the error column of a row owned by another worker")
	}

	// The decisive symptom of the bug: a third worker being handed a job that
	// worker B is at this instant executing.
	if j, claimed, _ := s.ClaimNext(ctx, "wC", nil, nil); claimed {
		t.Fatalf("job %d became claimable while worker B is still running it — two workers now execute the same job", j.ID)
	}

	// Complete has the identical hole (it would stamp 'done' over B's live run).
	if changed, err := s.Complete(ctx, id, a.LockedBy); err != nil || changed {
		t.Errorf("stale worker's Complete: changed=%v err=%v, want changed=false", changed, err)
	}
	if st, _, _, _, _, _ := rowState(t, s, ctx, id); st != string(StatusRunning) {
		t.Fatalf("row status = %q after stale Complete, want running", st)
	}

	// And A must not keep B's row looking alive on B's behalf: if it did, a row
	// whose real owner also died would never be reaped again.
	if _, err := s.pool.Exec(ctx,
		`UPDATE jobs SET heartbeat_at = now() - interval '10 minutes' WHERE id = $1`, id); err != nil {
		t.Fatalf("backdate heartbeat: %v", err)
	}
	before := heartbeatAt(t, s, ctx, id)
	if err := s.Heartbeat(ctx, id, a.LockedBy); err != nil {
		t.Fatalf("Heartbeat from stale worker: %v", err)
	}
	if after := heartbeatAt(t, s, ctx, id); !after.Equal(*before) {
		t.Errorf("stale worker's heartbeat refreshed a row owned by wB: %v -> %v", before, after)
	}
	// The real owner's heartbeat still works.
	if err := s.Heartbeat(ctx, id, b.LockedBy); err != nil {
		t.Fatalf("Heartbeat from live owner: %v", err)
	}
	if after := heartbeatAt(t, s, ctx, id); !after.After(*before) {
		t.Errorf("live owner's heartbeat did not advance heartbeat_at: %v -> %v", before, after)
	}
}

// TestCancelledJobIsNotResurrectedByALateTerminalWrite is the cancel-resurrect
// window. The admin path is MarkCancelled (durable) THEN provider.Cancel (signal
// the handler's ctx). If the handler happens to return its own error in between,
// execute's single ctx.Err() check still reads nil and it writes a retry —
// unguarded, that flips 'cancelled' back to 'queued' and the job re-runs even
// though the API already answered {cancelled:true}.
func TestCancelledJobIsNotResurrectedByALateTerminalWrite(t *testing.T) {
	s, ctx := newTestStore(t)
	id, err := s.Enqueue(ctx, "demo", "", nil, 3, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	j, ok, err := s.ClaimNext(ctx, "wA", nil, nil)
	if err != nil || !ok {
		t.Fatalf("ClaimNext: ok=%v err=%v", ok, err)
	}

	if cancelled, err := s.MarkCancelled(ctx, id); err != nil || !cancelled {
		t.Fatalf("MarkCancelled: ok=%v err=%v", cancelled, err)
	}

	retryAt := time.Now().Add(-time.Minute)
	if changed, err := s.Fail(ctx, id, j.LockedBy, "handler error that raced the cancel", &retryAt); err != nil || changed {
		t.Errorf("Fail after cancel: changed=%v err=%v, want changed=false", changed, err)
	}
	if st, _, _, _, _, _ := rowState(t, s, ctx, id); st != string(StatusCancelled) {
		t.Fatalf("row status = %q after a post-cancel retry write, want cancelled", st)
	}
	if j2, claimed, _ := s.ClaimNext(ctx, "wB", nil, nil); claimed {
		t.Fatalf("cancelled job %d was re-queued and claimed — the admin's {cancelled:true} was a lie", j2.ID)
	}

	// Success races the cancel the same way.
	if changed, err := s.Complete(ctx, id, j.LockedBy); err != nil || changed {
		t.Errorf("Complete after cancel: changed=%v err=%v, want changed=false", changed, err)
	}
	if st, _, _, _, _, _ := rowState(t, s, ctx, id); st != string(StatusCancelled) {
		t.Fatalf("row status = %q after a post-cancel complete, want cancelled", st)
	}

	// And the slot-race requeue path cannot resurrect it either.
	if requeued, err := s.Requeue(ctx, id, j.LockedBy); err != nil || requeued {
		t.Errorf("Requeue after cancel: requeued=%v err=%v, want requeued=false", requeued, err)
	}
	if st, _, _, _, _, _ := rowState(t, s, ctx, id); st != string(StatusCancelled) {
		t.Fatalf("row status = %q after a post-cancel requeue, want cancelled", st)
	}
}

// TestRequeueRefundsTheAttemptChargedByClaim pins Requeue's documented contract:
// losing the concurrency-slot race "costs nothing". ClaimNext charges an attempt
// up front, so without a refund a saturated kind burns MaxAttempts on a job that
// never executed — its first REAL error is then terminal with zero retries, and
// the reaper terminal-fails it on the next deploy instead of requeueing.
func TestRequeueRefundsTheAttemptChargedByClaim(t *testing.T) {
	s, ctx := newTestStore(t)
	const maxAttempts = 3
	id, err := s.Enqueue(ctx, "capped", "", nil, maxAttempts, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// A saturated kind: ClaimNext orders by run_at, so the SAME oldest row is the
	// claim target on every pass and loses the Acquire race again and again.
	for race := 1; race <= 5; race++ {
		j, ok, err := s.ClaimNext(ctx, "w1", nil, nil)
		if err != nil || !ok {
			t.Fatalf("race %d: ClaimNext ok=%v err=%v", race, ok, err)
		}
		if j.Attempts != 1 {
			t.Fatalf("race %d: attempts=%d after claim, want 1 — %d lost slot-races have burned the retry budget of a job that has never run",
				race, j.Attempts, race-1)
		}
		requeued, err := s.Requeue(ctx, id, j.LockedBy)
		if err != nil || !requeued {
			t.Fatalf("race %d: Requeue requeued=%v err=%v", race, requeued, err)
		}
		var attempts int
		if err := s.pool.QueryRow(ctx, `SELECT attempts FROM jobs WHERE id=$1`, id).Scan(&attempts); err != nil {
			t.Fatalf("read attempts: %v", err)
		}
		if attempts != 0 {
			t.Fatalf("race %d: attempts=%d after Requeue, want 0 (the charge is refunded)", race, attempts)
		}
	}

	// After five lost races the job still has its whole retry budget: it can fail
	// for real and still be retried, which is the property the bug destroyed.
	j, ok, err := s.ClaimNext(ctx, "w1", nil, nil)
	if err != nil || !ok {
		t.Fatalf("final ClaimNext: ok=%v err=%v", ok, err)
	}
	if j.Attempts >= j.MaxAttempts {
		t.Fatalf("attempts=%d/%d on the first real execution — the job is one failure from terminal without ever having run",
			j.Attempts, j.MaxAttempts)
	}
	retryAt := time.Now().Add(-time.Minute)
	if changed, err := s.Fail(ctx, id, j.LockedBy, "first real failure", &retryAt); err != nil || !changed {
		t.Fatalf("Fail(retry): changed=%v err=%v, want changed=true", changed, err)
	}
	if _, ok, _ := s.ClaimNext(ctx, "w1", nil, nil); !ok {
		t.Fatal("job was not retried after its first real failure — its retries were consumed by slot-races")
	}
}

// TestUpsertScheduleShortenedIntervalTakesEffectImmediately pins the prod change
// that motivated the finding: BOOM_AUDIBLE_SYNC_INTERVAL 6h → 1h. The ON CONFLICT
// branch updated interval_seconds only, so next_run_at stayed up to 6h out and
// nothing fired at the new cadence for a full old period — while the admin
// /schedules view showed the new interval beside an inconsistent next run.
func TestUpsertScheduleShortenedIntervalTakesEffectImmediately(t *testing.T) {
	s, ctx := newTestStore(t)
	const kind = "books-audible-sync"

	scheduleFor := func() Schedule {
		t.Helper()
		all, err := s.ListSchedules(ctx)
		if err != nil {
			t.Fatalf("ListSchedules: %v", err)
		}
		for _, sc := range all {
			if sc.Kind == kind {
				return sc
			}
		}
		t.Fatalf("schedule %q not registered", kind)
		return Schedule{}
	}

	if err := s.UpsertSchedule(ctx, kind, 6*time.Hour); err != nil {
		t.Fatalf("UpsertSchedule(6h): %v", err)
	}
	if until := time.Until(scheduleFor().NextRun); until < 5*time.Hour {
		t.Fatalf("first registration should schedule one interval out, got %s", until)
	}

	// The operator shortens the interval and restarts.
	if err := s.UpsertSchedule(ctx, kind, time.Hour); err != nil {
		t.Fatalf("UpsertSchedule(1h): %v", err)
	}
	sc := scheduleFor()
	if sc.Interval != time.Hour {
		t.Fatalf("interval = %s, want 1h", sc.Interval)
	}
	if until := time.Until(sc.NextRun); until > 90*time.Minute {
		t.Fatalf("next run is %s out after shortening the interval to 1h — the new cadence does not take effect until the OLD 6h timer fires", until)
	}

	// LENGTHENING must not push an already-scheduled run further out (that would
	// let a restart-looping pod keep deferring its own next run indefinitely).
	if err := s.UpsertSchedule(ctx, kind, 24*time.Hour); err != nil {
		t.Fatalf("UpsertSchedule(24h): %v", err)
	}
	sc = scheduleFor()
	if sc.Interval != 24*time.Hour {
		t.Fatalf("interval = %s, want 24h", sc.Interval)
	}
	if until := time.Until(sc.NextRun); until > 90*time.Minute {
		t.Fatalf("lengthening the interval pushed next_run_at to %s out; the pending run should have been left alone", until)
	}
}

// TestShutdownInterruptsHandlerAndLeavesRowForTheReaper pins the ACTUAL graceful
// shutdown contract, which LocalProvider.Run's docstring used to describe
// backwards ("in-flight handlers finish rather than being abandoned mid-run").
// They do not: the per-job context is a child of Run's ctx, so a SIGTERM cancels
// the handler immediately. Run still waits for its workers, and execute
// deliberately writes NO terminal status on a cancelled ctx — the row stays
// 'running' with a stale heartbeat and the reaper on the next pod re-queues it.
//
// This is worth pinning both directions: someone "fixing" the doc by making Run
// wait out its handlers would break deploy latency, and someone dropping the
// no-terminal-write-on-cancel rule would let a shutdown stamp 'failed' over jobs
// that are simply going to be re-run.
func TestShutdownInterruptsHandlerAndLeavesRowForTheReaper(t *testing.T) {
	s, ctx := newTestStore(t)
	id, err := s.Enqueue(ctx, "blocker", "", nil, 3, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	reg := NewRegistry()
	started := make(chan struct{})
	reg.Register("blocker", HandlerFunc(func(hctx context.Context, _ Job) error {
		close(started)
		<-hctx.Done() // an interruption-safe handler: stops when told to
		return hctx.Err()
	}))

	runCtx, cancel := context.WithCancel(ctx)
	p := NewLocalProvider(s, quietLogger(), "shutdown-test")
	done := make(chan struct{})
	go func() {
		_ = p.Run(runCtx, reg)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("the job was never claimed and run")
	}

	cancel() // SIGTERM
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context was cancelled — shutdown blocks on in-flight handlers")
	}

	// No terminal write: the interrupted run must not be recorded as done/failed.
	status, _, _, _, _, _ := rowState(t, s, ctx, id)
	if status != string(StatusRunning) {
		t.Fatalf("row status = %q after an interrupted shutdown, want running (the reaper owns it) — a shutdown must not stamp a terminal status on work that will be re-run", status)
	}

	// The reaper on the next pod reclaims it and it becomes runnable again.
	lapseLease(t, s, ctx, id)
	if n, err := s.ReapStaleRunning(ctx, time.Minute); err != nil || n != 1 {
		t.Fatalf("ReapStaleRunning: n=%d err=%v, want 1", n, err)
	}
	j, ok, err := s.ClaimNext(ctx, "next-pod", nil, nil)
	if err != nil || !ok || j.ID != id {
		t.Fatalf("interrupted job was not re-claimable after the reaper swept: ok=%v err=%v", ok, err)
	}
}
