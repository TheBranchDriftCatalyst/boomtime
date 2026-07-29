// evaluator_test.go — 15 highest-value cases ported from
// web/src/features/publicprofile/labels/conditions.test.ts + evaluator.test.ts.
// Priorities: threshold-inclusive semantics, case-insensitive axis matching,
// tier dedupe, all/any/not composition, JSON round-trip. Full ~30-case
// parity is tracked in follow-up bead gaka-hc6.1.1.
package labels

import (
	"encoding/json"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// makePayload builds a Payload with the given resource lists. Callers pass
// only the axes they care about — the rest stay nil, which the evaluator
// treats as "no data".
func makePayload() *Payload { return &Payload{} }

func TestAxisTime_ThresholdInclusive(t *testing.T) {
	p := &Payload{Languages: []model.ResourceStats{
		{Name: "Python", TotalSeconds: 100 * 3600},
	}}
	cond := AxisTimeCond{Axis: AxisLanguages, Value: "Python", Op: OpGE, Hours: 100}
	if !EvaluateCondition(cond, p) {
		t.Errorf("expected 100h == 100h to fire (>=), got false")
	}
	// Just under threshold → not fires.
	p.Languages[0].TotalSeconds = 100*3600 - 1
	if EvaluateCondition(cond, p) {
		t.Errorf("expected 99.999...h to not fire (>=100), got true")
	}
}

func TestAxisTime_CaseInsensitive(t *testing.T) {
	// Catalog says "Python", heartbeats attribute as "python" — must still fire.
	p := &Payload{Languages: []model.ResourceStats{
		{Name: "python", TotalSeconds: 200 * 3600},
	}}
	cond := AxisTimeCond{Axis: AxisLanguages, Value: "Python", Op: OpGE, Hours: 100}
	if !EvaluateCondition(cond, p) {
		t.Error("expected case-insensitive match to fire")
	}
	// And the reverse — catalog "python", data "PYTHON".
	p.Languages[0].Name = "PYTHON"
	cond.Value = "python"
	if !EvaluateCondition(cond, p) {
		t.Error("expected inverse case match to fire")
	}
}

func TestAxisTimeSum_TerminalPuristShape(t *testing.T) {
	// vim (20h) + neovim (20h) + emacs (15h) = 55h. Threshold 50h → fires.
	p := &Payload{Editors: []model.ResourceStats{
		{Name: "vim", TotalSeconds: 20 * 3600},
		{Name: "neovim", TotalSeconds: 20 * 3600},
		{Name: "emacs", TotalSeconds: 15 * 3600},
		{Name: "vscode", TotalSeconds: 100 * 3600}, // ignored — not in values
	}}
	cond := AxisTimeSumCond{
		Axis: AxisEditors, Values: []string{"vim", "neovim", "emacs"},
		Op: OpGE, Hours: 50,
	}
	if !EvaluateCondition(cond, p) {
		t.Error("expected vim+neovim+emacs=55h ≥ 50h to fire")
	}
	// Under-threshold variant.
	cond.Hours = 60
	if EvaluateCondition(cond, p) {
		t.Error("expected 55h < 60h to not fire")
	}
}

func TestAxisPct_PayloadIsHundredScale(t *testing.T) {
	// Payload TotalPct is 0..100; DSL pct is 0..1.
	p := &Payload{Languages: []model.ResourceStats{
		{Name: "Go", TotalSeconds: 500 * 3600, TotalPct: 60}, // = 0.60 in DSL
	}}
	cond := AxisPctCond{Axis: AxisLanguages, Value: "Go", Op: OpGE, Pct: 0.5}
	if !EvaluateCondition(cond, p) {
		t.Error("expected 60% ≥ 50% to fire")
	}
	cond.Pct = 0.7
	if EvaluateCondition(cond, p) {
		t.Error("expected 60% < 70% to not fire")
	}
}

func TestTopShare_UsesFirstEntry(t *testing.T) {
	// Payload is pre-sorted desc; TopShare inspects [0] against axis total.
	p := &Payload{Languages: []model.ResourceStats{
		{Name: "TypeScript", TotalSeconds: 300 * 3600},
		{Name: "Python", TotalSeconds: 200 * 3600},
	}}
	cond := TopShareCond{Axis: AxisLanguages, Op: OpGE, Pct: 0.5}
	// TS is 300/500 = 60% ≥ 50%.
	if !EvaluateCondition(cond, p) {
		t.Error("expected top-share 60% ≥ 50% to fire")
	}
	cond.Pct = 0.7
	if EvaluateCondition(cond, p) {
		t.Error("expected 60% < 70% to not fire")
	}
}

func TestDistinctCount_MinHoursEachFloor(t *testing.T) {
	// Polyglot: 5+ languages each ≥ 20h.
	p := &Payload{Languages: []model.ResourceStats{
		{Name: "Go", TotalSeconds: 25 * 3600},         // qualifies
		{Name: "TS", TotalSeconds: 30 * 3600},         // qualifies
		{Name: "Py", TotalSeconds: 20 * 3600},         // qualifies (== floor)
		{Name: "Rust", TotalSeconds: 19*3600 + 3599},  // does NOT qualify
		{Name: "Zig", TotalSeconds: 100},              // does NOT
	}}
	cond := DistinctCountCond{Axis: AxisLanguages, MinHoursEach: 20, Op: OpGE, N: 3}
	if !EvaluateCondition(cond, p) {
		t.Errorf("expected 3 qualifying languages to satisfy N=3")
	}
	cond.N = 4
	if EvaluateCondition(cond, p) {
		t.Errorf("expected only 3 qualifying to fail N=4")
	}
}

func TestPunchcardHourPct_UsesTotalDenominator(t *testing.T) {
	// 40% of time between 22:00-05:00.
	p := &Payload{Punchcard: model.PunchcardPayload{
		Cells: []model.PunchcardCell{
			{Hour: 23, Seconds: 400}, // in the set
			{Hour: 10, Seconds: 600}, // outside
		},
		TotalSeconds: 1000,
	}}
	cond := PunchcardHourPctCond{HoursIn: []int{22, 23, 0, 1, 2, 3, 4, 5}, Op: OpGE, Pct: 0.4}
	if !EvaluateCondition(cond, p) {
		t.Error("expected 400/1000 = 40% ≥ 40% to fire")
	}
	cond.Pct = 0.5
	if EvaluateCondition(cond, p) {
		t.Error("expected 40% < 50% to not fire")
	}
}

func TestStreak_CurrentVsLongest(t *testing.T) {
	// dailyTotal has a gap then a trailing run.
	p := &Payload{DailyTotal: []int64{1, 1, 1, 1, 0, 1, 1}} // longest 4, current 2
	c1 := StreakCond{Which: "current", Op: OpGE, Days: 2}
	if !EvaluateCondition(c1, p) {
		t.Error("current streak = 2, ≥ 2 should fire")
	}
	c2 := StreakCond{Which: "longest", Op: OpGE, Days: 4}
	if !EvaluateCondition(c2, p) {
		t.Error("longest streak = 4, ≥ 4 should fire")
	}
	c3 := StreakCond{Which: "current", Op: OpGE, Days: 3}
	if EvaluateCondition(c3, p) {
		t.Error("current streak = 2 should fail ≥ 3")
	}
}

func TestTrend_InsufficientHistoryDoesNotFire(t *testing.T) {
	// Only 10 days — trend needs 14.
	p := &Payload{DailyTotal: []int64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}}
	cond := TrendCond{Window: "last7-vs-prior7", Op: OpGE, Ratio: 1.0}
	if EvaluateCondition(cond, p) {
		t.Error("expected < 14 days to not fire (no false positive)")
	}
	// 14 days flat: ratio = 1.0 → matches ≥ 1.0.
	p.DailyTotal = []int64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	if !EvaluateCondition(cond, p) {
		t.Error("expected flat 14 days ratio = 1.0 to fire ≥ 1.0")
	}
	// Doubled last 7 → ratio = 2.
	p.DailyTotal = []int64{1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2}
	cond.Ratio = 2.0
	if !EvaluateCondition(cond, p) {
		t.Error("expected ratio 2.0 to fire ≥ 2.0")
	}
}

