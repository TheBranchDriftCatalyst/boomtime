package stats

import (
	"fmt"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// TestProjectExtrasWriteReadSplit checks writeSeconds/readSeconds totals and that
// dailyWriteRatio aligns index-for-index with DailyTotal's day series.
func TestProjectExtrasWriteReadSplit(t *testing.T) {
	d1 := day(2025, 5, 1)
	d3 := day(2025, 5, 3)

	// Main rows drive the day series (d1..d3). The middle day has no main row but
	// the extras still cover it — the arrays must be length 3 and aligned.
	xs := []db.ProjectStatRow{
		{Day: d1, Language: "Go", Entity: "a.go", TotalSeconds: 100, Weekday: "4", Hour: "10"},
		{Day: d3, Language: "Go", Entity: "b.go", TotalSeconds: 60, Weekday: "6", Hour: "11"},
	}
	extras := &db.ProjectExtras{
		Daily: []db.ProjectDailyExtra{
			{Day: d1, WriteSeconds: 30, ReadSeconds: 70, DistinctEntities: 2},
			// d2 intentionally absent -> ratio 0, entities 0
			{Day: d3, WriteSeconds: 60, ReadSeconds: 0, DistinctEntities: 1},
		},
	}

	p := ToProjectStatistics(d1, d3, xs, extras)

	if len(p.DailyTotal) != 3 {
		t.Fatalf("DailyTotal len = %d, want 3", len(p.DailyTotal))
	}
	if len(p.DailyWriteRatio) != 3 || len(p.DailyEntities) != 3 {
		t.Fatalf("daily arrays not aligned to 3 days: ratio=%d entities=%d", len(p.DailyWriteRatio), len(p.DailyEntities))
	}
	if p.WriteSeconds != 90 || p.ReadSeconds != 70 {
		t.Fatalf("write/read = %d/%d, want 90/70", p.WriteSeconds, p.ReadSeconds)
	}
	// d1: 30/(30+70)=0.3; d2: no file activity -> 0; d3: 60/60=1.0
	wantRatio := []float64{0.3, 0.0, 1.0}
	for i, w := range wantRatio {
		if diff := p.DailyWriteRatio[i] - w; diff > 1e-9 || diff < -1e-9 {
			t.Fatalf("DailyWriteRatio[%d] = %v, want %v", i, p.DailyWriteRatio[i], w)
		}
	}
	wantEnt := []int64{2, 0, 1}
	for i, w := range wantEnt {
		if p.DailyEntities[i] != w {
			t.Fatalf("DailyEntities[%d] = %d, want %d", i, p.DailyEntities[i], w)
		}
	}
}

// TestProjectExtrasAlignsToDailyTotal guards the alignment bug: when the main
// data ends before the requested range end, DailyTotal is truncated to the last
// day with data, and the extra daily arrays must match that truncated length
// index-for-index (not the full requested range).
func TestProjectExtrasAlignsToDailyTotal(t *testing.T) {
	d1 := day(2025, 5, 1)
	d2 := day(2025, 5, 2)
	// Main data only on d1/d2, but the requested range runs to d1+10 days.
	rangeEnd := d1.AddDate(0, 0, 10)
	xs := []db.ProjectStatRow{
		{Day: d1, Language: "Go", Entity: "a.go", TotalSeconds: 100, Weekday: "4", Hour: "10"},
		{Day: d2, Language: "Go", Entity: "b.go", TotalSeconds: 50, Weekday: "5", Hour: "11"},
	}
	extras := &db.ProjectExtras{
		Daily: []db.ProjectDailyExtra{
			{Day: d1, WriteSeconds: 10, ReadSeconds: 90, DistinctEntities: 1},
			{Day: d2, WriteSeconds: 25, ReadSeconds: 25, DistinctEntities: 1},
		},
	}
	p := ToProjectStatistics(d1, rangeEnd, xs, extras)

	// DailyTotal is truncated to the 2 days with data; the extra arrays must match.
	if len(p.DailyTotal) != 2 {
		t.Fatalf("DailyTotal len = %d, want 2 (truncated to last data day)", len(p.DailyTotal))
	}
	if len(p.DailyWriteRatio) != len(p.DailyTotal) || len(p.DailyEntities) != len(p.DailyTotal) {
		t.Fatalf("misaligned: dailyTotal=%d writeRatio=%d entities=%d",
			len(p.DailyTotal), len(p.DailyWriteRatio), len(p.DailyEntities))
	}
	if p.EntitiesCount != p.FilesCount {
		t.Fatalf("entitiesCount(%d) must equal filesCount(%d)", p.EntitiesCount, p.FilesCount)
	}
}

// TestProjectExtrasNil ensures the daily arrays are always initialized (never
// nil) and branches is empty when no extras are provided (tag path).
func TestProjectExtrasNil(t *testing.T) {
	d1 := day(2025, 5, 1)
	d2 := day(2025, 5, 2)
	xs := []db.ProjectStatRow{
		{Day: d1, Language: "Go", Entity: "a.go", TotalSeconds: 100, Weekday: "4", Hour: "10"},
		{Day: d2, Language: "Go", Entity: "b.go", TotalSeconds: 50, Weekday: "5", Hour: "11"},
	}
	p := ToProjectStatistics(d1, d2, xs, nil)
	if p.DailyWriteRatio == nil || len(p.DailyWriteRatio) != 2 {
		t.Fatalf("DailyWriteRatio = %v, want length-2 non-nil", p.DailyWriteRatio)
	}
	if p.DailyEntities == nil || len(p.DailyEntities) != 2 {
		t.Fatalf("DailyEntities = %v, want length-2 non-nil", p.DailyEntities)
	}
	if p.Branches == nil || len(p.Branches) != 0 {
		t.Fatalf("Branches = %v, want empty non-nil", p.Branches)
	}
	if p.WriteSeconds != 0 || p.ReadSeconds != 0 || p.BranchesCount != 0 {
		t.Fatalf("nil extras should leave write/read/branchesCount zero")
	}
}

// TestProjectExtrasBranchesCap verifies branch shaping caps at top-12 + "Other"
// while branchesCount reports the true distinct count, and daily arrays align.
func TestProjectExtrasBranchesCap(t *testing.T) {
	d1 := day(2025, 5, 1)
	d2 := day(2025, 5, 2)
	xs := []db.ProjectStatRow{
		{Day: d1, Language: "Go", Entity: "a.go", TotalSeconds: 10, Weekday: "4", Hour: "10"},
		{Day: d2, Language: "Go", Entity: "b.go", TotalSeconds: 10, Weekday: "5", Hour: "11"},
	}

	// 15 distinct branches; branch i has total i+1 seconds, split across d1/d2.
	var branchRows []db.ProjectBranchRow
	for i := 0; i < 15; i++ {
		name := fmt.Sprintf("branch-%02d", i)
		branchRows = append(branchRows,
			db.ProjectBranchRow{Day: d1, Branch: name, TotalSeconds: int64(i + 1), Pct: 0.01, DailyPct: 0.02},
			db.ProjectBranchRow{Day: d2, Branch: name, TotalSeconds: int64(i + 1), Pct: 0.01, DailyPct: 0.02},
		)
	}
	extras := &db.ProjectExtras{Branches: branchRows}

	p := ToProjectStatistics(d1, d2, xs, extras)

	if p.BranchesCount != 15 {
		t.Fatalf("BranchesCount = %d, want 15 (true distinct)", p.BranchesCount)
	}
	// capWithOther keeps 12 + 1 "Other" bucket = 13 entries.
	if len(p.Branches) != 13 {
		t.Fatalf("Branches len = %d, want 13 (top-12 + Other)", len(p.Branches))
	}
	last := p.Branches[len(p.Branches)-1]
	if last.Name != "Other (3 more)" {
		t.Fatalf("last branch = %q, want \"Other (3 more)\"", last.Name)
	}
	// Every branch's TotalDaily aligns to the 2-day series.
	for _, b := range p.Branches {
		if len(b.TotalDaily) != 2 || len(b.PctDaily) != 2 {
			t.Fatalf("branch %q daily arrays not aligned to 2 days: %+v", b.Name, b)
		}
	}
	// The "Other" bucket sums the 3 smallest branches (totals 1+2+3 across 2 days = 12).
	if last.TotalSeconds != int64((1+2+3)*2) {
		t.Fatalf("Other total = %d, want 12", last.TotalSeconds)
	}
}
