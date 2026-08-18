// reading_time_test.go — table tests for composeSessions, the pure core of the
// forward Kindle reading-time composition. This is where the value lives: the
// gap-sum session model (heartbeat semantics applied to reading position) is
// exhaustively pinned here without a DB or network.
package kindle

import (
	"testing"
	"time"
)

// day builds a UTC timestamp for 2026-08-10 at h:m.
func ts(h, m int) time.Time {
	return time.Date(2026, 8, 10, h, m, 0, 0, time.UTC)
}

// dayUTC is the UTC midnight of 2026-08-DD.
func dayUTC(dd int) time.Time {
	return time.Date(2026, 8, dd, 0, 0, 0, 0, time.UTC)
}

// totalSeconds sums a composition result.
func totalSeconds(out []DailyReadingSeconds) int64 {
	var s int64
	for _, d := range out {
		s += d.Seconds
	}
	return s
}

// secondsForDay returns the composed seconds for a given UTC day (0 if absent).
func secondsForDay(out []DailyReadingSeconds, day time.Time) int64 {
	for _, d := range out {
		if d.Day.Equal(day) {
			return d.Seconds
		}
	}
	return 0
}

func TestComposeSessions_SingleSession(t *testing.T) {
	// Three samples 5 min apart, position advancing each time → one continuous
	// session of 10 minutes (two 5-min intra-session intervals).
	samples := []PositionSample{
		{Position: 100, SampledAt: ts(9, 0)},
		{Position: 250, SampledAt: ts(9, 5)},
		{Position: 400, SampledAt: ts(9, 10)},
	}
	out := composeSessions(samples, KindleSessionGap)
	if got := totalSeconds(out); got != 600 {
		t.Fatalf("total = %d, want 600 (10 min continuous)", got)
	}
	if len(out) != 1 || !out[0].Day.Equal(dayUTC(10)) {
		t.Fatalf("expected one bucket on 2026-08-10, got %+v", out)
	}
}

func TestComposeSessions_MultiSessionSplitOnBigGap(t *testing.T) {
	// Two 5-min reading intervals separated by a 2-hour gap (> KindleSessionGap):
	// the idle gap is NOT counted, only the two 5-min intervals (600s total).
	samples := []PositionSample{
		{Position: 100, SampledAt: ts(9, 0)},
		{Position: 250, SampledAt: ts(9, 5)}, // +5m reading
		// 2-hour gap here — session break, not counted
		{Position: 260, SampledAt: ts(11, 5)},
		{Position: 500, SampledAt: ts(11, 10)}, // +5m reading
	}
	out := composeSessions(samples, KindleSessionGap)
	if got := totalSeconds(out); got != 600 {
		t.Fatalf("total = %d, want 600 (two 5-min sessions; 2h gap excluded)", got)
	}
}

func TestComposeSessions_IdleNoAdvanceExcluded(t *testing.T) {
	// Position static across samples within the gap = idle (book open, reader
	// away). No advance → no reading time, even though the samples are close.
	samples := []PositionSample{
		{Position: 100, SampledAt: ts(9, 0)},
		{Position: 100, SampledAt: ts(9, 5)},  // no advance → idle
		{Position: 100, SampledAt: ts(9, 10)}, // still no advance → idle
	}
	out := composeSessions(samples, KindleSessionGap)
	if got := totalSeconds(out); got != 0 {
		t.Fatalf("total = %d, want 0 (static position is idle, not reading)", got)
	}
}

func TestComposeSessions_MixedIdleAndReading(t *testing.T) {
	// Advance (count) → static (idle, skip) → advance (count). Only the two
	// advancing intervals count.
	samples := []PositionSample{
		{Position: 100, SampledAt: ts(9, 0)},
		{Position: 200, SampledAt: ts(9, 5)},  // +5m reading (advance)
		{Position: 200, SampledAt: ts(9, 10)}, // idle (no advance) — skipped
		{Position: 300, SampledAt: ts(9, 15)}, // +5m reading (advance)
	}
	out := composeSessions(samples, KindleSessionGap)
	if got := totalSeconds(out); got != 600 {
		t.Fatalf("total = %d, want 600 (2 advancing 5-min intervals; idle middle skipped)", got)
	}
}

func TestComposeSessions_DayBoundarySplit(t *testing.T) {
	// A reading interval that straddles UTC midnight is split so each day's
	// bucket is exact. 23:55 → 00:05 next day (10 min, within the gap), position
	// advancing = 10 min total, 5 min on each day.
	samples := []PositionSample{
		{Position: 100, SampledAt: time.Date(2026, 8, 10, 23, 55, 0, 0, time.UTC)},
		{Position: 400, SampledAt: time.Date(2026, 8, 11, 0, 5, 0, 0, time.UTC)},
	}
	out := composeSessions(samples, KindleSessionGap)
	if got := totalSeconds(out); got != 600 {
		t.Fatalf("total = %d, want 600 (10 min across midnight)", got)
	}
	if got := secondsForDay(out, dayUTC(10)); got != 300 {
		t.Errorf("2026-08-10 = %d, want 300", got)
	}
	if got := secondsForDay(out, dayUTC(11)); got != 300 {
		t.Errorf("2026-08-11 = %d, want 300", got)
	}
}

func TestComposeSessions_GapExactlyThresholdCounts(t *testing.T) {
	// A gap of EXACTLY KindleSessionGap is within-session (<=), so it counts.
	samples := []PositionSample{
		{Position: 100, SampledAt: ts(9, 0)},
		{Position: 200, SampledAt: ts(9, 0).Add(KindleSessionGap)},
	}
	out := composeSessions(samples, KindleSessionGap)
	if got := totalSeconds(out); got != int64(KindleSessionGap.Seconds()) {
		t.Fatalf("total = %d, want %d (gap == threshold counts)", got, int64(KindleSessionGap.Seconds()))
	}
	// One second over the threshold must NOT count.
	samples[1].SampledAt = ts(9, 0).Add(KindleSessionGap + time.Second)
	if got := totalSeconds(composeSessions(samples, KindleSessionGap)); got != 0 {
		t.Fatalf("total = %d, want 0 (gap > threshold is a break)", got)
	}
}

func TestComposeSessions_UnorderedInputSortedDefensively(t *testing.T) {
	// Samples arrive out of order; the composition sorts them and produces the
	// same result as the ordered case (one 10-min session).
	samples := []PositionSample{
		{Position: 400, SampledAt: ts(9, 10)},
		{Position: 100, SampledAt: ts(9, 0)},
		{Position: 250, SampledAt: ts(9, 5)},
	}
	out := composeSessions(samples, KindleSessionGap)
	if got := totalSeconds(out); got != 600 {
		t.Fatalf("total = %d, want 600 (unordered input sorted)", got)
	}
}

func TestComposeSessions_Degenerate(t *testing.T) {
	if out := composeSessions(nil, KindleSessionGap); out != nil {
		t.Errorf("nil samples → %+v, want nil", out)
	}
	if out := composeSessions([]PositionSample{{Position: 1, SampledAt: ts(9, 0)}}, KindleSessionGap); out != nil {
		t.Errorf("single sample → %+v, want nil (no interval)", out)
	}
	// Duplicate timestamps (delta 0) contribute nothing.
	dup := []PositionSample{
		{Position: 100, SampledAt: ts(9, 0)},
		{Position: 200, SampledAt: ts(9, 0)},
	}
	if got := totalSeconds(composeSessions(dup, KindleSessionGap)); got != 0 {
		t.Errorf("duplicate-timestamp total = %d, want 0", got)
	}
}
