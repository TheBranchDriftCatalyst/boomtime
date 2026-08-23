// coverage_test.go — gap-fill specs targeting the surfaces the two existing
// test files leave uncovered (boom-d6x):
//
//   - marker methods: every Condition's Kind() + isCondition() (stringer/tag
//     methods that carry the discriminator identity; if any drift, JSON round
//     trips silently corrupt).
//   - tierStrength: the ordering table that drives EvaluateAll's tier dedupe.
//     If Apprentice suddenly ranks above Master, the whole tier system breaks
//     silently at award time.
//   - axisEntries: dispatches by Axis; a typo in the switch would cause a
//     whole payload dimension to render as empty (silent, no-award class of
//     bug).
//   - punchcardTotalSeconds fallback: when the top-level TotalSeconds is 0
//     but Cells carry data, the evaluator MUST sum cells or every
//     punchcard-* condition would evaluate against a zero denominator and
//     never fire.
//   - last7VsPrior7Ratio "prior week idle" branches: the TS mirror uses
//     Infinity so a resumed streak still awards; the Go port returns a very
//     large number. Both zero-lastAvg and non-zero-lastAvg paths need pins.
//   - EvaluateCondition nil-condition + unknown type: defensive; must return
//     false without panicking (protects against future condition types not
//     yet wired into the switch).
//   - MarshalCondition / UnmarshalCondition: every primitive kind + every
//     composer + error paths. Round-trip pins the discriminator so a
//     rename of one Kind() string breaks tests, not production JSON.
//   - LabelAward.MarshalJSON: both the nil-Condition fast path and the
//     spliced-Condition path (which must land the "kind" discriminator on
//     the nested condition object).
//
// Non-tautological framing: each block asserts a NAMED INVARIANT (see the
// It() text) — no `insert x; get x back` roundtrips without a security or
// contract angle.
package labels

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// -- Kind() + isCondition() markers ---------------------------------------
//
// INVARIANT: every concrete Condition type reports the exact JSON
// discriminator string UnmarshalCondition switches on. A mismatch between
// the Kind() literal here and the case label in UnmarshalCondition would
// let an encoded condition fail to round-trip; the marker methods are the
// contract between the two sides.

var _ = Describe("Condition markers (Kind + isCondition)", func() {
	DescribeTable("Kind() returns the JSON discriminator UnmarshalCondition switches on",
		func(c Condition, want string) {
			Expect(c.Kind()).To(Equal(want),
				"Kind() drift silently breaks JSON round-trip for %T", c)
			// isCondition is the sealed-interface guard — calling it just
			// proves the concrete type implements the interface. If a future
			// primitive forgets the marker, this test won't even compile.
			c.isCondition()
		},
		Entry("axis-time", AxisTimeCond{}, "axis-time"),
		Entry("axis-time-sum", AxisTimeSumCond{}, "axis-time-sum"),
		Entry("axis-pct", AxisPctCond{}, "axis-pct"),
		Entry("top-share", TopShareCond{}, "top-share"),
		Entry("distinct-count", DistinctCountCond{}, "distinct-count"),
		Entry("punchcard-hour-pct", PunchcardHourPctCond{}, "punchcard-hour-pct"),
		Entry("punchcard-dow-pct", PunchcardDowPctCond{}, "punchcard-dow-pct"),
		Entry("streak", StreakCond{}, "streak"),
		Entry("daily-avg", DailyAvgCond{}, "daily-avg"),
		Entry("trend", TrendCond{}, "trend"),
		Entry("all", AllCond{}, "all"),
		Entry("any", AnyCond{}, "any"),
		Entry("not", NotCond{}, "not"),
	)
})

// -- tierStrength ordering table -----------------------------------------

