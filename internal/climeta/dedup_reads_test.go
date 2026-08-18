package climeta

import (
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/hardcover"
)

func tp(y, m, d int) *time.Time {
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return &t
}

// TestReadsToDelete pins the keep-policy: drop dateless when a dated read exists,
// collapse exact-duplicate dates, keep legit distinct reads, keep one when all
// dateless.
func TestReadsToDelete(t *testing.T) {
	cases := []struct {
		name  string
		reads []hardcover.UserBookRead
		want  []int64 // ids expected to be deleted (order-insensitive)
	}{
		{
			name: "dateless dropped when a dated read exists",
			reads: []hardcover.UserBookRead{
				{ID: 1, FinishedAt: tp(2026, 8, 1)},
				{ID: 2}, // dateless
				{ID: 3}, // dateless
			},
			want: []int64{2, 3},
		},
		{
			name: "legit distinct re-reads are kept",
			reads: []hardcover.UserBookRead{
				{ID: 1, FinishedAt: tp(2020, 1, 1)},
				{ID: 2, FinishedAt: tp(2026, 8, 1)},
			},
			want: nil,
		},
		{
			name: "exact-duplicate dates collapse to the lowest id",
			reads: []hardcover.UserBookRead{
				{ID: 5, FinishedAt: tp(2026, 8, 1)},
				{ID: 9, FinishedAt: tp(2026, 8, 1)},
			},
			want: []int64{9},
		},
		{
			name: "all dateless keeps one (lowest id)",
			reads: []hardcover.UserBookRead{
				{ID: 7}, {ID: 3}, {ID: 5},
			},
			want: []int64{5, 7},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readsToDelete(tc.reads)
			set := map[int64]bool{}
			for _, id := range got {
				set[id] = true
			}
			if len(got) != len(tc.want) {
				t.Fatalf("delete ids = %v, want %v", got, tc.want)
			}
			for _, id := range tc.want {
				if !set[id] {
					t.Errorf("expected %d in delete set %v", id, got)
				}
			}
		})
	}
}
