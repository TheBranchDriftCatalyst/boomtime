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

// TestRegistryConcurrency verifies the SetConcurrency/Concurrency policy map:
// 0/absent = unlimited, and the returned map is a defensive copy.
func TestRegistryConcurrency(t *testing.T) {
	r := NewRegistry()
	if got := r.Concurrency(); len(got) != 0 {
		t.Fatalf("fresh registry Concurrency = %v, want empty", got)
	}
	r.SetConcurrency("k", 2)
	r.SetConcurrency("u", 0) // explicit unlimited

	got := r.Concurrency()
	if got["k"] != 2 {
		t.Fatalf("Concurrency[k] = %d, want 2", got["k"])
	}
	// Mutating the returned copy must not affect the registry.
	got["k"] = 99
	if r.Concurrency()["k"] != 2 {
		t.Fatal("Concurrency() must return a copy, not the internal map")
	}
}

// TestKindLimiterThrottleFlow exercises the Excluded-then-Acquire combo the
// providers rely on: a kind at its cap is reported by Excluded (so ClaimNext
// skips it) AND refused by Acquire (the real guard); releasing frees a slot.
func TestKindLimiterThrottleFlow(t *testing.T) {
	ctx := context.Background()
	reg := NewRegistry()
	reg.SetConcurrency("k", 1)

	lim := NewKindLimiter(nil) // in-process fallback (mem limiter)

	// Seed one running holder to reach the cap.
	release, ok, err := lim.Acquire(ctx, "k", "holder-1", reg.Concurrency()["k"])
	if err != nil || !ok {
		t.Fatalf("first Acquire: ok=%v err=%v", ok, err)
	}

	// At the cap, Excluded reports the kind so ClaimNext leaves its backlog queued.
	excl, err := lim.Excluded(ctx, reg.Concurrency())
	if err != nil {
		t.Fatalf("Excluded: %v", err)
	}
	found := false
	for _, kexcl := range excl {
		if kexcl == "k" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Excluded = %v, want it to contain \"k\" at cap", excl)
	}

	// And a second Acquire is refused (the atomic guard) — no run over the cap.
	if rel2, ok2, err2 := lim.Acquire(ctx, "k", "holder-2", reg.Concurrency()["k"]); err2 != nil || ok2 || rel2 != nil {
		t.Fatalf("Acquire at cap: want ok=false rel=nil, got ok=%v rel!=nil=%v err=%v", ok2, rel2 != nil, err2)
	}

	// Release the slot; the kind is acquirable again and no longer excluded.
	release()
	if excl2, _ := lim.Excluded(ctx, reg.Concurrency()); len(excl2) != 0 {
		t.Fatalf("after release Excluded = %v, want empty", excl2)
	}
	if _, ok3, err3 := lim.Acquire(ctx, "k", "holder-3", reg.Concurrency()["k"]); err3 != nil || !ok3 {
		t.Fatalf("Acquire after release: ok=%v err=%v", ok3, err3)
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
