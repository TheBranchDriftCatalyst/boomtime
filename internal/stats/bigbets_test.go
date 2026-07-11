package stats

import (
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// ---- Categories fold-in ----

func TestCategoriesFoldIn(t *testing.T) {
	d1 := day(2025, 5, 1)
	d2 := day(2025, 5, 2)
	xs := []db.StatRow{
		{Day: d1, Project: "p", Language: "Go", Editor: "vim", Platform: "linux", Machine: "m", Entity: "a.go", TotalSeconds: 100},
		{Day: d2, Project: "p", Language: "Go", Editor: "vim", Platform: "linux", Machine: "m", Entity: "b.go", TotalSeconds: 50},
	}
	cats := []db.CategoryDailyRow{
		{Day: d1, Category: "coding", TotalSeconds: 80, Pct: 0.5, DailyPct: 0.8},
		{Day: d1, Category: "debugging", TotalSeconds: 20, Pct: 0.1, DailyPct: 0.2},
		{Day: d2, Category: "coding", TotalSeconds: 50, Pct: 0.3, DailyPct: 1.0},
	}
	p := ToStatsPayload(d1, d2, xs, cats)

	if p.CategoriesCount != 2 {
		t.Fatalf("CategoriesCount = %d, want 2", p.CategoriesCount)
	}
	// Each category's TotalDaily aligns to the 2-day series.
	var coding *int64
	for i := range p.Categories {
		c := p.Categories[i]
		if len(c.TotalDaily) != 2 {
			t.Fatalf("category %q TotalDaily len = %d, want 2", c.Name, len(c.TotalDaily))
		}
		if c.Name == "coding" {
			v := c.TotalSeconds
			coding = &v
			if c.TotalDaily[0] != 80 || c.TotalDaily[1] != 50 {
				t.Fatalf("coding TotalDaily = %v, want [80 50]", c.TotalDaily)
			}
		}
	}
	if coding == nil || *coding != 130 {
		t.Fatalf("coding total = %v, want 130", coding)
	}
}

func TestCategoriesNil(t *testing.T) {
	d1 := day(2025, 5, 1)
	xs := []db.StatRow{
		{Day: d1, Project: "p", Language: "Go", Editor: "vim", Platform: "linux", Machine: "m", Entity: "a.go", TotalSeconds: 100},
	}
	p := ToStatsPayload(d1, d1, xs, nil)
	if p.Categories == nil {
		t.Fatal("Categories should be non-nil (empty) when no category rows given")
	}
	if len(p.Categories) != 0 || p.CategoriesCount != 0 {
		t.Fatalf("nil categories -> empty list + zero count, got %d/%d", len(p.Categories), p.CategoriesCount)
	}
}

// ---- Punchcard ----

func TestPunchcardPayload(t *testing.T) {
	cells := []db.PunchcardCell{
		{Dow: 1, Hour: 9, Seconds: 300},
		{Dow: 1, Hour: 10, Seconds: 900},
		{Dow: 3, Hour: 14, Seconds: 600},
	}
	p := ToPunchcardPayload(cells)
	if p.TotalSeconds != 1800 {
		t.Fatalf("TotalSeconds = %d, want 1800", p.TotalSeconds)
	}
	if p.MaxSeconds != 900 {
		t.Fatalf("MaxSeconds = %d, want 900", p.MaxSeconds)
	}
	if len(p.Cells) != 3 || p.Cells[1].Dow != 1 || p.Cells[1].Hour != 10 || p.Cells[1].Seconds != 900 {
		t.Fatalf("cells not passed through: %+v", p.Cells)
	}
}

func TestPunchcardEmpty(t *testing.T) {
	p := ToPunchcardPayload(nil)
	if p.MaxSeconds != 0 || p.TotalSeconds != 0 || len(p.Cells) != 0 {
		t.Fatalf("empty punchcard should be all-zero, got %+v", p)
	}
}

// ---- Sessions ----

func TestSessionsPayload(t *testing.T) {
	d1 := day(2025, 5, 1)
	d2 := day(2025, 5, 2)
	// Range d1..d3 (3 days); d3 has no sessions -> gap-filled zero row.
	d3 := day(2025, 5, 3)

	rows := []db.SessionRow{
		{Day: d1, Seconds: 600},   // 10m -> "0–15m"
		{Day: d1, Seconds: 2000},  // ~33m -> "30–60m"
		{Day: d2, Seconds: 5400},  // 90m -> "1–2h"
		{Day: d2, Seconds: 100},   // <15m -> "0–15m"
		{Day: d2, Seconds: 10000}, // ~2.7h -> "2h+"
	}
	p := ToSessionsPayload(d1, d3, rows)

	if p.Summary.Count != 5 {
		t.Fatalf("count = %d, want 5", p.Summary.Count)
	}
	if p.Summary.TotalSeconds != 18100 {
		t.Fatalf("total = %d, want 18100", p.Summary.TotalSeconds)
	}
	if p.Summary.MaxSeconds != 10000 {
		t.Fatalf("max = %d, want 10000", p.Summary.MaxSeconds)
	}
	// avg = 18100/5 = 3620.
	if p.Summary.AvgSeconds != 3620 {
		t.Fatalf("avg = %d, want 3620", p.Summary.AvgSeconds)
	}
	// sorted: [100,600,2000,5400,10000] -> median 2000.
	if p.Summary.MedianSeconds != 2000 {
		t.Fatalf("median = %d, want 2000", p.Summary.MedianSeconds)
	}

	// Daily gap-filled to 3 days.
	if len(p.Daily) != 3 {
		t.Fatalf("daily len = %d, want 3", len(p.Daily))
	}
	if p.Daily[0].Date != "2025-05-01" || p.Daily[0].Sessions != 2 || p.Daily[0].TotalSeconds != 2600 || p.Daily[0].LongestSeconds != 2000 {
		t.Fatalf("daily[0] = %+v", p.Daily[0])
	}
	if p.Daily[2].Date != "2025-05-03" || p.Daily[2].Sessions != 0 || p.Daily[2].TotalSeconds != 0 {
		t.Fatalf("daily[2] (empty) = %+v", p.Daily[2])
	}

	// Histogram: 5 fixed bins, in order.
	want := map[string]int64{"0–15m": 2, "15–30m": 0, "30–60m": 1, "1–2h": 1, "2h+": 1}
	if len(p.Histogram) != 5 {
		t.Fatalf("histogram bins = %d, want 5", len(p.Histogram))
	}
	for _, b := range p.Histogram {
		if want[b.Label] != b.Count {
			t.Fatalf("bin %q = %d, want %d", b.Label, b.Count, want[b.Label])
		}
	}
}

func TestSessionsEmpty(t *testing.T) {
	d1 := day(2025, 5, 1)
	d2 := day(2025, 5, 2)
	p := ToSessionsPayload(d1, d2, nil)
	if p.Summary.Count != 0 || p.Summary.MedianSeconds != 0 || p.Summary.AvgSeconds != 0 {
		t.Fatalf("empty summary should be zero: %+v", p.Summary)
	}
	if len(p.Daily) != 2 {
		t.Fatalf("daily should be gap-filled to 2 days even with no sessions, got %d", len(p.Daily))
	}
	if len(p.Histogram) != 5 {
		t.Fatalf("histogram should always have 5 bins, got %d", len(p.Histogram))
	}
}

// ---- Momentum ----

func TestMomentumPayload(t *testing.T) {
	// Range spanning 3 ISO weeks. Pick Mondays.
	w1 := time.Date(2025, 5, 5, 0, 0, 0, 0, time.UTC)  // Mon
	w2 := time.Date(2025, 5, 12, 0, 0, 0, 0, time.UTC) // Mon
	w3 := time.Date(2025, 5, 19, 0, 0, 0, 0, time.UTC) // Mon

	rows := []db.MomentumRow{
		{Project: "alpha", WeekStart: w1, Seconds: 3600},
		{Project: "alpha", WeekStart: w3, Seconds: 7200}, // skips w2 -> gap-filled 0
		{Project: "beta", WeekStart: w2, Seconds: 1800},
		{Project: "gamma", WeekStart: w1, Seconds: 100},
	}
	// top=2 keeps alpha(10800) and beta(1800); gamma(100) dropped.
	p := ToMomentumPayload(w1, w3, rows, 2)

	if len(p.Weeks) != 3 {
		t.Fatalf("weeks = %v, want 3", p.Weeks)
	}
	if p.Weeks[0] != "2025-05-05" || p.Weeks[1] != "2025-05-12" || p.Weeks[2] != "2025-05-19" {
		t.Fatalf("week keys = %v", p.Weeks)
	}
	if len(p.Projects) != 2 {
		t.Fatalf("projects = %d, want 2 (top-2)", len(p.Projects))
	}
	// Ranked by total desc: alpha first.
	if p.Projects[0].Name != "alpha" || p.Projects[0].TotalSeconds != 10800 {
		t.Fatalf("top project = %+v, want alpha/10800", p.Projects[0])
	}
	// alpha weekly aligned to weeks: [3600, 0, 7200].
	if got := p.Projects[0].Weekly; len(got) != 3 || got[0] != 3600 || got[1] != 0 || got[2] != 7200 {
		t.Fatalf("alpha weekly = %v, want [3600 0 7200]", got)
	}
	if p.Projects[1].Name != "beta" {
		t.Fatalf("second project = %q, want beta", p.Projects[1].Name)
	}
}

func TestIsoWeekStart(t *testing.T) {
	// A Wednesday should map back to that week's Monday.
	wed := time.Date(2025, 5, 7, 15, 30, 0, 0, time.UTC)
	if got := isoWeekStart(wed); got.Format("2006-01-02") != "2025-05-05" {
		t.Fatalf("isoWeekStart(Wed) = %s, want 2025-05-05", got.Format("2006-01-02"))
	}
	// A Monday maps to itself.
	mon := time.Date(2025, 5, 5, 0, 0, 0, 0, time.UTC)
	if got := isoWeekStart(mon); !got.Equal(mon) {
		t.Fatalf("isoWeekStart(Mon) = %s, want itself", got)
	}
	// A Sunday maps back to the previous Monday.
	sun := time.Date(2025, 5, 11, 23, 0, 0, 0, time.UTC)
	if got := isoWeekStart(sun); got.Format("2006-01-02") != "2025-05-05" {
		t.Fatalf("isoWeekStart(Sun) = %s, want 2025-05-05", got.Format("2006-01-02"))
	}
}