var _ = Describe("tierStrength ordering", func() {
	It("keeps novice < apprentice < adept < master < legend (dedupe depends on it)", func() {
		Expect(tierStrength(TierNovice)).To(Equal(0))
		Expect(tierStrength(TierApprentice)).To(Equal(1))
		Expect(tierStrength(TierAdept)).To(Equal(2))
		Expect(tierStrength(TierMaster)).To(Equal(3))
		Expect(tierStrength(TierLegend)).To(Equal(4))
	})

	It("returns -1 for an unknown tier so a non-tier spec wins any collision", func() {
		// The -1 sentinel is load-bearing: EvaluateAll compares
		// tierStrength(new) > tierStrength(existing) and a non-tier row that
		// somehow enters the dedupe pool must NEVER shadow a real tier.
		Expect(tierStrength(LabelTier("nonsense"))).To(Equal(-1))
		Expect(tierStrength(LabelTier(""))).To(Equal(-1))
	})
})

// -- axisEntries dispatch --------------------------------------------------

var _ = Describe("axisEntries dispatch", func() {
	// INVARIANT: every Axis constant maps to the correspondingly-named
	// Payload field. A silent typo (e.g. AxisEditors → p.Projects) would
	// cross-wire the whole evaluator: every editor condition would evaluate
	// against project totals.
	p := &Payload{
		Languages:  []model.ResourceStats{{Name: "lang", TotalSeconds: 1}},
		Editors:    []model.ResourceStats{{Name: "ed", TotalSeconds: 2}},
		Projects:   []model.ResourceStats{{Name: "proj", TotalSeconds: 3}},
		Categories: []model.ResourceStats{{Name: "cat", TotalSeconds: 4}},
		Platforms:  []model.ResourceStats{{Name: "plat", TotalSeconds: 5}},
	}

	DescribeTable("axis constant → identifying seconds value",
		func(a Axis, wantSec int64) {
			got := axisEntries(p, a)
			Expect(got).To(HaveLen(1))
			Expect(got[0].TotalSeconds).To(Equal(wantSec),
				"Axis %q wired to the wrong Payload field", a)
		},
		Entry("languages", AxisLanguages, int64(1)),
		Entry("editors", AxisEditors, int64(2)),
		Entry("projects", AxisProjects, int64(3)),
		Entry("categories", AxisCategories, int64(4)),
		Entry("platforms", AxisPlatforms, int64(5)),
	)

	It("returns nil for an unknown Axis (treated as no-data by primitives)", func() {
		Expect(axisEntries(p, Axis("machines"))).To(BeNil())
		Expect(axisEntries(p, Axis(""))).To(BeNil())
	})

	It("returns nil for a nil payload (short-circuit before switch)", func() {
		Expect(axisEntries(nil, AxisLanguages)).To(BeNil())
	})
})

// -- punchcardTotalSeconds fallback ---------------------------------------

var _ = Describe("punchcardTotalSeconds", func() {
	It("returns 0 on nil payload (defensive; every punchcard-* rule short-circuits)", func() {
		Expect(punchcardTotalSeconds(nil)).To(Equal(int64(0)))
	})

	It("prefers the top-level TotalSeconds when non-zero", func() {
		// If both are set, TotalSeconds wins — cells are the fallback only.
		p := &Payload{Punchcard: model.PunchcardPayload{
			TotalSeconds: 999,
			Cells: []model.PunchcardCell{
				{Seconds: 1}, {Seconds: 2},
			},
		}}
		Expect(punchcardTotalSeconds(p)).To(Equal(int64(999)))
	})

	It("sums cells when TotalSeconds is zero (would otherwise divide by zero)", func() {
		// INVARIANT: an aggregation that forgot to populate TotalSeconds
		// must not silently reduce every punchcard-* condition to a
		// zero-denominator (never fires) — the fallback rescues the eval.
		p := &Payload{Punchcard: model.PunchcardPayload{
			TotalSeconds: 0,
			Cells: []model.PunchcardCell{
				{Seconds: 300}, {Seconds: 700},
			},
		}}
		Expect(punchcardTotalSeconds(p)).To(Equal(int64(1000)))
	})
})

