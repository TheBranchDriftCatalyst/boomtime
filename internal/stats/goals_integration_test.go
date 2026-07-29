// goals_integration_ginkgo_test.go — ginkgo mirror of goals_integration_test.go (gaka-tst-ginkgo).
// 1:1 case map (14 stdlib TestXxx, all DB-backed via testutil.Harness):
//
//	TestEvaluate_LeafTimeCaseFold          → Evaluate leaf > "sums mixed-case axis values via lower(col)=lower($n)"
//	TestEvaluate_LeafTimeNilValue          → Evaluate leaf > "value=null means any-value-on-axis (sum across values)"
//	TestEvaluate_ActiveDays                → Evaluate active_days > "counts distinct days (dedup same-day double-seed)"
//	TestEvaluate_StreakStopsAtGap          → Evaluate streak > "stops at first gap; counts only consecutive from today"
//	TestEvaluate_AllComposition            → Evaluate all > "hit=false when one child misses; progress = min(children)"
//	TestEvaluate_AnyComposition            → Evaluate any > "hit=true when one child hits; progress = max(children)"
//	TestEvaluate_NotInverts                → Evaluate not > "wrapping passing leaf → not-hit; wrapping failing leaf → hit"
//	TestEvaluate_StreakMinDaysZero         → Evaluate streak > "min_days<=0 short-circuits: (true, 1), no child query"
//	TestEvaluate_StreakTodayMissesReturnsZero → Evaluate streak > "no data → current=0 (initial daysHit=0)"
//	TestEvaluate_StreakExactlyMinDaysHits  → Evaluate streak > "walk cap = min_days; extras beyond don't count"
//	TestEvaluate_ActiveDaysOwnerScoping    → Evaluate active_days > "owner filter present on active_days SQL"
//	TestEvaluate_TimeLeafDayWindow         → Evaluate leaf > "day window covers only TODAY; excludes yesterday"
//	TestEvaluate_LifetimeIncludesAncient   → Evaluate leaf > "lifetime window includes ancient rows (start pinned at epoch)"
//	TestEvaluate_NotProgressInversion      → Evaluate not > "progress = 1 - child.progress (arithmetic, not 1-bool)"
//	TestEvaluate_OwnerScoping              → Evaluate leaf > "owner filter present on leaf SQL"
package stats_test

