package imagejobs

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// newTestRegistry returns a Registry with tiny retention windows so tests
// don't have to wait minutes. Uses a discard logger to keep test output
// quiet.
func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRegistryWith(logger, 50*time.Millisecond, 50*time.Millisecond)
}

func TestRegistry_EnqueueNewLabelReturnsFreshJob(t *testing.T) {
	r := newTestRegistry(t)
	job, existing := r.Enqueue(EnqueueInput{LabelID: "late-night-coder", Prompt: "prompt", Model: "", Size: ""})
	if existing {
		t.Fatalf("Enqueue on empty registry: existing=true, want false")
	}
	if job.ID == "" {
		t.Fatalf("Enqueue: job.ID empty")
	}
	if job.Status != StatusQueued {
		t.Fatalf("Enqueue: Status=%q want %q", job.Status, StatusQueued)
	}
	if job.LabelID != "late-night-coder" {
		t.Fatalf("Enqueue: LabelID=%q want late-night-coder", job.LabelID)
	}
}

func TestRegistry_EnqueueDedupesQueuedLabel(t *testing.T) {
	r := newTestRegistry(t)
	a, existingA := r.Enqueue(EnqueueInput{LabelID: "polyglot", Prompt: "p", Model: "", Size: ""})
	if existingA {
		t.Fatalf("first Enqueue: existing=true")
	}
	b, existingB := r.Enqueue(EnqueueInput{LabelID: "polyglot", Prompt: "p2", Model: "different-model", Size: "512x512"})
	if !existingB {
		t.Fatalf("second Enqueue: existing=false, want true (dedupe)")
	}
	if a.ID != b.ID {
		t.Fatalf("dedupe: ids differ a=%q b=%q", a.ID, b.ID)
	}
	// The returned "existing" job should reflect the ORIGINAL parameters
	// (not the second call's overrides) — subsequent Enqueues do not
	// mutate an in-flight job.
	if b.Model != "" {
		t.Fatalf("dedupe: Model=%q want empty (original wins)", b.Model)
	}
}

func TestRegistry_EnqueueDedupesRunningLabel(t *testing.T) {
	r := newTestRegistry(t)
	a, _ := r.Enqueue(EnqueueInput{LabelID: "machine", Prompt: "p", Model: "", Size: ""})
	r.MarkRunning(a.ID)
	b, existing := r.Enqueue(EnqueueInput{LabelID: "machine", Prompt: "p", Model: "", Size: ""})
	if !existing {
		t.Fatalf("dedupe on running: existing=false")
	}
	if a.ID != b.ID {
		t.Fatalf("dedupe on running: ids differ")
	}
	if b.Status != StatusRunning {
		t.Fatalf("dedupe on running: status=%q want %q", b.Status, StatusRunning)
	}
}

func TestRegistry_EnqueueSupersedesTerminalLabel(t *testing.T) {
	r := newTestRegistry(t)
	a, _ := r.Enqueue(EnqueueInput{LabelID: "weekend-warrior", Prompt: "p", Model: "", Size: ""})
	r.MarkRunning(a.ID)
	r.MarkDone(a.ID)
	// Retention timer is 50ms; call Enqueue immediately (well before it
	// fires) and confirm we get a NEW job — operator intent (fresh regen)
	// wins over the aging done row.
	b, existing := r.Enqueue(EnqueueInput{LabelID: "weekend-warrior", Prompt: "p2", Model: "", Size: ""})
	if existing {
		t.Fatalf("supersede terminal: existing=true, want false")
	}
	if a.ID == b.ID {
		t.Fatalf("supersede: ids match (%s), want new job", a.ID)
	}
}

func TestRegistry_SubscribeReceivesLifecycleEvents(t *testing.T) {
	r := newTestRegistry(t)
	sub, unsub := r.Subscribe()
	defer unsub()

	job, _ := r.Enqueue(EnqueueInput{LabelID: "consistent", Prompt: "p", Model: "", Size: ""})
	ev := mustReceive(t, sub, time.Second)
	if ev.Kind != EventAdded || ev.Job.ID != job.ID {
		t.Fatalf("added event: got kind=%q id=%q", ev.Kind, ev.Job.ID)
	}

	r.MarkRunning(job.ID)
	ev = mustReceive(t, sub, time.Second)
	if ev.Kind != EventUpdated || ev.Job.Status != StatusRunning {
		t.Fatalf("running event: got kind=%q status=%q", ev.Kind, ev.Job.Status)
	}

	r.MarkDone(job.ID)
	ev = mustReceive(t, sub, time.Second)
	if ev.Kind != EventUpdated || ev.Job.Status != StatusDone {
		t.Fatalf("done event: got kind=%q status=%q", ev.Kind, ev.Job.Status)
	}

	// Retention is 50ms; wait for the removal event.
	ev = mustReceive(t, sub, 500*time.Millisecond)
	if ev.Kind != EventRemoved || ev.Job.ID != job.ID {
		t.Fatalf("removed event: got kind=%q id=%q want removed for %q", ev.Kind, ev.Job.ID, job.ID)
	}
}

