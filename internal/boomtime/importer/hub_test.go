// hub_ginkgo_test.go — ginkgo mirror of hub_test.go (boom-0vp).
// 1:1 case map (4 stdlib TestXxx):
//
//	TestHubPublishDeliversToSubscribers        → Hub > "Publish delivers to all subscribers"
//	TestHubBufferFullDropsWithoutBlocking      → Hub > "buffer-full drops without blocking Publish"
//	TestHubUnsubscribeClosesAndSilencesPublish → Hub > "Unsubscribe closes channel and silences later Publishes"
//	TestHubPublishNoSubscribersIsNoOp          → Hub > "Publish with no subscribers is a no-op"
package importer

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Hub", func() {
	It("Publish delivers to all subscribers", func() {
		h := NewHub()
		chA := h.Subscribe(1)
		chB := h.Subscribe(1)

		h.Publish(1, Event{Type: "state"})

		for i, ch := range []chan Event{chA, chB} {
			select {
			case ev := <-ch:
				Expect(ev.Type).To(Equal("state"), "subscriber %d", i)
			default:
				Fail("subscriber channel empty when an event was expected")
			}
		}
	})

	It("buffer-full drops without blocking Publish (cap 64)", func() {
		h := NewHub()
		ch := h.Subscribe(7) // cap 64, undrained

		// Publish 65 events; the 65th must be silently dropped (Publish never blocks).
		for i := 0; i < 65; i++ {
			h.Publish(7, Event{Type: "log"})
		}

		Expect(ch).To(HaveLen(64))

		// Drain and confirm exactly 64 buffered, no panic occurred.
		drained := 0
	drainLoop:
		for {
			select {
			case <-ch:
				drained++
			default:
				break drainLoop
			}
		}
		Expect(drained).To(Equal(64))
	})

	It("Unsubscribe closes the channel and silences later Publishes", func() {
		h := NewHub()
		ch := h.Subscribe(3)

		h.Unsubscribe(3, ch)

		// Receiving on a closed channel returns zero-value, ok=false.
		ev, ok := <-ch
		Expect(ok).To(BeFalse())
		Expect(ev.Type).To(Equal(""))

		// A subsequent Publish to that jobID must be a no-op (no panic, no send on closed).
		Expect(func() { h.Publish(3, Event{Type: "state"}) }).NotTo(Panic())
	})

	It("Publish with no subscribers is a no-op (no panic)", func() {
		h := NewHub()
		Expect(func() { h.Publish(999, Event{Type: "state"}) }).NotTo(Panic())
	})
})
