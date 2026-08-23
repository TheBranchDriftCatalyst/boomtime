package audible

import (
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// in_progress_test.go — pins which reading_items trigger a continuous-progress
// Hardcover push (boom-books A): only actively in-progress titles (status
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

// TestInProgressPushMatched_Dedup pins the anti-flood skip/push predicate: only
// a MATCHED row whose percent MOVED since the last real push gets pushed (with
// its stored book/edition ids); a matched+unchanged row and an unmatched row are
// both skipped — the latter so the push loop never re-runs the match ladder.
func TestInProgressPushMatched_Dedup(t *testing.T) {
	book := func(id int64) *int64 { return &id }
	pct := func(p int) *int { return &p }

	cases := []struct {
		name        string
		item        db.ReadingItem
		wantDo      bool
		wantBookID  int64
		wantEdition int64
		wantPct     int
	}{
		{
			name:        "matched + never pushed → push",
			item:        db.ReadingItem{Status: "reading", ProgressPercent: 50, HardcoverBookID: book(556), HardcoverEditionID: book(8802)},
			wantDo:      true,
			wantBookID:  556,
			wantEdition: 8802,
			wantPct:     50,
		},
		{
			name:        "matched + progress moved → push",
			item:        db.ReadingItem{Status: "reading", ProgressPercent: 50, HardcoverBookID: book(556), HardcoverEditionID: book(8802), HardcoverPushedProgress: pct(40)},
			wantDo:      true,
			wantBookID:  556,
			wantEdition: 8802,
			wantPct:     50,
		},
		{
			name:   "matched + unchanged (pushed==pct) → skip",
			item:   db.ReadingItem{Status: "reading", ProgressPercent: 50, HardcoverBookID: book(556), HardcoverPushedProgress: pct(50)},
			wantDo: false,
		},
		{
			name:   "unmatched (no book id) → skip, never re-match",
			item:   db.ReadingItem{Status: "reading", ProgressPercent: 50},
			wantDo: false,
		},
		{
			name:   "matched but finished → skip",
			item:   db.ReadingItem{Status: "read", ProgressPercent: 100, HardcoverBookID: book(556)},
			wantDo: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bookID, editionID, p, _, do := inProgressPushMatched(tc.item)
			if do != tc.wantDo {
				t.Fatalf("do = %v, want %v", do, tc.wantDo)
			}
			if !do {
				return
			}
			if bookID != tc.wantBookID {
				t.Errorf("bookID = %d, want %d", bookID, tc.wantBookID)
			}
			if editionID != tc.wantEdition {
				t.Errorf("editionID = %d, want %d", editionID, tc.wantEdition)
			}
			if p != tc.wantPct {
				t.Errorf("pct = %d, want %d", p, tc.wantPct)
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
