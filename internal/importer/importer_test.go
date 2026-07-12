package importer

import (
	"context"
	"testing"
	"time"
)

func TestDayRangeInclusivePlusOne(t *testing.T) {
	// Utils.genDateRange iterates 0..diffDays+1, so a same-day range yields 2
	// entries (the day itself and the day after), matching hakatime.
	t0 := time.Date(2025, 4, 1, 12, 0, 0, 0, time.UTC)
	t1 := time.Date(2025, 4, 1, 23, 0, 0, 0, time.UTC)
	got := DayRange(t0, t1)
	want := []string{"2025-04-01", "2025-04-02"}
	if len(got) != len(want) {
		t.Fatalf("DayRange len = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DayRange[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if TotalDays(t0, t1) != 2 {
		t.Fatalf("TotalDays = %d, want 2", TotalDays(t0, t1))
	}
}

func TestDayRangeMultiDay(t *testing.T) {
	t0 := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2025, 4, 3, 0, 0, 0, 0, time.UTC)
	// 3-day span (1,2,3) + 1 = 4 entries.
	if got := TotalDays(t0, t1); got != 4 {
		t.Fatalf("TotalDays(3-day span) = %d, want 4", got)
	}
	days := DayRange(t0, t1)
	if days[0] != "2025-04-01" || days[len(days)-1] != "2025-04-04" {
		t.Fatalf("DayRange bounds = %v", days)
	}
}

// TestMapState was removed with MapState + JobPending/Failed/Finished (gaka-al6).

// gaka-al6: Cancel returns a done channel so callers can wait for the worker
// goroutine's terminal DB write instead of racing it with a fixed sleep. When
// the job isn't running in this process the channel is pre-closed — one call
// site, one wait pattern.
func TestCancelReturnsPreClosedChannelForUnknownJob(t *testing.T) {
	w := NewWorker(context.Background(), nil, nil, nil)
	done, running := w.Cancel(42)
	if running {
		t.Fatal("Cancel of unknown job should report running=false")
	}
	select {
	case <-done:
		// Expected: pre-closed so callers can `<-done` uniformly.
	case <-time.After(50 * time.Millisecond):
		t.Fatal("done channel should be pre-closed for a not-running job")
	}
}

func TestCancelDoneChannelClosesAfterWorkerExit(t *testing.T) {
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
	if !running {
		t.Fatal("Cancel of registered job should report running=true")
	}
	select {
	case <-done:
		t.Fatal("done should NOT close until we close it explicitly")
	case <-time.After(10 * time.Millisecond):
	}
	close(rj.done)
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("done should close as soon as the worker's defer fires")
	}
}
