// goals_test.go — PURE unit tests for the predicate validator, compareOp
// arithmetic, and windowRange resolution. No DB required. Integration
// tests that seed hb_rollup_daily and run the evaluator live in
// goals_integration_test.go (external package, needs internal/testutil).
//
// The non-tautology anchors here:
//
//   - Every branch of ValidateSpec has a matching REJECT test (unknown
//     axis, unknown kind, unknown window, depth>5, negative target,
//     empty `of`). If someone loosens a check, one of these fires.
//
//   - compareOp is asserted at every boundary: current<target,
//     current==target, current>target for both ">=" and "<="; the "=="
//     case's near-miss ramp; and the target==0 edge case that gates a
//     divide-by-zero.
//
//   - windowRange returns exact day counts (7 for week, 30 for month,
//     365 for year) and a full-lifetime start (1970). A future off-by-
//     one in date math would show up as a wrong day count.
package stats

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestValidateSpec_AcceptsHappyLeaves(t *testing.T) {
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
		if _, err := ValidateSpec(json.RawMessage(c)); err != nil {
			t.Errorf("case %d unexpectedly rejected: %v\nspec: %s", i, err, c)
		}
	}
}

func TestValidateSpec_RejectsEachInvariant(t *testing.T) {
	type row struct {
		name string
		spec string
		want string // substring that MUST appear in the error message
	}
	cases := []row{
		{"unknown kind", `{"kind":"noSuchThing"}`, "unknown predicate kind"},
		{"unknown axis", `{"kind":"time","axis":"chicken","op":">=","target_seconds":1,"window":"week"}`, "unknown axis"},
		{"unknown window on time",
			`{"kind":"time","axis":"language","op":">=","target_seconds":1,"window":"decade"}`, "unknown window"},
		{"unknown window on active_days",
			`{"kind":"active_days","op":">=","n":1,"window":"day"}`, "unknown window"},
		{"unknown op", `{"kind":"time","axis":"language","op":"!=","target_seconds":1,"window":"week"}`, "unknown op"},
		{"negative target_seconds",
			`{"kind":"time","axis":"language","op":">=","target_seconds":-1,"window":"week"}`, "non-negative"},
		{"negative n on active_days",
			`{"kind":"active_days","op":">=","n":-3,"window":"week"}`, "non-negative"},
		{"streak missing condition",
			`{"kind":"streak","min_days":7}`, "missing condition"},
		{"streak min_days too large",
			`{"kind":"streak","min_days":9999,"condition":{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"day"}}`, "exceeds maximum"},
		{"empty all",
			`{"kind":"all","of":[]}`, "at least one child"},
		{"empty any",
			`{"kind":"any","of":[]}`, "at least one child"},
		{"not wrong arity",
			`{"kind":"not","of":[{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"week"},{"kind":"time","axis":"project","value":null,"op":">=","target_seconds":1,"window":"week"}]}`, "exactly one child"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ValidateSpec(json.RawMessage(c.spec))
			if err == nil {
				t.Fatalf("wanted error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want to contain %q", err, c.want)
			}
		})
	}
}

// TestValidateSpec_DepthCap builds a linear nesting deeper than the cap
// and asserts the exact depth boundary. Non-tautology: the cap is a
// SINGLE constant (MaxPredicateDepth); the failing case must be at
// depth cap+1 exactly, not "some big depth".
func TestValidateSpec_DepthCap(t *testing.T) {
	// Build a nested `all` chain N deep, each level wrapping a single
	// time-leaf child through more `all`s.
	build := func(n int) string {
		leaf := `{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"week"}`
		s := leaf
		for i := 0; i < n; i++ {
			s = `{"kind":"all","of":[` + s + `]}`
		}
		return s
	}
	// Depth of the result = wraps + 1 (leaf is depth 1 by itself).
	// MaxPredicateDepth wraps = MaxPredicateDepth-1 → root is depth
	// MaxPredicateDepth, still OK.
	okSpec := build(MaxPredicateDepth - 1)
	if _, err := ValidateSpec(json.RawMessage(okSpec)); err != nil {
		t.Fatalf("depth exactly at cap unexpectedly rejected: %v", err)
	}
	// One more wrap = depth MaxPredicateDepth+1 → must reject.
	tooDeep := build(MaxPredicateDepth)
	_, err := ValidateSpec(json.RawMessage(tooDeep))
	if err == nil {
		t.Fatalf("depth %d wraps must exceed cap %d, got no error", MaxPredicateDepth, MaxPredicateDepth)
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("wanted 'exceeds maximum' in err, got %v", err)
	}
}