// -- last7VsPrior7Ratio idle-week semantics -------------------------------

var _ = Describe("last7VsPrior7Ratio edge cases", func() {
	It("returns (0, false) when there are fewer than 14 days", func() {
		p := &Payload{DailyTotal: make([]int64, 13)}
		_, ok := last7VsPrior7Ratio(p)
		Expect(ok).To(BeFalse())
	})

	It("returns +Inf-ish when prior week was fully idle but last week was active", func() {
		// Prior 7 zero, last 7 non-zero → ratio must be huge so ANY finite
		// trend threshold in the seed manifests still fires. Mirrors the TS
		// Infinity behavior.
		p := &Payload{DailyTotal: []int64{
			0, 0, 0, 0, 0, 0, 0, // prior 7
			1, 1, 1, 1, 1, 1, 1, // last 7
		}}
		ratio, ok := last7VsPrior7Ratio(p)
		Expect(ok).To(BeTrue())
		Expect(ratio).To(BeNumerically(">=", 1e17))
		// TrendCond with any reasonable threshold must fire in this case:
		Expect(EvaluateCondition(
			TrendCond{Window: "last7-vs-prior7", Op: OpGE, Ratio: 100},
			p,
		)).To(BeTrue())
	})

	It("returns 0 when both weeks were fully idle (no false positive)", func() {
		// INVARIANT: two idle weeks must NOT award a comeback badge just
		// because both averages are zero. Ratio should collapse to 0, not
		// NaN, so cmp(0, >=, positive threshold) is false.
		p := &Payload{DailyTotal: make([]int64, 14)}
		ratio, ok := last7VsPrior7Ratio(p)
		Expect(ok).To(BeTrue())
		Expect(ratio).To(Equal(float64(0)))
		Expect(math.IsNaN(ratio)).To(BeFalse())
		Expect(EvaluateCondition(
			TrendCond{Window: "last7-vs-prior7", Op: OpGE, Ratio: 1.0},
			p,
		)).To(BeFalse())
	})
})

// -- EvaluateCondition defensive paths ------------------------------------

