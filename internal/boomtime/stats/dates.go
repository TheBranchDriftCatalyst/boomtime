package stats

import (
	"time"
)

// ---- Date helpers ----

func truncateDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// genDates returns midnight-UTC days from t0..t1 inclusive (Stats.genDates).
func genDates(t0, t1 time.Time) []time.Time {
	start := truncateDay(t0)
	end := truncateDay(t1)
	var out []time.Time
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

// dayKey normalizes a timestamp to a UTC calendar-day key.
func dayKey(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}
