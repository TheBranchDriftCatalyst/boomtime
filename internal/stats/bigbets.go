package stats

import (
	"sort"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// ToPunchcardPayload maps raw dow/hour cells to the payload, computing the max
// and total. Cells are passed through as-is (already grouped in SQL).
func ToPunchcardPayload(cells []db.PunchcardCell) model.PunchcardPayload {
	out := model.PunchcardPayload{Cells: make([]model.PunchcardCell, 0, len(cells))}
	for _, c := range cells {
		out.Cells = append(out.Cells, model.PunchcardCell{Dow: c.Dow, Hour: c.Hour, Seconds: c.Seconds})
		out.TotalSeconds += c.Seconds
		if c.Seconds > out.MaxSeconds {
			out.MaxSeconds = c.Seconds
		}
	}
	return out
}

// sessionHistBins defines the duration buckets (upper bound in seconds, inclusive
// of the lower / exclusive of the upper; the last is open-ended).
var sessionHistBins = []struct {
	label string
	upper int64 // seconds; 0 == no upper bound (last bin)
}{
	{"0–15m", 15 * 60},
	{"15–30m", 30 * 60},
	{"30–60m", 60 * 60},
	{"1–2h", 2 * 60 * 60},
	{"2h+", 0},
}

// ToSessionsPayload aggregates sessionized rows into summary + daily (gap-filled
// across the range) + a duration histogram. It never returns individual sessions.
func ToSessionsPayload(t0, t1 time.Time, rows []db.SessionRow) model.SessionsPayload {
	days := genDates(t0, t1)

	// Daily aggregation keyed by calendar day.
	type dayAgg struct {
		sessions int64
		total    int64
		longest  int64
	}
	byDay := map[string]*dayAgg{}
	durations := make([]int64, 0, len(rows))
	var total, maxSecs int64
	for _, r := range rows {
		durations = append(durations, r.Seconds)
		total += r.Seconds
		if r.Seconds > maxSecs {
			maxSecs = r.Seconds
		}
		k := dayKey(r.Day)
		a := byDay[k]
		if a == nil {
			a = &dayAgg{}
			byDay[k] = a
		}
		a.sessions++
		a.total += r.Seconds
		if r.Seconds > a.longest {
			a.longest = r.Seconds
		}
	}

	count := int64(len(rows))
	var avg, median int64
	if count > 0 {
		avg = total / count
		sorted := append([]int64(nil), durations...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		median = medianOf(sorted)
	}

	daily := make([]model.SessionDaily, len(days))
	for i, d := range days {
		sd := model.SessionDaily{Date: dayKey(d)}
		if a, ok := byDay[dayKey(d)]; ok {
			sd.Sessions = a.sessions
			sd.TotalSeconds = a.total
			sd.LongestSeconds = a.longest
		}
		daily[i] = sd
	}

	hist := make([]model.SessionHistBin, len(sessionHistBins))
	for i, b := range sessionHistBins {
		hist[i] = model.SessionHistBin{Label: b.label}
	}
	for _, d := range durations {
		hist[histBinIndex(d)].Count++
	}

	return model.SessionsPayload{
		Summary: model.SessionSummary{
			Count:         count,
			TotalSeconds:  total,
			AvgSeconds:    avg,
			MaxSeconds:    maxSecs,
			MedianSeconds: median,
		},
		Daily:     daily,
		Histogram: hist,
	}
}

// histBinIndex returns the sessionHistBins index for a duration in seconds.
func histBinIndex(sec int64) int {
	for i, b := range sessionHistBins {
		if b.upper == 0 || sec < b.upper {
			return i
		}
	}
	return len(sessionHistBins) - 1
}

// medianOf returns the median of an already-sorted non-empty slice.
func medianOf(sorted []int64) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// ToMomentumPayload builds the top-N project weekly series, gap-filling the week
// index across the range so every project's Weekly[] aligns to Weeks[].
func ToMomentumPayload(t0, t1 time.Time, rows []db.MomentumRow, top int) model.MomentumPayload {
	if top <= 0 {
		top = 8
	}

	// Build the full ascending week-start index across the range (ISO Mondays).
	weeks := weekStarts(t0, t1)
	weekIndex := make(map[string]int, len(weeks))
	weekKeys := make([]string, len(weeks))
	for i, w := range weeks {
		k := dayKey(w)
		weekKeys[i] = k
		weekIndex[k] = i
	}

	// Aggregate per project: total + weekly series aligned to weeks.
	type proj struct {
		total  int64
		weekly []int64
	}
	byProject := map[string]*proj{}
	var order []string
	for _, r := range rows {
		wi, ok := weekIndex[dayKey(r.WeekStart)]
		if !ok {
			continue // outside the computed week index
		}
		p := byProject[r.Project]
		if p == nil {
			p = &proj{weekly: make([]int64, len(weeks))}
			byProject[r.Project] = p
			order = append(order, r.Project)
		}
		p.total += r.Seconds
		p.weekly[wi] += r.Seconds
	}

	// Rank by total desc (stable by name for ties) and keep the top-N.
	sort.SliceStable(order, func(a, b int) bool {
		if byProject[order[a]].total != byProject[order[b]].total {
			return byProject[order[a]].total > byProject[order[b]].total
		}
		return order[a] < order[b]
	})
	if len(order) > top {
		order = order[:top]
	}

	projects := make([]model.MomentumProject, 0, len(order))
	for _, name := range order {
		p := byProject[name]
		projects = append(projects, model.MomentumProject{
			Name:         name,
			Weekly:       p.weekly,
			TotalSeconds: p.total,
		})
	}

	return model.MomentumPayload{Weeks: weekKeys, Projects: projects}
}

// weekStarts returns the ISO Monday week-start dates covering [t0, t1] inclusive,
// ascending. Matches Postgres date_trunc('week', ...) which anchors on Monday.
func weekStarts(t0, t1 time.Time) []time.Time {
	start := isoWeekStart(t0)
	end := isoWeekStart(t1)
	var out []time.Time
	for w := start; !w.After(end); w = w.AddDate(0, 0, 7) {
		out = append(out, w)
	}
	return out
}

// isoWeekStart returns the Monday 00:00 UTC of t's ISO week.
func isoWeekStart(t time.Time) time.Time {
	t = t.UTC()
	// Go: Sunday=0..Saturday=6; ISO week starts Monday. Offset back to Monday.
	offset := (int(t.Weekday()) + 6) % 7
	y, m, d := t.Date()
	monday := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -offset)
	return monday
}

// ToActiveFilesPayload maps the cross-project active-file rows to the payload.
// Rows arrive already ordered lynchpins-first (projects desc, seconds desc) and
// capped by the DB; this shaper only wraps them with the truncation flag and
// guarantees a non-nil Files slice for a stable JSON array.
func ToActiveFilesPayload(rows []db.ActiveFile, truncated bool) model.ActiveFilesPayload {
	files := make([]model.ActiveFile, 0, len(rows))
	for _, r := range rows {
		files = append(files, model.ActiveFile{
			Entity:   r.Entity,
			Seconds:  r.Seconds,
			Projects: r.Projects,
		})
	}
	return model.ActiveFilesPayload{Files: files, Truncated: truncated}
}
