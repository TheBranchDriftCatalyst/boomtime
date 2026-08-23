// goals_ginkgo_test.go — ginkgo mirror of goals_test.go (boom-tst-ginkgo).
// 1:1 case map (18 stdlib TestXxx; several have subtests):
//
//	TestValidateSpec_AcceptsHappyLeaves        → ValidateSpec > "accepts every happy-leaf shape (table of 7)"
//	TestValidateSpec_RejectsEachInvariant/*    → ValidateSpec rejects > entry per invariant (11 entries)
//	TestValidateSpec_DepthCap                  → ValidateSpec > "depth cap accepts at MaxPredicateDepth and rejects at +1"
//	TestCompareOp_GreaterEqual                 → compareOp > ">= under/at/over/target=0"
//	TestCompareOp_LessEqual                    → compareOp > "<= under/at/over/target=0"
//	TestCompareOp_Equal                        → compareOp > "== exact/near-miss/wildly-off"
//	TestWindowRange                            → windowRange > "day/week/month/year/lifetime"
//	TestRewriteWindowToDay                     → rewriteWindowToDay > "descends all/any and rewrites time leaves; original untouched"
//	TestSpecShallowFingerprint                 → SpecShallowFingerprint > "time leaf and group cases"
//	TestCompareOp_EqualUnder                   → compareOp > "== under-target symmetric (abs(diff) contract)"
//	TestCompareOp_EqualTargetZero              → compareOp > "== target=0 only hit when current=0"
//	TestCompareOp_UnknownOp                    → compareOp > "unknown op returns (false, 0) default"
//	TestWindowRange_UnknownReturnsZeroSpan     → windowRange > "unknown window returns zero-length range anchored to today"
//	TestRewriteWindowToDay_NestedStreak        → rewriteWindowToDay > "does NOT descend through streak (owns its own recurrence)"
//	TestRewriteWindowToDay_Not                 → rewriteWindowToDay > "descends into not's child"
//	TestClamp01_Boundaries                     → clamp01 > "boundary table"
//	TestValidateSpec_NestedAllAxisPropagates   → ValidateSpec > "recurses into every child of all (unknown axis in 2nd child rejects)"
//	TestValidateSpec_NotChildValidated         → ValidateSpec > "recurses into not's child"
//	TestValidateSpec_StreakConditionValidated  → ValidateSpec > "recurses into streak's condition"
//	TestMarshalUnmarshalProgress               → Progress > "MarshalProgress/UnmarshalProgress round-trip preserves every field"
//	TestUnmarshalProgress_EmptyRaw             → UnmarshalProgress > "nil/empty raw returns (nil, nil) fast-path"
package goals

