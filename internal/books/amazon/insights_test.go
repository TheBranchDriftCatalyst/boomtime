package amazon

import (
	"context"
	"testing"
	"time"
)

// insightsFixture is a captured /kindle/reading/insights/data response, trimmed
// to the shape the ingest depends on. It deliberately mixes date_read formats
// (a "YYYY-MM-DD" string, an RFC3339 string, and an epoch-seconds number), a
// title with a blank ASIN (must be dropped), and a title with an unparseable
// date (surfaced with a zero DateRead), so the parser's tolerance is pinned.
const insightsFixture = `{
  "goal_info": {
    "is_backfill_completed": true,
    "goals": {"annual": {"target": 24}},
    "titles_read": [
      {"asin": "B00KINDLE1", "date_read": "2020-03-14", "content_type": "EBOOK", "read_event_id": "evt-1", "source_origin": "KINDLE"},
      {"asin": "B00KINDLE2", "date_read": "2023-11-02T08:30:00Z", "content_type": "EBOOK", "read_event_id": "evt-2", "source_origin": "KINDLE"},
      {"asin": "B00KINDLE3", "date_read": 1704067200, "content_type": "EBOOK", "read_event_id": "evt-3", "source_origin": "KINDLE"},
      {"asin": "", "date_read": "2022-01-01", "content_type": "EBOOK", "read_event_id": "evt-x", "source_origin": "KINDLE"},
      {"asin": "B00KINDLE4", "date_read": "not-a-date", "content_type": "EBOOK", "read_event_id": "evt-4", "source_origin": "KINDLE"}
    ]
  },
  "current_daily_streak": {"duration": 7},
  "longest_daily_streak": {"duration": 42, "start": "2021-01-01", "end": "2021-02-11", "utcEndTime": "1613001600000", "readingMarketplace": "ATVPDKIKX0DER"},
  "current_weekly_streak": {"duration": 3, "ttl": 1699999999},
  "longest_weekly_streak": {"duration": 12},
  "current_weekly_streak_state": "ACTIVE",
  "achievements_data": {"daysLeftInCurrentChallenge": 5, "totalAvailableAchievements": 30, "totalEarnedAchievements": 11, "achievementsDisplayAttributes": {"x": 1}},
  "preferences": {"opt_in": true},
  "urcGatingWeblabTreatment": "T1"
}`

func TestParseKindleInsights(t *testing.T) {
	ins, err := parseKindleInsights([]byte(insightsFixture))
	if err != nil {
		t.Fatalf("parseKindleInsights: %v", err)
	}

	// titles_read: the blank-ASIN row is dropped; the other four survive.
	if len(ins.TitlesRead) != 4 {
		t.Fatalf("want 4 titles (blank ASIN dropped), got %d: %+v", len(ins.TitlesRead), ins.TitlesRead)
	}
	byASIN := map[string]TitleRead{}
	for _, tr := range ins.TitlesRead {
		byASIN[tr.ASIN] = tr
	}

	// YYYY-MM-DD string.
	if got := byASIN["B00KINDLE1"].DateRead; !got.Equal(time.Date(2020, 3, 14, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("B00KINDLE1 date = %v, want 2020-03-14", got)
	}
	if byASIN["B00KINDLE1"].ReadEventID != "evt-1" {
		t.Fatalf("B00KINDLE1 read_event_id = %q", byASIN["B00KINDLE1"].ReadEventID)
	}
	// RFC3339 string.
	if got := byASIN["B00KINDLE2"].DateRead; !got.Equal(time.Date(2023, 11, 2, 8, 30, 0, 0, time.UTC)) {
		t.Fatalf("B00KINDLE2 date = %v, want 2023-11-02T08:30:00Z", got)
	}
	// Epoch seconds (1704067200 == 2024-01-01T00:00:00Z).
	if got := byASIN["B00KINDLE3"].DateRead; !got.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("B00KINDLE3 epoch date = %v, want 2024-01-01", got)
	}
	// Unparseable date: surfaced with a zero DateRead but the raw value retained.
	u := byASIN["B00KINDLE4"]
	if !u.DateRead.IsZero() {
		t.Fatalf("B00KINDLE4 unparseable date should be zero, got %v", u.DateRead)
	}
	if u.DateReadRaw != "not-a-date" {
		t.Fatalf("B00KINDLE4 raw date = %q, want %q", u.DateReadRaw, "not-a-date")
	}

	// Streaks parse (number-typed duration; string/number mix in the longest).
	if ins.CurrentDailyStreak.Duration != 7 {
		t.Fatalf("current daily streak duration = %d, want 7", ins.CurrentDailyStreak.Duration)
	}
	if ins.LongestDailyStreak.Duration != 42 || ins.LongestDailyStreak.Start != "2021-01-01" {
		t.Fatalf("longest daily streak = %+v", ins.LongestDailyStreak)
	}
	// utcEndTime arrived as a numeric-looking string — flexString keeps it as-is.
	if ins.LongestDailyStreak.UTCEndTime != "1613001600000" {
		t.Fatalf("longest daily utcEndTime = %q", ins.LongestDailyStreak.UTCEndTime)
	}
	if ins.CurrentWeeklyStreak.TTL != 1699999999 {
		t.Fatalf("current weekly ttl = %d", ins.CurrentWeeklyStreak.TTL)
	}
	if ins.CurrentWeeklyStreakState != "ACTIVE" {
		t.Fatalf("current weekly streak state = %q", ins.CurrentWeeklyStreakState)
	}

	// Achievements + backfill flag + gating.
	if ins.Achievements.TotalEarnedAchievements != 11 || ins.Achievements.TotalAvailableAchievements != 30 {
		t.Fatalf("achievements = %+v", ins.Achievements)
	}
	if !ins.IsBackfillCompleted {
		t.Fatal("is_backfill_completed should be true")
	}
	if ins.URCGatingWeblabTreatment != "T1" {
		t.Fatalf("urc gating = %q", ins.URCGatingWeblabTreatment)
	}

	// Raw snapshot is retained verbatim for later storage.
	if len(ins.Raw) != len(insightsFixture) {
		t.Fatalf("Raw snapshot length = %d, want %d", len(ins.Raw), len(insightsFixture))
	}
}

// TestParseKindleInsightsEmpty: an empty/streak-less payload decodes to a zero
// struct with no titles rather than erroring.
func TestParseKindleInsightsEmpty(t *testing.T) {
	ins, err := parseKindleInsights([]byte(`{}`))
	if err != nil {
		t.Fatalf("parseKindleInsights({}): %v", err)
	}
	if len(ins.TitlesRead) != 0 {
		t.Fatalf("want no titles, got %d", len(ins.TitlesRead))
	}
	if ins.CurrentDailyStreak.Duration != 0 {
		t.Fatalf("empty payload streak should be zero, got %+v", ins.CurrentDailyStreak)
	}
}

// Compile-time contract: the real Cloud Reader client satisfies the insights
// fetch method the books domain depends on.
var _ interface {
	FetchKindleInsights(context.Context, map[string]string) (*KindleInsights, error)
} = (*CloudReaderClient)(nil)
