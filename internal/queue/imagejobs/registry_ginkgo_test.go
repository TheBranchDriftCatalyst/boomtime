// registry_ginkgo_test.go — ginkgo mirror of registry_test.go.
// 1:1 case map (8 stdlib TestXxx):
//   TestRegistry_EnqueueNewLabelReturnsFreshJob            → Registry > "Enqueue on empty registry returns a fresh Queued job"
//   TestRegistry_EnqueueDedupesQueuedLabel                 → Registry > "Enqueue dedupes a queued label and preserves original params"
//   TestRegistry_EnqueueDedupesRunningLabel                → Registry > "Enqueue dedupes a running label"
//   TestRegistry_EnqueueSupersedesTerminalLabel            → Registry > "Enqueue supersedes a terminal label with a fresh job"
//   TestRegistry_SubscribeReceivesLifecycleEvents          → Registry > "Subscribe receives added/running/done/removed lifecycle events"
//   TestRegistry_MarkErrorRetainsErrorAndSchedulesRemoval  → Registry > "MarkError retains the error message and schedules removal"
//   TestRegistry_SnapshotReturnsCurrentJobsOrdered         → Registry > "Snapshot returns current jobs in enqueue order"
//   TestRegistry_SlowSubscriberDoesNotBlockBroadcast       → Registry > "slow subscriber does not block broadcast"
//   TestRegistry_ClaimUnblocksOnContextCancel              → Registry > "claim unblocks on context cancel"
package imagejobs

