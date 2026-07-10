package stats

import (
	"testing"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
)

// TestSuppressionShapingExcluded documents the shaping guarantee that pairs with
// the DB-layer suppression test (internal/db/suppression_test.go): ToStatsPayload
// surfaces exactly the rows it is given, so once the DB path has excluded a
// suppressed value, it never reappears in any StatsPayload breakdown. The DB test
// proves the exclusion; this proves the shaping doesn't reintroduce it and that a
// suppressed category (fetched separately) is likewise absent.
func TestSuppressionShapingExcluded(t *testing.T) {
	d1 := day(2025, 6, 1)
	// Rows as the DB would return them AFTER excluding SUPPRESS: only KEEP values.
	rows := []db.StatRow{
		{Day: d1, Project: "keepProj", Language: "Go", Editor: "vim", Platform: "linux", Machine: "laptop", Entity: "a.go", TotalSeconds: 300},
	}
	// Categories as GetCategoryDaily would return them after excluding SUPPRESS.
	cats := []db.CategoryDailyRow{
		{Day: d1, Category: "Coding", TotalSeconds: 300, Pct: 1, DailyPct: 1},
	}

	p := ToStatsPayload(d1, d1, rows, cats)

	// SUPPRESS must not appear in any breakdown.
	for _, r := range p.Projects {
		if r.Name == "SUPPRESS" {
			t.Fatal("projects breakdown leaked SUPPRESS")
		}
	}
	for _, r := range p.Languages {
		if r.Name == "SUPPRESS" {
			t.Fatal("languages breakdown leaked SUPPRESS")
		}
	}
	for _, r := range p.Categories {
		if r.Name == "SUPPRESS" {
			t.Fatal("categories breakdown leaked SUPPRESS")
		}
	}
	// KEEP present and totals conserved (no time invented/lost by shaping).
	if p.TotalSeconds != 300 {
		t.Fatalf("total = %d, want 300", p.TotalSeconds)
	}
	if len(p.Projects) != 1 || p.Projects[0].Name != "keepProj" {
		t.Fatalf("projects = %+v, want single keepProj", p.Projects)
	}
	if len(p.Categories) != 1 || p.Categories[0].Name != "Coding" {
		t.Fatalf("categories = %+v, want single Coding", p.Categories)
	}
}
