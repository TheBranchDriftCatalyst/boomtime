package logging

import (
	"testing"
)

func TestLogHubPublishDeliversToSubscribers(t *testing.T) {
	h := NewLogHub(10)
	chA := h.Subscribe()
	chB := h.Subscribe()

	h.Publish(LogEntry{Level: "INFO", Msg: "hello"})

	for i, ch := range []chan LogEntry{chA, chB} {
		select {
		case e := <-ch:
			if e.Msg != "hello" {
				t.Errorf("subscriber %d: Msg = %q, want hello", i, e.Msg)
			}
			if e.ID != 1 {
				t.Errorf("subscriber %d: ID = %d, want 1 (monotonic)", i, e.ID)
			}
		default:
			t.Errorf("subscriber %d: expected an entry, channel empty", i)
		}
	}
}

func TestLogHubAssignsMonotonicIDs(t *testing.T) {
	h := NewLogHub(10)
	for i := 0; i < 5; i++ {
		h.Publish(LogEntry{Msg: "x"})
	}
	all := h.Backfill(0)
	if len(all) != 5 {
		t.Fatalf("Backfill(0) returned %d, want 5", len(all))
	}
	for i, e := range all {
		if e.ID != int64(i+1) {
			t.Errorf("entry %d: ID = %d, want %d", i, e.ID, i+1)
		}
	}
}

func TestLogHubRingBufferEvictsOldest(t *testing.T) {
	h := NewLogHub(3)
	for i := 1; i <= 5; i++ {
		h.Publish(LogEntry{Msg: "x"})
	}
	all := h.Backfill(0)
	if len(all) != 3 {
		t.Fatalf("ring holds %d entries, want 3 (capacity)", len(all))
	}
	// Oldest two (IDs 1,2) evicted; retained tail is IDs 3,4,5 in order.
	if all[0].ID != 3 || all[2].ID != 5 {
		t.Fatalf("retained IDs = [%d..%d], want [3..5]", all[0].ID, all[2].ID)
	}
}

func TestLogHubBackfillAfterID(t *testing.T) {
	h := NewLogHub(10)
	for i := 0; i < 5; i++ {
		h.Publish(LogEntry{Msg: "x"})
	}
	// Resume after ID 3: expect only IDs 4 and 5.
	got := h.Backfill(3)
	if len(got) != 2 || got[0].ID != 4 || got[1].ID != 5 {
		t.Fatalf("Backfill(3) = %+v, want IDs [4,5]", got)
	}
}

func TestLogHubBufferFullDropsWithoutBlocking(t *testing.T) {
	h := NewLogHub(2000)
	ch := h.Subscribe() // cap 256, undrained

	// Publish 300 entries; publishing must never block even though the
	// subscriber buffer (256) fills up — the excess is silently dropped.
	for i := 0; i < 300; i++ {
		h.Publish(LogEntry{Msg: "x"})
	}

	if len(ch) != 256 {
		t.Fatalf("channel holds %d entries, want 256 (rest dropped)", len(ch))
	}
	drained := 0
	for {
		select {
		case <-ch:
			drained++
			continue
		default:
		}
		break
	}
	if drained != 256 {
		t.Fatalf("drained %d entries, want 256", drained)
	}
}

func TestLogHubUnsubscribeClosesAndSilencesPublish(t *testing.T) {
	h := NewLogHub(10)
	ch := h.Subscribe()

	h.Unsubscribe(ch)

	e, ok := <-ch
	if ok {
		t.Fatalf("receive after unsubscribe: ok = true, want false (closed)")
	}
	if e.Msg != "" {
		t.Fatalf("receive after unsubscribe: Msg = %q, want zero value", e.Msg)
	}

	// A subsequent Publish must be a no-op (no send on closed channel).
	h.Publish(LogEntry{Msg: "after"})
}

func TestLogHubNilReceiverIsNoOp(t *testing.T) {
	var h *LogHub
	h.Publish(LogEntry{Msg: "x"}) // must not panic
	if got := h.Backfill(0); got != nil {
		t.Fatalf("nil hub Backfill = %v, want nil", got)
	}
}
