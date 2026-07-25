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
