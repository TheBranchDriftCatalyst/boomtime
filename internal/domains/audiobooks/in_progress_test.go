package audiobooks

import (
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// in_progress_test.go — pins which reading_items trigger a continuous-progress
// Hardcover push (gaka-books A): only actively in-progress titles (status
// "reading", 0 < percent < 95). Finished/want/edge-percent rows are skipped, so
// the forward sync never pushes progress for a book that isn't in progress. Pure
// (inProgressPush) — no client, no DB.

func riMin(m int) *int { return &m }

func TestInProgressPush_Selection(t *testing.T) {
	cases := []struct {
		name   string
		item   db.ReadingItem
		wantOK bool
	}{
		{"reading midway", db.ReadingItem{Status: "reading", ProgressPercent: 50}, true},
		{"reading just started", db.ReadingItem{Status: "reading", ProgressPercent: 1}, true},
		{"finished/read", db.ReadingItem{Status: "read", ProgressPercent: 100}, false},
		{"want (unstarted)", db.ReadingItem{Status: "want", ProgressPercent: 0}, false},
		{"reading at 0%", db.ReadingItem{Status: "reading", ProgressPercent: 0}, false},
		{"reading at 95% (finished floor)", db.ReadingItem{Status: "reading", ProgressPercent: 95}, false},
		{"reading at 99%", db.ReadingItem{Status: "reading", ProgressPercent: 99}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, ok := inProgressPush(tc.item)
			if ok != tc.wantOK {
				t.Errorf("inProgressPush ok = %v, want %v", ok, tc.wantOK)
			}
		})
	}
}

func TestInProgressPush_BuildsMatchAndLength(t *testing.T) {
	it := db.ReadingItem{
		Status:          "reading",
		ProgressPercent: 40,
		ExternalID:      "B07ASIN",
		AmazonASIN:      "B07AMZ",
		ISBN:            "9781234567890",
		Title:           "The Fifth Season",
		Authors:         "N. K. Jemisin",
		RuntimeMin:      riMin(600), // 10h → 36000s
	}
	in, pct, lenSeconds, ok := inProgressPush(it)
	if !ok {
		t.Fatal("expected an in-progress push")
	}
	if pct != 40 {
		t.Errorf("pct = %v, want 40", pct)
	}
	if lenSeconds != 36000 {
		t.Errorf("lenSeconds = %d, want 36000 (600min*60)", lenSeconds)
	}
	if in.ASIN != "B07ASIN" { // ExternalID preferred over AmazonASIN
		t.Errorf("ASIN = %q, want B07ASIN", in.ASIN)
	}
	if in.ISBN13 != "9781234567890" {
		t.Errorf("ISBN13 = %q", in.ISBN13)
	}
	if in.Title != "The Fifth Season" || in.Author != "N. K. Jemisin" {
		t.Errorf("title/author = %q / %q", in.Title, in.Author)
	}
}
