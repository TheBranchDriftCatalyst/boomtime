// eval_reading_integration_test.go — DB-backed eval of the reading-source
// time leaf (gaka reading-goal). Additive to eval_integration_test.go; the
// coding path there is unchanged. Seeds reading_activity rows and asserts the
// evaluator sums listening_seconds over the goal window (met + not-met) and
// that the sum is owner-scoped and window-bounded.
package goals_test

import (
	"context"
	"encoding/json"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/goals"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// seedReadingRowG inserts one reading_activity bucket. UNIQUE is
// (owner, source, bucket_date, granularity); we upsert listening_seconds so a
// re-seed of the same bucket is idempotent.
func seedReadingRowG(hz *testutil.Harness, owner string, day time.Time, source string, seconds int64) {
	GinkgoHelper()
	_, err := hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO reading_activity (owner, source, granularity, bucket_date, listening_seconds)
		VALUES ($1, $2, 'day', $3::date, $4)
		ON CONFLICT (owner, source, bucket_date, granularity)
		DO UPDATE SET listening_seconds = EXCLUDED.listening_seconds`,
		owner, source, day, seconds)
	Expect(err).NotTo(HaveOccurred(), "seed reading_activity")
}

// seedReadingItemG inserts one reading_items row. UNIQUE is
// (owner, source, external_id); each call passes a distinct external_id so rows
// accumulate. finished_at is the DateCol the runtime measure buckets on; a nil
// finishedAt seeds an unfinished book (must never contribute to a runtime goal).
func seedReadingItemG(hz *testutil.Harness, owner, extID, genre, series string, runtimeMin int, finishedAt *time.Time) {
	GinkgoHelper()
	_, err := hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO reading_items
			(owner, source, external_id, title, authors, status, series, runtime_min, genres, finished, finished_at)
		VALUES ($1, 'audible', $2, $2, 'Some Author', 'finished', $3, $4,
		        jsonb_build_array($5::text), $6, $7)`,
		owner, extID, series, runtimeMin, genre, finishedAt != nil, finishedAt)
	Expect(err).NotTo(HaveOccurred(), "seed reading_items")
}

var _ = Describe("Evaluate genre'd reading time leaf (DB integration, gaka-dvy9)", func() {
	// A genre'd reading goal sums runtime_min (→ seconds) of books with
	// genre=value FINISHED in the window. It must EXCLUDE: other genres, books
	// finished outside the window, and unfinished books.
	It("sums runtime of fiction books finished in the window (met + not-met); excludes other genre / out-of-window / unfinished", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_genre_reading")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		inWin1 := now.AddDate(0, 0, -1)  // 07-14 — in the 7-day window
		inWin2 := now.AddDate(0, 0, -3)  // 07-12 — in
		outWin := now.AddDate(0, 0, -20) // 06-25 — out

		// Fiction, finished in-window: 120 + 60 = 180 min → 10800 s.
		seedReadingItemG(hz, owner, "asin-f1", "Fiction", "Foundation", 120, &inWin1)
		seedReadingItemG(hz, owner, "asin-f2", "Fiction", "Dune", 60, &inWin2)
		// Non-fiction finished in-window — must NOT count toward genre=Fiction.
		seedReadingItemG(hz, owner, "asin-nf", "Non-Fiction", "History", 300, &inWin1)
		// Fiction finished OUT of window — excluded by finished_at range.
		seedReadingItemG(hz, owner, "asin-f3", "Fiction", "Neuromancer", 999, &outWin)
		// Fiction but UNFINISHED (finished_at NULL) — excluded (no DateCol value).
		seedReadingItemG(hz, owner, "asin-f4", "Fiction", "Snow Crash", 999, nil)

		const inWindowSecs = int64(180 * 60) // 10800

		// not-met: target 5h (18000s) > 3h fiction → hit=false, progress=0.6.
		notMet := `{"kind":"time","source":"reading","axis":"genre","value":"Fiction","op":">=","target_seconds":18000,"window":"week"}`
		p, err := goals.ValidateSpec(json.RawMessage(notMet))
		Expect(err).NotTo(HaveOccurred())
		prog, err := goals.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions).To(HaveLen(1))
		sc := prog.SubConditions[0]
		Expect(sc.Source).To(Equal("reading"))
		Expect(sc.Axis).To(Equal("genre"))
		Expect(sc.Value).NotTo(BeNil())
		Expect(*sc.Value).To(Equal("Fiction"))
		Expect(sc.Current).To(BeEquivalentTo(inWindowSecs),
			"must sum only the two in-window FINISHED fiction books (120+60 min), excluding non-fiction / out-of-window / unfinished")
		Expect(prog.Hit).To(BeFalse())
		Expect(prog.Progress).To(BeNumerically("~", float64(inWindowSecs)/18000.0, 1e-9))

		// met: target 2h (7200s) <= 3h fiction → hit=true, progress=1.
		met := `{"kind":"time","source":"reading","axis":"genre","value":"Fiction","op":">=","target_seconds":7200,"window":"week"}`
		p2, err := goals.ValidateSpec(json.RawMessage(met))
		Expect(err).NotTo(HaveOccurred())
		prog2, err := goals.Evaluate(context.Background(), hz.DB.Pool, owner, p2, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog2.SubConditions[0].Current).To(BeEquivalentTo(inWindowSecs))
		Expect(prog2.Hit).To(BeTrue())
		Expect(prog2.Progress).To(Equal(float64(1)))
	})

	// Case-insensitive genre match (the query compiler lower()s both sides):
	// a goal for "fiction" counts a book tagged "Fiction".
	It("matches the genre case-insensitively", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_genre_ci")
		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		fin := now.AddDate(0, 0, -1)
		seedReadingItemG(hz, owner, "asin-1", "Fiction", "S", 90, &fin)

		spec := `{"kind":"time","source":"reading","axis":"genre","value":"fiction","op":">=","target_seconds":1,"window":"week"}`
		p, err := goals.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())
		prog, err := goals.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions[0].Current).To(BeEquivalentTo(int64(90 * 60)))
		Expect(prog.Hit).To(BeTrue())
	})
})

