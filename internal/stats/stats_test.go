package stats

import (
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
)

func TestCountDuration(t *testing.T) {
	// Documented fixture from Utils.hs: countDuration [1,2,3,10,21,22,33,100,104,109] 5 == 12.
	got := CountDuration([]int{1, 2, 3, 10, 21, 22, 33, 100, 104, 109}, 5)
	if got != 12 {
		t.Fatalf("CountDuration = %d, want 12", got)
	}
}

func TestCountDurationEmpty(t *testing.T) {
	if got := CountDuration(nil, 5); got != 0 {
		t.Fatalf("CountDuration(nil) = %d, want 0", got)
	}
	if got := CountDuration([]int{42}, 5); got != 0 {
		t.Fatalf("CountDuration(single) = %d, want 0", got)
	}
}

func TestCompoundDuration(t *testing.T) {
	cases := []struct {
		name string
		in   *int64
		want string
	}{
		{"nil", nil, "no data"},
		{"zero", ptr(0), "no data"},
		// 2h 15m 30s = 8130s -> drop the seconds unit -> "2 hrs 15 min".
		{"hours+min", ptr(2*3600 + 15*60 + 30), "2 hrs 15 min"},
		// 90s = 1 min 30 sec -> drop sec -> "1 min".
		{"just-min", ptr(90), "1 min"},
		// 45s -> only seconds -> init drops it -> "".
		{"just-sec", ptr(45), ""},
		// 26h = 1 day 2 hrs; compoundDuration drops the LAST non-zero unit
		// (init), leaving "1 day" (matches Utils.compoundDuration exactly).
		{"day", ptr(24*3600 + 2*3600), "1 day"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CompoundDuration(c.in); got != c.want {
				t.Fatalf("CompoundDuration(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func ptr(v int64) *int64 { return &v }

func TestToStatsPayloadShaping(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC) // 2 days inclusive

	day1 := t0
	day2 := t1
	rows := []db.StatRow{
		{Day: day1, Project: "alpha", Language: "Go", Editor: "vim", Platform: "linux", Machine: "m1", Entity: "a.go", TotalSeconds: 100, Pct: 0.5, DailyPct: 1.0},
		{Day: day1, Project: "beta", Language: "Go", Editor: "vim", Platform: "linux", Machine: "m1", Entity: "b.go", TotalSeconds: 50, Pct: 0.25, DailyPct: 0.5},
		{Day: day2, Project: "alpha", Language: "Rust", Editor: "code", Platform: "mac", Machine: "m2", Entity: "c.rs", TotalSeconds: 50, Pct: 0.25, DailyPct: 1.0},
	}

	p := ToStatsPayload(t0, t1, rows, nil)

	if p.TotalSeconds != 200 {
		t.Fatalf("TotalSeconds = %d, want 200", p.TotalSeconds)
	}
	if len(p.DailyTotal) != 2 {
		t.Fatalf("DailyTotal len = %d, want 2", len(p.DailyTotal))
	}
	if p.DailyTotal[0] != 150 || p.DailyTotal[1] != 50 {
		t.Fatalf("DailyTotal = %v, want [150 50]", p.DailyTotal)
	}
	if p.DailyAvg != 100 {
		t.Fatalf("DailyAvg = %f, want 100", p.DailyAvg)
	}

	// Projects: alpha appears both days (100 + 50 = 150), beta only day1 (50, 0).
	var alpha *int64
	for i := range p.Projects {
		if p.Projects[i].Name == "alpha" {
			v := p.Projects[i].TotalSeconds
			alpha = &v
			if len(p.Projects[i].TotalDaily) != 2 {
				t.Fatalf("alpha TotalDaily len = %d, want 2", len(p.Projects[i].TotalDaily))
			}
			if p.Projects[i].TotalDaily[0] != 100 || p.Projects[i].TotalDaily[1] != 50 {
				t.Fatalf("alpha TotalDaily = %v, want [100 50]", p.Projects[i].TotalDaily)
			}
		}
	}
	if alpha == nil || *alpha != 150 {
		t.Fatalf("alpha total = %v, want 150", alpha)
	}

	// Languages: Go (day1 only, len-2 daily) and Rust (day2).
	if len(p.Languages) != 2 {
		t.Fatalf("languages = %d, want 2", len(p.Languages))
	}
}

func TestToLeaderboardsPayload(t *testing.T) {
	rows := []db.LeaderboardRow{
		{Project: "p", Language: "Go", Sender: "alice", TotalSeconds: 500},
		{Project: "p", Language: "Go", Sender: "bob", TotalSeconds: 40}, // < 60, filtered
		{Project: "q", Language: "Rust", Sender: "alice", TotalSeconds: 300},
	}
	lb := ToLeaderboardsPayload(rows)
	// alice total = 800 global; bob filtered out.
	if len(lb.Global) != 1 || lb.Global[0].Name != "alice" || lb.Global[0].Value != 800 {
		t.Fatalf("global = %+v, want [{alice 800}]", lb.Global)
	}
	if _, ok := lb.Lang["Go"]; !ok {
		t.Fatalf("expected Go language leaderboard")
	}
	// Go has alice 500 (bob filtered).
	if len(lb.Lang["Go"]) != 1 || lb.Lang["Go"][0].Value != 500 {
		t.Fatalf("Go lang = %+v", lb.Lang["Go"])
	}
}
