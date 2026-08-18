package stats

import (
	"os"
	"strconv"
	"strings"
)

// ApplyGradeConfigFromEnv overrides DefaultGradeConfig from BOOM_GRADE_* env vars,
// keeping the shipped calibration for any unset/invalid var. Called once at boot
// (cmd/boomtime). Lives HERE (the stats domain owns GradeConfig) rather than in
// internal/shared/config — config must stay domain-neutral so every binary that
// imports it (incl. a standalone catalyst-books) doesn't drag in the code domain.
func ApplyGradeConfigFromEnv() {
	d := DefaultGradeConfig
	DefaultGradeConfig = GradeConfig{
		StreakMedian:    gradeEnvFloat("BOOM_GRADE_STREAK_MEDIAN", d.StreakMedian),
		StreakWeight:    gradeEnvFloat("BOOM_GRADE_STREAK_WEIGHT", d.StreakWeight),
		ActiveMedian:    gradeEnvFloat("BOOM_GRADE_ACTIVE_MEDIAN", d.ActiveMedian),
		ActiveWeight:    gradeEnvFloat("BOOM_GRADE_ACTIVE_WEIGHT", d.ActiveWeight),
		LanguagesMedian: gradeEnvFloat("BOOM_GRADE_LANGUAGES_MEDIAN", d.LanguagesMedian),
		LanguagesWeight: gradeEnvFloat("BOOM_GRADE_LANGUAGES_WEIGHT", d.LanguagesWeight),
		ProjectsMedian:  gradeEnvFloat("BOOM_GRADE_PROJECTS_MEDIAN", d.ProjectsMedian),
		ProjectsWeight:  gradeEnvFloat("BOOM_GRADE_PROJECTS_WEIGHT", d.ProjectsWeight),
		DailyAvgMedian:  gradeEnvFloat("BOOM_GRADE_DAILY_AVG_MEDIAN", d.DailyAvgMedian),
		DailyAvgWeight:  gradeEnvFloat("BOOM_GRADE_DAILY_AVG_WEIGHT", d.DailyAvgWeight),
		HoursMedian:     gradeEnvFloat("BOOM_GRADE_HOURS_MEDIAN", d.HoursMedian),
		HoursWeight:     gradeEnvFloat("BOOM_GRADE_HOURS_WEIGHT", d.HoursWeight),
		MinRangeDays:    gradeEnvInt("BOOM_GRADE_MIN_RANGE_DAYS", d.MinRangeDays),
	}
}

func gradeEnvFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return def
}

func gradeEnvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}
