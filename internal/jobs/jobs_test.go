package jobs

import (
	"context"
	"testing"
	"time"
)

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Handler("x"); ok {
		t.Fatal("empty registry returned a handler")
	}

	called := 0
	r.Register("x", HandlerFunc(func(context.Context, Job) error { called++; return nil }))
	h, ok := r.Handler("x")
	if !ok {
		t.Fatal("registered handler not found")
	}
	_ = h.Handle(context.Background(), Job{})
	if called != 1 {
		t.Fatalf("handler invoked %d times, want 1", called)
	}

	// Last write wins; Kinds is de-duped + sorted.
	r.Register("x", HandlerFunc(func(context.Context, Job) error { return nil }))
	r.Register("a", HandlerFunc(func(context.Context, Job) error { return nil }))
	if got := r.Kinds(); len(got) != 2 || got[0] != "a" || got[1] != "x" {
		t.Fatalf("Kinds = %v, want [a x]", got)
	}
}

func TestEnqueueOptions(t *testing.T) {
	def := resolveEnqueue(nil)
	if def.maxAttempts != 1 {
		t.Errorf("default maxAttempts = %d, want 1", def.maxAttempts)
	}
	if !def.runAt.IsZero() {
		t.Error("default runAt should be zero (store treats it as now)")
	}

	got := resolveEnqueue([]EnqueueOption{MaxAttempts(5), Delay(2 * time.Hour)})
	if got.maxAttempts != 5 {
		t.Errorf("maxAttempts = %d, want 5", got.maxAttempts)
	}
	if got.runAt.IsZero() {
		t.Error("Delay should set a future runAt")
	}

	at := time.Now().Add(time.Hour).Truncate(time.Second)
	if got := resolveEnqueue([]EnqueueOption{At(at)}); !got.runAt.Equal(at) {
		t.Errorf("At set runAt = %v, want %v", got.runAt, at)
	}
}

func TestRetryDelay(t *testing.T) {
	if retryDelay(1) != 30*time.Second {
		t.Errorf("attempt 1 = %v, want 30s", retryDelay(1))
	}
	if retryDelay(3) != 90*time.Second {
		t.Errorf("attempt 3 = %v, want 90s", retryDelay(3))
	}
	if retryDelay(100) != 10*time.Minute {
		t.Errorf("attempt 100 = %v, want the 10m cap", retryDelay(100))
	}
}
