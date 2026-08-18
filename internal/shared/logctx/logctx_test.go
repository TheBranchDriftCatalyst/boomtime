package logctx

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// FromContext must return the exact logger that NewContext stored.
func TestFromContext_ReturnsInjected(t *testing.T) {
	injected := newLogger()
	fallback := newLogger()

	ctx := NewContext(context.Background(), injected)
	got := FromContext(ctx, fallback)

	if got != injected {
		t.Fatalf("FromContext returned %p, want the injected logger %p", got, injected)
	}
	if got == fallback {
		t.Fatal("FromContext returned the fallback, want the injected logger")
	}
}

// With no logger stored, FromContext must return the fallback.
func TestFromContext_FallbackWhenAbsent(t *testing.T) {
	fallback := newLogger()
	if got := FromContext(context.Background(), fallback); got != fallback {
		t.Fatalf("FromContext(no value) = %p, want fallback %p", got, fallback)
	}
}

// A nil ctx must not panic and must yield the fallback.
func TestFromContext_NilContext(t *testing.T) {
	fallback := newLogger()
	//nolint:staticcheck // deliberately passing a nil context to prove nil-safety.
	if got := FromContext(nil, fallback); got != fallback {
		t.Fatalf("FromContext(nil ctx) = %p, want fallback %p", got, fallback)
	}
}

// A nil logger stored on ctx must be treated as absent → fallback (so a
// mistakenly-injected nil never nil-panics a caller that has a real fallback).
func TestFromContext_NilStoredValueFallsBack(t *testing.T) {
	fallback := newLogger()
	ctx := NewContext(context.Background(), nil)
	if got := FromContext(ctx, fallback); got != fallback {
		t.Fatalf("FromContext(nil stored) = %p, want fallback %p", got, fallback)
	}
}

// When both the stored logger and the fallback are nil, the result is nil (the
// caller's own nil-check guards the actual log call).
func TestFromContext_BothNil(t *testing.T) {
	if got := FromContext(context.Background(), nil); got != nil {
		t.Fatalf("FromContext(nothing, nil fallback) = %p, want nil", got)
	}
}
