// Package logctx carries a *slog.Logger on a context.Context so a job-scoped
// logger (built in internal/jobs/exec.go with job_id/kind/owner attrs) can flow
// through a handler call chain WITHOUT every function growing a *slog.Logger
// parameter. Domain log helpers pull the logger back out via FromContext, so
// every line they emit inherits the job's structured attrs and the Admin log
// viewer can filter the stream down to a single job's run (gaka-f0is).
//
// This is a LEAF package on purpose: it imports only the standard library
// (context + log/slog), so any boomtime package can import it with zero
// import-cycle risk.
package logctx

import (
	"context"
	"log/slog"
)

// ctxKey is the unexported context key type so no other package can collide with
// (or overwrite) the stored logger.
type ctxKey struct{}

// NewContext returns a copy of ctx carrying l. A nil logger is stored as-is;
// FromContext treats an absent OR nil stored logger as "use the fallback".
func NewContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the *slog.Logger stored on ctx by NewContext, or fallback
// when ctx is nil, carries no logger, or carries a nil one. The result is only
// nil if fallback itself is nil — callers already nil-check their fallback
// logger (the domain helpers guard s.Logger), so this stays nil-safe.
func FromContext(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if ctx == nil {
		return fallback
	}
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return fallback
}
