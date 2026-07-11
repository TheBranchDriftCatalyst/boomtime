package stats

import (
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// ToStatsPayload builds the Overview StatsPayload for GET /stats. categoryRows
// (per-day-per-category time) may be nil; when present it is folded into the
// Categories segment aligned to the same day series as the other segments.
func ToStatsPayload(t0, t1 time.Time, xs []db.StatRow, categoryRows []db.CategoryDailyRow) model.StatsPayload {
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

	projects := segmentStat(byDate, func(r db.StatRow) string { return r.Project })
	editors := segmentStat(byDate, func(r db.StatRow) string { return r.Editor })
	languages := segmentStat(byDate, func(r db.StatRow) string { return r.Language })
	platforms := segmentStat(byDate, func(r db.StatRow) string { return r.Platform })
	machines := segmentStat(byDate, func(r db.StatRow) string { return r.Machine })

	// Categories are fetched separately (the StatRow set / rollup carries no
	// category column) and aligned to the SAME day series as DailyTotal
	// (alignedDays, i.e. days truncated at the last day with data).
	categories := segmentAligned(alignedDays, categoryRows,
		func(r db.CategoryDailyRow) time.Time { return r.Day },
		func(r db.CategoryDailyRow) string { return r.Category },
		func(r db.CategoryDailyRow) calcStat { return calcStat{r.TotalSeconds, r.Pct, r.DailyPct} })

	return model.StatsPayload{
		StartDate:       t0,
		EndDate:         t1,
		TotalSeconds:    allSecs,
		DailyAvg:        dailyAvg,
		DailyTotal:      dailyTotal,
		ProjectsCount:   len(projects),
		LanguagesCount:  len(languages),
		PlatformsCount:  len(platforms),
		MachinesCount:   len(machines),
		EditorsCount:    len(editors),
		CategoriesCount: len(categories),
		Projects:        capWithOther(projects),
		Editors:         capWithOther(editors),
		Languages:       capWithOther(languages),
		Platforms:       capWithOther(platforms),
		Machines:        capWithOther(machines),
		Categories:      capWithOther(categories),
	}
}
