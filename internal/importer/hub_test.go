package importer

import (
	"testing"
)

func TestHubPublishDeliversToSubscribers(t *testing.T) {
	h := NewHub()
	chA := h.Subscribe(1)
	chB := h.Subscribe(1)

	h.Publish(1, Event{Type: "state"})

	for i, ch := range []chan Event{chA, chB} {
		select {
		case ev := <-ch:
			if ev.Type != "state" {
				t.Errorf("subscriber %d: Type = %q, want state", i, ev.Type)
			}
		default:
			t.Errorf("subscriber %d: expected an event, channel empty", i)
		}
	}
}

func TestHubBufferFullDropsWithoutBlocking(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe(7) // cap 64, undrained

	// Publish 65 events; the 65th must be silently dropped (Publish never blocks).
	for i := 0; i < 65; i++ {
		h.Publish(7, Event{Type: "log"})
	}

	if len(ch) != 64 {
		t.Fatalf("channel holds %d events, want 64 (65th dropped)", len(ch))
	}
	// Drain and confirm exactly 64 buffered, no panic occurred.
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
	if drained != 64 {
		t.Fatalf("drained %d events, want 64", drained)
	}
}

func TestHubUnsubscribeClosesAndSilencesPublish(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe(3)

	h.Unsubscribe(3, ch)

	// Receiving on a closed channel returns zero-value, ok=false.
	ev, ok := <-ch
	if ok {
		t.Fatalf("receive after unsubscribe: ok = true, want false (closed)")
	}
	if ev.Type != "" {
		t.Fatalf("receive after unsubscribe: Type = %q, want zero value", ev.Type)
	}

	// A subsequent Publish to that jobID must be a no-op (no panic, no send on closed).
	h.Publish(3, Event{Type: "state"})
}

func TestHubPublishNoSubscribersIsNoOp(t *testing.T) {
	h := NewHub()
	h.Publish(999, Event{Type: "state"}) // must not panic
}
