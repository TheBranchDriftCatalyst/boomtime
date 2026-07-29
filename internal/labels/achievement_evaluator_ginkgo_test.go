// achievement_evaluator_ginkgo_test.go — ginkgo mirror of
// achievement_evaluator_test.go (gaka-0vp.1). Parallel migration:
// both files run until the kill switch drops the stdlib variant.
//
// 1:1 case map (22 stdlib TestXxx → 22 ginkgo Its / entries):
//   TestAxisTime_ThresholdInclusive       → axis-time > "threshold inclusive"
//   TestAxisTime_CaseInsensitive          → axis-time > "case-insensitive matching"
//   TestAxisTimeSum_TerminalPuristShape   → axis-time-sum > "TERMINAL PURIST shape"
//   TestAxisPct_ComputedFromSeconds       → axis-pct > "computed from seconds"
//   TestTopShare_UsesFirstEntry           → top-share > "uses list[0]"
//   TestDistinctCount_MinHoursEachFloor   → distinct-count > "minHoursEach floor"
//   TestPunchcardHourPct_UsesTotalDenominator
//                                         → punchcard-hour-pct > "total denominator"
//   TestStreak_CurrentVsLongest           → streak > "current vs longest"
//   TestTrend_InsufficientHistoryDoesNotFire
//                                         → trend > "insufficient history"
//   TestAll_AllMustHold                   → composition > "all"
//   TestAny_OneEnough                     → composition > "any"
//   TestNot_Inverts                       → composition > "not"
//   TestEvaluateAll_EmptyCatalogReturnsNil→ EvaluateAll > "empty catalog"
//   TestEvaluateAll_TierDedupeKeepsHighest→ EvaluateAll > "tier dedupe keeps highest"
//   TestEvaluateAll_SortByRankDescIdAscSecondary
//                                         → EvaluateAll > "sort by rank/id"
//   TestJSONRoundTrip_ExampleFromSeed     → JSON round-trip > "axis-time"
//   TestJSONRoundTrip_AllComposition      → JSON round-trip > "all composition"
//   TestDailyAvg_Fires                    → daily-avg > "fires at threshold"
//   TestPunchcardDowPct_Fires             → punchcard-dow-pct > "fires at threshold"
//   TestNilPayloadNoPanic                 → defensive > "nil payload no panic"
//   TestEvaluateAll_NilPayloadReturnsNil  → EvaluateAll > "nil payload returns nil"
//   TestAxisTimeLE_Fires                  → axis-time > "LE comparator"
package labels