func TestAll_AllMustHold(t *testing.T) {
	p := &Payload{Languages: []model.ResourceStats{
		{Name: "python", TotalSeconds: 100 * 3600},
	}}
	pass := AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 50}
	fail := AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 200}
	if !EvaluateCondition(AllCond{Of: []Condition{pass, pass}}, p) {
		t.Error("all([pass, pass]) should fire")
	}
	if EvaluateCondition(AllCond{Of: []Condition{pass, fail}}, p) {
		t.Error("all([pass, fail]) should not fire")
	}
}

func TestAny_OneEnough(t *testing.T) {
	p := &Payload{Languages: []model.ResourceStats{
		{Name: "python", TotalSeconds: 100 * 3600},
	}}
	pass := AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 50}
	fail := AxisTimeCond{Axis: AxisLanguages, Value: "rust", Op: OpGE, Hours: 50}
	if !EvaluateCondition(AnyCond{Of: []Condition{fail, pass}}, p) {
		t.Error("any([fail, pass]) should fire")
	}
	if EvaluateCondition(AnyCond{Of: []Condition{fail, fail}}, p) {
		t.Error("any([fail, fail]) should not fire")
	}
}

func TestNot_Inverts(t *testing.T) {
	p := &Payload{Languages: []model.ResourceStats{
		{Name: "python", TotalSeconds: 100 * 3600},
	}}
	pass := AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 50}
	if EvaluateCondition(NotCond{Of: pass}, p) {
		t.Error("not(pass) should be false")
	}
	fail := AxisTimeCond{Axis: AxisLanguages, Value: "python", Op: OpGE, Hours: 200}
	if !EvaluateCondition(NotCond{Of: fail}, p) {
		t.Error("not(fail) should be true")
	}
}

