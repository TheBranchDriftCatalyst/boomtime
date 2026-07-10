// Package stats ports hakatime's post-processing of DB rows into API payloads
// (Stats.hs toStatsPayload/fillMissing/aggregateBy, Utils.hs countDuration/compoundDuration,
// Projects.hs project shaping, Leaderboards.hs list building).
package stats

import (
	"fmt"
	"sort"
	"time"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/model"
)

// ---- Utils.hs: countDuration / compoundDuration ----

// rollingGroupBy groups consecutive elements while the predicate holds against
// the LAST element of the current group (Utils.rollingGroupBy).
func rollingGroupBy(points []int, pred func(x, y int) bool) [][]int {
	var groups [][]int
	var curr []int
	for _, m := range points {
		if len(curr) == 0 {
			curr = []int{m}
			continue
		}
		if pred(m, curr[len(curr)-1]) {
			curr = append(curr, m)
		} else {
			groups = append(groups, curr)
			curr = []int{m}
		}
	}
	if curr != nil {
		groups = append(groups, curr)
	}
	return groups
}

// CountDuration sums, over groups formed by an interval cut-off, the span
// (last-first) of each group (Utils.countDuration). Fixture:
// CountDuration([1,2,3,10,21,22,33,100,104,109], 5) == 12.
func CountDuration(points []int, interval int) int {
	groups := rollingGroupBy(points, func(x, y int) bool {
		d := y - x
		if d < 0 {
			d = -d
		}
		return d <= interval
	})
	total := 0
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		d := g[0] - g[len(g)-1]
		if d < 0 {
			d = -d
		}
		total += d
	}
	return total
}

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

// ---- StatRow shaping (Stats.hs) ----

type calcStat struct {
	seconds  int64
	pct      float64
	dailyPct float64
}

// groupByDay splits an ordered slice of StatRow into contiguous per-day groups.
func groupStatRowsByDay(xs []db.StatRow) [][]db.StatRow {
	var groups [][]db.StatRow
	var curr []db.StatRow
	for _, x := range xs {
		if len(curr) == 0 || sameDay(curr[0].Day, x.Day) {
			curr = append(curr, x)
		} else {
			groups = append(groups, curr)
			curr = []db.StatRow{x}
		}
	}
	if curr != nil {
		groups = append(groups, curr)
	}
	return groups
}

// fillMissingStat aligns day-groups against the generated date list, inserting
// empty groups for missing days (Stats.fillMissing).
func fillMissingStat(times []time.Time, rows [][]db.StatRow) [][]db.StatRow {
	var res [][]db.StatRow
	ti, ri := 0, 0
	for ti < len(times) && ri < len(rows) {
		if sameDay(times[ti], rows[ri][0].Day) {
			res = append(res, rows[ri])
			ti++
			ri++
		} else {
			res = append(res, []db.StatRow{})
			ti++
		}
	}
	return res
}

// aggregateStatBy sums (seconds,pct,dailyPct) per field value for one day's rows.
func aggregateStatBy(field func(db.StatRow) string, rows []db.StatRow) map[string]calcStat {
	m := map[string]calcStat{}
	for _, r := range rows {
		k := field(r)
		c := m[k]
		c.seconds += r.TotalSeconds
		c.pct += r.Pct
		c.dailyPct += r.DailyPct
		m[k] = c
	}
	return m
}

