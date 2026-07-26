package imagejobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newQuietPool(t *testing.T, r *Registry, exec Executor, concurrency int) *Pool {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewPool(PoolConfig{
		Concurrency: concurrency,
		Registry:    r,
		Executor:    exec,
		Logger:      logger,
	})
}

// TestPool_ExecutesEveryQueuedJob confirms the pool drains everything the
// registry enqueues in front of it.
func TestPool_ExecutesEveryQueuedJob(t *testing.T) {
	r := newTestRegistry(t)
	var seen int32
	exec := ExecutorFunc(func(_ context.Context, _ Job) error {
		atomic.AddInt32(&seen, 1)
		return nil
	})
	pool := newQuietPool(t, r, exec, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop(time.Second)

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		r.Enqueue(EnqueueInput{LabelID: id, Prompt: "p"})
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&seen) == 5 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pool executed %d jobs, want 5", atomic.LoadInt32(&seen))
}

// TestPool_HonorsConcurrencyLimit uses an Executor with a semaphore to
// observe peak parallelism. At concurrency=2, no more than 2 Executor
// calls should be in flight at once, even with 10 queued.
func TestPool_HonorsConcurrencyLimit(t *testing.T) {
	r := newTestRegistry(t)
	var (
		mu     sync.Mutex
		active int
		peak   int
	)
	exec := ExecutorFunc(func(ctx context.Context, _ Job) error {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return nil
	})
	pool := newQuietPool(t, r, exec, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop(time.Second)

	for i := 0; i < 10; i++ {
		// Distinct label IDs so dedupe doesn't collapse them.
		r.Enqueue(EnqueueInput{LabelID: labelN(i), Prompt: "p"})
	}

	// Wait long enough for all 10 * 50ms / 2 workers = ~250ms of work
	// PLUS some scheduling slack.
	time.Sleep(1 * time.Second)

	mu.Lock()
	got := peak
	mu.Unlock()
	if got != 2 {
		t.Fatalf("peak concurrency=%d want 2", got)
	}
}

// TestPool_ExecutorErrorDoesNotStopWorker confirms one job failing does not
// wedge the worker — subsequent jobs still get processed.
func TestPool_ExecutorErrorDoesNotStopWorker(t *testing.T) {
	r := newTestRegistry(t)
	var seen int32
	exec := ExecutorFunc(func(_ context.Context, job Job) error {
		atomic.AddInt32(&seen, 1)
		if job.LabelID == "boom" {
			return errors.New("blew up")
		}
		return nil
	})
	pool := newQuietPool(t, r, exec, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop(time.Second)

	r.Enqueue(EnqueueInput{LabelID: "boom", Prompt: "p", Model: "", Size: ""})
	r.Enqueue(EnqueueInput{LabelID: "ok-1", Prompt: "p", Model: "", Size: ""})
	r.Enqueue(EnqueueInput{LabelID: "ok-2", Prompt: "p", Model: "", Size: ""})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&seen) == 3 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("executor invocations=%d want 3", atomic.LoadInt32(&seen))
}

// TestPool_StopUnblocksClaim confirms Stop signals workers via ctx cancel.
func TestPool_StopUnblocksClaim(t *testing.T) {
	r := newTestRegistry(t)
	exec := ExecutorFunc(func(_ context.Context, _ Job) error { return nil })
	pool := newQuietPool(t, r, exec, 2)
	ctx := context.Background()
	pool.Start(ctx)
	// With no jobs queued, workers are blocked in claim(). Stop should
	// cancel their context and let them exit.
	ok := pool.Stop(time.Second)
	if !ok {
		t.Fatal("pool.Stop timed out; workers did not observe cancellation")
	}
}

// TestPool_ObservesStateTransitionsInRegistry confirms the pool marks jobs
// Running before invoking the Executor and Done afterwards, so subscribers
// see the full lifecycle.
func TestPool_ObservesStateTransitionsInRegistry(t *testing.T) {
	r := newTestRegistry(t)
	var mid Job
	exec := ExecutorFunc(func(_ context.Context, j Job) error {
		mid = j
		return nil
	})
	pool := newQuietPool(t, r, exec, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop(time.Second)

	sub, unsub := r.Subscribe()
	defer unsub()
	job, _ := r.Enqueue(EnqueueInput{LabelID: "late-night-coder", Prompt: "p", Model: "", Size: ""})

	seenStatuses := map[JobStatus]bool{}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !seenStatuses[StatusDone] {
		select {
		case ev := <-sub:
			if ev.Job.ID == job.ID {
				seenStatuses[ev.Job.Status] = true
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !seenStatuses[StatusRunning] {
		t.Errorf("never observed StatusRunning event")
	}
	if !seenStatuses[StatusDone] {
		t.Errorf("never observed StatusDone event")
	}
	if mid.LabelID != "late-night-coder" {
		t.Errorf("Executor received wrong job: LabelID=%q", mid.LabelID)
	}
}

func labelN(i int) string {
	return "label-" + string(rune('a'+i))
}