var _ = Describe("EvaluateCondition defensive paths", func() {
	It("returns false when the condition itself is nil (does not panic)", func() {
		// A catalog row with a JSONB null condition would decode to a nil
		// Condition; the evaluator must treat that as a non-firing rule
		// rather than dereferencing a nil pointer.
		Expect(EvaluateCondition(nil, &Payload{})).To(BeFalse())
	})

	It("returns false for an unknown Condition implementation (fallthrough default)", func() {
		// Future migrations might introduce a new Condition type before
		// EvaluateCondition adds its case. The default branch MUST bias
		// safe (no award) rather than panic.
		Expect(EvaluateCondition(unknownCond{}, &Payload{})).To(BeFalse())
	})

	It("falls back to zero-length daily when computing top-share on an empty axis", func() {
		// Named invariant: an axis with no entries returns share=0, which
		// only fires an OpLE condition. No panic, no division by zero.
		p := &Payload{Languages: nil}
		Expect(EvaluateCondition(
			TopShareCond{Axis: AxisLanguages, Op: OpLE, Pct: 0.1}, p,
		)).To(BeTrue())
		Expect(EvaluateCondition(
			TopShareCond{Axis: AxisLanguages, Op: OpGE, Pct: 0.1}, p,
		)).To(BeFalse())
	})

	It("top-share returns 0 when axis total sums to zero (all-zero entries)", func() {
		// Divide-by-zero guard: entries exist but every TotalSeconds is 0
		// (a legitimate output from an aggregation window with no data).
		p := &Payload{Languages: []model.ResourceStats{
			{Name: "Go", TotalSeconds: 0},
			{Name: "Rust", TotalSeconds: 0},
		}}
		Expect(EvaluateCondition(
			TopShareCond{Axis: AxisLanguages, Op: OpLE, Pct: 0.5}, p,
		)).To(BeTrue())
	})

	It("axis-pct returns 0 pct when axis total is zero (no divide-by-zero)", func() {
		// Same divide-by-zero angle for axis-pct. Value found but total is
		// zero → share collapses to 0 rather than NaN.
		p := &Payload{Languages: []model.ResourceStats{
			{Name: "Go", TotalSeconds: 0},
		}}
		Expect(EvaluateCondition(
			AxisPctCond{Axis: AxisLanguages, Value: "Go", Op: OpLE, Pct: 0.5}, p,
		)).To(BeTrue())
	})

	It("punchcard-hour-pct returns 0 pct when punchcard is empty", func() {
		p := &Payload{Punchcard: model.PunchcardPayload{}}
		Expect(EvaluateCondition(
			PunchcardHourPctCond{HoursIn: []int{0, 1, 2}, Op: OpGE, Pct: 0.1}, p,
		)).To(BeFalse())
		Expect(EvaluateCondition(
			PunchcardHourPctCond{HoursIn: []int{0, 1, 2}, Op: OpLE, Pct: 0.1}, p,
		)).To(BeTrue())
	})

	It("punchcard-dow-pct returns 0 pct when punchcard is empty", func() {
		p := &Payload{Punchcard: model.PunchcardPayload{}}
		Expect(EvaluateCondition(
			PunchcardDowPctCond{DowIn: []int{0, 6}, Op: OpGE, Pct: 0.1}, p,
		)).To(BeFalse())
	})

	It("streak defaults to 'longest' when Which is not 'current'", func() {
		// INVARIANT: any non-'current' string falls through to longest.
		// A rogue "longestish" from a hand-edited row must still compute
		// the longest streak (not silently return 0).
		p := &Payload{DailyTotal: []int64{1, 1, 1, 1, 0, 0}}
		Expect(EvaluateCondition(
			StreakCond{Which: "longest", Op: OpGE, Days: 4}, p,
		)).To(BeTrue())
		Expect(EvaluateCondition(
			StreakCond{Which: "anything-else", Op: OpGE, Days: 4}, p,
		)).To(BeTrue())
	})
})

// unknownCond is a stand-in for a future Condition type not yet wired into
// EvaluateCondition's type switch. Verifies the default branch is safe.
type unknownCond struct{}

func (unknownCond) Kind() string { return "unknown-future-kind" }
func (unknownCond) isCondition() {}

// -- UnmarshalCondition primitive coverage --------------------------------

var _ = Describe("UnmarshalCondition primitive coverage", func() {
	// INVARIANT: each JSON discriminator lands in the correct concrete
	// Go type. If a new "kind" is added but the switch missed it, the
	// error message ("unknown kind …") must be plainly wrong for a
	// listed primitive.
	DescribeTable("kind literal → concrete type",
		func(jsonBlob string, wantType any) {
			c, err := UnmarshalCondition([]byte(jsonBlob))
			Expect(err).NotTo(HaveOccurred())
			Expect(c).To(BeAssignableToTypeOf(wantType))
		},
		Entry("axis-time",
			`{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":10}`,
			AxisTimeCond{}),
		Entry("axis-time-sum",
			`{"kind":"axis-time-sum","axis":"editors","values":["vim","neovim"],"op":">=","hours":50}`,
			AxisTimeSumCond{}),
		Entry("axis-pct",
			`{"kind":"axis-pct","axis":"languages","value":"go","op":">=","pct":0.5}`,
			AxisPctCond{}),
		Entry("top-share",
			`{"kind":"top-share","axis":"languages","op":">=","pct":0.5}`,
			TopShareCond{}),
		Entry("distinct-count",
			`{"kind":"distinct-count","axis":"languages","minHoursEach":20,"op":">=","n":5}`,
			DistinctCountCond{}),
		Entry("punchcard-hour-pct",
			`{"kind":"punchcard-hour-pct","hoursIn":[22,23,0,1],"op":">=","pct":0.4}`,
			PunchcardHourPctCond{}),
		Entry("punchcard-dow-pct",
			`{"kind":"punchcard-dow-pct","dowIn":[0,6],"op":">=","pct":0.4}`,
			PunchcardDowPctCond{}),
		Entry("streak",
			`{"kind":"streak","which":"current","op":">=","days":7}`,
			StreakCond{}),
		Entry("daily-avg",
			`{"kind":"daily-avg","op":">=","hours":3}`,
			DailyAvgCond{}),
		Entry("trend",
			`{"kind":"trend","window":"last7-vs-prior7","op":">=","ratio":1.5}`,
			TrendCond{}),
		Entry("any",
			`{"kind":"any","of":[{"kind":"daily-avg","op":">=","hours":1}]}`,
			AnyCond{}),
		Entry("not",
			`{"kind":"not","of":{"kind":"daily-avg","op":">=","hours":1}}`,
			NotCond{}),
	)
})