// segmentStat builds per-resource ResourceStats from the per-day maps, preserving
// first-seen order of names (List.nub semantics).
func segmentStat(byDate [][]db.StatRow, field func(db.StatRow) string) []model.ResourceStats {
	maps := make([]map[string]calcStat, len(byDate))
	var order []string
	seen := map[string]bool{}
	for i, day := range byDate {
		var m map[string]calcStat
		if len(day) > 0 {
			m = aggregateStatBy(field, day)
		} else {
			m = map[string]calcStat{}
		}
		maps[i] = m
		// nub order: iterate the day's rows in order to preserve appearance.
		for _, r := range day {
			k := field(r)
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}
	out := make([]model.ResourceStats, 0, len(order))
	for _, name := range order {
		secs := make([]int64, len(maps))
		dailyPct := make([]float64, len(maps))
		var totalSec int64
		var totalPct float64
		for i, m := range maps {
			c := m[name] // zero value when missing
			secs[i] = c.seconds
			dailyPct[i] = c.dailyPct
			totalSec += c.seconds
			totalPct += c.pct
		}
		out = append(out, model.ResourceStats{
			Name:         name,
			TotalSeconds: totalSec,
			TotalPct:     totalPct,
			TotalDaily:   secs,
			PctDaily:     dailyPct,
		})
	}
	return out
}

// ToStatsPayload builds the StatsPayload for GET /stats (Stats.toStatsPayload).
func ToStatsPayload(t0, t1 time.Time, xs []db.StatRow) model.StatsPayload {
	// Clamp the start to the earliest day that actually has data, so wide/"All
	// time" ranges don't produce a huge empty leading span in the charts.
	if len(xs) > 0 {
		minDay := xs[0].Day
		for _, x := range xs {
			if x.Day.Before(minDay) {
				minDay = x.Day
			}
		}
		if minDay.After(t0) {
			t0 = minDay
		}
	}
	byDate := fillMissingStat(genDates(t0, t1), groupStatRowsByDay(xs))

	var allSecs int64
	for _, x := range xs {
		allSecs += x.TotalSeconds
	}
	numDays := len(byDate)
	dailyAvg := 0.0
	if numDays > 0 {
		dailyAvg = float64(allSecs) / float64(numDays)
	}
	dailyTotal := make([]int64, len(byDate))
	for i, day := range byDate {
		var s int64
		for _, r := range day {
			s += r.TotalSeconds
		}
		dailyTotal[i] = s
	}

	projects := segmentStat(byDate, func(r db.StatRow) string { return r.Project })
	editors := segmentStat(byDate, func(r db.StatRow) string { return r.Editor })
	languages := segmentStat(byDate, func(r db.StatRow) string { return r.Language })
	platforms := segmentStat(byDate, func(r db.StatRow) string { return r.Platform })
	machines := segmentStat(byDate, func(r db.StatRow) string { return r.Machine })

	return model.StatsPayload{
		StartDate:      t0,
		EndDate:        t1,
		TotalSeconds:   allSecs,
		DailyAvg:       dailyAvg,
		DailyTotal:     dailyTotal,
		ProjectsCount:  len(projects),
		LanguagesCount: len(languages),
		PlatformsCount: len(platforms),
		MachinesCount:  len(machines),
		EditorsCount:   len(editors),
		Projects:       capWithOther(projects),
		Editors:        capWithOther(editors),
		Languages:      capWithOther(languages),
		Platforms:      capWithOther(platforms),
		Machines:       capWithOther(machines),
	}
}

// resourceTopN is how many resources (by total time) each dimension keeps before
// collapsing the remainder into one aggregated "Other (N more)" bucket. Keeps the
// payload small and every chart's series count bounded on wide/all-time ranges,
// while the aggregate row still carries real (summed) day-by-day data.
const resourceTopN = 12

// capWithOther returns the top-N resources by total time plus, if there are more,
// a single "Other (N more)" entry whose TotalSeconds and per-day arrays are the
// element-wise sums of the remaining tail.
func capWithOther(list []model.ResourceStats) []model.ResourceStats {
	if len(list) <= resourceTopN {
		return list
	}
	sort.SliceStable(list, func(a, b int) bool { return list[a].TotalSeconds > list[b].TotalSeconds })
	top := list[:resourceTopN]
	tail := list[resourceTopN:]

	other := model.ResourceStats{Name: fmt.Sprintf("Other (%d more)", len(tail))}
	// Derive the array length from a top entry (all share the day count).
	n := 0
	if len(top) > 0 {
		n = len(top[0].TotalDaily)
	}
	other.TotalDaily = make([]int64, n)
	other.PctDaily = make([]float64, n)
	for _, r := range tail {
		other.TotalSeconds += r.TotalSeconds
		other.TotalPct += r.TotalPct
		for i := 0; i < n && i < len(r.TotalDaily); i++ {
			other.TotalDaily[i] += r.TotalDaily[i]
			other.PctDaily[i] += r.PctDaily[i]
		}
	}
	return append(top, other)
}

// ---- ProjectStatRow shaping (Projects.hs) ----

func groupProjRowsByDay(xs []db.ProjectStatRow) [][]db.ProjectStatRow {
	var groups [][]db.ProjectStatRow
	var curr []db.ProjectStatRow
	for _, x := range xs {
		if len(curr) == 0 || sameDay(curr[0].Day, x.Day) {
			curr = append(curr, x)
		} else {
			groups = append(groups, curr)
			curr = []db.ProjectStatRow{x}
		}
	}
	if curr != nil {
		groups = append(groups, curr)
	}
	return groups
}

func fillMissingProj(times []time.Time, rows [][]db.ProjectStatRow) [][]db.ProjectStatRow {
	var res [][]db.ProjectStatRow
	ti, ri := 0, 0
	for ti < len(times) && ri < len(rows) {
		if sameDay(times[ti], rows[ri][0].Day) {
			res = append(res, rows[ri])
			ti++
			ri++
		} else {
			res = append(res, []db.ProjectStatRow{})
			ti++
		}
	}
	return res
}

func segmentProj(byDate [][]db.ProjectStatRow, field func(db.ProjectStatRow) string) []model.ResourceStats {
	maps := make([]map[string]calcStat, len(byDate))
	var order []string
	seen := map[string]bool{}
	for i, day := range byDate {
		m := map[string]calcStat{}
		for _, r := range day {
			k := field(r)
			c := m[k]
			c.seconds += r.TotalSeconds
			c.pct += r.Pct
			c.dailyPct += r.DailyPct
			m[k] = c
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
		maps[i] = m
	}
	out := make([]model.ResourceStats, 0, len(order))
	for _, name := range order {
		secs := make([]int64, len(maps))
		dailyPct := make([]float64, len(maps))
		var totalSec int64
		var totalPct float64
		for i, m := range maps {
			c := m[name]
			secs[i] = c.seconds
			dailyPct[i] = c.dailyPct
			totalSec += c.seconds
			totalPct += c.pct
		}
		out = append(out, model.ResourceStats{
			Name:         name,
			TotalSeconds: totalSec,
			TotalPct:     totalPct,
			TotalDaily:   secs,
			PctDaily:     dailyPct,
		})
	}
	return out
}

// ToProjectStatistics builds ProjectStatistics for project/tag stats (Projects.toStatsPayload).
func ToProjectStatistics(t0, t1 time.Time, xs []db.ProjectStatRow) model.ProjectStatistics {
	// Clamp the start to the earliest day with data (avoids a huge empty leading
	// span on "All time").
	if len(xs) > 0 {
		minDay := xs[0].Day
		for _, x := range xs {
			if x.Day.Before(minDay) {
				minDay = x.Day
			}
		}
		if minDay.After(t0) {
			t0 = minDay
		}
	}
	byDate := fillMissingProj(genDates(t0, t1), groupProjRowsByDay(xs))
	var allSecs int64
	for _, x := range xs {
		allSecs += x.TotalSeconds
	}
	dailyTotal := make([]int64, len(byDate))
	for i, day := range byDate {
		var s int64
		for _, r := range day {
			s += r.TotalSeconds
		}
		dailyTotal[i] = s
	}
	languages := segmentProj(byDate, func(r db.ProjectStatRow) string { return r.Language })
	files := segmentProj(byDate, func(r db.ProjectStatRow) string { return r.Entity })
	return model.ProjectStatistics{
		StartDate:      t0,
		EndDate:        t1,
		TotalSeconds:   allSecs,
		DailyTotal:     dailyTotal,
		LanguagesCount: len(languages),
		FilesCount:     len(files),
		Languages:      capWithOther(languages),
		Files:          capWithOther(files),
		// WeekDay (7) and Hour (24) are already bounded.
		WeekDay: segmentProj(byDate, func(r db.ProjectStatRow) string { return r.Weekday }),
		Hour:    segmentProj(byDate, func(r db.ProjectStatRow) string { return r.Hour }),
	}
}

// ---- Timeline (Stats.hs) ----

// ToTimelinePayload builds the timeline map, dropping spans shorter than 60s
// (Stats.timelineStatsHandler.go).
func ToTimelinePayload(rows []db.TimelineRow) model.TimelinePayload {
	langs := map[string][]model.TimelineItem{}
	for _, r := range rows {
		if r.RangeEnd.Sub(r.RangeStart).Seconds() < 60 {
			continue
		}
		langs[r.Lang] = append(langs[r.Lang], model.TimelineItem{
			Name:       r.Project,
			RangeStart: r.RangeStart,
			RangeEnd:   r.RangeEnd,
		})
	}
	return model.TimelinePayload{TimelineLangs: langs}
}

// ---- Leaderboards (Leaderboards.hs) ----

// ToLeaderboardsPayload builds the global + per-language top-20 lists (>60s filter).
func ToLeaderboardsPayload(rows []db.LeaderboardRow) model.LeaderboardsPayload {
	// group by user (global)
	global := mkGlobalList(groupBySender(rows))

	// group by language, then by user within each language.
	byLang := map[string][]db.LeaderboardRow{}
	var langOrder []string
	for _, r := range rows {
		if _, ok := byLang[r.Language]; !ok {
			langOrder = append(langOrder, r.Language)
		}
		byLang[r.Language] = append(byLang[r.Language], r)
	}
	langs := map[string][]model.UserTime{}
	for _, lang := range langOrder {
		list := mkGlobalList(groupBySender(byLang[lang]))
		if len(list) > 0 {
			langs[lang] = list
		}
	}
	return model.LeaderboardsPayload{Global: global, Lang: langs}
}

func groupBySender(rows []db.LeaderboardRow) map[string]int64 {
	m := map[string]int64{}
	for _, r := range rows {
		m[r.Sender] += r.TotalSeconds
	}
	return m
}

func mkGlobalList(sums map[string]int64) []model.UserTime {
	var list []model.UserTime
	for name, v := range sums {
		if v > 60 {
			list = append(list, model.UserTime{Name: name, Value: v})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Value != list[j].Value {
			return list[i].Value > list[j].Value
		}
		return list[i].Name < list[j].Name
	})
	if len(list) > 20 {
		list = list[:20]
	}
	return list
}
