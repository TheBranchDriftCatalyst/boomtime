package jobs

import (
	"context"
	"sort"
	"testing"
)

func TestMemLimiterAcquireUpToMax(t *testing.T) {
	l := newMemLimiter()
	ctx := context.Background()
	const kind = "github-stats-refresh"

	// Acquire up to max returns ok each time.
	rel1, ok, err := l.Acquire(ctx, kind, "h1", 2)
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	_, ok, err = l.Acquire(ctx, kind, "h2", 2)
	if err != nil || !ok {
		t.Fatalf("second acquire: ok=%v err=%v", ok, err)
	}

	// At limit, the next acquire is refused.
	if rel, ok, err := l.Acquire(ctx, kind, "h3", 2); err != nil || ok || rel != nil {
		t.Fatalf("acquire at limit: expected ok=false rel=nil, got ok=%v rel!=nil=%v err=%v", ok, rel != nil, err)
	}

	// Releasing a slot lets the next one through.
	rel1()
	if _, ok, err := l.Acquire(ctx, kind, "h3", 2); err != nil || !ok {
		t.Fatalf("acquire after release: ok=%v err=%v", ok, err)
	}
}

func TestMemLimiterExcluded(t *testing.T) {
	l := newMemLimiter()
	ctx := context.Background()

	// Fill "hardcover-push" to its limit of 1; leave "avatar-render" (limit 3)
	// with a single holder (under limit).
	if _, ok, _ := l.Acquire(ctx, "hardcover-push", "h1", 1); !ok {
		t.Fatal("expected hardcover-push acquire to succeed")
	}
	if _, ok, _ := l.Acquire(ctx, "avatar-render", "a1", 3); !ok {
		t.Fatal("expected avatar-render acquire to succeed")
	}

	limits := map[string]int{
		"hardcover-push": 1,
		"avatar-render":  3,
		"label-image":    2, // never acquired -> not excluded
	}
	got, err := l.Excluded(ctx, limits)
	if err != nil {
		t.Fatalf("Excluded: %v", err)
	}
	sort.Strings(got)
	if len(got) != 1 || got[0] != "hardcover-push" {
		t.Fatalf("Excluded = %v, want [hardcover-push]", got)
	}
}

func TestMemLimiterUnlimited(t *testing.T) {
	l := newMemLimiter()
	ctx := context.Background()

	// max<=0 is unlimited: Acquire always ok, and it's never Excluded.
	for i := 0; i < 100; i++ {
		if rel, ok, err := l.Acquire(ctx, "books-audible-backfill", "h", 0); err != nil || !ok || rel == nil {
			t.Fatalf("unlimited acquire #%d: ok=%v rel!=nil=%v err=%v", i, ok, rel != nil, err)
		}
	}
	got, err := l.Excluded(ctx, map[string]int{"books-audible-backfill": 0})
	if err != nil {
		t.Fatalf("Excluded: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Excluded = %v, want empty for unlimited kind", got)
	}
}

func TestMemLimiterReleaseIdempotent(t *testing.T) {
	l := newMemLimiter()
	ctx := context.Background()
	const kind = "audiobooks-audible-sync"

	rel, ok, _ := l.Acquire(ctx, kind, "h1", 1)
	if !ok {
		t.Fatal("expected acquire to succeed")
	}
	// Double-release must not drive the count negative or free a second slot.
	rel()
	rel()

	// Only one slot exists; acquire it, then the next must be refused.
	if _, ok, _ := l.Acquire(ctx, kind, "h2", 1); !ok {
		t.Fatal("expected acquire after release to succeed")
	}
	if _, ok, _ := l.Acquire(ctx, kind, "h3", 1); ok {
		t.Fatal("expected acquire at limit to fail after idempotent release")
	}
}

func TestMemLimiterRefreshNoOp(t *testing.T) {
	// The in-process limiter has no TTL prune, so Refresh is a no-op that must not
	// alter the count or error — a held slot stays held, an unknown holder is fine.
	l := newMemLimiter()
	ctx := context.Background()
	const kind = "hardcover-match"

	_, ok, _ := l.Acquire(ctx, kind, "h1", 1)
	if !ok {
		t.Fatal("expected acquire to succeed")
	}
	if err := l.Refresh(ctx, kind, "h1"); err != nil {
		t.Fatalf("Refresh returned %v, want nil", err)
	}
	if err := l.Refresh(ctx, kind, "nonexistent"); err != nil {
		t.Fatalf("Refresh(unknown) returned %v, want nil", err)
	}
	// The slot is still held after refresh — a second acquire at cap 1 is refused.
	if _, ok, _ := l.Acquire(ctx, kind, "h2", 1); ok {
		t.Fatal("expected acquire at limit to fail (slot still held after refresh)")
	}
}

func TestStartSlotRefreshNilLimiter(t *testing.T) {
	// With no limiter, startSlotRefresh must return a stop func that is safe to
	// call and returns promptly (no goroutine to wait on).
	p := &LocalProvider{}
	stop := p.startSlotRefresh(context.Background(), "k", "h")
	stop() // must not block or panic
}

func TestNewKindLimiterNilClient(t *testing.T) {
	// A nil *redis.Client must yield the in-process fallback, never nil.
	l := NewKindLimiter(nil)
	if l == nil {
		t.Fatal("NewKindLimiter(nil) returned nil")
	}
	if _, ok := l.(*memLimiter); !ok {
		t.Fatalf("NewKindLimiter(nil) = %T, want *memLimiter", l)
	}
}
