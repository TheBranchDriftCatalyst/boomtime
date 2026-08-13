// kindle_reading_test.go — the unification contract (gaka-books): once the
// forward Kindle reading-time composition writes reading_activity(source='kindle'),
// the reading `seconds` measure sums Kindle reading-time ALONGSIDE Audible
// listening-time, grouped by source. This pins that a kindle row shows up in the
// seconds/by-source result — the whole point of writing listening_seconds under
// source='kindle'.
package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/query"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

func TestReading_SecondsBySource_UnifiesKindleAndAudible(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_reading_kindle")
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	// Audible listening-time + Kindle reading-time land in the SAME table
	// (reading_activity), distinguished only by source.
	seedActivity(t, hz, owner, "audible", now, 1800) // 30 min listening
	seedActivity(t, hz, owner, "kindle", now, 600)   // 10 min reading
	seedActivity(t, hz, owner, "kindle", now.AddDate(0, 0, -1), 300)

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Measure("seconds").Group("source"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	bySource := map[string]float64{}
	for _, g := range res.Groups {
		bySource[g.Key] = g.Value
	}
	if _, ok := bySource["kindle"]; !ok {
		t.Fatalf("kindle source ABSENT from seconds/by-source result %v — reading-time did NOT unify", bySource)
	}
	if bySource["kindle"] != 900 { // 600 + 300, both kindle rows summed
		t.Errorf("kindle seconds = %v, want 900", bySource["kindle"])
	}
	if bySource["audible"] != 1800 {
		t.Errorf("audible seconds = %v, want 1800", bySource["audible"])
	}
}