func TestCompareOp_GreaterEqual(t *testing.T) {
	// under → hit=false, progress=current/target
	if hit, prog := compareOp(">=", 30, 60); hit || prog != 0.5 {
		t.Errorf(">= under-target: hit=%v prog=%v (want false, 0.5)", hit, prog)
	}
	// at → hit=true, progress=1
	if hit, prog := compareOp(">=", 60, 60); !hit || prog != 1 {
		t.Errorf(">= at-target: hit=%v prog=%v (want true, 1)", hit, prog)
	}
	// over → hit=true, progress capped at 1
	if hit, prog := compareOp(">=", 1000, 60); !hit || prog != 1 {
		t.Errorf(">= over-target: hit=%v prog=%v (want true, 1 clamped)", hit, prog)
	}
	// target=0 is trivially satisfied.
	if hit, prog := compareOp(">=", 0, 0); !hit || prog != 1 {
		t.Errorf(">= target=0: hit=%v prog=%v (want true, 1)", hit, prog)
	}
}

func TestCompareOp_LessEqual(t *testing.T) {
	// current<target → hit=true, progress = 1 - current/target
	if hit, prog := compareOp("<=", 30, 60); !hit || prog != 0.5 {
		t.Errorf("<= under-target: hit=%v prog=%v (want true, 0.5)", hit, prog)
	}
	// current==target → hit=true, progress=0
	if hit, prog := compareOp("<=", 60, 60); !hit || prog != 0 {
		t.Errorf("<= at-target: hit=%v prog=%v (want true, 0)", hit, prog)
	}
	// current>target → hit=false, progress=0
	if hit, prog := compareOp("<=", 100, 60); hit || prog != 0 {
		t.Errorf("<= over-target: hit=%v prog=%v (want false, 0)", hit, prog)
	}
	// target=0: "at most 0" is only hit when current=0.
	if hit, prog := compareOp("<=", 0, 0); !hit || prog != 1 {
		t.Errorf("<= target=0 current=0: hit=%v prog=%v (want true, 1)", hit, prog)
	}
	if hit, prog := compareOp("<=", 5, 0); hit || prog != 0 {
		t.Errorf("<= target=0 current=5: hit=%v prog=%v (want false, 0)", hit, prog)
	}
}

func TestCompareOp_Equal(t *testing.T) {
	if hit, prog := compareOp("==", 60, 60); !hit || prog != 1 {
		t.Errorf("== exact: hit=%v prog=%v", hit, prog)
	}
	// Half-off gets 0.5 (diff=30, target=60 → 1-0.5).
	if hit, prog := compareOp("==", 90, 60); hit || prog != 0.5 {
		t.Errorf("== near-miss: hit=%v prog=%v (want false, 0.5)", hit, prog)
	}
	// Wildly off gets 0.
	if hit, prog := compareOp("==", 6000, 60); hit || prog != 0 {
		t.Errorf("== wildly off: hit=%v prog=%v (want false, 0)", hit, prog)
	}
}