import (
	"context"
	"io"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newRegistryGinkgo returns a Registry with tiny retention windows so
// time-sensitive Its finish in well under a second.
func newRegistryGinkgo() *Registry {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRegistryWith(logger, 50*time.Millisecond, 50*time.Millisecond)
}

var _ = Describe("Registry", func() {
	It("Enqueue on empty registry returns a fresh Queued job", func() {
		r := newRegistryGinkgo()
		job, existing := r.Enqueue(EnqueueInput{LabelID: "late-night-coder", Prompt: "prompt", Model: "", Size: ""})
		Expect(existing).To(BeFalse())
		Expect(job.ID).NotTo(BeEmpty())
		Expect(job.Status).To(Equal(StatusQueued))
		Expect(job.LabelID).To(Equal("late-night-coder"))
	})

	It("Enqueue dedupes a queued label and preserves the original params", func() {
		r := newRegistryGinkgo()
		a, existingA := r.Enqueue(EnqueueInput{LabelID: "polyglot", Prompt: "p", Model: "", Size: ""})
		Expect(existingA).To(BeFalse())

		b, existingB := r.Enqueue(EnqueueInput{LabelID: "polyglot", Prompt: "p2", Model: "different-model", Size: "512x512"})
		Expect(existingB).To(BeTrue())
		Expect(b.ID).To(Equal(a.ID))
		// The returned "existing" job should reflect the ORIGINAL parameters —
		// subsequent Enqueues do not mutate an in-flight job.
		Expect(b.Model).To(BeEmpty())
	})

	It("Enqueue dedupes a running label", func() {
		r := newRegistryGinkgo()
		a, _ := r.Enqueue(EnqueueInput{LabelID: "machine", Prompt: "p", Model: "", Size: ""})
		r.MarkRunning(a.ID)
		b, existing := r.Enqueue(EnqueueInput{LabelID: "machine", Prompt: "p", Model: "", Size: ""})
		Expect(existing).To(BeTrue())
		Expect(b.ID).To(Equal(a.ID))
		Expect(b.Status).To(Equal(StatusRunning))
	})

	It("Enqueue supersedes a terminal label with a fresh job", func() {
		r := newRegistryGinkgo()
		a, _ := r.Enqueue(EnqueueInput{LabelID: "weekend-warrior", Prompt: "p", Model: "", Size: ""})
		r.MarkRunning(a.ID)
		r.MarkDone(a.ID)
		// Retention timer is 50ms; call Enqueue immediately (well before it
		// fires) and confirm we get a NEW job.
		b, existing := r.Enqueue(EnqueueInput{LabelID: "weekend-warrior", Prompt: "p2", Model: "", Size: ""})
		Expect(existing).To(BeFalse())
		Expect(b.ID).NotTo(Equal(a.ID))
	})

	It("Subscribe receives added/running/done/removed lifecycle events", func() {
		r := newRegistryGinkgo()
		sub, unsub := r.Subscribe()
		DeferCleanup(unsub)

		job, _ := r.Enqueue(EnqueueInput{LabelID: "consistent", Prompt: "p", Model: "", Size: ""})
		ev := ginkgoMustReceive(sub, time.Second)
		Expect(ev.Kind).To(Equal(EventAdded))
		Expect(ev.Job.ID).To(Equal(job.ID))

		r.MarkRunning(job.ID)
		ev = ginkgoMustReceive(sub, time.Second)
		Expect(ev.Kind).To(Equal(EventUpdated))
		Expect(ev.Job.Status).To(Equal(StatusRunning))

		r.MarkDone(job.ID)
		ev = ginkgoMustReceive(sub, time.Second)
		Expect(ev.Kind).To(Equal(EventUpdated))
		Expect(ev.Job.Status).To(Equal(StatusDone))

		// Retention is 50ms; the removal event should follow shortly.
		ev = ginkgoMustReceive(sub, 500*time.Millisecond)
		Expect(ev.Kind).To(Equal(EventRemoved))
		Expect(ev.Job.ID).To(Equal(job.ID))
	})

	It("MarkError retains the error message and schedules removal", func() {
		r := newRegistryGinkgo()
		sub, unsub := r.Subscribe()
		DeferCleanup(unsub)

		job, _ := r.Enqueue(EnqueueInput{LabelID: "sprinter", Prompt: "p", Model: "", Size: ""})
		ginkgoDrain(sub) // drop the "added" event

		r.MarkError(job.ID, "comfyui exploded")
		ev := ginkgoMustReceive(sub, time.Second)
		Expect(ev.Kind).To(Equal(EventUpdated))
		Expect(ev.Job.Status).To(Equal(StatusError))
		Expect(ev.Job.Error).To(Equal("comfyui exploded"))

		// Removal fires after retentionError (50ms in the test registry).
		ev = ginkgoMustReceive(sub, 500*time.Millisecond)
		Expect(ev.Kind).To(Equal(EventRemoved))
	})

	It("Snapshot returns current jobs in enqueue order", func() {
		r := newRegistryGinkgo()
		// A tiny sleep between calls forces distinguishable EnqueuedAt
		// timestamps without leaning on sub-microsecond resolution.
		r.Enqueue(EnqueueInput{LabelID: "a", Prompt: "", Model: "", Size: ""})
		time.Sleep(2 * time.Millisecond)
		r.Enqueue(EnqueueInput{LabelID: "b", Prompt: "", Model: "", Size: ""})
		time.Sleep(2 * time.Millisecond)
		r.Enqueue(EnqueueInput{LabelID: "c", Prompt: "", Model: "", Size: ""})

		snap := r.Snapshot()
		Expect(snap).To(HaveLen(3))
		Expect(snap[0].LabelID).To(Equal("a"))
		Expect(snap[1].LabelID).To(Equal("b"))
		Expect(snap[2].LabelID).To(Equal("c"))
	})

	It("slow subscriber does not block broadcast", func() {
		r := newRegistryGinkgo()
		// Subscribe but never read — buffer is 16. Fire many events; each
		// broadcastLocked should complete in bounded time (dropping the
		// oldest on overflow rather than blocking on the wedged subscriber).
		_, unsub := r.Subscribe()
		DeferCleanup(unsub)

		done := make(chan struct{})
		go func() {
			for i := 0; i < 100; i++ {
				r.Enqueue(EnqueueInput{LabelID: "dedupe-me", Prompt: "", Model: "", Size: ""})
				// Enqueue after the first dedupes (no new event); force a
				// state transition to keep events flowing.
				id := r.byLabelSnapshot("dedupe-me")
				if id != "" {
					r.MarkRunning(id)
					r.MarkDone(id)
				}
			}
			close(done)
		}()

		Eventually(done, 3*time.Second, 10*time.Millisecond).Should(BeClosed())
	})

	It("claim unblocks on context cancel", func() {
		r := newRegistryGinkgo()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		var claimOK bool
		go func() {
			_, ok := r.claim(ctx)
			claimOK = ok
			close(done)
		}()
		cancel()
		Eventually(done, time.Second, 10*time.Millisecond).Should(BeClosed())
		Expect(claimOK).To(BeFalse())
	})
})

// ginkgoMustReceive mirrors the stdlib mustReceive helper. Fails the current
// spec via Gomega if no event arrives within timeout.
func ginkgoMustReceive(ch <-chan Event, timeout time.Duration) Event {
	GinkgoHelper()
	select {
	case ev, ok := <-ch:
		Expect(ok).To(BeTrue(), "channel closed while waiting for event")
		return ev
	case <-time.After(timeout):
		Fail("timed out waiting for event after " + timeout.String())
		return Event{}
	}
}

// ginkgoDrain mirrors the stdlib drain helper (non-blocking single receive).
func ginkgoDrain(ch <-chan Event) {
	select {
	case <-ch:
	default:
	}
}
