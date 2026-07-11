package stats

import (
	"fmt"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

func TestToLeaderboardsPayloadGlobalCapAndSort(t *testing.T) {
	// 25 distinct senders, all > 60s, with strictly descending totals so the
	// top-20 cap and value-desc ordering are unambiguous.
	var rows []db.LeaderboardRow
	for i := 0; i < 25; i++ {
		rows = append(rows, db.LeaderboardRow{
			Sender:       fmt.Sprintf("user%02d", i),
			Language:     "Go",
			TotalSeconds: int64(10000 - i*100), // 10000 down to 7600, all > 60
		})
	}

	p := ToLeaderboardsPayload(rows)

	if len(p.Global) != 20 {
		t.Fatalf("Global len = %d, want 20 (top-20 cap)", len(p.Global))
	}
	// Highest total is user00 (10000); descending thereafter.
	if p.Global[0].Name != "user00" || p.Global[0].Value != 10000 {
		t.Errorf("Global[0] = %+v, want user00/10000", p.Global[0])
	}
	for i := 1; i < len(p.Global); i++ {
		if p.Global[i-1].Value < p.Global[i].Value {
			t.Errorf("Global not sorted desc at %d: %d < %d",
				i, p.Global[i-1].Value, p.Global[i].Value)
		}
	}
	// The 21st..25th (lowest totals) must be dropped: user20..user24 absent.
	for _, ut := range p.Global {
		if ut.Name == "user24" {
			t.Error("user24 (lowest) should have been dropped by top-20 cap")
		}
	}
}

func TestToLeaderboardsPayloadTieBreakByName(t *testing.T) {
	// Equal totals -> tie-break by name ascending.
	rows := []db.LeaderboardRow{
		{Sender: "charlie", Language: "Go", TotalSeconds: 500},
		{Sender: "alice", Language: "Go", TotalSeconds: 500},
		{Sender: "bob", Language: "Go", TotalSeconds: 500},
	}
	p := ToLeaderboardsPayload(rows)
	got := []string{p.Global[0].Name, p.Global[1].Name, p.Global[2].Name}
	want := []string{"alice", "bob", "charlie"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tie-break order = %v, want %v", got, want)
		}
	}
}

func TestToLeaderboardsPayloadFiltersUnder60AndEmptyLangBuckets(t *testing.T) {
	rows := []db.LeaderboardRow{
		{Sender: "keepGlobal", Language: "Go", TotalSeconds: 120},
		// Exactly 60 is filtered out (filter is v > 60, strictly).
		{Sender: "borderline", Language: "Go", TotalSeconds: 60},
		// Sub-60 sender in Python: its only total is 30 -> Python bucket empty -> omitted.
		{Sender: "tiny", Language: "Python", TotalSeconds: 30},
	}
	p := ToLeaderboardsPayload(rows)

	// Global: only keepGlobal survives (120 > 60); borderline (==60) and tiny (30) dropped.
	if len(p.Global) != 1 || p.Global[0].Name != "keepGlobal" {
		t.Fatalf("Global = %+v, want single keepGlobal", p.Global)
	}

	// Go bucket has keepGlobal (>60); borderline (==60) filtered but bucket non-empty.
	if goList, ok := p.Lang["Go"]; !ok || len(goList) != 1 || goList[0].Name != "keepGlobal" {
		t.Errorf("Lang[Go] = %+v, want single keepGlobal", p.Lang["Go"])
	}

	// Python bucket only had a sub-60 total -> empty list -> must be omitted entirely.
	if _, ok := p.Lang["Python"]; ok {
		t.Error("Lang[Python] should be omitted (all entries filtered)")
	}
}