import (
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateSpec (goals predicate validator)", func() {
	It("accepts every happy-leaf shape", func() {
		cases := []string{
			`{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":3600,"window":"week"}`,
			`{"kind":"time","axis":"project","value":null,"op":"<=","target_seconds":1800,"window":"day"}`,
			`{"kind":"active_days","op":">=","n":5,"window":"month"}`,
			`{"kind":"streak","min_days":7,"condition":{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":600,"window":"day"}}`,
			`{"kind":"all","of":[{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":1,"window":"week"}]}`,
			`{"kind":"any","of":[{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":1,"window":"week"},{"kind":"time","axis":"language","value":"Rust","op":">=","target_seconds":1,"window":"week"}]}`,
			`{"kind":"not","of":[{"kind":"time","axis":"language","value":"YouTube","op":">=","target_seconds":600,"window":"day"}]}`,
		}
		for i, c := range cases {
			_, err := ValidateSpec(json.RawMessage(c))
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("case %d unexpectedly rejected: %s", i, c))
		}
	})

	DescribeTable("rejects each invariant with a stable error substring",
		func(spec, want string) {
			_, err := ValidateSpec(json.RawMessage(spec))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(want))
		},
		Entry("unknown kind", `{"kind":"noSuchThing"}`, "unknown predicate kind"),
		Entry("unknown axis", `{"kind":"time","axis":"chicken","op":">=","target_seconds":1,"window":"week"}`, "unknown axis"),
		Entry("unknown window on time", `{"kind":"time","axis":"language","op":">=","target_seconds":1,"window":"decade"}`, "unknown window"),
		Entry("unknown window on active_days", `{"kind":"active_days","op":">=","n":1,"window":"day"}`, "unknown window"),
		Entry("unknown op", `{"kind":"time","axis":"language","op":"!=","target_seconds":1,"window":"week"}`, "unknown op"),
		Entry("negative target_seconds", `{"kind":"time","axis":"language","op":">=","target_seconds":-1,"window":"week"}`, "non-negative"),
		Entry("negative n on active_days", `{"kind":"active_days","op":">=","n":-3,"window":"week"}`, "non-negative"),
		Entry("streak missing condition", `{"kind":"streak","min_days":7}`, "missing condition"),
		Entry("streak min_days too large", `{"kind":"streak","min_days":9999,"condition":{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"day"}}`, "exceeds maximum"),
		Entry("empty all", `{"kind":"all","of":[]}`, "at least one child"),
		Entry("empty any", `{"kind":"any","of":[]}`, "at least one child"),
		Entry("not wrong arity", `{"kind":"not","of":[{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"week"},{"kind":"time","axis":"project","value":null,"op":">=","target_seconds":1,"window":"week"}]}`, "exactly one child"),
	)

	// TestValidateSpec_DepthCap: cap is a single constant (MaxPredicateDepth); the
	// failing case must be at depth cap+1 EXACTLY.
	It("depth cap accepts at MaxPredicateDepth and rejects at +1", func() {
		build := func(n int) string {
			leaf := `{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"week"}`
			s := leaf
			for i := 0; i < n; i++ {
				s = `{"kind":"all","of":[` + s + `]}`
			}
			return s
		}
		okSpec := build(MaxPredicateDepth - 1)
		_, err := ValidateSpec(json.RawMessage(okSpec))
		Expect(err).NotTo(HaveOccurred(), "depth exactly at cap should be accepted")
		tooDeep := build(MaxPredicateDepth)
		_, err = ValidateSpec(json.RawMessage(tooDeep))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("exceeds maximum"))
	})

	// TestValidateSpec_NestedAllAxisPropagates
	It("recurses into every child of all (unknown axis in 2nd child rejects)", func() {
		bad := `{"kind":"all","of":[
			{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"week"},
			{"kind":"time","axis":"chicken","value":null,"op":">=","target_seconds":1,"window":"week"}
		]}`
		_, err := ValidateSpec(json.RawMessage(bad))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown axis"))
	})

	// TestValidateSpec_NotChildValidated
	It("recurses into not's single child", func() {
		bad := `{"kind":"not","of":[
			{"kind":"time","axis":"language","value":null,"op":"!=","target_seconds":1,"window":"week"}
		]}`
		_, err := ValidateSpec(json.RawMessage(bad))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown op"))
	})

	// TestValidateSpec_StreakConditionValidated
	It("recurses into streak's condition", func() {
		bad := `{"kind":"streak","min_days":3,"condition":{"kind":"time","axis":"chicken","op":">=","target_seconds":1,"window":"day"}}`
		_, err := ValidateSpec(json.RawMessage(bad))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown axis"))
	})
})

