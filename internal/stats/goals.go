// goals.go — composite predicate-tree evaluator for user-defined goals
// (gaka-wpb).
//
// The public surface:
//
//   - ValidateSpec(spec)    — parse + validate a JSONB spec (kind /
//     axis whitelists, non-negative numbers, recursion depth ≤ 5).
//     Called from handler POST/PATCH before persistence.
//   - Evaluate(ctx, pool, owner, spec) — walk the tree, return Progress.
//     Called from handler GET /progress (via the cache-freshness policy).
//   - GoalCacheTTL — single constant for "stale after".
//
// The evaluator queries hb_rollup_daily directly, mirroring the fast-path
// used by the Overview stats. Case-fold on the aggregation axis follows
// the same `lower(col) = lower($n)` convention gaka-5db locked in for
// curation/rename queries — a leaf `time` predicate targeting
// value="Python" MUST also count "python" / "PYTHON" rows, otherwise
// goals silently under-count.
//
// Window semantics:
//   - day       — last 24 h ending now.
//   - week      — last 7 days ending today (inclusive).
//   - month     — last 30 days ending today.
//   - year      — last 365 days ending today.
//   - lifetime  — no start bound (day >= '1970-01-01').
//
// Streak semantics:
//   - Evaluate the inner condition day by day going BACKWARD from today.
//     Count consecutive "hit" days until the first "miss". A day is a
//     hit only if the inner condition (re-evaluated with day-window
//     substituted for its own window) reports hit=true. min_days is the
//     TARGET; progress = min(1, streak_days / min_days).
//   - Max min_days is capped at 365 by the validator so a hostile spec
//     can't force N evaluator round trips.
//
// Composition:
//   - all — hit = ∧children_hit; progress = min(children).
//   - any — hit = ∨children_hit; progress = max(children).
//   - not — hit = !child_hit; progress = 1 - child.progress.
//
// The evaluator is stateless — reuse the pool across requests, one
// Evaluate call per goal. The cache lives in the goals table
// (last_progress + last_evaluated_at), NOT in this package.
package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GoalCacheTTL is the "fresh" window for cached goal progress. Reads
// within this window of the last evaluation reuse the cached
// last_progress row; older reads trigger a re-evaluation. Defined once
// here — the handler references it to compare timestamps, the DB layer
// clears the cache on write. Changing this constant is the ONLY place
// the freshness policy is tuned.
const GoalCacheTTL = 60 * time.Second

// MaxPredicateDepth caps the recursion depth of a spec tree. 5 is deep
// enough for any real goal ("this AND (that OR the-other)" is depth 2 —
// depth 5 is truly nested). Rejects hostile deep trees at validate
// time.
const MaxPredicateDepth = 5

// MaxStreakDays caps the min_days field on a streak predicate. Every
// unit of min_days costs one leaf evaluation, so we bound it explicitly
// (a 90-day streak is 90 SQL round trips even under batching). One year
// is generous — nobody sets a "500-day streak" goal for real.
const MaxStreakDays = 365

// validHeartbeatAxes mirrors the raw + rollup columns of hb_rollup_daily
// that a `time` leaf may target. Kept as a small set for the validator
// AND used by the SQL query to guard axis→column mapping (an unknown
// axis never reaches the query — validator gates it).
var validHeartbeatAxes = map[string]string{
	"language": "language",
	"project":  "project",
	"editor":   "editor",
	"category": "category",
	"branch":   "branch",
	"plugin":   "plugin",
	"machine":  "machine",
	"platform": "platform",
}

// validTimeWindows lists the enum values a `time` leaf may set.
var validTimeWindows = map[string]bool{
	"day":      true,
	"week":     true,
	"month":    true,
	"year":     true,
	"lifetime": true,
}

// validActiveDaysWindows: active_days measures the count of distinct
// active days, so "day" and "lifetime" would be meaningless (a single
// day is always 0 or 1 active days; lifetime is unbounded). Restrict to
// finite multi-day windows.
var validActiveDaysWindows = map[string]bool{
	"week":  true,
	"month": true,
	"year":  true,
}

var validOps = map[string]bool{">=": true, "<=": true, "==": true}