// -- UnmarshalCondition error paths ---------------------------------------

var _ = Describe("UnmarshalCondition error paths", func() {
	It("rejects non-JSON bytes at kind-peek time", func() {
		_, err := UnmarshalCondition([]byte("not json"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("condition"))
	})

	It("rejects an unknown discriminator with a diagnostic mentioning the kind", func() {
		// INVARIANT: a bad kind must include the offending string so a bad
		// seed row is greppable in logs.
		_, err := UnmarshalCondition([]byte(`{"kind":"who-knows"}`))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("who-knows"))
	})

	It("propagates a decode failure INSIDE an all-composer's sub-condition", func() {
		// The child has a valid kind but a bad payload shape (hours is a
		// string). The recursive UnmarshalCondition in the 'all' branch
		// must surface it, not swallow silently.
		_, err := UnmarshalCondition([]byte(`{"kind":"all","of":[` +
			`{"kind":"daily-avg","op":">=","hours":"NOT_A_NUMBER"}` +
			`]}`))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(Or(
			ContainSubstring("all/of[0]"),
			ContainSubstring("hours"),
		))
	})

	It("propagates a decode failure INSIDE an any-composer's sub-condition", func() {
		_, err := UnmarshalCondition([]byte(`{"kind":"any","of":[` +
			`{"kind":"UNKNOWN_KIND"}` +
			`]}`))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("any/of[0]"))
	})

	It("propagates a decode failure INSIDE a not-composer's sub-condition", func() {
		_, err := UnmarshalCondition([]byte(`{"kind":"not","of":{"kind":"UNKNOWN"}}`))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not/of"))
	})

	It("rejects an all-composer whose 'of' is malformed JSON (not an array)", func() {
		_, err := UnmarshalCondition([]byte(`{"kind":"all","of":"not-an-array"}`))
		Expect(err).To(HaveOccurred())
	})

	It("rejects an any-composer whose 'of' is malformed JSON (not an array)", func() {
		_, err := UnmarshalCondition([]byte(`{"kind":"any","of":42}`))
		Expect(err).To(HaveOccurred())
	})

	It("rejects a not-composer whose 'of' is malformed at the outer envelope", func() {
		// The outer envelope must decode; simulate with an invalid array
		// where an object is required.
		_, err := UnmarshalCondition([]byte(`{"kind":"not","of":[1,2,3]}`))
		Expect(err).To(HaveOccurred())
	})
})

// -- MarshalCondition primitive + composer + error coverage --------------