func TestWindowRange(t *testing.T) {
	// Fixed anchor so day-count math is deterministic (no
	// timezone/local-time surprises).
	anchor := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// day: single day (start == end == today's date).
	s, e := windowRange(anchor, "day")
	if !s.Equal(e) || s.Year() != 2026 || s.Month() != 7 || s.Day() != 15 {
		t.Errorf("day: got [%v, %v], want single date 2026-07-15", s, e)
	}
	// week: 7 inclusive days ending today.
	s, e = windowRange(anchor, "week")
	if e.Sub(s) != 6*24*time.Hour {
		t.Errorf("week: span %v, want 6 days (7 inclusive)", e.Sub(s))
	}
	if s.Day() != 9 {
		t.Errorf("week start day = %d, want 9 (15 - 6)", s.Day())
	}
	// month: 30 inclusive days.
	s, e = windowRange(anchor, "month")
	if e.Sub(s) != 29*24*time.Hour {
		t.Errorf("month: span %v, want 29 days (30 inclusive)", e.Sub(s))
	}
	// year: 365 inclusive days.
	s, e = windowRange(anchor, "year")
	if e.Sub(s) != 364*24*time.Hour {
		t.Errorf("year: span %v, want 364 days (365 inclusive)", e.Sub(s))
	}
	// lifetime: start pinned at Unix epoch.
	s, e = windowRange(anchor, "lifetime")
	if s.Year() != 1970 {
		t.Errorf("lifetime start year = %d, want 1970", s.Year())
	}
	if e.Year() != 2026 {
		t.Errorf("lifetime end year = %d, want 2026", e.Year())
	}
}

// TestRewriteWindowToDay demonstrates that a nested spec has EVERY
// time leaf's window overridden to "day" but the structure is
// preserved.
func TestRewriteWindowToDay(t *testing.T) {
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
	if out.Kind != "all" || len(out.Of) != 2 {
		t.Fatalf("structure lost: %+v", out)
	}
	if out.Of[0].Window != "day" {
		t.Errorf("leaf window not rewritten: %s", out.Of[0].Window)
	}
	inner := out.Of[1].Of[0]
	if inner.Window != "day" {
		t.Errorf("nested leaf window not rewritten: %s", inner.Window)
	}
	// The original must be untouched (deep-copy semantics).
	if src.Of[0].Window != "week" {
		t.Errorf("source mutated (was week, now %s)", src.Of[0].Window)
	}
}

// TestSpecShallowFingerprint gives us a stable log key for time leaves;
// exercised so a future refactor doesn't silently break log
// correlation.
func TestSpecShallowFingerprint(t *testing.T) {
	p := &Predicate{Kind: "time", Axis: "language", Op: ">=", Window: "week"}
	if got := SpecShallowFingerprint(p); got != "time:language:week" {
		t.Errorf("fingerprint = %q, want time:language:week", got)
	}
	all := &Predicate{Kind: "all"}
	if got := SpecShallowFingerprint(all); got != "all" {
		t.Errorf("group fingerprint = %q, want all", got)
	}
}

// TestCompareOp_EqualUnder plugs the missing under-target branch of
// the == operator. The existing test only covers exact (60==60), over
// (90 vs 60), and wildly off (6000 vs 60) — the SYMMETRIC under-target
// case (30 vs 60 → diff=30, prog=0.5) wasn't asserted, so a change
// that dropped the abs() on `diff` would still pass the wildly-off
// case but fail here with a negative or clamped-to-zero progress.
func TestCompareOp_EqualUnder(t *testing.T) {
	// current<target: same diff magnitude as the over-target test.
	if hit, prog := compareOp("==", 30, 60); hit || prog != 0.5 {
		t.Errorf("== under-target: hit=%v prog=%v (want false, 0.5 — symmetric around target)", hit, prog)
	}
	// The absolute-value contract is what makes this test meaningful.
	// Without abs(), diff would be -30 and prog would be 1 - (-30/60) =
	// 1.5, clamped to 1 → the test would fail claiming prog==1.
}

// TestCompareOp_EqualTargetZero fills the target==0 branch of the ==
// operator (existing tests only cover >= and <= at target=0). Only
// current==0 satisfies "exactly zero"; anything else must be false
// with a well-defined progress (max64(target,1) avoids DivByZero).
func TestCompareOp_EqualTargetZero(t *testing.T) {
	if hit, prog := compareOp("==", 0, 0); !hit || prog != 1 {
		t.Errorf("== 0==0: hit=%v prog=%v (want true, 1)", hit, prog)
	}
	// current=1 vs target=0: diff=1, max64(0,1)=1, prog=1-1/1=0.
	if hit, prog := compareOp("==", 1, 0); hit || prog != 0 {
		t.Errorf("== 1!=0: hit=%v prog=%v (want false, 0)", hit, prog)
	}
}

