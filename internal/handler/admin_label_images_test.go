package handler

// gaka-8bz: wire-format tests for the image-job queue handler.
//
// The full WS handshake path is exercised implicitly by the FE hook test
// (web/src/features/admin/useImageJobQueue.test.ts, which mocks the
// WebSocket global). These backend tests cover the two pieces that MUST
// stay stable in the Go handler code:
//
//   1. event2json — the wire shape sent per WS frame. The FE hook keys
//      its Map by job.id and switches on kind. Any drift here silently
//      breaks the FE without a compile error.
//   2. The enqueue → response mapping in AdminLabelImagesRegenerate is
//      integration-tested at the FE layer (the mock server there returns
//      whatever the FE hook wants); the shape lives in regenResponseJob.
//      A tiny JSON round-trip here guards against a struct-tag typo.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/imagejobs"
)

func TestEvent2JSON_WireShape(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	seed := int64(42)
	ev := imagejobs.Event{
		Kind: imagejobs.EventAdded,
		Job: imagejobs.Job{
			ID:         "job-1",
			LabelID:    "late-night-coder",
			Prompt:     "hooded figure",
			Model:      "sdxl-illustrious-xl",
			Size:       "1024x1024",
			Seed:       &seed,
			Status:     imagejobs.StatusQueued,
			EnqueuedAt: now,
		},
	}
	raw, err := json.Marshal(event2json(ev))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["kind"] != "added" {
		t.Fatalf("kind = %v, want \"added\"", got["kind"])
	}
	job, ok := got["job"].(map[string]any)
	if !ok {
		t.Fatalf("job field missing or wrong type: %#v", got["job"])
	}
	// The FE hook expects camelCase for id/labelId/status/enqueuedAt (see
	// useImageJobQueue.ts:JobState).
	for _, k := range []string{"id", "labelId", "prompt", "status", "enqueuedAt"} {
		if _, ok := job[k]; !ok {
			t.Errorf("missing wire field: %q (raw=%s)", k, string(raw))
		}
	}
	if job["id"] != "job-1" {
		t.Errorf("id = %v, want job-1", job["id"])
	}
	if job["labelId"] != "late-night-coder" {
		t.Errorf("labelId = %v, want late-night-coder", job["labelId"])
	}
	if job["status"] != "queued" {
		t.Errorf("status = %v, want queued", job["status"])
	}
}

func TestRegenResponseJob_WireShape(t *testing.T) {
	j := regenResponseJob{JobID: "j1", LabelID: "polyglot", Existing: true}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Must match what the FE api.regenerateLabelImages() types on the
	// Promise return: { jobId, labelId, existing }.
	want := `{"jobId":"j1","labelId":"polyglot","existing":true}`
	if string(b) != want {
		t.Fatalf("wire shape: got %s want %s", string(b), want)
	}
}

func TestJob_JSONOmitsZeroTimestamps(t *testing.T) {
	// A freshly-queued job has StartedAt=nil, FinishedAt=nil. The FE hook
	// treats those as absent (undefined?) rather than epoch-zero strings.
	// Confirm omitempty drops the pointer fields cleanly.
	j := imagejobs.Job{
		ID:         "x",
		LabelID:    "a",
		Status:     imagejobs.StatusQueued,
		EnqueuedAt: time.Now().UTC(),
	}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if _, has := got["startedAt"]; has {
		t.Errorf("startedAt present on queued job: %s", string(b))
	}
	if _, has := got["finishedAt"]; has {
		t.Errorf("finishedAt present on queued job: %s", string(b))
	}
	if _, has := got["error"]; has {
		t.Errorf("error present on queued job: %s", string(b))
	}
	if _, has := got["seed"]; has {
		t.Errorf("seed present on nil-seed job: %s", string(b))
	}
}