func TestRegistry_MarkErrorRetainsErrorAndSchedulesRemoval(t *testing.T) {
	r := newTestRegistry(t)
	sub, unsub := r.Subscribe()
	defer unsub()

	job, _ := r.Enqueue(EnqueueInput{LabelID: "sprinter", Prompt: "p", Model: "", Size: ""})
	drain(sub) // added
	r.MarkError(job.ID, "comfyui exploded")
	ev := mustReceive(t, sub, time.Second)
	if ev.Kind != EventUpdated || ev.Job.Status != StatusError {
		t.Fatalf("error event: got kind=%q status=%q", ev.Kind, ev.Job.Status)
	}
	if ev.Job.Error != "comfyui exploded" {
		t.Fatalf("error msg: got %q want %q", ev.Job.Error, "comfyui exploded")
	}
	// Removal fires after retentionError (50ms in the test registry).
	ev = mustReceive(t, sub, 500*time.Millisecond)
	if ev.Kind != EventRemoved {
		t.Fatalf("after retention: got kind=%q want removed", ev.Kind)
	}
}

func TestRegistry_SnapshotReturnsCurrentJobsOrdered(t *testing.T) {
	r := newTestRegistry(t)
	// EnqueuedAt is time.Now(); a tiny sleep between calls forces
	// distinguishable timestamps without leaning on sub-microsecond
	// resolution.
	r.Enqueue(EnqueueInput{LabelID: "a", Prompt: "", Model: "", Size: ""})
	time.Sleep(2 * time.Millisecond)
	r.Enqueue(EnqueueInput{LabelID: "b", Prompt: "", Model: "", Size: ""})
	time.Sleep(2 * time.Millisecond)
	r.Enqueue(EnqueueInput{LabelID: "c", Prompt: "", Model: "", Size: ""})

	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("snapshot len=%d want 3", len(snap))
	}
	if snap[0].LabelID != "a" || snap[1].LabelID != "b" || snap[2].LabelID != "c" {
		t.Fatalf("snapshot order: got [%s %s %s] want [a b c]",
			snap[0].LabelID, snap[1].LabelID, snap[2].LabelID)
	}
}

func TestRegistry_SlowSubscriberDoesNotBlockBroadcast(t *testing.T) {
	r := newTestRegistry(t)
	// Subscribe but never read — buffer is 16. Fire many events; each
	// broadcastLocked should complete in bounded time (dropping the
	// oldest on overflow rather than blocking on the wedged subscriber).
	_, unsub := r.Subscribe()
	defer unsub()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			// Each Enqueue triggers an EventAdded broadcast; on a wedged
			// subscriber this would block after 16, deadlocking the test.
			r.Enqueue(EnqueueInput{LabelID: "dedupe-me", Prompt: "", Model: "", Size: ""})
			// Enqueue after the first will dedupe (no new event); force
			// a state transition to keep events flowing.
			id := r.byLabelSnapshot("dedupe-me")
			if id != "" {
				r.MarkRunning(id)
				r.MarkDone(id)
			}
		}
		close(done)
	}()
	select {
	case <-done:
		// Emitter finished; slow subscriber didn't block.
	case <-time.After(3 * time.Second):
		t.Fatal("broadcast blocked on wedged subscriber")
	}
}

// byLabelSnapshot is a test helper that reads the current byLabel mapping
// under lock. Not part of the public API — Enqueue supersede behavior
// makes it hard to observe the running jobID from outside otherwise.
func (r *Registry) byLabelSnapshot(labelID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byLabel[labelID]
}

// --- helpers ----------------------------------------------------------------

func mustReceive(t *testing.T, ch <-chan Event, timeout time.Duration) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatalf("channel closed while waiting for event")
		}
		return ev
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for event after %s", timeout)
		return Event{}
	}
}

func drain(ch <-chan Event) {
	select {
	case <-ch:
	default:
	}
}

// TestRegistry_ClaimUnblocksOnContextCancel confirms the pool worker's
// claim() call returns when its context is done — protects the graceful
// shutdown path.
func TestRegistry_ClaimUnblocksOnContextCancel(t *testing.T) {
	r := newTestRegistry(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, ok := r.claim(ctx)
		if ok {
			t.Errorf("claim returned ok=true after cancel")
		}
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("claim did not unblock on cancel")
	}
}
