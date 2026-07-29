// ai_activity_ginkgo_test.go — ginkgo mirror of ai_activity_test.go (gaka-0vp.13).
// 1:1 case map (4 stdlib TestXxx → 4 Its):
//
//	TestAIActivityFiltersNonAIHeartbeats             → "filters heartbeats with no AI signal"
//	TestAIActivitySessionCountDeduplicatesAcrossDays → "session count dedupes across days"
//	TestAIActivityEmptyRangeHasFalseData             → "empty range → HasData=false + Days=[]"
//	TestAIActivityOwnerScoping                       → "owner scoping: other user's rows don't leak"
package db

import (
	"context"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// insertAIHBG inserts one heartbeat carrying AI-signal columns (ginkgo variant).
func insertAIHBG(d *DB, ctx context.Context, sender string, ts time.Time, session *string, in, out, aiLines, humanLines int64) {
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO heartbeats
		  (sender, entity, ty, time_sent, user_agent, gap_seconds,
		   ai_input_tokens, ai_output_tokens, ai_line_changes, human_line_changes, ai_session)
		VALUES ($1, 'a.go', 'file', $2, 'ua', 60,
		        $3, $4, $5, $6, $7)`,
		sender, ts, in, out, aiLines, humanLines, session)
	Expect(err).NotTo(HaveOccurred())
}

// insertPlainHBG inserts a heartbeat with NO AI columns set (ginkgo variant).
func insertPlainHBG(d *DB, ctx context.Context, sender string, ts time.Time) {
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO heartbeats
		  (sender, entity, ty, time_sent, user_agent, gap_seconds)
		VALUES ($1, 'plain.go', 'file', $2, 'ua', 60)`,
		sender, ts)
	Expect(err).NotTo(HaveOccurred())
}

var _ = ginkgo.Describe("GetAIActivity", func() {
	ginkgo.It("filters out heartbeats with no AI signal from every aggregate", func() {
		d := openTestDBG()
		f := newSenderG(d, "ai_filter")
		sender := f.Sender()
		ctx := f.Ctx()

		day := time.Date(2025, 5, 1, 10, 0, 0, 0, time.UTC)

		sess := "sess-1"
		insertAIHBG(d, ctx, sender, day, &sess, 10, 20, 5, 3)
		insertPlainHBG(d, ctx, sender, day.Add(time.Minute))
		// A row with any single AI column non-null still qualifies (only-tokens variant).
		insertAIHBG(d, ctx, sender, day.Add(2*time.Minute), nil, 15, 0, 0, 0)

		start := day.AddDate(0, 0, -1)
		end := day.AddDate(0, 0, 1)

		sum, err := d.GetAIActivity(ctx, sender, start, end)
		Expect(err).NotTo(HaveOccurred())
		Expect(sum.HasData).To(BeTrue())
		Expect(sum.HeartbeatsWithAI).To(BeEquivalentTo(2), "plain hb must be filtered")
		Expect(sum.TotalInputTokens).To(BeEquivalentTo(25), "10+15")
		Expect(sum.TotalOutputTokens).To(BeEquivalentTo(20))
		Expect(sum.TotalAILineChanges).To(BeEquivalentTo(5))
		Expect(sum.TotalHumanLineChanges).To(BeEquivalentTo(3))
	})

	ginkgo.It("TotalSessions is DISTINCT across the whole range (multi-day session counts once)", func() {
		d := openTestDBG()
		f := newSenderG(d, "ai_dedup")
		sender := f.Sender()
		ctx := f.Ctx()

		day1 := time.Date(2025, 5, 2, 10, 0, 0, 0, time.UTC)
		day2 := day1.AddDate(0, 0, 1)

		shared := "sess-multi-day"
		insertAIHBG(d, ctx, sender, day1, &shared, 5, 5, 0, 0)
		insertAIHBG(d, ctx, sender, day2, &shared, 5, 5, 0, 0)
		only1 := "sess-one-day"
		insertAIHBG(d, ctx, sender, day1.Add(time.Hour), &only1, 5, 5, 0, 0)

		start := day1.AddDate(0, 0, -1)
		end := day2.AddDate(0, 0, 1)

		sum, err := d.GetAIActivity(ctx, sender, start, end)
		Expect(err).NotTo(HaveOccurred())
		Expect(sum.TotalSessions).To(BeEquivalentTo(2), "a multi-day session must not double-count")
	})

	ginkgo.It("returns HasData=false + Days=[] (not nil) when the range has no AI rows", func() {
		d := openTestDBG()
		f := newSenderG(d, "ai_empty")
		sender := f.Sender()
		ctx := f.Ctx()

		day := time.Date(2025, 5, 3, 10, 0, 0, 0, time.UTC)
		insertPlainHBG(d, ctx, sender, day)

		sum, err := d.GetAIActivity(ctx, sender, day.AddDate(0, 0, -1), day.AddDate(0, 0, 1))
		Expect(err).NotTo(HaveOccurred())
		Expect(sum.HasData).To(BeFalse())
		Expect(sum.HeartbeatsWithAI).To(BeEquivalentTo(0))
		Expect(sum.Days).NotTo(BeNil(), "Days is nil; must be an empty slice for stable JSON marshaling")
	})

	ginkgo.It("owner scoping: another user's AI heartbeats never leak into this user's summary", func() {
		d := openTestDBG()
		a := newSenderG(d, "ai_ownA")
		b := newSenderG(d, "ai_ownB")

		day := time.Date(2025, 5, 4, 10, 0, 0, 0, time.UTC)
		sa, sb := "a-sess", "b-sess"
		insertAIHBG(d, a.Ctx(), a.Sender(), day, &sa, 100, 200, 5, 3)
		insertAIHBG(d, b.Ctx(), b.Sender(), day, &sb, 999, 999, 99, 99)

		sum, err := d.GetAIActivity(a.Ctx(), a.Sender(), day.AddDate(0, 0, -1), day.AddDate(0, 0, 1))
		Expect(err).NotTo(HaveOccurred())
		Expect(sum.TotalInputTokens).To(BeEquivalentTo(100), "B's 999 must not leak")
		Expect(sum.TotalSessions).To(BeEquivalentTo(1), "B's session must not leak")
		Expect(sum.HeartbeatsWithAI).To(BeEquivalentTo(1))
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
func insertAIHB(t *testing.T, d *DB, ctx context.Context, sender string, ts time.Time, session *string, in, out, aiLines, humanLines int64) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx, `
		INSERT INTO heartbeats
		  (sender, entity, ty, time_sent, user_agent, gap_seconds,
		   ai_input_tokens, ai_output_tokens, ai_line_changes, human_line_changes, ai_session)
		VALUES ($1, 'a.go', 'file', $2, 'ua', 60,
		        $3, $4, $5, $6, $7)`,
		sender, ts, in, out, aiLines, humanLines, session); err != nil {
		t.Fatal(err)
	}
}

func insertPlainHB(t *testing.T, d *DB, ctx context.Context, sender string, ts time.Time) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx, `
		INSERT INTO heartbeats
		  (sender, entity, ty, time_sent, user_agent, gap_seconds)
		VALUES ($1, 'plain.go', 'file', $2, 'ua', 60)`,
		sender, ts); err != nil {
		t.Fatal(err)
	}
}
