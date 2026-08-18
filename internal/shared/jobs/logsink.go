// logsink.go — durable persistence of a background job's log stream (gaka-hney).
//
// A FINISHED job's lines vanish once the in-memory LogHub ring rolls over, so
// the Admin Jobs viewer shows "No logs yet" for anything that completed a while
// ago. This wires a best-effort capture: for the duration of a job we subscribe
// to the process LogHub, collect the entries tagged with this job's id
// (Attrs["job_id"] — exec.go stamps every lifecycle + handler line), and on
// completion marshal them to JSONL and hand them to a JobLogSink (objstore in
// prod). The GET .../logs endpoint reads them back.
//
// Everything here is OPTIONAL and NON-FATAL: NewLogCapture returns nil when the
// hub or sink is absent, a nil *LogCapture is a no-op, and the flush is async +
// log-and-continue so log persistence can never fail or slow a job.
package jobs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/logging"
)

// JobLogPrefix is the object-key namespace every persisted job-log blob lives
// under. The admin bulk log-clear lists this prefix to enumerate the stored logs.
const JobLogPrefix = "job-logs/"

// JobLogKey is the object key a job's persisted logs live under. Shared by the
// writer (objLogSink) and the reader (admin GET/DELETE) so the format lives in
// exactly one place.
func JobLogKey(jobID int64) string { return fmt.Sprintf("%s%d.jsonl", JobLogPrefix, jobID) }

// jobLogContentType is the newline-delimited-JSON media type for the stored blob.
const jobLogContentType = "application/x-ndjson"

// jobLogFlushTimeout bounds the best-effort async flush so a wedged S3 can't leak
// a goroutine forever.
const jobLogFlushTimeout = 30 * time.Second

// JobLogSink persists one job's captured log entries. Implemented by objLogSink
// (objstore-backed); nil = persistence off.
type JobLogSink interface {
	PutJobLogs(ctx context.Context, jobID int64, entries []logging.LogEntry) error
}

// BlobStore is the minimal object-storage surface the sink needs. *objstore.Client
// satisfies it structurally; kept local so the jobs package doesn't import
// objstore and tests can stub a one-method fake.
type BlobStore interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
}

// MarshalJobLogs encodes entries as JSONL (one JSON object per line) — a stream
// format so the reader can decode incrementally and a truncated tail still
// yields every complete leading line.
func MarshalJobLogs(entries []logging.LogEntry) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return nil, fmt.Errorf("jobs: marshal log entry: %w", err)
		}
	}
	return buf.Bytes(), nil
}

// ReadJobLogs decodes a JSONL job-log blob back into entries. It tolerates a
// truncated final line (returns every complete line before it) so a partial
// upload still renders. Always returns a non-nil slice on success.
func ReadJobLogs(r io.Reader) ([]logging.LogEntry, error) {
	out := make([]logging.LogEntry, 0, 64)
	sc := bufio.NewScanner(r)
	// Log lines can be long (stack-ish messages); lift the default 64KiB cap.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e logging.LogEntry
		if err := json.Unmarshal(line, &e); err != nil {
			// Skip a corrupt/truncated final line rather than failing the whole read.
			continue
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("jobs: read log blob: %w", err)
	}
	return out, nil
}

// objLogSink is the objstore-backed JobLogSink. It's defined here (not in
// objstore) so the JSONL format lives beside the reader/writer helpers.
type objLogSink struct {
	store BlobStore
	log   *slog.Logger
}

// NewObjLogSink builds the objstore-backed sink. Returns nil when store is nil so
// NewLogCapture cleanly disables persistence.
func NewObjLogSink(store BlobStore, log *slog.Logger) JobLogSink {
	if store == nil {
		return nil
	}
	return &objLogSink{store: store, log: log}
}

// PutJobLogs marshals entries to JSONL and writes them to the job's key.
func (s *objLogSink) PutJobLogs(ctx context.Context, jobID int64, entries []logging.LogEntry) error {
	blob, err := MarshalJobLogs(entries)
	if err != nil {
		return err
	}
	return s.store.Put(ctx, JobLogKey(jobID), bytes.NewReader(blob), int64(len(blob)), jobLogContentType)
}

// LogCapture wires per-job log persistence. It holds the process LogHub (to
// subscribe for a job's duration) and the sink (to flush on completion). A nil
// *LogCapture is a no-op so every call site stays branch-free.
type LogCapture struct {
	hub  *logging.LogHub
	sink JobLogSink
}

// NewLogCapture returns a capture, or nil when either dependency is absent (so
// persistence is fully optional — a nil capture short-circuits every method).
func NewLogCapture(hub *logging.LogHub, sink JobLogSink) *LogCapture {
	if hub == nil || sink == nil {
		return nil
	}
	return &LogCapture{hub: hub, sink: sink}
}

// begin starts collecting this job's log entries and returns a finish func that
// stops collecting and flushes them to the sink. It must be called BEFORE the
// job's first line so early lines of a long job are captured (we subscribe to
// the live LogHub — no reliance on Backfill of a possibly-rolled ring). The
// returned func is safe to defer; a nil *LogCapture yields a no-op finish.
func (lc *LogCapture) begin(jobID int64) func() {
	if lc == nil {
		return func() {}
	}
	ch := lc.hub.Subscribe()
	idStr := strconv.FormatInt(jobID, 10)

	var mu sync.Mutex
	collected := make([]logging.LogEntry, 0, 32)
	done := make(chan struct{})

	go func() {
		defer close(done)
		// Ranges until Unsubscribe closes ch; buffered entries already queued are
		// still drained before the loop sees the close, so the terminal
		// done/failed line published just before finish() is captured.
		for e := range ch {
			if e.Attrs[jobLogJobIDKey] == idStr {
				mu.Lock()
				collected = append(collected, e)
				mu.Unlock()
			}
		}
	}()

	return func() {
		lc.hub.Unsubscribe(ch) // closes ch → collector goroutine drains then exits
		<-done
		mu.Lock()
		entries := collected
		mu.Unlock()
		if len(entries) == 0 {
			return // nothing to persist (e.g. no handler wired) — skip the write
		}
		// Best-effort, async: a slow/wedged S3 must never block or slow a job's
		// completion path. Detached from the (possibly cancelled) job ctx with a
		// bounded timeout of its own.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), jobLogFlushTimeout)
			defer cancel()
			if err := lc.sink.PutJobLogs(ctx, jobID, entries); err != nil && lc.log() != nil {
				lc.log().Warn("jobs: persist logs failed", "job_id", jobID, "err", err)
			}
		}()
	}
}

// jobLogJobIDKey is the LogEntry.Attrs key exec.go stamps with the job id.
const jobLogJobIDKey = "job_id"

// log returns the sink's logger for the best-effort warn, or nil.
func (lc *LogCapture) log() *slog.Logger {
	if s, ok := lc.sink.(*objLogSink); ok {
		return s.log
	}
	return nil
}