// Predicate is the discriminated union representing one node of the
// goal spec tree. The Kind field is the tag; only the fields relevant
// to the kind are populated (others left zero). Marshals back out to
// the same JSON shape the FE sent thanks to `omitempty`.
type Predicate struct {
	Kind string `json:"kind"`

	// time leaf
	Axis          string  `json:"axis,omitempty"`
	Value         *string `json:"value,omitempty"`
	Op            string  `json:"op,omitempty"`
	TargetSeconds int64   `json:"target_seconds,omitempty"`
	Window        string  `json:"window,omitempty"`

	// streak
	Condition *Predicate `json:"condition,omitempty"`
	MinDays   int        `json:"min_days,omitempty"`

	// active_days
	N int `json:"n,omitempty"`
	// active_days also uses Op and Window (declared above).

	// all / any
	Of []Predicate `json:"of,omitempty"`
	// not uses `of` as a length-1 array to keep the wire shape uniform;
	// the type doesn't need a separate NotOf field.
}

// ValidateSpec parses `spec` as a Predicate tree and enforces every
// shape constraint at once. Returns nil on OK, a user-safe error
// otherwise. The handler surfaces the error text on the 400 response
// so authors can correct their spec.
//
// Non-tautology anchor: every branch checked here has a matching
// negative test in goals_test.go — a change here that loosens a
// constraint MUST be caught by a test.
func ValidateSpec(spec json.RawMessage) (*Predicate, error) {
	if len(spec) == 0 {
		return nil, errors.New("spec is empty")
	}
	var p Predicate
	if err := json.Unmarshal(spec, &p); err != nil {
		return nil, fmt.Errorf("spec is not valid JSON: %w", err)
	}
	if err := validateNode(&p, 1); err != nil {
		return nil, err
	}
	return &p, nil
}

// validateNode is the recursive workhorse. `depth` is 1-based: the root
// call gets depth=1, its immediate children depth=2, etc. Reject when
// depth > MaxPredicateDepth.
func validateNode(p *Predicate, depth int) error {
	if p == nil {
		return errors.New("predicate is nil")
	}
	if depth > MaxPredicateDepth {
		return fmt.Errorf("spec depth %d exceeds maximum %d", depth, MaxPredicateDepth)
	}
	switch p.Kind {
	case "time":
		if _, ok := validHeartbeatAxes[p.Axis]; !ok {
			return fmt.Errorf("unknown axis %q on time predicate", p.Axis)
		}
		if !validTimeWindows[p.Window] {
			return fmt.Errorf("unknown window %q on time predicate", p.Window)
		}
		if !validOps[p.Op] {
			return fmt.Errorf("unknown op %q on time predicate", p.Op)
		}
		if p.TargetSeconds < 0 {
			return fmt.Errorf("target_seconds must be non-negative (got %d)", p.TargetSeconds)
		}
		return nil
	case "streak":
		if p.MinDays < 0 {
			return fmt.Errorf("min_days must be non-negative (got %d)", p.MinDays)
		}
		if p.MinDays > MaxStreakDays {
			return fmt.Errorf("min_days %d exceeds maximum %d", p.MinDays, MaxStreakDays)
		}
		if p.Condition == nil {
			return errors.New("streak predicate missing condition")
		}
		return validateNode(p.Condition, depth+1)
	case "active_days":
		if !validActiveDaysWindows[p.Window] {
			return fmt.Errorf("unknown window %q on active_days predicate (must be week/month/year)", p.Window)
		}
		if !validOps[p.Op] {
			return fmt.Errorf("unknown op %q on active_days predicate", p.Op)
		}
		if p.N < 0 {
			return fmt.Errorf("n must be non-negative (got %d)", p.N)
		}
		return nil
	case "all", "any":
		if len(p.Of) == 0 {
			return fmt.Errorf("%s predicate requires at least one child in `of`", p.Kind)
		}
		for i := range p.Of {
			if err := validateNode(&p.Of[i], depth+1); err != nil {
				return err
			}
		}
		return nil
	case "not":
		if len(p.Of) != 1 {
			return fmt.Errorf("not predicate requires exactly one child in `of` (got %d)", len(p.Of))
		}
		return validateNode(&p.Of[0], depth+1)
	default:
		return fmt.Errorf("unknown predicate kind %q", p.Kind)
	}
}

// SubCondition is one leaf's evaluated snapshot in the returned
// Progress tree. Group predicates (all/any/not) don't emit sub-
// conditions themselves — only leaves + streak+active_days summarize
// as SubCondition; the group's contribution is captured in its own
// progress + hit fields.
type SubCondition struct {
	Kind     string  `json:"kind"`
	Axis     string  `json:"axis,omitempty"`
	Value    *string `json:"value,omitempty"`
	Op       string  `json:"op,omitempty"`
	Window   string  `json:"window,omitempty"`
	Current  int64   `json:"current"`
	Target   int64   `json:"target"`
	Progress float64 `json:"progress"`
	Hit      bool    `json:"hit"`
}

