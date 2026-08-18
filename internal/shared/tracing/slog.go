package tracing

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// SlogHandler wraps any slog.Handler and stamps the active span's IDs onto
// every record that carries one. This is what lets a log UI (HyperDX, Grafana,
// …) pivot from a log line to its trace.
//
// IMPORTANT: only the *Context slog variants (InfoContext, ErrorContext, …)
// hand the context to the handler. A plain logger.Info() passes
// context.Background(), which carries no span, so those records will have no
// trace IDs. Call sites that should correlate must use the *Context form.
type SlogHandler struct{ slog.Handler }

// NewSlogHandler wraps h so records emitted inside a span carry trace_id and
// span_id. Zero cost when there is no active span.
func NewSlogHandler(h slog.Handler) slog.Handler { return &SlogHandler{Handler: h} }

func (h *SlogHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs/WithGroup must re-wrap: slog would otherwise unwrap back to the
// inner handler and derived loggers would silently stop emitting trace IDs.
func (h *SlogHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &SlogHandler{Handler: h.Handler.WithAttrs(as)}
}

func (h *SlogHandler) WithGroup(name string) slog.Handler {
	return &SlogHandler{Handler: h.Handler.WithGroup(name)}
}