import (
	"encoding/json"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("EvaluateCondition primitives (rollup ops)", func() {

	Describe("axis-time", func() {
		It("fires inclusively at the threshold (>=)", func() {
			p := &Payload{Languages: []model.ResourceStats{
				{Name: "Python", TotalSeconds: 100 * 3600},
			}}
			cond := AxisTimeCond{Axis: AxisLanguages, Value: "Python", Op: OpGE, Hours: 100}
			Expect(EvaluateCondition(cond, p)).To(BeTrue())

			// Just under → does not fire.
			p.Languages[0].TotalSeconds = 100*3600 - 1
			Expect(EvaluateCondition(cond, p)).To(BeFalse())
		})

		It("matches axis values case-insensitively", func() {
			p := &Payload{Languages: []model.ResourceStats{
				{Name: "python", TotalSeconds: 200 * 3600},
			}}
			Expect(EvaluateCondition(
				AxisTimeCond{Axis: AxisLanguages, Value: "Python", Op: OpGE, Hours: 100},
				p,
			)).To(BeTrue())

			p.Languages[0].Name = "PYTHON"
			Expect(EvaluateCondition(
				AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 100},
				p,
			)).To(BeTrue())
		})

		It("honors the LE comparator", func() {
			p := &Payload{Languages: []model.ResourceStats{
				{Name: "typescript", TotalSeconds: 5 * 3600},
			}}
			Expect(EvaluateCondition(
				AxisTimeCond{Axis: AxisLanguages, Value: "typescript", Op: OpLE, Hours: 10},
				p,
			)).To(BeTrue())
			Expect(EvaluateCondition(
				AxisTimeCond{Axis: AxisLanguages, Value: "typescript", Op: OpLE, Hours: 3},
				p,
			)).To(BeFalse())
		})
	})

	Describe("axis-time-sum", func() {
		It("fires when the SUM across values crosses (TERMINAL PURIST shape)", func() {
			p := &Payload{Editors: []model.ResourceStats{
				{Name: "vim", TotalSeconds: 20 * 3600},
				{Name: "neovim", TotalSeconds: 20 * 3600},
				{Name: "emacs", TotalSeconds: 15 * 3600},
				{Name: "vscode", TotalSeconds: 100 * 3600}, // ignored
			}}
			base := AxisTimeSumCond{
				Axis: AxisEditors, Values: []string{"vim", "neovim", "emacs"},
				Op: OpGE, Hours: 50,
			}
			Expect(EvaluateCondition(base, p)).To(BeTrue())

			over := base
			over.Hours = 60
			Expect(EvaluateCondition(over, p)).To(BeFalse())
		})
	})

	Describe("axis-pct", func() {
		It("computes share from TotalSeconds (immune to TotalPct scale)", func() {
			p := &Payload{Languages: []model.ResourceStats{
				{Name: "Go", TotalSeconds: 300 * 3600},
				{Name: "Rust", TotalSeconds: 200 * 3600},
			}}
			Expect(EvaluateCondition(
				AxisPctCond{Axis: AxisLanguages, Value: "Go", Op: OpGE, Pct: 0.5},
				p,
			)).To(BeTrue())
			Expect(EvaluateCondition(
				AxisPctCond{Axis: AxisLanguages, Value: "Go", Op: OpGE, Pct: 0.7},
				p,
			)).To(BeFalse())
		})
	})

	Describe("top-share", func() {
		It("uses payload's list[0] as the top entry", func() {
			// Payload is pre-sorted desc; list[0] is top.
			p := &Payload{Languages: []model.ResourceStats{
				{Name: "TypeScript", TotalSeconds: 300 * 3600},
				{Name: "Python", TotalSeconds: 200 * 3600},
			}}
			Expect(EvaluateCondition(
				TopShareCond{Axis: AxisLanguages, Op: OpGE, Pct: 0.5},
				p,
			)).To(BeTrue())
			Expect(EvaluateCondition(
				TopShareCond{Axis: AxisLanguages, Op: OpGE, Pct: 0.7},
				p,
			)).To(BeFalse())
		})
	})

	Describe("distinct-count", func() {
		It("counts only entries meeting minHoursEach", func() {
			p := &Payload{Languages: []model.ResourceStats{
				{Name: "Go", TotalSeconds: 25 * 3600},        // qualifies
				{Name: "TS", TotalSeconds: 30 * 3600},        // qualifies
				{Name: "Py", TotalSeconds: 20 * 3600},        // qualifies (== floor)
				{Name: "Rust", TotalSeconds: 19*3600 + 3599}, // does NOT
				{Name: "Zig", TotalSeconds: 100},             // does NOT
			}}
			Expect(EvaluateCondition(
				DistinctCountCond{Axis: AxisLanguages, MinHoursEach: 20, Op: OpGE, N: 3},
				p,
			)).To(BeTrue())
			Expect(EvaluateCondition(
				DistinctCountCond{Axis: AxisLanguages, MinHoursEach: 20, Op: OpGE, N: 4},
				p,
			)).To(BeFalse())
		})
	})

	Describe("punchcard-hour-pct", func() {
		It("uses the punchcard total as denominator", func() {
			p := &Payload{Punchcard: model.PunchcardPayload{
				Cells: []model.PunchcardCell{
					{Hour: 23, Seconds: 400}, // in the set
					{Hour: 10, Seconds: 600}, // outside
				},
				TotalSeconds: 1000,
			}}
			Expect(EvaluateCondition(
				PunchcardHourPctCond{
					HoursIn: []int{22, 23, 0, 1, 2, 3, 4, 5}, Op: OpGE, Pct: 0.4,
				},
				p,
			)).To(BeTrue())
			Expect(EvaluateCondition(
				PunchcardHourPctCond{
					HoursIn: []int{22, 23, 0, 1, 2, 3, 4, 5}, Op: OpGE, Pct: 0.5,
				},
				p,
			)).To(BeFalse())
		})
	})

	Describe("punchcard-dow-pct", func() {
		It("fires when the dow-in-set share crosses", func() {
			p := &Payload{Punchcard: model.PunchcardPayload{
				Cells: []model.PunchcardCell{
					{Dow: 0, Seconds: 400}, // in the set (Sun)
					{Dow: 3, Seconds: 600},
				},
				TotalSeconds: 1000,
			}}
			Expect(EvaluateCondition(
				PunchcardDowPctCond{DowIn: []int{0, 6}, Op: OpGE, Pct: 0.35},
				p,
			)).To(BeTrue())
			Expect(EvaluateCondition(
				PunchcardDowPctCond{DowIn: []int{0, 6}, Op: OpGE, Pct: 0.5},
				p,
			)).To(BeFalse())
		})
	})

	Describe("streak", func() {
		It("current vs longest", func() {
			p := &Payload{DailyTotal: []int64{1, 1, 1, 1, 0, 1, 1}} // longest 4, current 2
			Expect(EvaluateCondition(
				StreakCond{Which: "current", Op: OpGE, Days: 2}, p,
			)).To(BeTrue())
			Expect(EvaluateCondition(
				StreakCond{Which: "longest", Op: OpGE, Days: 4}, p,
			)).To(BeTrue())
			Expect(EvaluateCondition(
				StreakCond{Which: "current", Op: OpGE, Days: 3}, p,
			)).To(BeFalse())
		})
	})

	Describe("daily-avg", func() {
		It("fires when the day-average crosses", func() {
			p := &Payload{DailyAvg: 3.5 * 3600} // 3.5 hours/day
			Expect(EvaluateCondition(
				DailyAvgCond{Op: OpGE, Hours: 3}, p,
			)).To(BeTrue())
			Expect(EvaluateCondition(
				DailyAvgCond{Op: OpGE, Hours: 4}, p,
			)).To(BeFalse())
		})
	})

	Describe("trend", func() {
		It("does not fire without 14 days of history", func() {
			p := &Payload{DailyTotal: []int64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}} // 10 days
			Expect(EvaluateCondition(
				TrendCond{Window: "last7-vs-prior7", Op: OpGE, Ratio: 1.0}, p,
			)).To(BeFalse())
		})

		It("fires with flat 14 days at ratio 1.0", func() {
			p := &Payload{DailyTotal: []int64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}}
			Expect(EvaluateCondition(
				TrendCond{Window: "last7-vs-prior7", Op: OpGE, Ratio: 1.0}, p,
			)).To(BeTrue())
		})

		It("fires when last-7 doubled prior-7 (ratio 2)", func() {
			p := &Payload{DailyTotal: []int64{1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2}}
			Expect(EvaluateCondition(
				TrendCond{Window: "last7-vs-prior7", Op: OpGE, Ratio: 2.0}, p,
			)).To(BeTrue())
		})
	})
})