// Progress is the evaluator's output. Persisted verbatim into
// goals.last_progress; returned to the FE via GET /progress. `Hit` is
// the boolean roll-up (does the whole tree satisfy?); `Progress` is
// the same aggregated as a 0..1 fraction (min for all, max for any,
// 1-x for not). SubConditions is a FLAT list of all leaves under the
// root — the FE renders them as the per-condition detail rows.
type Progress struct {
	Hit           bool           `json:"hit"`
	Progress      float64        `json:"progress"`
	SubConditions []SubCondition `json:"sub_conditions"`
}

// Evaluate walks the predicate tree and returns the Progress. `now` is
// the anchor for time windows; passing a fixed time in tests keeps
// results deterministic (production callers pass time.Now().UTC()).
func Evaluate(ctx context.Context, pool *pgxpool.Pool, owner string, p *Predicate, now time.Time) (*Progress, error) {
	if pool == nil {
		return nil, errors.New("Evaluate: nil pool")
	}
	if owner == "" {
		return nil, errors.New("Evaluate: empty owner")
	}
	if p == nil {
		return nil, errors.New("Evaluate: nil predicate")
	}
	e := &evaluator{pool: pool, owner: owner, now: now}
	hit, prog, err := e.walk(ctx, p)
	if err != nil {
		return nil, err
	}
	return &Progress{Hit: hit, Progress: prog, SubConditions: e.subs}, nil
}

type evaluator struct {
	pool  *pgxpool.Pool
	owner string
	now   time.Time
	subs  []SubCondition
}

// walk returns (hit, progress) for one node and accumulates leaves
// into e.subs as it goes. progress is clamped 0..1.
func (e *evaluator) walk(ctx context.Context, p *Predicate) (bool, float64, error) {
	switch p.Kind {
	case "time":
		return e.evalTime(ctx, p)
	case "active_days":
		return e.evalActiveDays(ctx, p)
	case "streak":
		return e.evalStreak(ctx, p)
	case "all":
		return e.evalAll(ctx, p)
	case "any":
		return e.evalAny(ctx, p)
	case "not":
		return e.evalNot(ctx, p)
	default:
		// ValidateSpec should have caught this — belt-and-suspenders.
		return false, 0, fmt.Errorf("evaluator: unknown kind %q", p.Kind)
	}
}

// windowRange resolves a window enum + anchor time into an
// (inclusive-start, inclusive-end) date range for the rollup query.
// Both bounds are UTC dates (time-of-day zeroed). "lifetime" returns a
// very-old start.
func windowRange(now time.Time, window string) (time.Time, time.Time) {
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch window {
	case "day":
		return end, end
	case "week":
		return end.AddDate(0, 0, -6), end // 7 days inclusive
	case "month":
		return end.AddDate(0, 0, -29), end
	case "year":
		return end.AddDate(0, 0, -364), end
	case "lifetime":
		return time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), end
	default:
		// Validator gates this; a stray call returns a zero-length range.
		return end, end
	}
}

// evalTime runs the leaf time query: sum(total_seconds) from the
// rollup, owner-scoped, axis+value filtered case-insensitively, over
// the window. A nil Value means "match ANY value on the axis" (i.e.,
// total time on the axis, e.g. total coding time regardless of
// language). This mirrors how a hostage-taker composite might phrase
// "1 hour on any language" via a language leaf with value=null.
func (e *evaluator) evalTime(ctx context.Context, p *Predicate) (bool, float64, error) {
	col, ok := validHeartbeatAxes[p.Axis]
	if !ok {
		return false, 0, fmt.Errorf("evalTime: unknown axis %q", p.Axis)
	}
	start, end := windowRange(e.now, p.Window)

	// Case-fold on the axis value (gaka-5db lesson). When Value is nil,
	// the axis filter is dropped entirely — we want the total on the
	// axis regardless of value. When Value is "" we still lower-fold
	// (empty matches empty; distinct from nil).
	var (
		q       string
		args    []any
		current int64
	)
	if p.Value == nil {
		q = `SELECT COALESCE(SUM(total_seconds), 0)
		     FROM hb_rollup_daily
		     WHERE sender = $1 AND day >= $2::date AND day <= $3::date`
		args = []any{e.owner, start, end}
	} else {
		q = `SELECT COALESCE(SUM(total_seconds), 0)
		     FROM hb_rollup_daily
		     WHERE sender = $1 AND day >= $2::date AND day <= $3::date
		       AND lower(` + col + `) = lower($4)`
		args = []any{e.owner, start, end, *p.Value}
	}
	if err := e.pool.QueryRow(ctx, q, args...).Scan(&current); err != nil {
		return false, 0, fmt.Errorf("evalTime query: %w", err)
	}
	hit, prog := compareOp(p.Op, current, p.TargetSeconds)
	e.subs = append(e.subs, SubCondition{
		Kind: "time", Axis: p.Axis, Value: p.Value, Op: p.Op, Window: p.Window,
		Current: current, Target: p.TargetSeconds, Progress: prog, Hit: hit,
	})
	return hit, prog, nil
}

