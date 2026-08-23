package hardcover

import (
	"encoding/json"
	"testing"
	"time"
)

// sync_test.go — pins the pull→reading_activity aggregation (boom-books B): each
// user_book_read with progress_seconds>0 or a finished_at becomes a day bucket,
// reads on the same day SUM, and a dateless/timeless read is skipped. Pure
// (aggregateReadActivity) so it needs no live client or DB.

// activityFixtureJSON: a user_books array exercising every bucketing branch.
//   - book 601: two audio reads finishing the SAME day (3600 + 1800 → 5400)
//   - book 602: an in-progress audio read (progress_seconds, no finish) on 07-11
//   - book 603: a finished print read, NO progress_seconds (finish-only → 0s bucket)
//   - book 604: a read with neither a date nor seconds (skipped entirely)
const activityFixtureJSON = `[
  {
    "id": 1, "book_id": 601, "edition_id": 10, "status_id": 3,
    "updated_at": "2026-07-15T00:00:00Z", "book": {"title":"A","slug":"a"},
    "user_book_reads": [
      {"id": 91, "started_at": "2026-07-01", "finished_at": "2026-07-05", "progress_pages": 0, "progress_seconds": 3600},
      {"id": 92, "started_at": "2026-07-02", "finished_at": "2026-07-05", "progress_pages": 0, "progress_seconds": 1800}
    ]
  },
  {
    "id": 2, "book_id": 602, "edition_id": 20, "status_id": 2,
    "updated_at": "2026-07-15T00:00:00Z", "book": {"title":"B","slug":"b"},
    "user_book_reads": [
      {"id": 93, "started_at": "2026-07-11", "finished_at": null, "progress_pages": 0, "progress_seconds": 900}
    ]
  },
  {
    "id": 3, "book_id": 603, "edition_id": 30, "status_id": 3,
    "updated_at": "2026-07-15T00:00:00Z", "book": {"title":"C","slug":"c"},
    "user_book_reads": [
      {"id": 94, "started_at": "2026-07-08", "finished_at": "2026-07-09", "progress_pages": 300, "progress_seconds": 0}
    ]
  },
  {
    "id": 4, "book_id": 604, "edition_id": 40, "status_id": 2,
    "updated_at": "2026-07-15T00:00:00Z", "book": {"title":"D","slug":"d"},
    "user_book_reads": [
      {"id": 95, "started_at": null, "finished_at": null, "progress_pages": 0, "progress_seconds": 0}
    ]
  }
]`

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestAggregateReadActivity(t *testing.T) {
	books, err := unmarshalUserBooks(json.RawMessage(activityFixtureJSON))
	if err != nil {
		t.Fatalf("unmarshalUserBooks: %v", err)
	}
	got := aggregateReadActivity(books)

	// book 601: both reads bucket on their finished_at day (07-05) → 3600+1800.
	if v := got[day(2026, 7, 5)]; v != 5400 {
		t.Errorf("2026-07-05 = %d, want 5400 (two same-day reads summed)", v)
	}
	// book 602: in-progress read placed on its started_at (no finish) → 900s.
	if v := got[day(2026, 7, 11)]; v != 900 {
		t.Errorf("2026-07-11 = %d, want 900 (in-progress read via started_at)", v)
	}
	// book 603: finish-only, no seconds → a 0-second bucket on finished_at (07-09).
	if v, ok := got[day(2026, 7, 9)]; !ok || v != 0 {
		t.Errorf("2026-07-09 = (%d, present=%v), want a present 0-second bucket", v, ok)
	}
	// book 604: no date + no seconds → never bucketed.
	if len(got) != 3 {
		t.Errorf("bucket count = %d, want 3 (dateless/timeless read skipped): %v", len(got), got)
	}
}

// TestUnmarshalUserBooks_ProgressSeconds proves the new progress_seconds field
// round-trips through the pull parser (the B feature depends on it).
func TestUnmarshalUserBooks_ProgressSeconds(t *testing.T) {
	books, err := unmarshalUserBooks(json.RawMessage(activityFixtureJSON))
	if err != nil {
		t.Fatalf("unmarshalUserBooks: %v", err)
	}
	r := books[0].Reads[0]
	if r.ProgressSeconds == nil || *r.ProgressSeconds != 3600 {
		t.Fatalf("progress_seconds = %v, want 3600", r.ProgressSeconds)
	}
	// A read whose JSON omits progress_seconds stays nil (book 603 has 0, distinct
	// from absent — here we assert the print read's page value still parses).
	if books[2].Reads[0].ProgressPages == nil || *books[2].Reads[0].ProgressPages != 300 {
		t.Fatalf("progress_pages = %v, want 300", books[2].Reads[0].ProgressPages)
	}
}