var _ = Describe("EvaluateCondition composition", func() {
	pass := AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 50}
	fail := AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 200}
	failOther := AxisTimeCond{Axis: AxisLanguages, Value: "rust", Op: OpGE, Hours: 50}
	p := &Payload{Languages: []model.ResourceStats{
		{Name: "python", TotalSeconds: 100 * 3600},
	}}

	It("all requires every sub to hold", func() {
		Expect(EvaluateCondition(AllCond{Of: []Condition{pass, pass}}, p)).To(BeTrue())
		Expect(EvaluateCondition(AllCond{Of: []Condition{pass, fail}}, p)).To(BeFalse())
	})

	It("any requires at least one sub to hold", func() {
		Expect(EvaluateCondition(AnyCond{Of: []Condition{failOther, pass}}, p)).To(BeTrue())
		Expect(EvaluateCondition(AnyCond{Of: []Condition{failOther, failOther}}, p)).To(BeFalse())
	})

	It("not inverts", func() {
		Expect(EvaluateCondition(NotCond{Of: pass}, p)).To(BeFalse())
		Expect(EvaluateCondition(NotCond{Of: fail}, p)).To(BeTrue())
	})
})

var _ = Describe("EvaluateAll (walker + dedupe + sort)", func() {
	It("empty catalog returns nil", func() {
		Expect(EvaluateAll(&Payload{}, nil)).To(BeNil())
	})

	It("tier dedupe keeps the highest tier per tierKey", func() {
		p := &Payload{Languages: []model.ResourceStats{
			{Name: "python", TotalSeconds: 300 * 3600},
		}}
		catalog := []LabelSpec{
			{
				ID: "py-adept", Kind: KindTier, Label: "PYTHON ADEPT", Rank: 100,
				Tier: TierAdept, TierKey: "languages:python",
				Condition: AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 50},
			},
			{
				ID: "py-master", Kind: KindTier, Label: "PYTHON MASTER", Rank: 100,
				Tier: TierMaster, TierKey: "languages:python",
				Condition: AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 100},
			},
		}
		awards := EvaluateAll(p, catalog)
		Expect(awards).To(HaveLen(1))
		Expect(awards[0].ID).To(Equal("py-master"))
	})

	It("sorts by rank desc, id asc secondary", func() {
		p := &Payload{Languages: []model.ResourceStats{
			{Name: "python", TotalSeconds: 100 * 3600},
		}}
		always := AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 50}
		catalog := []LabelSpec{
			{ID: "b-hi", Kind: KindArchetype, Label: "B", Rank: 20, Condition: always},
			{ID: "a-hi", Kind: KindArchetype, Label: "A", Rank: 20, Condition: always},
			{ID: "c-lo", Kind: KindArchetype, Label: "C", Rank: 10, Condition: always},
		}
		awards := EvaluateAll(p, catalog)
		Expect(awards).To(HaveLen(3))
		ids := []string{awards[0].ID, awards[1].ID, awards[2].ID}
		Expect(ids).To(Equal([]string{"a-hi", "b-hi", "c-lo"}))
	})

	It("survives a nil payload gracefully (no panic, returns nil)", func() {
		catalog := []LabelSpec{
			{
				ID: "some", Kind: KindArchetype, Label: "SOME", Rank: 10,
				Condition: AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 5},
			},
		}
		Expect(EvaluateAll(nil, catalog)).To(BeEmpty())
	})
})

