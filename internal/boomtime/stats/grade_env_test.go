package stats

import (
	"os"
	"testing"
)

// TestApplyGradeConfigFromEnv pins the grade-knob wiring that moved here from
// internal/shared/config (gaka-zp2s decoupling): each BOOM_GRADE_* override lands
// on the right field (anti-swap sentinels), unset keeps the shipped default, and a
// bad value falls back to the default (parse-error isolation).
func TestApplyGradeConfigFromEnv(t *testing.T) {
	saved := DefaultGradeConfig
	t.Cleanup(func() { DefaultGradeConfig = saved })

	env := map[string]string{
		"BOOM_GRADE_STREAK_MEDIAN": "11", "BOOM_GRADE_STREAK_WEIGHT": "12",
		"BOOM_GRADE_ACTIVE_MEDIAN": "21", "BOOM_GRADE_ACTIVE_WEIGHT": "22",
		"BOOM_GRADE_LANGUAGES_MEDIAN": "31", "BOOM_GRADE_LANGUAGES_WEIGHT": "32",
		"BOOM_GRADE_PROJECTS_MEDIAN": "41", "BOOM_GRADE_PROJECTS_WEIGHT": "42",
		"BOOM_GRADE_DAILY_AVG_MEDIAN": "51", "BOOM_GRADE_DAILY_AVG_WEIGHT": "52",
		"BOOM_GRADE_HOURS_MEDIAN": "61", "BOOM_GRADE_HOURS_WEIGHT": "62",
		"BOOM_GRADE_MIN_RANGE_DAYS": "77",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	DefaultGradeConfig = saved
	ApplyGradeConfigFromEnv()
	g := DefaultGradeConfig
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"StreakMedian", g.StreakMedian, 11}, {"StreakWeight", g.StreakWeight, 12},
		{"ActiveMedian", g.ActiveMedian, 21}, {"ActiveWeight", g.ActiveWeight, 22},
		{"LanguagesMedian", g.LanguagesMedian, 31}, {"LanguagesWeight", g.LanguagesWeight, 32},
		{"ProjectsMedian", g.ProjectsMedian, 41}, {"ProjectsWeight", g.ProjectsWeight, 42},
		{"DailyAvgMedian", g.DailyAvgMedian, 51}, {"DailyAvgWeight", g.DailyAvgWeight, 52},
		{"HoursMedian", g.HoursMedian, 61}, {"HoursWeight", g.HoursWeight, 62},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v (field swap?)", c.name, c.got, c.want)
		}
	}
	if g.MinRangeDays != 77 {
		t.Errorf("MinRangeDays = %d, want 77", g.MinRangeDays)
	}

	// Unset → shipped defaults; bad value → default (parse-error isolation).
	for k := range env {
		os.Unsetenv(k)
	}
	DefaultGradeConfig = saved
	t.Setenv("BOOM_GRADE_STREAK_MEDIAN", "not-a-number")
	ApplyGradeConfigFromEnv()
	if DefaultGradeConfig.StreakMedian != saved.StreakMedian {
		t.Errorf("bad env must preserve default: got %v, want %v", DefaultGradeConfig.StreakMedian, saved.StreakMedian)
	}
}
