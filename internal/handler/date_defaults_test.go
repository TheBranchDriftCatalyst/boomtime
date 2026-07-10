package handler

import (
	"math"
	"testing"
	"time"
)

func TestParseTimeParam(t *testing.T) {
	t.Run("RFC3339", func(t *testing.T) {
		c := ctxWithQuery("start=2026-03-15T08:30:00%2B02:00")
		got, ok := parseTimeParam(c, "start")
		if !ok {
			t.Fatal("expected ok=true for RFC3339")
		}
		if got.Location() != time.UTC {
			t.Errorf("location = %v, want UTC", got.Location())
		}
		// 08:30 +02:00 == 06:30 UTC.
		want := time.Date(2026, 3, 15, 6, 30, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("date-only", func(t *testing.T) {
		c := ctxWithQuery("start=2026-03-15")
		got, ok := parseTimeParam(c, "start")
		if !ok {
			t.Fatal("expected ok=true for date-only")
		}
		want := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
		if !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("got %v (%v), want %v UTC", got, got.Location(), want)
		}
	})

	t.Run("empty -> zero,false", func(t *testing.T) {
		c := ctxWithQuery("")
		got, ok := parseTimeParam(c, "start")
		if ok {
			t.Error("expected ok=false for absent param")
		}
		if !got.IsZero() {
			t.Errorf("got %v, want zero time", got)
		}
	})
}

// spanDays returns the number of days between t0 and t1.
func spanDays(t0, t1 time.Time) float64 {
	return t1.Sub(t0).Hours() / 24
}

func TestDefaultWeekRange(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("no start, no end -> [now-7d, now]", func(t *testing.T) {
		c := ctxWithQuery("")
		t0, t1 := defaultWeekRange(c)
		// end is now (not truncated); start is midnight-of-now minus 7d, so span
		// is in [7, 8) days. Assert the whole-day floor is 7.
		if d := spanDays(t0, t1); math.Floor(d) != 7 {
			t.Errorf("span = %.3f days, want floor 7", d)
		}
	})

	t.Run("no start, end -> [end-7d, end]", func(t *testing.T) {
		c := ctxWithQuery("end=2026-06-01")
		t0, t1 := defaultWeekRange(c)
		if !t1.Equal(end) {
			t.Errorf("end = %v, want %v", t1, end)
		}
		if d := spanDays(t0, t1); d != 7 {
			t.Errorf("span = %.3f, want exactly 7", d)
		}
	})

	t.Run("start, no end -> [start, start+7d]", func(t *testing.T) {
		c := ctxWithQuery("start=2026-03-01")
		t0, t1 := defaultWeekRange(c)
		if !t0.Equal(start) {
			t.Errorf("start = %v, want %v", t0, start)
		}
		if d := spanDays(t0, t1); d != 7 {
			t.Errorf("span = %.3f, want exactly 7", d)
		}
	})

	t.Run("start, end -> honored as-is", func(t *testing.T) {
		c := ctxWithQuery("start=2026-03-01&end=2026-06-01")
		t0, t1 := defaultWeekRange(c)
		if !t0.Equal(start) || !t1.Equal(end) {
			t.Errorf("range = [%v, %v], want [%v, %v]", t0, t1, start, end)
		}
	})
}

func TestDefaultMonthRange(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("no start, no end -> [now-30d, now]", func(t *testing.T) {
		c := ctxWithQuery("")
		t0, t1 := defaultMonthRange(c)
		if d := spanDays(t0, t1); math.Floor(d) != 30 {
			t.Errorf("span = %.3f days, want floor 30", d)
		}
	})

	t.Run("no start, end -> [end-30d, end]", func(t *testing.T) {
		c := ctxWithQuery("end=2026-06-01")
		t0, t1 := defaultMonthRange(c)
		if !t1.Equal(end) {
			t.Errorf("end = %v, want %v", t1, end)
		}
		if d := spanDays(t0, t1); d != 30 {
			t.Errorf("span = %.3f, want exactly 30", d)
		}
	})

	t.Run("start, no end -> [start, start+30d]", func(t *testing.T) {
		c := ctxWithQuery("start=2026-03-01")
		t0, t1 := defaultMonthRange(c)
		if !t0.Equal(start) {
			t.Errorf("start = %v, want %v", t0, start)
		}
		if d := spanDays(t0, t1); d != 30 {
			t.Errorf("span = %.3f, want exactly 30", d)
		}
	})

	t.Run("start, end -> honored as-is", func(t *testing.T) {
		c := ctxWithQuery("start=2026-03-01&end=2026-06-01")
		t0, t1 := defaultMonthRange(c)
		if !t0.Equal(start) || !t1.Equal(end) {
			t.Errorf("range = [%v, %v], want [%v, %v]", t0, t1, start, end)
		}
	})
}

func TestQueryInt64(t *testing.T) {
	t.Run("absent -> default", func(t *testing.T) {
		c := ctxWithQuery("")
		if got := queryInt64(c, "n", 42); got != 42 {
			t.Errorf("got %d, want default 42", got)
		}
	})
	t.Run("valid -> parsed", func(t *testing.T) {
		c := ctxWithQuery("n=123")
		if got := queryInt64(c, "n", 42); got != 123 {
			t.Errorf("got %d, want 123", got)
		}
	})
	t.Run("invalid -> default", func(t *testing.T) {
		c := ctxWithQuery("n=abc")
		if got := queryInt64(c, "n", 42); got != 42 {
			t.Errorf("got %d, want default 42 on invalid", got)
		}
	})
}

func TestTimeLimitDefault(t *testing.T) {
	c := ctxWithQuery("")
	if got := timeLimit(c); got != 15 {
		t.Errorf("timeLimit default = %d, want 15", got)
	}
	c2 := ctxWithQuery("timeLimit=30")
	if got := timeLimit(c2); got != 30 {
		t.Errorf("timeLimit = %d, want 30", got)
	}
}
