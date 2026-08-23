// loghub_ginkgo_test.go — ginkgo mirror of loghub_test.go (boom-0vp).
// 1:1 case map (11 stdlib TestXxx):
//
//	TestLogHubPublishDeliversToSubscribers   → LogHub > "Publish delivers to every subscriber"
//	TestLogHubAssignsMonotonicIDs            → LogHub > "assigns monotonic IDs"
//	TestLogHubRingBufferEvictsOldest         → LogHub > "ring buffer evicts oldest"
//	TestLogHubBackfillAfterID                → LogHub > "Backfill(id) returns entries > id"
//	TestLogHubBufferFullDropsWithoutBlocking → LogHub > "buffer-full drops without blocking"
//	TestLogHubUnsubscribeClosesAndSilencesPublish
//	                                         → LogHub > "Unsubscribe closes + silences later Publish"
//	TestLogHubNilReceiverIsNoOp              → LogHub > "nil receiver is a no-op"
//	TestFilterForUser_PassesThroughOwnerMatch→ FilterForUser > "owner match passes through"
//	TestFilterForUser_DropsCrossOwner        → FilterForUser > "cross-owner leak is dropped"
//	TestFilterForUser_PassesThroughUnowned   → FilterForUser > "unowned passes through"
//	TestFilterForUser_EmptyInputEmptyOutput  → FilterForUser > "empty/nil in → empty/nil out"
//	TestFilterForUser_EmptyRequesterDropsAllUserScoped
//	                                         → FilterForUser > "empty requester fail-closed"
//	TestFilterForUser_MixedAudienceSegregation
//	                                         → FilterForUser > "mixed audience segregation"
package logging

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// asMsgsGinkgo is a local helper to avoid name-collision with the stdlib
// file's asMsgs (same-package; both would live in the compiled binary
// during parallel migration).
func asMsgsGinkgo(entries []LogEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Msg)
	}
	return out
}

var _ = Describe("LogHub", func() {
	It("Publish delivers to every subscriber", func() {
		h := NewLogHub(10)
		chA := h.Subscribe()
		chB := h.Subscribe()

		h.Publish(LogEntry{Level: "INFO", Msg: "hello"})

		for _, ch := range []chan LogEntry{chA, chB} {
			select {
			case e := <-ch:
				Expect(e.Msg).To(Equal("hello"))
				Expect(e.ID).To(BeEquivalentTo(1))
			default:
				Fail("expected an entry, channel empty")
			}
		}
	})

	It("assigns monotonic IDs", func() {
		h := NewLogHub(10)
		for i := 0; i < 5; i++ {
			h.Publish(LogEntry{Msg: "x"})
		}
		all := h.Backfill(0)
		Expect(all).To(HaveLen(5))
		for i, e := range all {
			Expect(e.ID).To(BeEquivalentTo(int64(i + 1)))
		}
	})

	It("ring buffer evicts oldest when full", func() {
		h := NewLogHub(3)
		for i := 1; i <= 5; i++ {
			h.Publish(LogEntry{Msg: "x"})
		}
		all := h.Backfill(0)
		Expect(all).To(HaveLen(3))
		Expect(all[0].ID).To(BeEquivalentTo(3))
		Expect(all[2].ID).To(BeEquivalentTo(5))
	})

	It("Backfill(id) returns only entries whose id > `id`", func() {
		h := NewLogHub(10)
		for i := 0; i < 5; i++ {
			h.Publish(LogEntry{Msg: "x"})
		}
		got := h.Backfill(3)
		Expect(got).To(HaveLen(2))
		Expect(got[0].ID).To(BeEquivalentTo(4))
		Expect(got[1].ID).To(BeEquivalentTo(5))
	})

	It("buffer-full drops without blocking Publish", func() {
		h := NewLogHub(2000)
		ch := h.Subscribe() // subscriber buffer cap = 256

		for i := 0; i < 300; i++ {
			h.Publish(LogEntry{Msg: "x"})
		}
		Expect(len(ch)).To(Equal(256))

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
		Expect(drained).To(Equal(256))
	})

	It("Unsubscribe closes the channel and silences later Publish", func() {
		h := NewLogHub(10)
		ch := h.Subscribe()

		h.Unsubscribe(ch)

		e, ok := <-ch
		Expect(ok).To(BeFalse(), "receive after unsubscribe should return !ok")
		Expect(e.Msg).To(BeEmpty())

		Expect(func() { h.Publish(LogEntry{Msg: "after"}) }).NotTo(Panic())
	})

	It("nil receiver is a no-op on every op", func() {
		var h *LogHub
		Expect(func() { h.Publish(LogEntry{Msg: "x"}) }).NotTo(Panic())
		Expect(h.Backfill(0)).To(BeNil())
	})

	It("defaults an unset Source to server (backward-compat for every pre-existing Publish call)", func() {
		h := NewLogHub(10)
		h.Publish(LogEntry{Msg: "x"})
		Expect(h.Backfill(0)[0].Source).To(Equal("server"))
	})

	It("preserves an explicitly-set Source (the worker relay's injection path)", func() {
		h := NewLogHub(10)
		h.Publish(LogEntry{Msg: "x", Source: "worker", Host: "boomtime-worker-1"})
		got := h.Backfill(0)[0]
		Expect(got.Source).To(Equal("worker"))
		Expect(got.Host).To(Equal("boomtime-worker-1"))
	})
})

