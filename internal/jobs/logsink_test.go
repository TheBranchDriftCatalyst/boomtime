package jobs

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/logging"
)

// recordingSink is a JobLogSink test double: it records every PutJobLogs call and
// signals on a channel so the async flush can be awaited deterministically.
type recordingSink struct {
	mu      sync.Mutex
	jobID   int64
	entries []logging.LogEntry
	calls   int
	got     chan struct{}
}

func newRecordingSink() *recordingSink { return &recordingSink{got: make(chan struct{}, 1)} }

func (s *recordingSink) PutJobLogs(_ context.Context, jobID int64, entries []logging.LogEntry) error {
	s.mu.Lock()
	s.jobID = jobID
	s.entries = entries
	s.calls++
	s.mu.Unlock()
	select {
	case s.got <- struct{}{}:
	default:
	}
	return nil
}

// TestMarshalReadJobLogsRoundTrip proves the JSONL wire format survives a
// marshal→read round trip with attrs + non-server source intact.
func TestMarshalReadJobLogsRoundTrip(t *testing.T) {
	in := []logging.LogEntry{
		{ID: 1, Level: "INFO", Msg: "jobs: started", Attrs: map[string]string{"job_id": "7", "kind": "k"}, Source: "worker", Host: "pod-a"},
		{ID: 2, Level: "WARN", Msg: "something", Attrs: map[string]string{"job_id": "7"}, Source: "server"},
	}
	blob, err := MarshalJobLogs(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// One JSON object per line.
	if got := bytes.Count(blob, []byte("\n")); got != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d in %q", got, blob)
	}
	out, err := ReadJobLogs(bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("round-trip len = %d, want 2", len(out))
	}
	if out[0].Msg != "jobs: started" || out[0].Attrs["job_id"] != "7" || out[0].Source != "worker" || out[0].Host != "pod-a" {
		t.Fatalf("entry[0] not preserved: %+v", out[0])
	}
	if out[1].Level != "WARN" || out[1].Source != "server" {
		t.Fatalf("entry[1] not preserved: %+v", out[1])
	}
}

