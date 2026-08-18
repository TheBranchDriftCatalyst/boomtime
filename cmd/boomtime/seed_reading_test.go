// seed_reading_test.go: unit + end-to-end smoke coverage for
// `boomtime seed-reading-demo`.
//
//   - TestSeedReadingAllowed pins the dev/test-only gate (+ --force override).
//   - TestBuildDemoReadingItems verifies the intrinsic fixture shape WITHOUT a
//     DB (status split, finishes spread across distinct months, in-progress
//     books strictly below the "looks finished" line, series + genres present).
//   - TestSeedReadingDemoSmoke drives runSeedReadingDemo against the isolated
//     test DB and asserts the rows actually landed + the run is idempotent.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

func TestSeedReadingAllowed(t *testing.T) {
	cases := []struct {
		env   string
		force bool
		want  bool
	}{
		{"dev", false, true},
		{"DEV", false, true},
		{"test", false, true},
		{"Test", false, true},
		{"prod", false, false},
		{"production", false, false},
		{"staging", false, false},
		{"", false, false},
		{"prod", true, true}, // --force overrides
	}
	for _, c := range cases {
		if got := seedReadingAllowed(c.env, c.force); got != c.want {
			t.Errorf("seedReadingAllowed(%q, force=%v) = %v, want %v", c.env, c.force, got, c.want)
		}
	}
}

func TestBuildDemoReadingItems(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	items := buildDemoReadingItems("demo", now)

	if len(items) < 30 {
		t.Fatalf("expected >30 reading items, got %d", len(items))
	}

	var read, reading, want, finished int
	months := map[string]struct{}{}
	series := map[string]struct{}{}
	genres := map[string]struct{}{}
	for _, it := range items {
		switch it.Status {
		case "read":
			read++
		case "reading":
			reading++
			// A "reading" book must be genuinely in-progress, never look finished.
			if it.ProgressPercent <= 0 || it.ProgressPercent >= 95 {
				t.Errorf("reading item %q has progress %d — must be in (0,95)", it.Title, it.ProgressPercent)
			}
			if it.Finished {
				t.Errorf("reading item %q is marked finished", it.Title)
			}
		case "want":
			want++
		default:
			t.Errorf("item %q has unexpected status %q", it.Title, it.Status)
		}
		if it.Finished {
			finished++
			if it.FinishedAt == nil {
				t.Errorf("finished item %q has nil FinishedAt", it.Title)
			} else {
				months[it.FinishedAt.Format("2006-01")] = struct{}{}
				if it.FinishedAt.After(now) {
					t.Errorf("finished item %q finished in the future: %v", it.Title, it.FinishedAt)
				}
			}
		}
		if it.Series != "" {
			series[it.Series] = struct{}{}
		}
		// genres is a JSON array; first entry is the primary genre.
		var g []string
		if err := json.Unmarshal(it.Genres, &g); err != nil || len(g) == 0 {
			t.Errorf("item %q has bad genres json: %v (%s)", it.Title, err, it.Genres)
		} else {
			genres[g[0]] = struct{}{}
		}
	}

	if read < 20 {
		t.Errorf("expected many read items, got %d", read)
	}
	if reading == 0 {
		t.Errorf("expected some in-progress items, got 0")
	}
	if want == 0 {
		t.Errorf("expected some want items, got 0")
	}
	if len(months) < 6 {
		t.Errorf("finishes should spread across many months, got %d distinct", len(months))
	}
	if len(series) < 3 {
		t.Errorf("expected >=3 distinct series, got %d", len(series))
	}
	if len(genres) < 6 {
		t.Errorf("expected >=6 distinct genres, got %d", len(genres))
	}
}

func TestBuildDemoReadingActivity(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	acts := buildDemoReadingActivity("demo", now)

	var monthly, daily int
	var recentDailySecs int64
	weekAgo := now.AddDate(0, 0, -7)
	for _, a := range acts {
		switch a.Granularity {
		case "month":
			monthly++
		case "day":
			daily++
			if !a.BucketDate.Before(weekAgo) {
				recentDailySecs += a.ListeningSeconds
			}
		}
		if a.ListeningSeconds <= 0 {
			t.Errorf("bucket %s has non-positive listening_seconds", a.BucketDate.Format("2006-01-02"))
		}
	}
	if monthly < 12 {
		t.Errorf("expected 12 monthly buckets, got %d", monthly)
	}
	if daily == 0 {
		t.Errorf("expected recent daily buckets, got 0")
	}
	if recentDailySecs == 0 {
		t.Errorf("expected non-zero listening in the last week, got 0")
	}
}

// TestSeedReadingDemoSmoke seeds a user + runs the pipeline against the isolated
// test DB, then asserts the rows landed and a re-run is idempotent (no dupes).
func TestSeedReadingDemoSmoke(t *testing.T) {
	database := testutil.OpenIsolatedDB(t, "readingseed")
	ctx := context.Background()

	user := fmt.Sprintf("seedreading_%d", time.Now().UnixNano())
	seedUser(t, database, user)
	t.Cleanup(func() {
		_, _ = database.Pool.Exec(context.Background(), `DELETE FROM reading_items WHERE owner=$1`, user)
		_, _ = database.Pool.Exec(context.Background(), `DELETE FROM reading_activity WHERE owner=$1`, user)
	})

	dsn := isolatedDBURL("readingseed")

	var buf bytes.Buffer
	if err := runSeedReadingDemo(ctx, dsn, user, &buf); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	if !strings.Contains(buf.String(), "Seeded reading demo") {
		t.Fatalf("missing summary line: %q", buf.String())
	}

	items, err := database.CountReadingItems(ctx, user, "")
	if err != nil {
		t.Fatalf("count items: %v", err)
	}
	if items <= 30 {
		t.Fatalf("expected >30 reading_items, got %d", items)
	}

	var activity int
	if err := database.Pool.QueryRow(ctx,
		`SELECT count(*) FROM reading_activity WHERE owner=$1`, user).Scan(&activity); err != nil {
		t.Fatalf("count activity: %v", err)
	}
	if activity <= 12 {
		t.Fatalf("expected >12 reading_activity buckets, got %d", activity)
	}

	var distinctMonths, readingUnfinished int
	if err := database.Pool.QueryRow(ctx,
		`SELECT count(DISTINCT date_trunc('month', finished_at)) FROM reading_items WHERE owner=$1 AND finished`, user).
		Scan(&distinctMonths); err != nil {
		t.Fatalf("distinct months: %v", err)
	}
	if distinctMonths < 6 {
		t.Fatalf("expected finishes across >=6 months, got %d", distinctMonths)
	}
	if err := database.Pool.QueryRow(ctx,
		`SELECT count(*) FROM reading_items WHERE owner=$1 AND status='reading' AND progress_percent < 95`, user).
		Scan(&readingUnfinished); err != nil {
		t.Fatalf("reading count: %v", err)
	}
	if readingUnfinished == 0 {
		t.Fatalf("expected some genuinely in-progress (<95%%) reading items, got 0")
	}

	// Idempotent: a second run upserts, so counts are unchanged.
	buf.Reset()
	if err := runSeedReadingDemo(ctx, dsn, user, &buf); err != nil {
		t.Fatalf("second seed run: %v", err)
	}
	items2, err := database.CountReadingItems(ctx, user, "")
	if err != nil {
		t.Fatalf("recount items: %v", err)
	}
	if items2 != items {
		t.Fatalf("non-idempotent: item count changed from %d to %d", items, items2)
	}
}