func TestEvaluateAll_EmptyCatalogReturnsNil(t *testing.T) {
	awards := EvaluateAll(&Payload{}, nil)
	if awards != nil {
		t.Errorf("empty catalog should return nil, got %v", awards)
	}
}

func TestEvaluateAll_TierDedupeKeepsHighest(t *testing.T) {
	// Same tierKey — the master and adept both fire; only master should
	// appear in the result.
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
	if len(awards) != 1 || awards[0].ID != "py-master" {
		t.Errorf("expected only py-master, got %+v", awards)
	}
}

func TestEvaluateAll_SortByRankDescIdAscSecondary(t *testing.T) {
	// All three fire. Ranks: 20, 20, 10 → the two 20s tie and secondary
	// sorts by id asc; 10 comes last.
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
	if len(awards) != 3 {
		t.Fatalf("expected 3 awards, got %d", len(awards))
	}
	want := []string{"a-hi", "b-hi", "c-lo"}
	for i, id := range want {
		if awards[i].ID != id {
			t.Errorf("awards[%d].ID = %q, want %q", i, awards[i].ID, id)
		}
	}
}

func TestJSONRoundTrip_ExampleFromSeed(t *testing.T) {
	// One of the simpler seed rows: {"kind":"axis-time","axis":"languages",
	// "value":"python","op":">=","hours":5} (from 00036_labels_catalog.sql,
	// python-novice tier).
	src := []byte(`{"kind":"axis-time","axis":"languages","value":"python","op":">=","hours":5}`)
	cond, err := UnmarshalCondition(src)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Assert semantics.
	c, ok := cond.(AxisTimeCond)
	if !ok || c.Axis != AxisLanguages || c.Value != "python" || c.Op != OpGE || c.Hours != 5 {
		t.Errorf("decoded shape wrong: %+v", cond)
	}
	// Round-trip must produce bytes representing the same JSON object.
	back, err := MarshalCondition(cond)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var origMap, backMap map[string]any
	if err := json.Unmarshal(src, &origMap); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(back, &backMap); err != nil {
		t.Fatalf("re-parse marshalled: %v (bytes: %s)", err, back)
	}
	if len(origMap) != len(backMap) {
		t.Errorf("field count differs: orig=%d re-encoded=%d", len(origMap), len(backMap))
	}
	for k, v := range origMap {
		bv, ok := backMap[k]
		if !ok {
			t.Errorf("missing key %q in round-trip", k)
			continue
		}
		// json.Unmarshal maps numbers to float64 uniformly, so this compare
		// works for our example.
		if bv != v {
			t.Errorf("field %q: orig=%v, back=%v", k, v, bv)
		}
	}
}

func TestJSONRoundTrip_AllComposition(t *testing.T) {
	// Composer: {"kind":"all","of":[<inner1>,<inner2>]}
	src := []byte(`{"kind":"all","of":[` +
		`{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":10},` +
		`{"kind":"streak","which":"current","op":">=","days":3}` +
		`]}`)
	cond, err := UnmarshalCondition(src)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	all, ok := cond.(AllCond)
	if !ok || len(all.Of) != 2 {
		t.Fatalf("expected AllCond with 2 subs, got %+v", cond)
	}
	if _, ok := all.Of[0].(AxisTimeCond); !ok {
		t.Errorf("of[0] type wrong: %T", all.Of[0])
	}
	if _, ok := all.Of[1].(StreakCond); !ok {
		t.Errorf("of[1] type wrong: %T", all.Of[1])
	}
	// Round-trip.
	back, err := MarshalCondition(cond)
	if err != nil {
		t.Fatal(err)
	}
	cond2, err := UnmarshalCondition(back)
	if err != nil {
		t.Fatalf("re-unmarshal: %v (bytes: %s)", err, back)
	}
	if _, ok := cond2.(AllCond); !ok {
		t.Errorf("round-trip lost outer AllCond: %T", cond2)
	}
}
