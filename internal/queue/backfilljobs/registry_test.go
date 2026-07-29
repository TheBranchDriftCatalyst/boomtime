// registry_ginkgo_test.go — ginkgo mirror of registry_test.go (gaka-0vp).
// 1:1 case map (5 stdlib TestXxx):
//
//	TestEnqueue_ReturnsFreshJob                → Registry > "Enqueue returns a fresh Queued job"
//	TestUpdate_AutoStartsAndFinishes           → Registry > "Update auto-stamps StartedAt/FinishedAt + retention drops row"
//	TestIncrementCounts_FlipsQueuedToRunning   → Registry > "IncrementCounts flips Queued→Running + accumulates"
//	TestSnapshotFor_OwnerFilter                → Registry > "SnapshotFor filters by owner"
//	TestSubscribe_ReceivesAddedEvents          → Registry > "Subscribe receives Added events"
package backfilljobs

import (
	"io"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newRegistryGinkgo builds a fresh Registry with tiny tick/retention
// intervals so time-sensitive Its finish under a second.
func newRegistryGinkgo() *Registry {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRegistryWith(logger, 50*time.Millisecond, 50*time.Millisecond)
}

var _ = Describe("Registry", func() {
	It("Enqueue returns a fresh Queued job discoverable via Get", func() {
		r := newRegistryGinkgo()
		j := r.Enqueue(EnqueueInput{
			Owner:    "panda",
			RepoName: "boomtime",
			RepoPath: "/tmp/x",
			Total:    10,
		})
		Expect(j.ID).NotTo(BeEmpty())
		Expect(j.Status).To(Equal(StatusQueued))

		got, ok := r.Get(j.ID)
		Expect(ok).To(BeTrue())
		Expect(got.Owner).To(Equal("panda"))
	})

	It("Update stamps StartedAt/FinishedAt; retention drops the row", func() {
		r := newRegistryGinkgo()
		j := r.Enqueue(EnqueueInput{Owner: "p", RepoName: "r"})
		r.Update(j.ID, UpdatePatch{Status: StatusRunning})

		got, _ := r.Get(j.ID)
		Expect(got.StartedAt).NotTo(BeNil())

		r.Update(j.ID, UpdatePatch{Status: StatusDone})
		got, _ = r.Get(j.ID)
		Expect(got.FinishedAt).NotTo(BeNil())

		// Retention should drop within ~200ms (50ms window × several ticks).
		Eventually(func() bool {
			_, ok := r.Get(j.ID)
			return ok
		}, 500*time.Millisecond, 20*time.Millisecond).Should(BeFalse())
	})

	It("IncrementCounts flips Queued→Running and accumulates the counters", func() {
		r := newRegistryGinkgo()
		j := r.Enqueue(EnqueueInput{Owner: "p", RepoName: "r"})
		got, ok := r.IncrementCounts(j.ID, 3, 100, 5)
		Expect(ok).To(BeTrue())
		Expect(got.Status).To(Equal(StatusRunning))
		Expect(got.Processed).To(BeEquivalentTo(3))
		Expect(got.Written).To(BeEquivalentTo(100))
		Expect(got.Skipped).To(BeEquivalentTo(5))
	})

	It("SnapshotFor filters by owner", func() {
		r := newRegistryGinkgo()
		r.Enqueue(EnqueueInput{Owner: "alice", RepoName: "r1"})
		r.Enqueue(EnqueueInput{Owner: "bob", RepoName: "r2"})
		r.Enqueue(EnqueueInput{Owner: "alice", RepoName: "r3"})

		got := r.SnapshotFor("alice")
		Expect(got).To(HaveLen(2))
		for _, j := range got {
			Expect(j.Owner).To(Equal("alice"))
		}
	})

	It("Subscribe receives Added events for new Enqueues", func() {
		r := newRegistryGinkgo()
		sub, unsub := r.Subscribe()
		defer unsub()

		go r.Enqueue(EnqueueInput{Owner: "p", RepoName: "r"})

		Eventually(sub, 500*time.Millisecond, 10*time.Millisecond).Should(Receive(WithTransform(
			func(ev Event) EventKind { return ev.Kind }, Equal(EventAdded),
		)))
	})
})