import (
	"context"
	"encoding/json"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// seedRollupRowG inserts one hb_rollup_daily row via the ginkgo bridge.
// Same INSERT as goals_integration_test.go's seedRollupRow but uses the
// harness's testing.TB (GinkgoTB) instead of *testing.T so it works from
// inside It blocks.
func seedRollupRowG(hz *testutil.Harness, owner string, day time.Time, project, language, editor string, seconds int64) {
	GinkgoHelper()
	ctx := context.Background()
	_, err := hz.DB.Pool.Exec(ctx, `
		INSERT INTO hb_rollup_daily (sender, day, project, language, editor,
			platform, machine, category, plugin, branch, total_seconds)
		VALUES ($1, $2::date, $3, $4, $5, 'linux', 'm', 'Coding', 'pl', 'main', $6)
		ON CONFLICT (sender, day, project, language, editor, platform, machine, category, plugin, branch)
		DO UPDATE SET total_seconds = EXCLUDED.total_seconds`,
		owner, day, project, language, editor, seconds)
	Expect(err).NotTo(HaveOccurred(), "seed rollup")
}

var _ = Describe("Evaluate (DB integration, gaka-tst-ginkgo)", func() {
	// TestEvaluate_LeafTimeCaseFold
	It("leaf sums mixed-case axis values via lower(col)=lower($n)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_leaf")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -1), "P", "Python", "vim", 1800)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -3), "P", "python", "vim", 1200)
		// One day OUTSIDE the week window (should NOT contribute).
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -20), "P", "Python", "vim", 9999)

		spec := `{"kind":"time","axis":"language","value":"Python","op":">=","target_seconds":3000,"window":"week"}`
		p, err := stats.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())
		prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions).To(HaveLen(1))
		sc := prog.SubConditions[0]
		// If case-fold regressed, we'd see 1800 (only "Python") or 1200 (only "python"), not 3000.
		Expect(sc.Current).To(BeEquivalentTo(3000),
			"case-fold regression: sum should be 1800+1200 across mixed-case Python rows")
		Expect(prog.Hit).To(BeTrue())
		Expect(prog.Progress).To(Equal(float64(1)))
	})

	// TestEvaluate_LeafTimeNilValue
	It("leaf value=null means any-value-on-axis (sum across values)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_leafnil")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -1), "P", "Go", "vim", 1000)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -2), "P", "Rust", "vim", 2000)

		spec := `{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":2500,"window":"week"}`
		p, err := stats.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())
		prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions[0].Current).To(BeEquivalentTo(3000), "Go + Rust")
		Expect(prog.Hit).To(BeTrue())
	})

	// TestEvaluate_ActiveDays
	It("active_days counts DISTINCT days (dedup same-day double-seed)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_activedays")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		// Two rows on the SAME day to prove distinct dedupes.
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -1), "A", "Go", "vim", 600)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -1), "B", "Rust", "vim", 600)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -3), "A", "Go", "vim", 600)

		spec := `{"kind":"active_days","op":">=","n":2,"window":"week"}`
		p, _ := stats.ValidateSpec(json.RawMessage(spec))
		prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions[0].Current).To(BeEquivalentTo(2), "dedup on double-seed same day")
		Expect(prog.Hit).To(BeTrue())
	})

	// TestEvaluate_StreakStopsAtGap
	It("streak stops at first gap; counts only consecutive from today", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_streak")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		for _, offset := range []int{0, 1, 2} {
			seedRollupRowG(hz, owner, now.AddDate(0, 0, -offset), "P", "Go", "vim", 900)
		}
		// today-3: GAP.
		for _, offset := range []int{4, 5, 6, 7} {
			seedRollupRowG(hz, owner, now.AddDate(0, 0, -offset), "P", "Go", "vim", 900)
		}

		spec := `{"kind":"streak","min_days":7,"condition":{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":600,"window":"day"}}`
		p, err := stats.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())
		prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions).To(HaveLen(1))
		sc := prog.SubConditions[0]
		Expect(sc.Current).To(BeEquivalentTo(3), "walk must stop at the gap")
		Expect(sc.Hit).To(BeFalse(), "streak of 3 must NOT hit target 7")
		Expect(prog.Progress).To(Equal(3.0 / 7.0))
	})

	// TestEvaluate_AllComposition
	It("all: hit=false when one child misses; progress = min(children)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_all")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -1), "P", "Go", "vim", 1000)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -2), "P", "Rust", "vim", 500)

		spec := `{"kind":"all","of":[
			{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":500,"window":"week"},
			{"kind":"time","axis":"language","value":"Rust","op":">=","target_seconds":2000,"window":"week"}
		]}`
		p, err := stats.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())
		prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.Hit).To(BeFalse())
		Expect(prog.Progress).To(Equal(0.25), "min of children (max/avg would differ)")
		Expect(prog.SubConditions).To(HaveLen(2))
	})

	// TestEvaluate_AnyComposition
	It("any: hit=true when one child hits; progress = max(children)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_any")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -1), "P", "Go", "vim", 1000)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -2), "P", "Rust", "vim", 500)

		spec := `{"kind":"any","of":[
			{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":500,"window":"week"},
			{"kind":"time","axis":"language","value":"Rust","op":">=","target_seconds":2000,"window":"week"}
		]}`
		p, err := stats.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())
		prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.Hit).To(BeTrue())
		Expect(prog.Progress).To(Equal(float64(1)), "max of children — passing Go leaf is at 1")
	})

	// TestEvaluate_NotInverts
	It("not: wrapping passing leaf → not-hit; wrapping failing leaf → hit", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_not")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -1), "YT", "None", "browser", 3600)

		spec1 := `{"kind":"not","of":[
			{"kind":"time","axis":"project","value":"YT","op":">=","target_seconds":60,"window":"week"}
		]}`
		p1, _ := stats.ValidateSpec(json.RawMessage(spec1))
		prog1, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p1, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog1.Hit).To(BeFalse(), "not(passing leaf) expected hit=false")

		spec2 := `{"kind":"not","of":[
			{"kind":"time","axis":"project","value":"YT","op":">=","target_seconds":99999,"window":"week"}
		]}`
		p2, _ := stats.ValidateSpec(json.RawMessage(spec2))
		prog2, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p2, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog2.Hit).To(BeTrue(), "not(failing leaf) expected hit=true (safely avoided)")
	})

	// TestEvaluate_StreakMinDaysZero
	It("streak min_days<=0 short-circuits: (true, 1) with no child query", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_streak_zero")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		// Do NOT seed anything — a min_days=0 streak must trivially hit.
		spec := `{"kind":"streak","min_days":0,"condition":{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":600,"window":"day"}}`
		p, err := stats.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())
		prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.Hit).To(BeTrue())
		Expect(prog.Progress).To(Equal(float64(1)))
		Expect(prog.SubConditions).To(HaveLen(1), "streak summary only (no child evaluation)")
		sc := prog.SubConditions[0]
		Expect(sc.Kind).To(Equal("streak"))
		Expect(sc.Current).To(BeEquivalentTo(0))
		Expect(sc.Target).To(BeEquivalentTo(0))
	})

	// TestEvaluate_StreakTodayMissesReturnsZero
	It("streak with no data → current=0 (loop's initial daysHit=0)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_streak_miss")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

		spec := `{"kind":"streak","min_days":3,"condition":{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":600,"window":"day"}}`
		p, _ := stats.ValidateSpec(json.RawMessage(spec))
		prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions).To(HaveLen(1))
		sc := prog.SubConditions[0]
		Expect(sc.Current).To(BeEquivalentTo(0))
		Expect(sc.Hit).To(BeFalse())
		Expect(prog.Progress).To(Equal(float64(0)))
	})

	// TestEvaluate_StreakExactlyMinDaysHits
	It("streak walk cap = min_days; extras beyond don't count", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_streak_exact")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		for _, offset := range []int{0, 1, 2} {
			seedRollupRowG(hz, owner, now.AddDate(0, 0, -offset), "P", "Go", "vim", 900)
		}
		// Extras beyond that shouldn't be counted (walk cap is 3).
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -3), "P", "Go", "vim", 900)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -4), "P", "Go", "vim", 900)

		spec := `{"kind":"streak","min_days":3,"condition":{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":600,"window":"day"}}`
		p, _ := stats.ValidateSpec(json.RawMessage(spec))
		prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		sc := prog.SubConditions[0]
		Expect(sc.Current).To(BeEquivalentTo(3))
		Expect(sc.Hit).To(BeTrue())
		Expect(prog.Progress).To(Equal(float64(1)))
	})

	// TestEvaluate_ActiveDaysOwnerScoping
	It("owner filter present on active_days SQL", func() {
		hz := testutil.NewHarness(GinkgoTB())
		ownerA, _ := hz.MintUser("eval_ad_scope_a")
		ownerB, _ := hz.MintUser("eval_ad_scope_b")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		seedRollupRowG(hz, ownerA, now.AddDate(0, 0, -1), "P", "Go", "vim", 600)
		seedRollupRowG(hz, ownerA, now.AddDate(0, 0, -3), "P", "Go", "vim", 600)
		seedRollupRowG(hz, ownerA, now.AddDate(0, 0, -5), "P", "Go", "vim", 600)

		spec := `{"kind":"active_days","op":">=","n":1,"window":"week"}`
		p, _ := stats.ValidateSpec(json.RawMessage(spec))
		progA, err := stats.Evaluate(context.Background(), hz.DB.Pool, ownerA, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(progA.SubConditions[0].Current).To(BeEquivalentTo(3))
		progB, err := stats.Evaluate(context.Background(), hz.DB.Pool, ownerB, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(progB.SubConditions[0].Current).To(BeEquivalentTo(0),
			"sender filter fell off on active_days SQL?")
	})

	// TestEvaluate_TimeLeafDayWindow
	It("day window covers only TODAY; excludes yesterday", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_day_win")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		seedRollupRowG(hz, owner, now, "P", "Go", "vim", 500)                    // TODAY
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -1), "P", "Go", "vim", 9999) // yesterday — MUST be excluded

		spec := `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":1,"window":"day"}`
		p, _ := stats.ValidateSpec(json.RawMessage(spec))
		prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions[0].Current).To(BeEquivalentTo(500), "today only")
	})

	// TestEvaluate_LifetimeIncludesAncient
	It("lifetime window includes ancient rows (start pinned at epoch)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_lifetime")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		ancient := time.Date(2020, 3, 1, 0, 0, 0, 0, time.UTC)
		recent := now.AddDate(0, 0, -1)
		seedRollupRowG(hz, owner, ancient, "P", "Go", "vim", 1234)
		seedRollupRowG(hz, owner, recent, "P", "Go", "vim", 5678)

		spec := `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":1,"window":"lifetime"}`
		p, _ := stats.ValidateSpec(json.RawMessage(spec))
		prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions[0].Current).To(BeEquivalentTo(1234+5678),
			"ancient + recent (start pinned at epoch, not clamped)")
	})

	// TestEvaluate_NotProgressInversion
	It("not: progress = 1 - child.progress (arithmetic, not 1-bool)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_not_prog")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		// 500s Go; leaf target 1000 → child.progress = 0.5, hit=false.
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -1), "P", "Go", "vim", 500)

		spec := `{"kind":"not","of":[
			{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":1000,"window":"week"}
		]}`
		p, _ := stats.ValidateSpec(json.RawMessage(spec))
		prog, err := stats.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.Hit).To(BeTrue())
		Expect(prog.Progress).To(Equal(0.5),
			"1 - child.progress=0.5 (was 1-bool used instead of 1-frac?)")
	})

	// TestEvaluate_OwnerScoping
	It("owner filter present on leaf SQL", func() {
		hz := testutil.NewHarness(GinkgoTB())
		ownerA, _ := hz.MintUser("eval_scope_a")
		ownerB, _ := hz.MintUser("eval_scope_b")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		seedRollupRowG(hz, ownerA, now.AddDate(0, 0, -1), "P", "Go", "vim", 10000)

		spec := `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":1000,"window":"week"}`
		p, _ := stats.ValidateSpec(json.RawMessage(spec))
		progA, err := stats.Evaluate(context.Background(), hz.DB.Pool, ownerA, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(progA.SubConditions[0].Current).To(BeEquivalentTo(10000))
		progB, err := stats.Evaluate(context.Background(), hz.DB.Pool, ownerB, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(progB.SubConditions[0].Current).To(BeEquivalentTo(0),
			"owner-scoping breach — leaf query lost sender filter")
	})
})
