package jobs

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestLocalProviderCancelRunningJob is the core in-process cancel guarantee: a
// handler blocked on ctx.Done() returns PROMPTLY once Cancel(id) fires, proving
// the per-job context is actually wired through execTracked → execute → handler.
// It needs no DB — a cancelled run never reaches a store write (execute bails on
// ctx.Err() before Complete/Fail), so the nil store is never dereferenced.
func TestLocalProviderCancelRunningJob(t *testing.T) {
	reg := NewRegistry()
	started := make(chan struct{})
	reg.Register("blocker", HandlerFunc(func(ctx context.Context, _ Job) error {
		close(started)
		<-ctx.Done() // block until the per-job context is cancelled
		return ctx.Err()
	}))

	p := NewLocalProvider(nil, discardLogger(), "w-test")

	done := make(chan struct{})
	go func() {
		p.execTracked(context.Background(), reg, Job{ID: 42, Kind: "blocker", MaxAttempts: 1})
		close(done)
	}()

	<-started // the handler is running → its CancelFunc is registered

	if !p.Cancel(42) {
		t.Fatal("Cancel(42) = false, want true for a job running on this provider")
	}
	select {
	case <-done:
		// handler observed ctx cancellation and returned promptly — success.
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after Cancel — its context was not cancelled")
	}

	// Once the run finishes the entry is unregistered, so a later Cancel is a
	// no-op that reports the job is no longer running here.
	if p.Cancel(42) {
		t.Fatal("Cancel(42) after completion = true, want false (unregistered)")
	}
	// A job that never ran here was never registered.
	if p.Cancel(999) {
		t.Fatal("Cancel(999) = true for an unknown job, want false")
	}
}

// TestAMQPProviderCancelAlwaysFalse documents the out-of-scope AMQP path: it
// satisfies Canceller but can't interrupt an in-flight delivery, so it reports
// false (the durable MarkCancelled still stops a queued job on that transport).
func TestAMQPProviderCancelAlwaysFalse(t *testing.T) {
	var p Canceller = &AMQPProvider{}
	if p.Cancel(1) {
		t.Fatal("AMQPProvider.Cancel = true, want false (in-flight cancel unsupported)")
	}
}