var _ = Describe("defensive", func() {
	It("does not panic when EvaluateCondition receives a nil payload", func() {
		cases := []Condition{
			AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 5},
			AxisTimeSumCond{Axis: AxisEditors, Values: []string{"vim"}, Op: OpGE, Hours: 1},
			AxisPctCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Pct: 0.5},
			TopShareCond{Axis: AxisLanguages, Op: OpGE, Pct: 0.5},
			DistinctCountCond{Axis: AxisLanguages, MinHoursEach: 1, Op: OpGE, N: 3},
			PunchcardHourPctCond{HoursIn: []int{22, 23}, Op: OpGE, Pct: 0.4},
			PunchcardDowPctCond{DowIn: []int{0, 6}, Op: OpGE, Pct: 0.4},
			StreakCond{Which: "current", Op: OpGE, Days: 7},
			DailyAvgCond{Op: OpGE, Hours: 3},
			TrendCond{Window: "last7-vs-prior7", Op: OpGE, Ratio: 2},
		}
		for _, c := range cases {
			Expect(func() { _ = EvaluateCondition(c, nil) }).NotTo(Panic())
		}
	})
})

var _ = Describe("JSON round-trip", func() {
	It("round-trips an axis-time example from the seed", func() {
		src := []byte(`{"kind":"axis-time","axis":"languages","value":"python","op":">=","hours":5}`)
		cond, err := UnmarshalCondition(src)
		Expect(err).NotTo(HaveOccurred())

		c, ok := cond.(AxisTimeCond)
		Expect(ok).To(BeTrue())
		Expect(c.Axis).To(Equal(AxisLanguages))
		Expect(c.Value).To(Equal("python"))
		Expect(c.Op).To(Equal(OpGE))
		Expect(c.Hours).To(Equal(float64(5)))

		back, err := MarshalCondition(cond)
		Expect(err).NotTo(HaveOccurred())

		var origMap, backMap map[string]any
		Expect(json.Unmarshal(src, &origMap)).To(Succeed())
		Expect(json.Unmarshal(back, &backMap)).To(Succeed())
		Expect(backMap).To(Equal(origMap))
	})

	It("round-trips an all-composition (recursively)", func() {
		src := []byte(`{"kind":"all","of":[` +
			`{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":10},` +
			`{"kind":"streak","which":"current","op":">=","days":3}` +
			`]}`)
		cond, err := UnmarshalCondition(src)
		Expect(err).NotTo(HaveOccurred())

		all, ok := cond.(AllCond)
		Expect(ok).To(BeTrue())
		Expect(all.Of).To(HaveLen(2))
		Expect(all.Of[0]).To(BeAssignableToTypeOf(AxisTimeCond{}))
		Expect(all.Of[1]).To(BeAssignableToTypeOf(StreakCond{}))

		back, err := MarshalCondition(cond)
		Expect(err).NotTo(HaveOccurred())
		cond2, err := UnmarshalCondition(back)
		Expect(err).NotTo(HaveOccurred())
		Expect(cond2).To(BeAssignableToTypeOf(AllCond{}))
	})
})
