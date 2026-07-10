package importer

import (
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

func TestMapState(t *testing.T) {
	cases := map[string]string{
		"queued":    JobPending,
		"running":   JobPending,
		"completed": JobFinished,
		"failed":    JobFailed,
		"cancelled": JobFailed,
	}
	for state, want := range cases {
		if got := MapState(state); got != want {
			t.Fatalf("MapState(%q) = %q, want %q", state, got, want)
		}
	}
}
