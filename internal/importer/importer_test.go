// importer_ginkgo_test.go — ginkgo mirror of importer_test.go (gaka-0vp).
// 1:1 case map (4 stdlib TestXxx):
//   TestDayRangeInclusivePlusOne                   → DayRange / TotalDays > "same-day range yields 2 entries"
//   TestDayRangeMultiDay                           → DayRange / TotalDays > "3-day span → 4 entries"
//   TestCancelReturnsPreClosedChannelForUnknownJob → Worker.Cancel > "unknown job returns pre-closed done channel"
//   TestCancelDoneChannelClosesAfterWorkerExit     → Worker.Cancel > "registered job's done channel closes after worker exit"
package importer

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DayRange / TotalDays", func() {
	It("same-day range yields 2 entries (the day itself and the day after) — matches hakatime", func() {
		t0 := time.Date(2025, 4, 1, 12, 0, 0, 0, time.UTC)
		t1 := time.Date(2025, 4, 1, 23, 0, 0, 0, time.UTC)
		got := DayRange(t0, t1)
		Expect(got).To(Equal([]string{"2025-04-01", "2025-04-02"}))
		Expect(TotalDays(t0, t1)).To(Equal(2))
	})

	It("3-day span → 4 entries (inclusive + 1)", func() {
		t0 := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
		t1 := time.Date(2025, 4, 3, 0, 0, 0, 0, time.UTC)
		Expect(TotalDays(t0, t1)).To(Equal(4))
		days := DayRange(t0, t1)
		Expect(days[0]).To(Equal("2025-04-01"))
		Expect(days[len(days)-1]).To(Equal("2025-04-04"))
	})
})

var _ = Describe("Worker.Cancel", func() {
	It("unknown job → running=false and pre-closed done channel", func() {
		w := NewWorker(context.Background(), nil, nil, nil)
		done, running := w.Cancel(42)
		Expect(running).To(BeFalse())
		select {
		case <-done:
			// Expected: pre-closed so callers can `<-done` uniformly.
		case <-time.After(50 * time.Millisecond):
			Fail("done channel should be pre-closed for a not-running job")
		}
	})

	It("registered job → running=true; done stays open until we close it explicitly", func() {
		// Register a runningJob by hand so we don't need the full DB harness — the
		// contract under test is StartJob's post-return defers (cancel, close(done))
		// and Cancel's plumbing, both of which live in importer.go's plain sync
		// bookkeeping.
		w := NewWorker(context.Background(), nil, nil, nil)
		rj := &runningJob{done: make(chan struct{})}
		rj.cancel = func() {}
		w.mu.Lock()
		w.running[7] = rj
		w.mu.Unlock()

		done, running := w.Cancel(7)
		Expect(running).To(BeTrue())

		select {
		case <-done:
			Fail("done should NOT close until we close it explicitly")
		case <-time.After(10 * time.Millisecond):
		}
		close(rj.done)
		select {
		case <-done:
		case <-time.After(50 * time.Millisecond):
			Fail("done should close as soon as the worker's defer fires")
		}
	})
})