// TestReadJobLogsToleratesTruncatedTail: a partial final line yields every
// complete leading line rather than failing the whole read.
func TestReadJobLogsToleratesTruncatedTail(t *testing.T) {
	blob := []byte(`{"id":1,"level":"INFO","msg":"a"}` + "\n" + `{"id":2,"level":"INFO","ms`)
	out, err := ReadJobLogs(bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(out) != 1 || out[0].Msg != "a" {
		t.Fatalf("expected 1 good line, got %+v", out)
	}
}

// TestObjLogSinkPutMarshalsJSONL: the objstore-backed sink writes the JSONL blob
// under the job's key with the ndjson content type.
func TestObjLogSinkPutMarshalsJSONL(t *testing.T) {
	var (
		gotKey, gotCT string
		gotBody       []byte
		gotSize       int64
	)
	stub := blobStoreFunc(func(_ context.Context, key string, r io.Reader, size int64, ct string) error {
		gotKey, gotCT, gotSize = key, ct, size
		gotBody, _ = io.ReadAll(r)
		return nil
	})
	sink := NewObjLogSink(stub, discardLogger())
	if sink == nil {
		t.Fatal("NewObjLogSink returned nil for a non-nil store")
	}
	entries := []logging.LogEntry{{ID: 1, Msg: "hi", Attrs: map[string]string{"job_id": "42"}}}
	if err := sink.PutJobLogs(context.Background(), 42, entries); err != nil {
		t.Fatalf("put: %v", err)
	}
	if gotKey != "job-logs/42.jsonl" {
		t.Fatalf("key = %q, want job-logs/42.jsonl", gotKey)
	}
	if gotCT != jobLogContentType {
		t.Fatalf("content-type = %q, want %q", gotCT, jobLogContentType)
	}
	if gotSize != int64(len(gotBody)) || gotSize == 0 {
		t.Fatalf("size = %d, body len = %d", gotSize, len(gotBody))
	}
	round, _ := ReadJobLogs(bytes.NewReader(gotBody))
	if len(round) != 1 || round[0].Msg != "hi" {
		t.Fatalf("stored body not the entries: %+v", round)
	}
}

// TestNewObjLogSinkNilStore: a nil store yields a nil sink so persistence stays off.
func TestNewObjLogSinkNilStore(t *testing.T) {
	if s := NewObjLogSink(nil, discardLogger()); s != nil {
		t.Fatalf("expected nil sink for nil store, got %#v", s)
	}
}

// TestNewLogCaptureDisabledWhenDepMissing: capture is nil (a no-op) unless BOTH
// the hub and a sink are present.
func TestNewLogCaptureDisabledWhenDepMissing(t *testing.T) {
	hub := logging.NewLogHub(0)
	sink := newRecordingSink()
	if NewLogCapture(nil, sink) != nil {
		t.Fatal("nil hub should disable capture")
	}
	if NewLogCapture(hub, nil) != nil {
		t.Fatal("nil sink should disable capture")
	}
	if NewLogCapture(hub, sink) == nil {
		t.Fatal("both present should enable capture")
	}
	// A nil *LogCapture.begin is a safe no-op returning a callable finish.
	var lc *LogCapture
	lc.begin(1)() // must not panic
}

// TestLogCaptureCollectsOnlyMatchingJob: the capture subscribes to the LogHub,
// keeps ONLY the entries tagged with this job's id (order preserved), and flushes
// them to the sink on finish. Entries for another job are dropped.
func TestLogCaptureCollectsOnlyMatchingJob(t *testing.T) {
	hub := logging.NewLogHub(0)
	sink := newRecordingSink()
	lc := NewLogCapture(hub, sink)

	finish := lc.begin(7)
	hub.Publish(logging.LogEntry{Msg: "first", Attrs: map[string]string{"job_id": "7"}})
	hub.Publish(logging.LogEntry{Msg: "other-job", Attrs: map[string]string{"job_id": "99"}})
	hub.Publish(logging.LogEntry{Msg: "no-attrs"}) // untagged: not this job
	hub.Publish(logging.LogEntry{Msg: "second", Attrs: map[string]string{"job_id": "7"}})
	finish() // unsubscribe + await collector, then async flush

	select {
	case <-sink.got:
	case <-time.After(2 * time.Second):
		t.Fatal("sink was never called")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.jobID != 7 {
		t.Fatalf("flushed jobID = %d, want 7", sink.jobID)
	}
	if len(sink.entries) != 2 {
		t.Fatalf("collected %d entries, want 2: %+v", len(sink.entries), sink.entries)
	}
	if sink.entries[0].Msg != "first" || sink.entries[1].Msg != "second" {
		t.Fatalf("wrong/mis-ordered entries: %+v", sink.entries)
	}
}

// TestLogCaptureSkipsFlushWhenEmpty: a job that logged nothing this capture
// produces no sink call (no empty object written).
func TestLogCaptureSkipsFlushWhenEmpty(t *testing.T) {
	hub := logging.NewLogHub(0)
	sink := newRecordingSink()
	lc := NewLogCapture(hub, sink)

	finish := lc.begin(5)
	hub.Publish(logging.LogEntry{Msg: "for-another", Attrs: map[string]string{"job_id": "6"}})
	finish()

	select {
	case <-sink.got:
		t.Fatal("sink should not be called when no matching entries were captured")
	case <-time.After(200 * time.Millisecond):
		// expected: no flush
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.calls != 0 {
		t.Fatalf("expected 0 sink calls, got %d", sink.calls)
	}
}

// blobStoreFunc adapts a func to the BlobStore interface.
type blobStoreFunc func(ctx context.Context, key string, r io.Reader, size int64, contentType string) error

func (f blobStoreFunc) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	return f(ctx, key, r, size, contentType)
}
