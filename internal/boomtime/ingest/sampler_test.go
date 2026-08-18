package ingest

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// capHandler captures slog records so a test can assert exactly how many
// sampled lines were emitted.
type capHandler struct {
	mu   sync.Mutex
	msgs []string
}

func (c *capHandler) Enabled(context.Context, slog.Level) bool { return true }
func (c *capHandler) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, r.Message)
	return nil
}
func (c *capHandler) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *capHandler) WithGroup(string) slog.Handler      { return c }
func (c *capHandler) count(msg string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, m := range c.msgs {
		if m == msg {
			n++
		}
	}
	return n
}

func sp(s string) *string { return &s }

func batch(n int, hb model.HeartbeatPayload) []model.HeartbeatPayload {
	out := make([]model.HeartbeatPayload, n)
	for i := range out {
		out[i] = hb
	}
	return out
}

// TestLogIngestSampled_CrossesBoundaryByCount: the 1:N line fires once per N
// heartbeats CROSSED (by cumulative count), not per request — a sub-N batch is
// silent, and a batch spanning multiple boundaries still emits a single line.
func TestLogIngestSampled_CrossesBoundaryByCount(t *testing.T) {
	cap := &capHandler{}
	h := &Handler{Cfg: &config.Config{HeartbeatLogSampleN: 100}, Logger: slog.New(cap)}
	hb := model.HeartbeatPayload{Project: sp("p"), Language: sp("go"), Editor: sp("vscode")}

	h.logIngestSampled("alice", batch(99, hb)) // total 99 — no boundary
	if got := cap.count("heartbeats ingested"); got != 0 {
		t.Fatalf("99 heartbeats should not log: got %d", got)
	}
	h.logIngestSampled("alice", batch(1, hb)) // total 100 — crosses 100
	if got := cap.count("heartbeats ingested"); got != 1 {
		t.Fatalf("crossing 100 should log once: got %d", got)
	}
	h.logIngestSampled("alice", batch(250, hb)) // total 350 — crosses 200 & 300
	if got := cap.count("heartbeats ingested"); got != 2 {
		t.Fatalf("a batch spanning multiple boundaries logs once: got %d", got)
	}
}

// TestLogIngestSampled_Disabled: N<=0 (and an empty batch) never logs.
func TestLogIngestSampled_Disabled(t *testing.T) {
	cap := &capHandler{}
	h := &Handler{Cfg: &config.Config{HeartbeatLogSampleN: 0}, Logger: slog.New(cap)}
	h.logIngestSampled("alice", batch(1000, model.HeartbeatPayload{Project: sp("p")}))
	if got := cap.count("heartbeats ingested"); got != 0 {
		t.Fatalf("sample N=0 must disable the line: got %d", got)
	}

	h2 := &Handler{Cfg: &config.Config{HeartbeatLogSampleN: 100}, Logger: slog.New(cap)}
	h2.logIngestSampled("alice", nil) // empty batch
	if got := cap.count("heartbeats ingested"); got != 0 {
		t.Fatalf("empty batch must not log: got %d", got)
	}
}

// TestDistinctPtr: distinct, sorted, nil/empty skipped, capped with a +N suffix.
func TestDistinctPtr(t *testing.T) {
	get := func(hb model.HeartbeatPayload) *string { return hb.Project }

	// dedupe + sort + skip nil/empty
	hbs := []model.HeartbeatPayload{
		{Project: sp("b")}, {Project: sp("a")}, {Project: sp("a")},
		{Project: sp("")}, {Project: nil},
	}
	if got := distinctPtr(hbs, get); got != "a,b" {
		t.Fatalf("distinct/sort/skip: want %q, got %q", "a,b", got)
	}

	// cap at maxDistinctInSummary with a +N suffix
	var many []model.HeartbeatPayload
	for _, p := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		many = append(many, model.HeartbeatPayload{Project: sp(p)})
	}
	if got := distinctPtr(many, get); got != "a,b,c,d,e,f +2" {
		t.Fatalf("cap+suffix: want %q, got %q", "a,b,c,d,e,f +2", got)
	}

	// never set → empty string
	if got := distinctPtr([]model.HeartbeatPayload{{Project: nil}}, get); got != "" {
		t.Fatalf("all-nil field: want empty, got %q", got)
	}
}