var _ = Describe("MarshalCondition round-trip", func() {
	// INVARIANT: encoding then decoding a Condition yields a semantically
	// equal Condition. Uses BeAssignableToTypeOf plus field spot-checks
	// so a shape-preserving encoder is provably faithful.
	DescribeTable("marshal → unmarshal is identity",
		func(orig Condition) {
			bytes, err := MarshalCondition(orig)
			Expect(err).NotTo(HaveOccurred())
			back, err := UnmarshalCondition(bytes)
			Expect(err).NotTo(HaveOccurred())
			Expect(back).To(BeAssignableToTypeOf(orig))
			// Re-encode and compare byte-for-byte via canonical map compare.
			bytes2, err := MarshalCondition(back)
			Expect(err).NotTo(HaveOccurred())
			var m1, m2 map[string]any
			Expect(json.Unmarshal(bytes, &m1)).To(Succeed())
			Expect(json.Unmarshal(bytes2, &m2)).To(Succeed())
			Expect(m2).To(Equal(m1))
		},
		Entry("axis-time-sum",
			AxisTimeSumCond{Axis: AxisEditors, Values: []string{"vim", "neovim"}, Op: OpGE, Hours: 50}),
		Entry("axis-pct",
			AxisPctCond{Axis: AxisLanguages, Value: "go", Op: OpGE, Pct: 0.5}),
		Entry("top-share",
			TopShareCond{Axis: AxisLanguages, Op: OpGE, Pct: 0.5}),
		Entry("distinct-count",
			DistinctCountCond{Axis: AxisLanguages, MinHoursEach: 20, Op: OpGE, N: 5}),
		Entry("punchcard-hour-pct",
			PunchcardHourPctCond{HoursIn: []int{22, 23, 0, 1}, Op: OpGE, Pct: 0.4}),
		Entry("punchcard-dow-pct",
			PunchcardDowPctCond{DowIn: []int{0, 6}, Op: OpGE, Pct: 0.4}),
		Entry("daily-avg",
			DailyAvgCond{Op: OpGE, Hours: 3}),
		Entry("trend",
			TrendCond{Window: "last7-vs-prior7", Op: OpGE, Ratio: 1.5}),
		Entry("any composer",
			AnyCond{Of: []Condition{
				DailyAvgCond{Op: OpGE, Hours: 3},
				StreakCond{Which: "current", Op: OpGE, Days: 7},
			}}),
		Entry("not composer wrapping a primitive",
			NotCond{Of: DailyAvgCond{Op: OpLE, Hours: 0.1}}),
		Entry("deeply nested any(all(not(...)))",
			AnyCond{Of: []Condition{
				AllCond{Of: []Condition{
					NotCond{Of: StreakCond{Which: "current", Op: OpLE, Days: 0}},
					AxisTimeCond{Axis: AxisLanguages, Value: "go", Op: OpGE, Hours: 1},
				}},
			}}),
	)

	It("emits the 'kind' discriminator FIRST in the object (contract with FE parser)", func() {
		// The FE parser peeks at "kind" the same way UnmarshalCondition
		// does. Keeping the field first is a stability contract — if
		// someone rewrites the encoder and drops the discriminator to the
		// end, the FE still works (JSON is unordered) but noisy diffs would
		// litter git history. Assert the current shape so a drift is loud.
		b, err := MarshalCondition(DailyAvgCond{Op: OpGE, Hours: 3})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.HasPrefix(string(b), `{"kind":"daily-avg"`)).To(BeTrue())
	})
})

// -- LabelAward.MarshalJSON both branches ---------------------------------