var _ = Describe("Evaluate reading-source time leaf (DB integration)", func() {
	// A weekly reading goal sums listening_seconds inside the 7-day window and
	// EXCLUDES a row outside it. We assert both the not-met and met cases
	// against the identical seeded data so the summed Current is load-bearing.
	It("sums listening_seconds over the week window; excludes out-of-window rows (met + not-met)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_reading")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		// In-window: 1h + 2h across two days from two sources = 10800s (3h).
		seedReadingRowG(hz, owner, now.AddDate(0, 0, -1), "audible", 3600)
		seedReadingRowG(hz, owner, now.AddDate(0, 0, -3), "kindle", 7200)
		// OUT of the 7-day window (today-20) — must NOT contribute.
		seedReadingRowG(hz, owner, now.AddDate(0, 0, -20), "audible", 99999)

		const inWindow = int64(10800)

		// not-met: target 5h (18000s) > 3h summed → hit=false, progress=0.6.
		notMet := `{"kind":"time","source":"reading","op":">=","target_seconds":18000,"window":"week"}`
		p, err := goals.ValidateSpec(json.RawMessage(notMet))
		Expect(err).NotTo(HaveOccurred())
		prog, err := goals.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions).To(HaveLen(1))
		sc := prog.SubConditions[0]
		Expect(sc.Source).To(Equal("reading"))
		Expect(sc.Current).To(BeEquivalentTo(inWindow),
			"must sum only the two in-window rows (3600+7200), excluding the today-20 row")
		Expect(sc.Target).To(BeEquivalentTo(18000))
		Expect(prog.Hit).To(BeFalse())
		Expect(prog.Progress).To(BeNumerically("~", float64(inWindow)/18000.0, 1e-9))

		// met: target 2h (7200s) <= 3h summed → hit=true, progress=1.
		met := `{"kind":"time","source":"reading","op":">=","target_seconds":7200,"window":"week"}`
		p2, err := goals.ValidateSpec(json.RawMessage(met))
		Expect(err).NotTo(HaveOccurred())
		prog2, err := goals.Evaluate(context.Background(), hz.DB.Pool, owner, p2, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog2.SubConditions[0].Current).To(BeEquivalentTo(inWindow))
		Expect(prog2.Hit).To(BeTrue())
		Expect(prog2.Progress).To(Equal(float64(1)))
	})

	// Owner scoping: a reading goal for ownerB must see 0 seconds even though
	// ownerA has plenty of listening time in the window.
	It("owner filter present on the reading query (cross-owner sees 0)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		ownerA, _ := hz.MintUser("eval_reading_a")
		ownerB, _ := hz.MintUser("eval_reading_b")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		seedReadingRowG(hz, ownerA, now.AddDate(0, 0, -1), "audible", 5000)

		spec := `{"kind":"time","source":"reading","op":">=","target_seconds":1,"window":"week"}`
		p, err := goals.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())

		progA, err := goals.Evaluate(context.Background(), hz.DB.Pool, ownerA, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(progA.SubConditions[0].Current).To(BeEquivalentTo(5000))

		progB, err := goals.Evaluate(context.Background(), hz.DB.Pool, ownerB, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(progB.SubConditions[0].Current).To(BeEquivalentTo(0),
			"owner-scoping breach — reading query lost the owner filter")
	})

	// A reading leaf composes with the boolean combinators exactly like a
	// coding leaf: AND with a coding leaf, progress = min(children). This proves
	// the two sources coexist in one tree without the coding path regressing.
	It("composes with a coding leaf under `all` (min of children)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_reading_mix")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		// Reading: 3600s in-window → vs target 3600 → child progress 1.0, hit.
		seedReadingRowG(hz, owner, now.AddDate(0, 0, -1), "audible", 3600)
		// Coding: 900s Go in-window → vs target 3600 → child progress 0.25, miss.
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -2), "P", "Go", "vim", 900)

		spec := `{"kind":"all","of":[
			{"kind":"time","source":"reading","op":">=","target_seconds":3600,"window":"week"},
			{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":3600,"window":"week"}
		]}`
		p, err := goals.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())
		prog, err := goals.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions).To(HaveLen(2))
		Expect(prog.Hit).To(BeFalse(), "coding child misses → all fails")
		Expect(prog.Progress).To(Equal(0.25), "min(reading=1.0, coding=0.25)")
	})
})
