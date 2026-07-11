package stats

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// ---- Row shaping shared by Stats.hs (StatRow) and Projects.hs (ProjectStatRow) ----

// calcStat is one resource's per-day contribution: (seconds, pct, dailyPct).
type calcStat struct {
	seconds  int64
	pct      float64
	dailyPct float64
}

// Accessors bridging the concrete db row types into the generic helpers below.
func statDay(r db.StatRow) time.Time        { return r.Day }
func projDay(r db.ProjectStatRow) time.Time { return r.Day }
func statContrib(r db.StatRow) calcStat     { return calcStat{r.TotalSeconds, r.Pct, r.DailyPct} }
func projContrib(r db.ProjectStatRow) calcStat {
	return calcStat{r.TotalSeconds, r.Pct, r.DailyPct}
}

// clampStartToData returns t0 clamped forward to the earliest row day, so
// wide/"All time" ranges don't produce a huge empty leading span in the charts.
func clampStartToData[T any](t0 time.Time, xs []T, day func(T) time.Time) time.Time {
	if len(xs) == 0 {
		return t0
	}
	minDay := day(xs[0])
	for _, x := range xs {
		if d := day(x); d.Before(minDay) {
			minDay = d
		}
	}
	if minDay.After(t0) {
		return minDay
	}
	return t0
}

// groupByDay splits an ordered slice of rows into contiguous per-day groups.
func groupByDay[T any](xs []T, day func(T) time.Time) [][]T {
	var groups [][]T
	var curr []T
	for _, x := range xs {
		if len(curr) == 0 || sameDay(day(curr[0]), day(x)) {
			curr = append(curr, x)
		} else {
			groups = append(groups, curr)
			curr = []T{x}
		}
	}
	if curr != nil {
		groups = append(groups, curr)
	}
	return groups
}

// fillMissing aligns day-groups against the generated date list, inserting
// empty groups for missing days (Stats.fillMissing). Leading and interior gaps
// are filled; trailing empty days are silently dropped (the loop stops once the
// row groups are exhausted). It returns the aligned groups plus the matching
// prefix of times, so byDate[i] always corresponds to alignedDays[i].
func fillMissing[T any](times []time.Time, rows [][]T, day func(T) time.Time) (byDate [][]T, alignedDays []time.Time) {
	ti, ri := 0, 0
	for ti < len(times) && ri < len(rows) {
		if sameDay(times[ti], day(rows[ri][0])) {
			byDate = append(byDate, rows[ri])
			ti++
			ri++
		} else {
			byDate = append(byDate, []T{})
			ti++
		}
	}
	return byDate, times[:len(byDate)]
}

// dailyTotals sums seconds per day-group, aligned to byDate.
func dailyTotals[T any](byDate [][]T, secs func(T) int64) []int64 {
	out := make([]int64, len(byDate))
	for i, day := range byDate {
		var s int64
		for _, r := range day {
			s += secs(r)
		}
		out[i] = s
	}
	return out
}