// TestCompareOp_UnknownOp exercises the default branch of compareOp
// (validator gates known ops upstream, but the evaluator's belt-and-
// suspenders default MUST return (false, 0) — a bug that returned
// (true, 1) on an unknown op would silently "hit" every unrecognized
// operator.
func TestCompareOp_UnknownOp(t *testing.T) {
	if hit, prog := compareOp("~=", 60, 60); hit || prog != 0 {
		t.Errorf("unknown op: hit=%v prog=%v (want false, 0)", hit, prog)
	}
}

// TestWindowRange_UnknownReturnsZeroSpan pins the fallback branch: an
// unknown window silently returns a zero-length range (start==end==
// today). Validator upstream rejects unknown windows so this branch
// shouldn't fire in practice — but if it EVER does (bypass, direct
// invocation), we want a zero-result query rather than a giant scan.
func TestWindowRange_UnknownReturnsZeroSpan(t *testing.T) {
	anchor := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	s, e := windowRange(anchor, "quarter") // not a valid window
	if !s.Equal(e) {
		t.Errorf("unknown window: span %v, want zero-length (start==end)", e.Sub(s))
	}
	// Should anchor to "today" (2026-07-15), NOT epoch.
	if s.Year() != 2026 || s.Month() != 7 || s.Day() != 15 {
		t.Errorf("unknown window fallback: got %v, want anchor date 2026-07-15", s)
	}
}

// TestRewriteWindowToDay_NestedStreak proves the streak node's window
// is NOT rewritten by rewriteWindowToDay (streak's recurrence is
// day-by-day inherently — rewriting it to "day" would corrupt the
// nested streak's semantics). A regression that added a
// `case "streak": dup.Window = "day"` branch would show up here.
func TestRewriteWindowToDay_NestedStreak(t *testing.T) {
	// active_days inside a nested streak: window MUST be preserved on
	// the streak node itself (streak has no Window; only its child does).
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
	if out.Kind != "streak" || out.Condition == nil {
		t.Fatalf("outer streak structure lost: %+v", out)
	}
	// Inner streak: window untouched (no window on streak itself);
	// its condition (deepest leaf) MUST be rewritten to "day".
	inner := out.Condition
	if inner.Kind != "streak" || inner.Condition == nil {
		t.Fatalf("nested streak structure lost: %+v", inner)
	}
	// Deepest leaf was "week" — rewriteWindowToDay must NOT descend
	// through streak (streak's own window controls its recurrence, not
	// its children's). So the leaf REMAINS "week" — this is the
	// documented contract in rewriteWindowToDay.
	if inner.Condition.Window != "week" {
		t.Errorf("rewriteWindowToDay descended through streak: nested leaf window = %q, want %q (streak owns its own recurrence)",
			inner.Condition.Window, "week")
	}
}

// TestRewriteWindowToDay_Not descends into `not`'s child. A regression
// that forgot the not branch would leave the child's window un-rewritten
// and streak wrapping-not-wrapping-time would evaluate as the outer
// author's window (say, week) instead of day.
func TestRewriteWindowToDay_Not(t *testing.T) {
	src := Predicate{
		Kind: "not",
		Of: []Predicate{{
			Kind: "time", Axis: "language", Op: ">=",
			TargetSeconds: 60, Window: "week",
		}},
	}
	out := rewriteWindowToDay(&src)
	if out.Kind != "not" || len(out.Of) != 1 {
		t.Fatalf("not structure lost: %+v", out)
	}
	if out.Of[0].Window != "day" {
		t.Errorf("not child window not rewritten: %q", out.Of[0].Window)
	}
}