var _ = Describe("FilterForUser (boom-awh.2 owner scoping)", func() {
	It("passes an owner-matched entry through", func() {
		in := []LogEntry{
			{Msg: "wakatime key saved for A", Attrs: map[string]string{OwnerAttrKey: "alice"}},
		}
		out := FilterForUser(in, "alice")
		Expect(out).To(HaveLen(1))
		Expect(out[0].Msg).To(Equal("wakatime key saved for A"))
	})

	It("drops a cross-owner entry (load-bearing anti-tautology)", func() {
		in := []LogEntry{
			{Msg: "wakatime key cleared for B", Attrs: map[string]string{OwnerAttrKey: "bob"}},
		}
		out := FilterForUser(in, "alice")
		Expect(out).To(BeEmpty())
		Expect(asMsgsGinkgo(out)).NotTo(ContainElement("wakatime key cleared for B"))
	})

	It("passes unowned entries through (server-scope records)", func() {
		in := []LogEntry{
			{Msg: "healthz served"},
			{Msg: "migrations up", Attrs: map[string]string{"phase": "27"}},
		}
		out := FilterForUser(in, "alice")
		Expect(out).To(HaveLen(2))
	})

	It("empty/nil in → empty/nil out (callers depend on the distinction)", func() {
		Expect(FilterForUser([]LogEntry{}, "alice")).To(BeEmpty())
		Expect(FilterForUser(nil, "alice")).To(BeNil())
	})

	It("empty requester → only unowned records (fail-closed)", func() {
		in := []LogEntry{
			{Msg: "server started"},
			{Msg: "wakatime key saved for A", Attrs: map[string]string{OwnerAttrKey: "alice"}},
			{Msg: "wakatime key saved for B", Attrs: map[string]string{OwnerAttrKey: "bob"}},
		}
		out := FilterForUser(in, "")
		msgs := asMsgsGinkgo(out)
		Expect(msgs).To(Equal([]string{"server started"}))
	})

	It("mixed audience → disjoint per-user views + shared server tail", func() {
		in := []LogEntry{
			{Msg: "for-A-1", Attrs: map[string]string{OwnerAttrKey: "alice"}},
			{Msg: "for-B-1", Attrs: map[string]string{OwnerAttrKey: "bob"}},
			{Msg: "server-1"},
			{Msg: "for-A-2", Attrs: map[string]string{OwnerAttrKey: "alice"}},
			{Msg: "for-B-2", Attrs: map[string]string{OwnerAttrKey: "bob"}},
			{Msg: "server-2"},
		}
		viewA := asMsgsGinkgo(FilterForUser(in, "alice"))
		viewB := asMsgsGinkgo(FilterForUser(in, "bob"))

		Expect(viewA).To(HaveLen(4))
		Expect(viewA).NotTo(ContainElements("for-B-1", "for-B-2"))

		Expect(viewB).To(HaveLen(4))
		Expect(viewB).NotTo(ContainElements("for-A-1", "for-A-2"))
	})
})