// evalActiveDays counts DISTINCT days with any activity for the owner
// in the window, compares against p.N. Distinct on the day column of
// hb_rollup_daily — the ingest path only writes rollup rows for days
// that received at least one heartbeat, so a distinct count IS the
// active-day count.
func (e *evaluator) evalActiveDays(ctx context.Context, p *Predicate) (bool, float64, error) {
	start, end := windowRange(e.now, p.Window)
	var current int64
	err := e.pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT day)
		 FROM hb_rollup_daily
		 WHERE sender = $1 AND day >= $2::date AND day <= $3::date`,
		e.owner, start, end).Scan(&current)
	if err != nil {
		return false, 0, fmt.Errorf("evalActiveDays query: %w", err)
	}
	hit, prog := compareOp(p.Op, current, int64(p.N))
	e.subs = append(e.subs, SubCondition{
		Kind: "active_days", Op: p.Op, Window: p.Window,
		Current: current, Target: int64(p.N), Progress: prog, Hit: hit,
	})
	return hit, prog, nil
}

// evalStreak walks backward from today, re-evaluating the inner
// condition with the WINDOW OVERRIDDEN TO "day" for each day. Counts
// consecutive hits until the first miss. min_days is the target.
//
// Cost: one leaf evaluation per day until first miss (or min_days
// reached). Capped by MaxStreakDays at validate time. Inner sub-
// conditions from the per-day evaluations are NOT accumulated — that
// would flood the SubConditions list with min_days entries. We emit ONE
// summary sub-condition for the streak node.
//
// Cycle-through evaluator note: we run a nested evaluator per day so
// the per-day scratch doesn't pollute the outer subs list.
func (e *evaluator) evalStreak(ctx context.Context, p *Predicate) (bool, float64, error) {
	if p.Condition == nil {
		return false, 0, errors.New("evalStreak: nil condition")
	}
	// Re-evaluate the inner condition each day going backward. Stop at
	// the first miss OR when we've counted enough (streak counts up to
	// min_days, not beyond — extra days don't help the progress ratio).
	// If min_days is 0, the goal is trivially hit; return (true, 1) so a
	// hostile "min_days=0 streak" doesn't cause an infinite loop.
	if p.MinDays <= 0 {
		e.subs = append(e.subs, SubCondition{
			Kind: "streak", Current: 0, Target: 0, Progress: 1, Hit: true,
		})
		return true, 1, nil
	}
	daysHit := 0
	// walkBackDaysMax bounds the streak walk. We NEED at LEAST min_days
	// consecutive hits ending today; there's no upside to looking further.
	walkBackDaysMax := p.MinDays
	for i := 0; i < walkBackDaysMax; i++ {
		daySpec := *p.Condition
		daySpec = *rewriteWindowToDay(&daySpec)
		// Anchor time = now - i days.
		anchor := e.now.AddDate(0, 0, -i)
		nested := &evaluator{pool: e.pool, owner: e.owner, now: anchor}
		hit, _, err := nested.walk(ctx, &daySpec)
		if err != nil {
			return false, 0, fmt.Errorf("evalStreak day-%d: %w", i, err)
		}
		if !hit {
			break
		}
		daysHit++
	}
	hit, prog := compareOp(">=", int64(daysHit), int64(p.MinDays))
	e.subs = append(e.subs, SubCondition{
		Kind: "streak", Current: int64(daysHit), Target: int64(p.MinDays),
		Progress: prog, Hit: hit,
	})
	return hit, prog, nil
}

// rewriteWindowToDay clones a predicate and overrides every window
// field to "day". Used by evalStreak so the inner condition is
// evaluated per-day even if the author wrote e.g. "week"-window time
// leaves. Rewrites recursively so nested groups get the treatment too.
func rewriteWindowToDay(p *Predicate) *Predicate {
	if p == nil {
		return nil
	}
	// Deep copy the outer node; recurse into children.
	dup := *p
	switch dup.Kind {
	case "time":
		dup.Window = "day"
	case "active_days":
		// active_days can't be evaluated over "day" (validator rejects).
		// A streak whose inner is active_days doesn't really make sense
		// but if authored we short-circuit to a permissive "day" check —
		// the outer streak count will still measure consecutive
		// "some-activity-that-day" hits under the child's own semantics.
		dup.Window = "week"
	case "streak":
		// Nested streaks are permitted by validator; here we don't
		// rewrite (streak's own window is inherent to the recurrence).
	case "all", "any":
		out := make([]Predicate, len(dup.Of))
		for i := range dup.Of {
			out[i] = *rewriteWindowToDay(&dup.Of[i])
		}
		dup.Of = out
	case "not":
		if len(dup.Of) == 1 {
			out := *rewriteWindowToDay(&dup.Of[0])
			dup.Of = []Predicate{out}
		}
	}
	return &dup
}

// evalAll: hit = ∧children_hit, progress = min(children).
func (e *evaluator) evalAll(ctx context.Context, p *Predicate) (bool, float64, error) {
	hit := true
	minProg := 1.0
	for i := range p.Of {
		h, pr, err := e.walk(ctx, &p.Of[i])
		if err != nil {
			return false, 0, err
		}
		if !h {
			hit = false
		}
		if pr < minProg {
			minProg = pr
		}
	}
	return hit, minProg, nil
}

// evalAny: hit = ∨children_hit, progress = max(children).
func (e *evaluator) evalAny(ctx context.Context, p *Predicate) (bool, float64, error) {
	hit := false
	maxProg := 0.0
	for i := range p.Of {
		h, pr, err := e.walk(ctx, &p.Of[i])
		if err != nil {
			return false, 0, err
		}
		if h {
			hit = true
		}
		if pr > maxProg {
			maxProg = pr
		}
	}
	return hit, maxProg, nil
}

// evalNot: hit = !child_hit, progress = 1 - child.progress (a "goal
// almost missed" corresponds to progress≈0, "safely avoided" ≈ 1).
func (e *evaluator) evalNot(ctx context.Context, p *Predicate) (bool, float64, error) {
	if len(p.Of) != 1 {
		return false, 0, fmt.Errorf("evalNot: expected 1 child, got %d", len(p.Of))
	}
	h, pr, err := e.walk(ctx, &p.Of[0])
	if err != nil {
		return false, 0, err
	}
	return !h, clamp01(1 - pr), nil
}

// compareOp evaluates `current OP target` and returns (hit, progress).
// Progress for `>=` is current/target (capped 0..1); for `<=` it's the
// "distance below" (1 when current==0, 0 when current>=target). For
// `==`, hit is exact equality and progress is a triangle around the
// target (rarely used but the semantic is "closest to target").
func compareOp(op string, current, target int64) (bool, float64) {
	switch op {
	case ">=":
		if target <= 0 {
			return true, 1
		}
		p := float64(current) / float64(target)
		return current >= target, clamp01(p)
	case "<=":
		if target <= 0 {
			// "<=0" is only hit at exactly 0.
			hit := current == 0
			return hit, boolAsFloat(hit)
		}
		if current <= target {
			// Room to spare: report how much room remains as progress.
			// 1 = "no seconds used", 0 = "exactly at the cap".
			return true, clamp01(1 - float64(current)/float64(target))
		}
		return false, 0
	case "==":
		if current == target {
			return true, 1
		}
		// Distance-based progress for near-misses; symmetric around target.
		diff := current - target
		if diff < 0 {
			diff = -diff
		}
		p := 1 - float64(diff)/float64(max64(target, 1))
		return false, clamp01(p)
	default:
		return false, 0
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
func boolAsFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// MarshalProgress is a tiny helper so callers can persist the Progress
// as JSONB without knowing the field layout. Isolated for symmetry with
// UnmarshalProgress and to make the cache-write path readable.
func MarshalProgress(p *Progress) (json.RawMessage, error) {
	return json.Marshal(p)
}

// UnmarshalProgress parses cached bytes back into a Progress. Used by
// the handler to serve cached progress without re-computing.
func UnmarshalProgress(raw json.RawMessage) (*Progress, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var p Progress
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// SpecShallowFingerprint returns a stable string uniquely identifying
// the top-level shape of a spec. Not currently used by the evaluator
// but exposed as a helper for the handler to key debug logs by. Kept
// tiny — string prefix + kind is enough for log correlation.
func SpecShallowFingerprint(p *Predicate) string {
	if p == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(p.Kind)
	if p.Kind == "time" {
		sb.WriteByte(':')
		sb.WriteString(p.Axis)
		sb.WriteByte(':')
		sb.WriteString(p.Window)
	}
	return sb.String()
}