var _ = Describe("compareOp arithmetic", func() {
	It(">= under-target / at-target / over-target / target=0", func() {
		hit, prog := compareOp(">=", 30, 60)
		Expect(hit).To(BeFalse())
		Expect(prog).To(Equal(0.5))

		hit, prog = compareOp(">=", 60, 60)
		Expect(hit).To(BeTrue())
		Expect(prog).To(Equal(float64(1)))

		hit, prog = compareOp(">=", 1000, 60)
		Expect(hit).To(BeTrue())
		Expect(prog).To(Equal(float64(1)))

		hit, prog = compareOp(">=", 0, 0)
		Expect(hit).To(BeTrue())
		Expect(prog).To(Equal(float64(1)))
	})

	It("<= under-target / at-target / over-target / target=0", func() {
		hit, prog := compareOp("<=", 30, 60)
		Expect(hit).To(BeTrue())
		Expect(prog).To(Equal(0.5))

		hit, prog = compareOp("<=", 60, 60)
		Expect(hit).To(BeTrue())
		Expect(prog).To(Equal(float64(0)))

		hit, prog = compareOp("<=", 100, 60)
		Expect(hit).To(BeFalse())
		Expect(prog).To(Equal(float64(0)))

		hit, prog = compareOp("<=", 0, 0)
		Expect(hit).To(BeTrue())
		Expect(prog).To(Equal(float64(1)))

		hit, prog = compareOp("<=", 5, 0)
		Expect(hit).To(BeFalse())
		Expect(prog).To(Equal(float64(0)))
	})

	It("== exact / near-miss / wildly-off", func() {
		hit, prog := compareOp("==", 60, 60)
		Expect(hit).To(BeTrue())
		Expect(prog).To(Equal(float64(1)))

		hit, prog = compareOp("==", 90, 60)
		Expect(hit).To(BeFalse())
		Expect(prog).To(Equal(0.5))

		hit, prog = compareOp("==", 6000, 60)
		Expect(hit).To(BeFalse())
		Expect(prog).To(Equal(float64(0)))
	})

	// TestCompareOp_EqualUnder — symmetric under-target (abs(diff) contract)
	It("== under-target symmetric to over-target (abs(diff) contract)", func() {
		hit, prog := compareOp("==", 30, 60)
		Expect(hit).To(BeFalse())
		Expect(prog).To(Equal(0.5))
	})

	// TestCompareOp_EqualTargetZero
	It("== target=0 only hit when current=0", func() {
		hit, prog := compareOp("==", 0, 0)
		Expect(hit).To(BeTrue())
		Expect(prog).To(Equal(float64(1)))

		hit, prog = compareOp("==", 1, 0)
		Expect(hit).To(BeFalse())
		Expect(prog).To(Equal(float64(0)))
	})

	// TestCompareOp_UnknownOp
	It("unknown op returns (false, 0) default", func() {
		hit, prog := compareOp("~=", 60, 60)
		Expect(hit).To(BeFalse())
		Expect(prog).To(Equal(float64(0)))
	})
})

var _ = Describe("windowRange", func() {
	anchor := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	It("day: single day (start == end == today's date)", func() {
		s, e := windowRange(anchor, "day")
		Expect(s.Equal(e)).To(BeTrue())
		Expect(s.Year()).To(Equal(2026))
		Expect(s.Month()).To(Equal(time.July))
		Expect(s.Day()).To(Equal(15))
	})

	It("week: 7 inclusive days ending today", func() {
		s, e := windowRange(anchor, "week")
		Expect(e.Sub(s)).To(Equal(6 * 24 * time.Hour))
		Expect(s.Day()).To(Equal(9))
	})

	It("month: 30 inclusive days", func() {
		s, e := windowRange(anchor, "month")
		Expect(e.Sub(s)).To(Equal(29 * 24 * time.Hour))
	})

	It("year: 365 inclusive days", func() {
		s, e := windowRange(anchor, "year")
		Expect(e.Sub(s)).To(Equal(364 * 24 * time.Hour))
	})

	It("lifetime: start pinned at Unix epoch", func() {
		s, e := windowRange(anchor, "lifetime")
		Expect(s.Year()).To(Equal(1970))
		Expect(e.Year()).To(Equal(2026))
	})

	// TestWindowRange_UnknownReturnsZeroSpan
	It("unknown window returns zero-length range anchored to today", func() {
		s, e := windowRange(anchor, "quarter") // not a valid window
		Expect(s.Equal(e)).To(BeTrue(), "zero-length span")
		Expect(s.Year()).To(Equal(2026))
		Expect(s.Month()).To(Equal(time.July))
		Expect(s.Day()).To(Equal(15))
	})
})

