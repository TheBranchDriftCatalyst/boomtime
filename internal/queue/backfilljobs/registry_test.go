package backfilljobs

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRegistryWith(logger, 50*time.Millisecond, 50*time.Millisecond)
}

func TestEnqueue_ReturnsFreshJob(t *testing.T) {
	r := newTestRegistry(t)
	j := r.Enqueue(EnqueueInput{
		Owner:    "panda",
		RepoName: "boomtime",
		RepoPath: "/tmp/x",
		Total:    10,
	})
	if j.ID == "" || j.Status != StatusQueued {
		t.Fatalf("bad job: %+v", j)
	}
	got, ok := r.Get(j.ID)
	if !ok || got.Owner != "panda" {
		t.Fatalf("Get failed or wrong owner: %+v", got)
	}
}

func TestUpdate_AutoStartsAndFinishes(t *testing.T) {
	r := newTestRegistry(t)
	j := r.Enqueue(EnqueueInput{Owner: "p", RepoName: "r"})
	r.Update(j.ID, UpdatePatch{Status: StatusRunning})
	got, _ := r.Get(j.ID)
	if got.StartedAt == nil {
		t.Errorf("StartedAt not set after running")
	}
	r.Update(j.ID, UpdatePatch{Status: StatusDone})
	got, _ = r.Get(j.ID)
	if got.FinishedAt == nil {
		t.Errorf("FinishedAt not set after done")
	}
	// Retention timer should remove the row within a couple of tick
	// periods. 200ms is generous vs the 50ms retention we configured.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, ok := r.Get(j.ID); !ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job still present after retention window")
}

func TestIncrementCounts_FlipsQueuedToRunning(t *testing.T) {
	r := newTestRegistry(t)
	j := r.Enqueue(EnqueueInput{Owner: "p", RepoName: "r"})
	got, ok := r.IncrementCounts(j.ID, 3, 100, 5)
	if !ok {
		t.Fatalf("IncrementCounts returned ok=false")
	}
	if got.Status != StatusRunning {
		t.Errorf("Status = %q, want running", got.Status)
	}
	if got.Processed != 3 || got.Written != 100 || got.Skipped != 5 {
		t.Errorf("counters = %d/%d/%d, want 3/100/5",
			got.Processed, got.Written, got.Skipped)
	}
}

func TestSnapshotFor_OwnerFilter(t *testing.T) {
	r := newTestRegistry(t)
	r.Enqueue(EnqueueInput{Owner: "alice", RepoName: "r1"})
	r.Enqueue(EnqueueInput{Owner: "bob", RepoName: "r2"})
	r.Enqueue(EnqueueInput{Owner: "alice", RepoName: "r3"})
	got := r.SnapshotFor("alice")
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (owner filter)", len(got))
	}
	for _, j := range got {
		if j.Owner != "alice" {
			t.Errorf("wrong owner: %s", j.Owner)
		}
	}
}

func TestSubscribe_ReceivesAddedEvents(t *testing.T) {
	r := newTestRegistry(t)
	sub, unsub := r.Subscribe()
	defer unsub()
	go r.Enqueue(EnqueueInput{Owner: "p", RepoName: "r"})
	select {
	case ev := <-sub:
		if ev.Kind != EventAdded {
			t.Errorf("kind = %q, want added", ev.Kind)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}
