// streaks.go — payload-derived streak / active-day helpers shared by the
// grade blend (grade.go) and the SVG stat-tile widgets (Part B Stage 1:
// current-streak-stat / longest-streak-stat / active-days-stat). Each mirrors
// the FE reference in web/src/features/publicprofile/grade.ts +
// web/src/features/widgets/renderers/WidgetRenderer.tsx EXACTLY so the SVG
// embed and the in-page React tile always agree on the number.
package stats

// CurrentStreak is the run of consecutive active days ENDING at the last day
// of the series — mirrors grade.ts currentStreak(). A trailing zero day means
// the streak is 0, no matter how active the earlier days were.
func CurrentStreak(daily []int64) int {
	cur := 0
	for i := len(daily) - 1; i >= 0; i-- {
		if daily[i] <= 0 {
			break
		}
		cur++
	}
	return cur
}

// LongestStreak is the longest consecutive run of active days anywhere in the
// series — exported twin of longestStreak (grade.ts longestStreakInRange()).
func LongestStreak(daily []int64) int { return longestStreak(daily) }

// ActiveDays counts days with any activity plus the range length — the
// active-days-stat ratio (WidgetRenderer.tsx: `active = filter(s>0).length;
// total = dailyTotal.length`).
func ActiveDays(daily []int64) (active, total int) {
	for _, s := range daily {
		if s > 0 {
			active++
		}
	}
	return active, len(daily)
}