var _ = Describe("LabelAward.MarshalJSON", func() {
	It("omits the condition field when Condition is nil (short-circuit branch)", func() {
		// INVARIANT: an award without a condition (would be odd in prod,
		// but the type allows it) MUST NOT emit a bogus "condition":null
		// that a strict FE parser would reject.
		a := LabelAward{ID: "x", Kind: KindArchetype, Label: "X", Rank: 1}
		b, err := json.Marshal(a)
		Expect(err).NotTo(HaveOccurred())
		var m map[string]any
		Expect(json.Unmarshal(b, &m)).To(Succeed())
		_, exists := m["condition"]
		Expect(exists).To(BeFalse())
	})

	It("splices the Condition through MarshalCondition so 'kind' lands on the nested object", func() {
		// This is the load-bearing test: LabelAward wraps Condition as
		// `any` — the naïve `json:"condition"` route would drop the "kind"
		// discriminator (concrete types have no Kind field). MarshalJSON
		// MUST route through MarshalCondition so the FE can round-trip.
		a := LabelAward{
			ID: "x", Kind: KindTier, Label: "X", Rank: 1, Tier: TierAdept,
			Condition: DailyAvgCond{Op: OpGE, Hours: 3},
		}
		b, err := json.Marshal(a)
		Expect(err).NotTo(HaveOccurred())
		var m map[string]any
		Expect(json.Unmarshal(b, &m)).To(Succeed())
		cond, ok := m["condition"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(cond["kind"]).To(Equal("daily-avg"))
		// The tier field must also round-trip when set (not omitempty'd).
		Expect(m["tier"]).To(Equal("adept"))
	})

	It("propagates a Condition-encoding failure rather than emitting a truncated award", func() {
		// If MarshalCondition returns an error (e.g. an unknown concrete
		// Condition type from a future migration), MarshalJSON MUST fail
		// rather than emit a partially-formed award missing the condition.
		// The only way to force MarshalCondition failure is with a
		// Condition whose marshalled form has an unexpected shape. We use
		// a Condition impl that Marshal-fails via a custom MarshalJSON.
		a := LabelAward{
			ID: "x", Kind: KindArchetype, Label: "X", Rank: 1,
			Condition: brokenMarshalCond{},
		}
		_, err := json.Marshal(a)
		Expect(err).To(HaveOccurred())
	})
})

// brokenMarshalCond forces MarshalCondition down the "inner encoding
// malformed" branch by producing a non-object JSON payload for the inner
// marshal step.
type brokenMarshalCond struct{}

func (brokenMarshalCond) Kind() string { return "broken" }
func (brokenMarshalCond) isCondition() {}
func (brokenMarshalCond) MarshalJSON() ([]byte, error) {
	// Return a JSON array — MarshalCondition's `inner[0] != '{'` guard
	// triggers the "malformed inner encoding" error path.
	return []byte(`["not","an","object"]`), nil
}

// -- MarshalCondition error / edge paths ----------------------------------

var _ = Describe("MarshalCondition edge paths", func() {
	It("returns an error when the inner encoding is not a JSON object", func() {
		_, err := MarshalCondition(brokenMarshalCond{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("malformed"))
	})

	It("propagates a child failure when composing an all()", func() {
		// AllCond → marshalComposer → MarshalCondition recursively; if any
		// child fails, the whole composer must fail.
		_, err := MarshalCondition(AllCond{Of: []Condition{brokenMarshalCond{}}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("all/of[0]"))
	})

	It("propagates a child failure when composing an any()", func() {
		_, err := MarshalCondition(AnyCond{Of: []Condition{brokenMarshalCond{}}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("any/of[0]"))
	})

	It("propagates a child failure when composing a not()", func() {
		_, err := MarshalCondition(NotCond{Of: brokenMarshalCond{}})
		Expect(err).To(HaveOccurred())
	})
})

// -- SpecsFromDBRows happy path ------------------------------------------

var _ = Describe("SpecsFromDBRows happy path", func() {
	It("converts every row and preserves order (tier + non-tier interleaved)", func() {
		// INVARIANT: the batch converter preserves input order — EvaluateAll
		// re-sorts by rank/id, so order-preservation here is the contract
		// callers rely on for stable trace/debug output.
		rows := []DBRow{
			{
				ID:   "languages-python-master",
				Kind: "tier", Tier: "master", Label: "PY MASTER", Rank: 100,
				Condition: json.RawMessage(`{"kind":"axis-time","axis":"languages","value":"python","op":">=","hours":100}`),
			},
			{
				ID:   "night-watch",
				Kind: "archetype", Label: "NIGHT WATCH", Rank: 50,
				Condition: json.RawMessage(`{"kind":"daily-avg","op":">=","hours":1}`),
			},
		}
		specs, err := SpecsFromDBRows(rows)
		Expect(err).NotTo(HaveOccurred())
		Expect(specs).To(HaveLen(2))
		Expect(specs[0].ID).To(Equal("languages-python-master"))
		Expect(specs[0].TierKey).To(Equal("languages:python"))
		Expect(specs[1].ID).To(Equal("night-watch"))
		Expect(specs[1].TierKey).To(BeEmpty())
	})

	It("returns an empty slice for empty input (no nil surprise)", func() {
		specs, err := SpecsFromDBRows(nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(specs).NotTo(BeNil())
		Expect(specs).To(BeEmpty())
	})
})

// -- EvaluateAll tie-break: non-tier + tier collision --------------------

var _ = Describe("EvaluateAll tier collision fallthrough", func() {
	It("routes a KindTier row with empty TierKey through the non-tier lane (no dedupe)", func() {
		// INVARIANT: a Kind=='tier' row with TierKey=='' (e.g. an id that
		// didn't match the axis-value-tier convention) MUST NOT get lost.
		// It must show up in the awards, just without collision dedupe.
		p := &Payload{Languages: []model.ResourceStats{
			{Name: "python", TotalSeconds: 200 * 3600},
		}}
		always := AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 50}
		catalog := []LabelSpec{
			{
				ID: "malformed-tier-row", Kind: KindTier, Label: "ORPHAN",
				Rank: 10, Tier: TierMaster, TierKey: "", // empty
				Condition: always,
			},
			{
				ID: "sibling", Kind: KindTier, Label: "SIB",
				Rank: 10, Tier: TierAdept, TierKey: "",
				Condition: always,
			},
		}
		awards := EvaluateAll(p, catalog)
		Expect(awards).To(HaveLen(2)) // both survive — no dedupe
	})

	It("keeps the first tier row per key when duplicate tiers collide (dedupe never regresses)", func() {
		// INVARIANT: if two rows share tierKey AND tier, the second must
		// NOT replace the first (tierStrength(new) > tierStrength(cur) is
		// strict; equal-tier keeps the existing).
		p := &Payload{Languages: []model.ResourceStats{
			{Name: "python", TotalSeconds: 200 * 3600},
		}}
		always := AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 50}
		catalog := []LabelSpec{
			{
				ID: "first", Kind: KindTier, Label: "FIRST", Rank: 10,
				Tier: TierMaster, TierKey: "languages:python", Condition: always,
			},
			{
				ID: "second", Kind: KindTier, Label: "SECOND", Rank: 10,
				Tier: TierMaster, TierKey: "languages:python", Condition: always,
			},
		}
		awards := EvaluateAll(p, catalog)
		Expect(awards).To(HaveLen(1))
		Expect(awards[0].ID).To(Equal("first"))
	})
})

// -- EvaluateAll: no-firing filter path ----------------------------------

var _ = Describe("EvaluateAll no-fire path", func() {
	It("returns nil when the catalog is non-empty but no condition fires", func() {
		// INVARIANT: an empty result comes back as nil (not []LabelAward{}),
		// so JSON marshalling emits `null`, not `[]`. The FE handles both,
		// but the contract is nil — pin it.
		p := &Payload{Languages: []model.ResourceStats{
			{Name: "python", TotalSeconds: 1 * 3600},
		}}
		catalog := []LabelSpec{
			{
				ID: "unreachable", Kind: KindArchetype, Label: "X", Rank: 1,
				Condition: AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 999},
			},
		}
		Expect(EvaluateAll(p, catalog)).To(BeNil())
	})
})
