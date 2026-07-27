package handler

// gaka-vh8: wire-format tests for the git-history backfill admin
// handlers. Full HTTP integration lives in FE mocks + the CLI e2e; here
// we cover:
//   1. backfillEvent2json — the WS wire shape. FE keys on
//      msg.kind/msg.job.id/msg.job.status, so a tag typo silently
//      breaks the UI.
//   2. clamping of PATCH bodies — the SourceTag prefix + numeric bands
//      are enforced by db.clampBackfillConfig on Set; we round-trip a
//      wire-shape struct here to guard the pass-through.
//
// A real HTTP-integration test would require the full server startup
// path (echo + registry + auth cookies). Those exist for label-images
// (labels handler tests). This file stays wire-only until we grow a
// backfill-specific test harness.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/backfilljobs"
)

func TestBackfillEvent2JSON_WireShape(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	ev := backfilljobs.Event{
		Kind: backfilljobs.EventAdded,
		Job: backfilljobs.Job{
			ID:         "job-1",
			Owner:      "panda",
			RepoName:   "boomtime",
			RepoPath:   "/Users/panda/code/boomtime",
			Status:     backfilljobs.StatusQueued,
			Total:      42,
			EnqueuedAt: now,
		},
	}
	raw, err := json.Marshal(backfillEvent2json(ev))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["kind"] != "added" {
		t.Errorf("kind = %v, want added", got["kind"])
	}
	job, ok := got["job"].(map[string]any)
	if !ok {
		t.Fatalf("job field missing/wrong type: %#v", got["job"])
	}
	for _, k := range []string{"id", "owner", "repoName", "repoPath", "status", "enqueuedAt", "total"} {
		if _, ok := job[k]; !ok {
			t.Errorf("missing wire field: %q (raw=%s)", k, string(raw))
		}
	}
	if job["status"] != "queued" {
		t.Errorf("status = %v, want queued", job["status"])
	}
}

// TestBackfillConfigPatch_WireShape_RoundTrips just guards against a
// json struct-tag typo on the PATCH body.
func TestBackfillConfigPatch_WireShape_RoundTrips(t *testing.T) {
	src := `{"clusterGapSec": 900, "authorEmails": ["a@b.c"], "sourceTag": "backfill:git", "langMap": {"ts":"TypeScript"}}`
	var p backfillConfigPatch
	if err := json.Unmarshal([]byte(src), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.ClusterGapSec == nil || *p.ClusterGapSec != 900 {
		t.Errorf("ClusterGapSec = %v, want 900", p.ClusterGapSec)
	}
	if p.SourceTag == nil || *p.SourceTag != "backfill:git" {
		t.Errorf("SourceTag = %v, want backfill:git", p.SourceTag)
	}
	if p.AuthorEmails == nil || len(*p.AuthorEmails) != 1 || (*p.AuthorEmails)[0] != "a@b.c" {
		t.Errorf("AuthorEmails = %v, want [a@b.c]", p.AuthorEmails)
	}
	if p.LangMap["ts"] != "TypeScript" {
		t.Errorf("LangMap = %v", p.LangMap)
	}
}
