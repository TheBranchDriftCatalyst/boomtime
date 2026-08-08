package stats

import (
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// ToStatsPayload builds the Overview StatsPayload for GET /stats. categoryRows
// (per-day-per-category time) may be nil; when present it is folded into the
// Categories segment aligned to the same day series as the other segments.
//
// githubGrid (gaka-csx P3) is the owner's cached GitHub contribution grid
// (one {date,count} per day, trailing year). It may be nil — when nil OR empty
// the resulting payload's GithubDailyTotal stays nil (omitted), keeping the
// wire shape byte-identical to a no-GitHub payload. When present, the grid is
// expanded into a date->count map and aligned to the SAME alignedDays series
// the Categories segment uses, yielding a []int64 parallel to DailyTotal. This
// is a pure in-memory alignment of an ALREADY-FETCHED local cache row — it
// makes no external GitHub call and therefore never blocks the Overview.
func ToStatsPayload(t0, t1 time.Time, xs []db.StatRow, categoryRows []db.CategoryDailyRow, githubGrid []model.GithubContributionDay) model.StatsPayload {
	// Clamp the start to the earliest day that actually has data, so wide/"All
	// time" ranges don't produce a huge empty leading span in the charts.
	t0 = clampStartToData(t0, xs, statDay)
	days := genDates(t0, t1)
	byDate, alignedDays := fillMissing(days, groupByDay(xs, statDay), statDay)

	var allSecs int64
	for _, x := range xs {
		allSecs += x.TotalSeconds
	}
	numDays := len(byDate)
	dailyAvg := 0.0
	if numDays > 0 {
		dailyAvg = float64(allSecs) / float64(numDays)
	}
	dailyTotal := dailyTotals(byDate, func(r db.StatRow) int64 { return r.TotalSeconds })

	// gaka-6ci: per-axis pies filter out rows whose axis was NULL on the
	// source heartbeat (browser tabs with no file open, AI console tabs,
	// plugin-less clients). Without this filter, all those null-axis rows
	// collapse into a bucket named 'Other' (from ingest's COALESCE fallback)
	// that renders as if it were capWithOther's synthetic aggregation cap —
	// but it's actually just "time spent doing something with no <axis>."
	// A Languages pie should only show real languages; the total-time card
	// (allSecs above) still counts everything.
	projects := segmentStatWhere(byDate, func(r db.StatRow) bool { return !r.ProjectMissing }, func(r db.StatRow) string { return r.Project })
	editors := segmentStatWhere(byDate, func(r db.StatRow) bool { return !r.EditorMissing }, func(r db.StatRow) string { return r.Editor })
	languages := segmentStatWhere(byDate, func(r db.StatRow) bool { return !r.LanguageMissing }, func(r db.StatRow) string { return r.Language })
	platforms := segmentStatWhere(byDate, func(r db.StatRow) bool { return !r.PlatformMissing }, func(r db.StatRow) string { return r.Platform })
	machines := segmentStatWhere(byDate, func(r db.StatRow) bool { return !r.MachineMissing }, func(r db.StatRow) string { return r.Machine })

	// Categories are fetched separately (the StatRow set / rollup carries no
	// category column) and aligned to the SAME day series as DailyTotal
	// (alignedDays, i.e. days truncated at the last day with data).
	categories := segmentAligned(alignedDays, categoryRows,
		func(r db.CategoryDailyRow) time.Time { return r.Day },
		func(r db.CategoryDailyRow) string { return r.Category },
		func(r db.CategoryDailyRow) calcStat { return calcStat{r.TotalSeconds, r.Pct, r.DailyPct} })

	// gaka-csx P3: align the (optional) GitHub contribution grid to the SAME
	// day axis as DailyTotal. nil/empty grid ⇒ nil series ⇒ omitted from the
	// wire (byte-identical to a no-GitHub payload).
	githubDailyTotal := alignGithubDaily(alignedDays, githubGrid)

	return model.StatsPayload{
		StartDate:        t0,
		EndDate:          t1,
		TotalSeconds:     allSecs,
		DailyAvg:         dailyAvg,
		DailyTotal:       dailyTotal,
		GithubDailyTotal: githubDailyTotal,
		ProjectsCount:    len(projects),
		LanguagesCount:   len(languages),
		PlatformsCount:   len(platforms),
		MachinesCount:    len(machines),
		EditorsCount:     len(editors),
		CategoriesCount:  len(categories),
		Projects:         capWithOther(projects),
		Editors:          capWithOther(editors),
		Languages:        capWithOther(languages),
		Platforms:        capWithOther(platforms),
		Machines:         capWithOther(machines),
		Categories:       capWithOther(categories),
	}
}

// alignGithubDaily expands a GitHub contribution grid ([{date,count}]) into a
// per-day int64 series parallel to `days` (the alignedDays axis that DailyTotal
// uses). Days with no grid entry are 0. A nil/empty grid returns nil so the
// caller's GithubDailyTotal field stays nil (omitted from the wire) — the
// additive-invariant no-op. Grid dates are keyed with dayKey (UTC YYYY-MM-DD),
// matching GitHub's contributionDays.date format; dates outside the window are
// ignored.
func alignGithubDaily(days []time.Time, grid []model.GithubContributionDay) []int64 {
	if len(grid) == 0 || len(days) == 0 {
		return nil
	}
	counts := make(map[string]int64, len(grid))
	for _, g := range grid {
		counts[g.Date] += int64(g.Count)
	}
	out := make([]int64, len(days))
	for i, d := range days {
		out[i] = counts[dayKey(d)]
	}
	return out
}
