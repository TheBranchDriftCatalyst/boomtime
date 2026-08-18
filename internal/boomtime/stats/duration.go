package stats

import (
	"fmt"
)

// ---- Utils.hs: compoundDuration ----

var durLabels = []struct {
	div   int64
	label string
}{
	{0, "wk"}, {7, "day"}, {24, "hrs"}, {60, "min"}, {60, "sec"},
}

// computeDurations mirrors Utils.computeDurations: reduce t by the divisors
// (tail of durLabels) via successive quotRem, then pair non-zero counts with labels.
func computeDurations(t int64) []struct {
	n     int64
	label string
} {
	// divisors = tail of durLabels' divs => [7,24,60,60]
	divs := []int64{7, 24, 60, 60}
	// reduceBy: mapAccumR quotRem t divs, prepending the final quotient.
	// Process from right to left accumulating remainders.
	rem := make([]int64, len(divs))
	acc := t
	for i := len(divs) - 1; i >= 0; i-- {
		q := acc / divs[i]
		r := acc % divs[i]
		rem[i] = r
		acc = q
	}
	ds := append([]int64{acc}, rem...) // [weeks, days, hrs, min, sec]

	var out []struct {
		n     int64
		label string
	}
	for i, n := range ds {
		if n != 0 {
			out = append(out, struct {
				n     int64
				label string
			}{n, durLabels[i].label})
		}
	}
	return out
}

// CompoundDuration formats a seconds count like "2 hrs 15 min", dropping the
// smallest ("sec") unit via init (Utils.compoundDuration). nil -> "no data".
func CompoundDuration(v *int64) string {
	if v == nil {
		return "no data"
	}
	durations := computeDurations(*v)
	if len(durations) == 0 {
		return "no data"
	}
	// unwords . init : drop the last element.
	parts := durations[:len(durations)-1]
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%d %s", p.n, p.label)
	}
	if s == "" {
		// init of a single-element list is empty -> "no data" is not produced here;
		// hakatime returns "" unwords of [] which is "". Keep parity.
		return ""
	}
	return s
}