// segment builds per-resource ResourceStats from the per-day groups, preserving
// first-seen order of names (List.nub semantics).
func segment[T any](byDate [][]T, field func(T) string, contrib func(T) calcStat) []model.ResourceStats {
	maps := make([]map[string]calcStat, len(byDate))
	var order []string
	seen := map[string]bool{}
	for i, day := range byDate {
		m := map[string]calcStat{}
		for _, r := range day {
			k := field(r)
			c := m[k]
			add := contrib(r)
			c.seconds += add.seconds
			c.pct += add.pct
			c.dailyPct += add.dailyPct
			m[k] = c
			// nub order: keys appear in the order the day's rows introduce them.
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

// segmentStat / segmentProj are thin type-specific entry points over segment.
func segmentStat(byDate [][]db.StatRow, field func(db.StatRow) string) []model.ResourceStats {
	return segment(byDate, field, statContrib)
}

func segmentProj(byDate [][]db.ProjectStatRow, field func(db.ProjectStatRow) string) []model.ResourceStats {
	return segment(byDate, field, projContrib)
}

// segmentProjWhere is segmentProj restricted to rows matching keep. Used for the
// "files" breakdown so only ty='file' entities are aggregated (browsing
// domains/apps are excluded). The resulting count equals the number of distinct
// file entities.
func segmentProjWhere(byDate [][]db.ProjectStatRow, keep func(db.ProjectStatRow) bool, field func(db.ProjectStatRow) string) []model.ResourceStats {
	filtered := make([][]db.ProjectStatRow, len(byDate))
	for i, day := range byDate {
		var kept []db.ProjectStatRow
		for _, r := range day {
			if keep(r) {
				kept = append(kept, r)
			}
		}
		filtered[i] = kept
	}
	return segmentProj(filtered, field)
}

// segmentAligned shapes flat per-(day,name) rows into per-name ResourceStats
// whose daily arrays are aligned to the `days` series (same layout as segment),
// preserving first-seen name order. Rows whose day falls outside the clamped
// series are skipped. Used for the Categories (overview) and Branches (project)
// breakdowns, which are fetched separately from the main row set.
func segmentAligned[T any](days []time.Time, rows []T, day func(T) time.Time, name func(T) string, contrib func(T) calcStat) []model.ResourceStats {
	dayIndex := make(map[string]int, len(days))
	for i, d := range days {
		dayIndex[dayKey(d)] = i
	}
	n := len(days)

	type acc struct {
		total    int64
		totalPct float64
		daily    []int64
		pctDaily []float64
	}
	byName := map[string]*acc{}
	var order []string
	for _, r := range rows {
		di, ok := dayIndex[dayKey(day(r))]
		if !ok {
			continue // day outside the clamped series
		}
		k := name(r)
		a := byName[k]
		if a == nil {
			a = &acc{daily: make([]int64, n), pctDaily: make([]float64, n)}
			byName[k] = a
			order = append(order, k)
		}
		c := contrib(r)
		a.total += c.seconds
		a.totalPct += c.pct
		a.daily[di] += c.seconds
		a.pctDaily[di] += c.dailyPct
	}

	out := make([]model.ResourceStats, 0, len(order))
	for _, k := range order {
		a := byName[k]
		out = append(out, model.ResourceStats{
			Name:         k,
			TotalSeconds: a.total,
			TotalPct:     a.totalPct,
			TotalDaily:   a.daily,
			PctDaily:     a.pctDaily,
		})
	}
	return out
}

// resourceTopN is how many resources (by total time) each dimension keeps before
// collapsing the remainder into one aggregated "Other (N more)" bucket. Keeps the
// payload small and every chart's series count bounded on wide/all-time ranges,
// while the aggregate row still carries real (summed) day-by-day data.
const resourceTopN = 12

// otherMembersCap is the max number of tail members carried on the synthesized
// "Other" entry for FE tooltip breakdown (gaka-7m4). Sized to comfortably
// cover typical long-tail cardinality (a dozen editors, a few dozen languages)
// while keeping the payload bounded.
const otherMembersCap = 20

// capWithOther returns the top-N resources by total time plus, if there are more,
// a single "Other (N more)" entry whose TotalSeconds and per-day arrays are the
// element-wise sums of the remaining tail. On the Other entry we also carry the
// top otherMembersCap tail members (name/totalSeconds/totalPct only) so the FE
// can render a breakdown tooltip. The caller's slice is never mutated: sorting
// happens on a clone, and the returned slice has its own backing array.
func capWithOther(list []model.ResourceStats) []model.ResourceStats {
	if len(list) <= resourceTopN {
		return list
	}
	sorted := slices.Clone(list)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].TotalSeconds > sorted[b].TotalSeconds })
	// Full-slice expression: the append below must allocate rather than write
	// into sorted's (or, before the clone existed, the caller's) backing array.
	top := sorted[:resourceTopN:resourceTopN]
	tail := sorted[resourceTopN:]

	other := model.ResourceStats{
		Name:       fmt.Sprintf("Other (%d more)", len(tail)),
		OtherCount: len(tail),
	}
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
	// Tail is already sorted desc by TotalSeconds (sorted was sorted before the
	// top/tail split). Carry the top otherMembersCap members for the FE tooltip.
	memberCount := len(tail)
	if memberCount > otherMembersCap {
		memberCount = otherMembersCap
	}
	other.OtherMembers = make([]model.OtherMember, memberCount)
	for i := 0; i < memberCount; i++ {
		other.OtherMembers[i] = model.OtherMember{
			Name:         tail[i].Name,
			TotalSeconds: tail[i].TotalSeconds,
			TotalPct:     tail[i].TotalPct,
		}
	}
	return append(top, other)
}