// TestClamp01_Boundaries pins clamp01 at the exact edges the evaluator
// depends on. If a future refactor swaps `<` for `<=` (or vice versa),
// the test catches it — the interior boundary values (0 and 1) MUST
// pass through unchanged.
func TestClamp01_Boundaries(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{-0.5, 0}, {-0.001, 0}, {0, 0}, {0.5, 0.5}, {1, 1}, {1.001, 1}, {99, 1},
	}
	for _, c := range cases {
		if got := clamp01(c.in); got != c.want {
			t.Errorf("clamp01(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestValidateSpec_NestedAllAxisPropagates ensures the validator
// recurses through every child of `all` — a bug that only checked the
// first child would fail to catch an unknown axis buried in position 2.
func TestValidateSpec_NestedAllAxisPropagates(t *testing.T) {
	// First child valid; second child has an unknown axis. If the
	// validator short-circuits after the first child, this passes
	// silently. The failure of this test proves recursion into
	// EVERY sibling.
	bad := `{"kind":"all","of":[
		{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"week"},
		{"kind":"time","axis":"chicken","value":null,"op":">=","target_seconds":1,"window":"week"}
	]}`
	_, err := ValidateSpec(json.RawMessage(bad))
	if err == nil {
		t.Fatalf("expected error on nested bad-axis child, got nil")
	}
	if !strings.Contains(err.Error(), "unknown axis") {
		t.Errorf("err = %v, want to contain 'unknown axis'", err)
	}
}

// TestValidateSpec_NotChildValidated makes sure the validator recurses
// INTO the not predicate's single child. A missing recursion would
// let an invalid child slip through wrapped in not.
func TestValidateSpec_NotChildValidated(t *testing.T) {
	bad := `{"kind":"not","of":[
		{"kind":"time","axis":"language","value":null,"op":"!=","target_seconds":1,"window":"week"}
	]}`
	_, err := ValidateSpec(json.RawMessage(bad))
	if err == nil {
		t.Fatalf("not-wraps-bad-op: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown op") {
		t.Errorf("err = %v, want to contain 'unknown op'", err)
	}
}

// TestValidateSpec_StreakConditionValidated confirms the streak
// predicate recurses into its condition. A regression that dropped
// validateNode(p.Condition, ...) would let an invalid streak child
// through.
func TestValidateSpec_StreakConditionValidated(t *testing.T) {
	bad := `{"kind":"streak","min_days":3,"condition":{"kind":"time","axis":"chicken","op":">=","target_seconds":1,"window":"day"}}`
	_, err := ValidateSpec(json.RawMessage(bad))
	if err == nil {
		t.Fatalf("streak-wraps-bad-axis: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown axis") {
		t.Errorf("err = %v, want 'unknown axis' in %v", err, err)
	}
}

// TestMarshalUnmarshalProgress round-trips a Progress through
// MarshalProgress → UnmarshalProgress and asserts every field
// survives. Used by the cache path — corruption here would silently
// serve wrong percentages after a cache hit.
func TestMarshalUnmarshalProgress(t *testing.T) {
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
	if err != nil {
		t.Fatalf("MarshalProgress: %v", err)
	}
	back, err := UnmarshalProgress(raw)
	if err != nil || back == nil {
		t.Fatalf("UnmarshalProgress: %v back=%v", err, back)
	}
	if back.Hit != src.Hit || back.Progress != src.Progress {
		t.Errorf("hit/progress drift: got %v/%v want %v/%v", back.Hit, back.Progress, src.Hit, src.Progress)
	}
	if len(back.SubConditions) != 1 {
		t.Fatalf("sub len = %d, want 1", len(back.SubConditions))
	}
	got := back.SubConditions[0]
	if got.Current != 3600 || got.Target != 7200 || got.Progress != 0.5 || got.Hit {
		t.Errorf("sub-condition drift: %+v", got)
	}
	if got.Value == nil || *got.Value != "Go" {
		t.Errorf("value pointer lost: %v", got.Value)
	}
}

// TestUnmarshalProgress_EmptyRaw covers the fast-path: an empty raw
// message means "no cache row" and returns (nil, nil). A regression
// that treated empty as an error would flood the read path with
// nuisance errors.
func TestUnmarshalProgress_EmptyRaw(t *testing.T) {
	p, err := UnmarshalProgress(nil)
	if err != nil || p != nil {
		t.Errorf("nil raw: got p=%v err=%v (want nil, nil)", p, err)
	}
	p, err = UnmarshalProgress(json.RawMessage(``))
	if err != nil || p != nil {
		t.Errorf("empty raw: got p=%v err=%v (want nil, nil)", p, err)
	}
}