var _ = Describe("rewriteWindowToDay", func() {
	// TestRewriteWindowToDay
	It("descends all/any and rewrites time leaves; original is untouched (deep-copy)", func() {
		src := Predicate{
			Kind: "all",
			Of: []Predicate{
				{Kind: "time", Axis: "language", Op: ">=", TargetSeconds: 60, Window: "week"},
				{Kind: "any", Of: []Predicate{
					{Kind: "time", Axis: "project", Op: ">=", TargetSeconds: 30, Window: "month"},
				}},
			},
		}
		out := rewriteWindowToDay(&src)
		Expect(out.Kind).To(Equal("all"))
		Expect(out.Of).To(HaveLen(2))
		Expect(out.Of[0].Window).To(Equal("day"))
		inner := out.Of[1].Of[0]
		Expect(inner.Window).To(Equal("day"))
		// The original must be untouched (deep-copy semantics).
		Expect(src.Of[0].Window).To(Equal("week"))
	})

	// TestRewriteWindowToDay_NestedStreak
	It("does NOT descend through streak (streak owns its own recurrence)", func() {
		src := Predicate{
			Kind:    "streak",
			MinDays: 3,
			Condition: &Predicate{
				Kind: "streak", MinDays: 2,
				Condition: &Predicate{
					Kind: "time", Axis: "language", Op: ">=",
					TargetSeconds: 60, Window: "week",
				},
			},
		}
		out := rewriteWindowToDay(&src)
		Expect(out.Kind).To(Equal("streak"))
		Expect(out.Condition).NotTo(BeNil())
		inner := out.Condition
		Expect(inner.Kind).To(Equal("streak"))
		Expect(inner.Condition).NotTo(BeNil())
		// Deepest leaf REMAINS "week" — streak's own window controls recurrence.
		Expect(inner.Condition.Window).To(Equal("week"))
	})

	// TestRewriteWindowToDay_Not
	It("descends into not's child", func() {
		src := Predicate{
			Kind: "not",
			Of: []Predicate{{
				Kind: "time", Axis: "language", Op: ">=",
				TargetSeconds: 60, Window: "week",
			}},
		}
		out := rewriteWindowToDay(&src)
		Expect(out.Kind).To(Equal("not"))
		Expect(out.Of).To(HaveLen(1))
		Expect(out.Of[0].Window).To(Equal("day"))
	})
})

var _ = Describe("SpecShallowFingerprint", func() {
	It("time leaf and group cases", func() {
		p := &Predicate{Kind: "time", Axis: "language", Op: ">=", Window: "week"}
		Expect(SpecShallowFingerprint(p)).To(Equal("time:language:week"))
		all := &Predicate{Kind: "all"}
		Expect(SpecShallowFingerprint(all)).To(Equal("all"))
	})
})

var _ = Describe("clamp01", func() {
	DescribeTable("boundary table (< 0 clamps to 0; > 1 clamps to 1; interior passes through)",
		func(in, want float64) {
			Expect(clamp01(in)).To(Equal(want))
		},
		Entry("-0.5 → 0", -0.5, 0.0),
		Entry("-0.001 → 0", -0.001, 0.0),
		Entry("0 → 0", 0.0, 0.0),
		Entry("0.5 → 0.5", 0.5, 0.5),
		Entry("1 → 1", 1.0, 1.0),
		Entry("1.001 → 1", 1.001, 1.0),
		Entry("99 → 1", 99.0, 1.0),
	)
})

var _ = Describe("Progress serialization", func() {
	// TestMarshalUnmarshalProgress
	It("MarshalProgress/UnmarshalProgress round-trip preserves every field", func() {
		str := "Go"
		src := &Progress{
			Hit: true, Progress: 0.75,
			SubConditions: []SubCondition{
				{Kind: "time", Axis: "language", Value: &str, Op: ">=",
					Window: "week", Current: 3600, Target: 7200,
					Progress: 0.5, Hit: false},
			},
		}
		raw, err := MarshalProgress(src)
		Expect(err).NotTo(HaveOccurred())
		back, err := UnmarshalProgress(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(back).NotTo(BeNil())
		Expect(back.Hit).To(Equal(src.Hit))
		Expect(back.Progress).To(Equal(src.Progress))
		Expect(back.SubConditions).To(HaveLen(1))
		got := back.SubConditions[0]
		Expect(got.Current).To(BeEquivalentTo(3600))
		Expect(got.Target).To(BeEquivalentTo(7200))
		Expect(got.Progress).To(Equal(0.5))
		Expect(got.Hit).To(BeFalse())
		Expect(got.Value).NotTo(BeNil())
		Expect(*got.Value).To(Equal("Go"))
	})

	// TestUnmarshalProgress_EmptyRaw
	It("UnmarshalProgress returns (nil, nil) fast-path on nil/empty raw", func() {
		p, err := UnmarshalProgress(nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(BeNil())
		p, err = UnmarshalProgress(json.RawMessage(``))
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(BeNil())
	})
})
