package stats

import (
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// ToProjectStatistics builds ProjectStatistics for project/tag stats (Projects.toStatsPayload).
// extras carries the per-project viz metrics (authoring/reading, branches,
// breadth) and may be nil (e.g. the tag path), in which case those fields stay
// zero-valued.
func ToProjectStatistics(t0, t1 time.Time, xs []db.ProjectStatRow, extras *db.ProjectExtras) model.ProjectStatistics {
	// Clamp the start to the earliest day with data (avoids a huge empty leading
	// span on "All time").
	t0 = clampStartToData(t0, xs, projDay)
	days := genDates(t0, t1)
	byDate, alignedDays := fillMissing(days, groupByDay(xs, projDay), projDay)
	var allSecs int64
	for _, x := range xs {
		allSecs += x.TotalSeconds
	}
	dailyTotal := dailyTotals(byDate, func(r db.ProjectStatRow) int64 { return r.TotalSeconds })
	languages := segmentProj(byDate, func(r db.ProjectStatRow) string { return r.Language })
	// Cap to top-N + "Other (N more)" ONCE, then reuse the same list for both the
	// Languages breakdown and the per-day LanguagesDaily matrix so their day
	// arrays (and the front-end colors keyed by list order) stay in lockstep.
	cappedLanguages := capWithOther(languages)
	// "Most active files" must contain only real file entities — exclude browsing
	// domains/apps (ty='domain'/'app'/'url') so github.com / https://… never show
	// up as files. Other segments (languages/weekDay/hour/dailyTotal/total) still
	// aggregate over every entity, so the total-time card is unaffected.
	files := segmentProjWhere(byDate,
		func(r db.ProjectStatRow) bool { return r.Ty == "file" },
		func(r db.ProjectStatRow) string { return r.Entity })

	out := model.ProjectStatistics{
		StartDate:      t0,
		EndDate:        t1,
		TotalSeconds:   allSecs,
		DailyTotal:     dailyTotal,
		LanguagesCount: len(languages),
		FilesCount:     len(files),
		EntitiesCount:  len(files), // distinct files == distinct entities (same set)
		Languages:      cappedLanguages,
		LanguagesDaily: languagesDaily(cappedLanguages),
		Files:          capWithOther(files),
		// WeekDay (7) and Hour (24) are already bounded.
		WeekDay: segmentProj(byDate, func(r db.ProjectStatRow) string { return r.Weekday }),
		Hour:    segmentProj(byDate, func(r db.ProjectStatRow) string { return r.Hour }),
	}

	// Align the extra daily arrays to the SAME day series as DailyTotal
	// (alignedDays: fillMissing truncates at the last day with data, and
	// byDate[i] corresponds to alignedDays[i]).
	applyProjectExtras(&out, alignedDays, extras)
	return out
}

// languagesDaily projects the already-capped (top-N + "Other") language list
// into the per-day-per-language matrix used by the stacked "Total activity"
// column chart. Each series' Daily copies the language's TotalDaily, so it stays
// aligned to DailyTotal's day axis and the per-day column sum equals DailyTotal.
func languagesDaily(languages []model.ResourceStats) []model.LanguageDaily {
	out := make([]model.LanguageDaily, 0, len(languages))
	for _, l := range languages {
		daily := make([]int64, len(l.TotalDaily))
		copy(daily, l.TotalDaily)
		out = append(out, model.LanguageDaily{Name: l.Name, Daily: daily})
	}
	return out
}

// applyProjectExtras fills the authoring/reading, branch, and breadth fields,
// aligning the daily arrays to the same `days` series as DailyTotal. Always
// initializes the daily arrays (length len(days)) so the FE can bind directly,
// even when extras is nil or empty.
func applyProjectExtras(out *model.ProjectStatistics, days []time.Time, extras *db.ProjectExtras) {
	n := len(days)
	out.DailyWriteRatio = make([]float64, n)
	out.DailyEntities = make([]int64, n)
	out.Branches = []model.ResourceStats{}

	if extras == nil {
		return
	}

	// Index the per-day extras by day for O(1) alignment.
	dailyByDay := make(map[string]db.ProjectDailyExtra, len(extras.Daily))
	for _, e := range extras.Daily {
		dailyByDay[dayKey(e.Day)] = e
	}
	for i, day := range days {
		e, ok := dailyByDay[dayKey(day)]
		if !ok {
			continue
		}
		out.WriteSeconds += e.WriteSeconds
		out.ReadSeconds += e.ReadSeconds
		out.DailyEntities[i] = e.DistinctEntities
		if fileSecs := e.WriteSeconds + e.ReadSeconds; fileSecs > 0 {
			out.DailyWriteRatio[i] = float64(e.WriteSeconds) / float64(fileSecs)
		}
	}

	branches := segmentAligned(days, extras.Branches,
		func(r db.ProjectBranchRow) time.Time { return r.Day },
		func(r db.ProjectBranchRow) string { return r.Branch },
		func(r db.ProjectBranchRow) calcStat { return calcStat{r.TotalSeconds, r.Pct, r.DailyPct} })
	out.BranchesCount = len(branches)
	out.Branches = capWithOther(branches)
}
