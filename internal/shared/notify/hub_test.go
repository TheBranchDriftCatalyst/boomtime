package notify

import (
	"context"
	"testing"
	"time"
)

type fakePersister struct {
	saved []string // titles saved
}

func (f *fakePersister) SaveNotification(_ context.Context, _, _, title, _ string, _ map[string]any, _ time.Time) error {
	f.saved = append(f.saved, title)
	return nil
}

// TestPublish_DurableSavesEphemeralDoesNot pins the durable/ephemeral split: a
// Durable event is written to the persister AND fanned out; an ephemeral one is
// fanned out only. Both reach live subscribers.
func TestPublish_DurableSavesEphemeralDoesNot(t *testing.T) {
	h := NewHub()
	fp := &fakePersister{}
	h.SetPersister(fp, nil)

	ch, unsub := h.Subscribe("u1")
	defer unsub()

	h.Publish(Event{Owner: "u1", Type: "book.finished", Title: "durable", Durable: true})
	h.Publish(Event{Owner: "u1", Type: "toast", Title: "ephemeral"})

	// Both delivered live.
	got := []string{}
	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			got = append(got, ev.Title)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
	if len(got) != 2 {
		t.Fatalf("want 2 live events, got %v", got)
	}
	// Only the durable one was persisted.
	if len(fp.saved) != 1 || fp.saved[0] != "durable" {
		t.Fatalf("persister saved = %v, want [durable]", fp.saved)
	}
}

// TestPublish_DurableWithoutPersisterStillDelivers: durability is best-effort — a
// nil persister disables storage but never blocks the live fan-out.
func TestPublish_DurableWithoutPersisterStillDelivers(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe("u1")
	defer unsub()
	h.Publish(Event{Owner: "u1", Title: "x", Durable: true})
	select {
	case ev := <-ch:
		if ev.Title != "x" {
			t.Fatalf("got %q", ev.Title)
		}
	case <-time.After(time.Second):
		t.Fatal("durable event with no persister must still deliver live")
	}
}
