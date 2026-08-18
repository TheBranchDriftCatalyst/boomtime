package audible

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/logctx"
)

// capturedRecord is one flattened log line: its message plus every attr the
// record carried (including logger-level With(...) attrs). This mirrors how the
// real teeHandler flattens attrs into LogEntry.Attrs, so asserting on it proves
// the same attrs the Admin viewer would see.
type capturedRecord struct {
	Msg   string
	Attrs map[string]string
}

// capturingHandler records every slog.Record with its accumulated WithAttrs, so
// a job-scoped logger built via slog.With("job_id", ...) surfaces job_id here.
type capturingHandler struct {
	mu      *sync.Mutex
	records *[]capturedRecord
	attrs   []slog.Attr
}

func newCapturingLogger() (*slog.Logger, *[]capturedRecord, *sync.Mutex) {
	recs := &[]capturedRecord{}
	mu := &sync.Mutex{}
	return slog.New(&capturingHandler{mu: mu, records: recs}), recs, mu
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	m := make(map[string]string)
	for _, a := range h.attrs {
		m[a.Key] = a.Value.String()
	}
	r.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	*h.records = append(*h.records, capturedRecord{Msg: r.Message, Attrs: m})
	h.mu.Unlock()
	return nil
}

func (h *capturingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	merged := append(append([]slog.Attr{}, h.attrs...), as...)
	return &capturingHandler{mu: h.mu, records: h.records, attrs: merged}
}

func (h *capturingHandler) WithGroup(string) slog.Handler { return h }

// Proves the wiring end-to-end: a Service whose OWN Logger is a throwaway still
// emits through the job-scoped logger carried on ctx, so the captured record
// carries the injected job_id (exactly what internal/jobs/exec.go injects before
// h.Handle). If logInfo ignored ctx, the record would lack job_id.
func TestLogInfo_UsesCtxLoggerAndCarriesJobID(t *testing.T) {
	jobLogger, recs, _ := newCapturingLogger()
	// Mimic exec.go: jl := log.With("job_id", ..., "kind", ..., "owner", ...).
	jl := jobLogger.With("job_id", "job-abc123", "kind", "audiobooks-audible-sync", "owner", "alice")

	// The Service's own logger writes to /dev/null — if the helper used THIS one,
	// nothing would be captured and job_id would be absent.
	s := &Service{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	ctx := logctx.NewContext(context.Background(), jl)
	s.logInfo(ctx, "audible: library sweep complete", "user", "alice", "pages", 2)

	if len(*recs) != 1 {
		t.Fatalf("expected 1 captured record via the ctx logger, got %d", len(*recs))
	}
	rec := (*recs)[0]
	if rec.Msg != "audible: library sweep complete" {
		t.Fatalf("captured msg = %q, want the emitted message", rec.Msg)
	}
	if got := rec.Attrs["job_id"]; got != "job-abc123" {
		t.Fatalf("captured job_id = %q, want %q — ctx logger was not used", got, "job-abc123")
	}
	if got := rec.Attrs["kind"]; got != "audiobooks-audible-sync" {
		t.Fatalf("captured kind = %q, want the injected job kind", got)
	}
	// The helper's own arg must still be present alongside the job attrs.
	if got := rec.Attrs["pages"]; got != "2" {
		t.Fatalf("captured pages = %q, want %q", got, "2")
	}
}

// Off a job (no logger on ctx) the helper falls back to s.Logger — proven by
// capturing through the Service logger instead.
func TestLogInfo_FallsBackToServiceLoggerOffJob(t *testing.T) {
	svcLogger, recs, _ := newCapturingLogger()
	s := &Service{Logger: svcLogger}

	s.logInfo(context.Background(), "audible backfill complete", "user", "bob")

	if len(*recs) != 1 {
		t.Fatalf("expected 1 record via the fallback service logger, got %d", len(*recs))
	}
	if _, ok := (*recs)[0].Attrs["job_id"]; ok {
		t.Fatal("off-job record unexpectedly carried a job_id")
	}
}
