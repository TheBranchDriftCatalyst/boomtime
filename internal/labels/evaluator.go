// evaluator.go — the pure predicate walker. Mirrors conditions.ts +
// evaluator.ts one-for-one so a server-side EvaluateAll produces the same
// []LabelAward the client-side evaluate() used to. Do not add stateful
// behavior here (no clock reads, no random, no I/O).

package labels

import (
	"sort"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// Payload is the minimum shape the evaluator reads. Matches the FE's
// PublicDashboardPayload. Callers wire up a real *model.StatsPayload +
// PunchcardPayload through NewPayloadFromStats — kept as a separate type
// so tests can construct one directly without loading every field.
type Payload struct {
	Languages  []model.ResourceStats
	Editors    []model.ResourceStats
	Projects   []model.ResourceStats
	Categories []model.ResourceStats
	Platforms  []model.ResourceStats
	Punchcard  model.PunchcardPayload
	DailyTotal []int64
	DailyAvg   float64 // seconds/day
}

// axisEntries returns the payload slice for one axis. Returns nil when the
// axis name is unrecognized — treated as "no data" by every primitive.
func axisEntries(p *Payload, axis Axis) []model.ResourceStats {
	if p == nil {
		return nil
	}
	switch axis {
	case AxisLanguages:
		return p.Languages
	case AxisEditors:
		return p.Editors
	case AxisProjects:
		return p.Projects
	case AxisCategories:
		return p.Categories
	case AxisPlatforms:
		return p.Platforms
	}
	return nil
}

// findAxisEntry mirrors conditions.ts's case-insensitive lookup. Callers
// author catalog values by hand ("Python") but heartbeats can arrive as
// "python" — lowercasing both sides removes a whole category of "why isn't
// my label firing" bugs.
func findAxisEntry(p *Payload, axis Axis, value string) *model.ResourceStats {
	list := axisEntries(p, axis)
	target := strings.ToLower(value)
	for i := range list {
		if strings.ToLower(list[i].Name) == target {
			return &list[i]
		}
	}
	return nil
}

// axisTotalSeconds sums TotalSeconds across an axis (for the pct
// denominators). Matches conditions.ts axisTotalSeconds().
func axisTotalSeconds(p *Payload, axis Axis) int64 {
	var sum int64
	for _, r := range axisEntries(p, axis) {
		sum += r.TotalSeconds
	}
	return sum
}

// punchcardTotalSeconds prefers the top-level payload total but falls back
// to summing cells when it's zero (matches conditions.ts).
func punchcardTotalSeconds(p *Payload) int64 {
	if p == nil {
		return 0
	}
	if p.Punchcard.TotalSeconds > 0 {
		return p.Punchcard.TotalSeconds
	}
	var sum int64
	for _, c := range p.Punchcard.Cells {
		sum += c.Seconds
	}
	return sum
}

// last7VsPrior7Ratio splits DailyTotal into the last 7 and prior 7 days,
// returns lastAvg/priorAvg. Returns (0, false) when there are fewer than
// 14 days of data — matches conditions.ts's null semantics. When priorAvg
// is zero, returns +Inf (matches TS's Infinity fallback so downstream
// cmp() against a finite threshold naturally passes).
func last7VsPrior7Ratio(p *Payload) (float64, bool) {
	daily := p.DailyTotal
	if len(daily) < 14 {
		return 0, false
	}
	last7 := daily[len(daily)-7:]
	prior7 := daily[len(daily)-14 : len(daily)-7]
	var lastSum, priorSum int64
	for _, v := range last7 {
		lastSum += v
	}
	for _, v := range prior7 {
		priorSum += v
	}
	lastAvg := float64(lastSum) / 7.0
	priorAvg := float64(priorSum) / 7.0
	if priorAvg == 0 {
		if lastAvg > 0 {
			// Match TS: Infinity, cmp() with any finite threshold is true.
			// Go doesn't have a positive-infinity constant we care about;
			// return a very large number — anything ≥ any threshold used
			// in the seed manifests (< 100).
			return 1e18, true
		}
		return 0, true
	}
	return lastAvg / priorAvg, true
}

// currentStreak counts trailing consecutive non-zero days in DailyTotal.
// Mirrors grade.ts currentStreak().
func currentStreak(daily []int64) int {
	cur := 0
	for i := len(daily) - 1; i >= 0; i-- {
		if daily[i] > 0 {
			cur++
			continue
		}
		break
	}
	return cur
}

// longestStreakInRange returns the longest run of consecutive non-zero
// days anywhere in DailyTotal. Mirrors grade.ts longestStreak().
func longestStreakInRange(daily []int64) int {
	best, cur := 0, 0
	for _, v := range daily {
		if v > 0 {
			cur++
			if cur > best {
				best = cur
			}
			continue
		}
		cur = 0
	}
	return best
}

// cmp evaluates threshold comparisons inclusively at the boundary. Matches
// conditions.ts cmp(actual, op, threshold).
func cmp(actual float64, op CmpOp, threshold float64) bool {
	if op == OpGE {
		return actual >= threshold
	}
	return actual <= threshold
}

// EvaluateCondition walks one Condition against a payload. Pure — never
// panics on nil payload; empty payload just makes numeric primitives
// evaluate against zero.
func EvaluateCondition(cond Condition, p *Payload) bool {
	if cond == nil {
		return false
	}
	if p == nil {
		p = &Payload{} // treat nil as empty so every branch is well-defined
	}
	switch c := cond.(type) {
	case AxisTimeCond:
		hit := findAxisEntry(p, c.Axis, c.Value)
		var hours float64
		if hit != nil {
			hours = float64(hit.TotalSeconds) / 3600.0
		}
		return cmp(hours, c.Op, c.Hours)
	case AxisTimeSumCond:
		var sumSec int64
		for _, v := range c.Values {
			if hit := findAxisEntry(p, c.Axis, v); hit != nil {
				sumSec += hit.TotalSeconds
			}
		}
		return cmp(float64(sumSec)/3600.0, c.Op, c.Hours)
	case AxisPctCond:
		// gaka-hc6.6: the port initially mirrored the TS eval which
		// divided TotalPct by 100. That was wrong — the aggregation
		// emits TotalPct as a 0..1 decimal (from the SQL
		// `total_seconds / SUM(total_seconds) OVER ()`), not a percent.
		// TS was equally broken; the coverage test surfaced it.
		// Compute the share directly from seconds so we're immune to
		// whatever scale TotalPct happens to use.
		hit := findAxisEntry(p, c.Axis, c.Value)
		var pct float64
		if hit != nil {
			total := axisTotalSeconds(p, c.Axis)
			if total > 0 {
				pct = float64(hit.TotalSeconds) / float64(total)
			}
		}
		return cmp(pct, c.Op, c.Pct)
	case TopShareCond:
		list := axisEntries(p, c.Axis)
		if len(list) == 0 {
			return cmp(0, c.Op, c.Pct)
		}
		total := axisTotalSeconds(p, c.Axis)
		if total == 0 {
			return cmp(0, c.Op, c.Pct)
		}
		// Payload is pre-sorted desc by TotalSeconds — list[0] is the top.
		top := float64(list[0].TotalSeconds)
		return cmp(top/float64(total), c.Op, c.Pct)
	case DistinctCountCond:
		list := axisEntries(p, c.Axis)
		minSec := int64(c.MinHoursEach * 3600.0)
		qualifying := 0
		for _, r := range list {
			if r.TotalSeconds >= minSec {
				qualifying++
			}
		}
		return cmp(float64(qualifying), c.Op, float64(c.N))
	case PunchcardHourPctCond:
		total := punchcardTotalSeconds(p)
		if total == 0 {
			return cmp(0, c.Op, c.Pct)
		}
		hourSet := intSet(c.HoursIn)
		var bucket int64
		for _, cell := range p.Punchcard.Cells {
			if hourSet[cell.Hour] {
				bucket += cell.Seconds
			}
		}
		return cmp(float64(bucket)/float64(total), c.Op, c.Pct)
	case PunchcardDowPctCond:
		total := punchcardTotalSeconds(p)
		if total == 0 {
			return cmp(0, c.Op, c.Pct)
		}
		dowSet := intSet(c.DowIn)
		var bucket int64
		for _, cell := range p.Punchcard.Cells {
			if dowSet[cell.Dow] {
				bucket += cell.Seconds
			}
		}
		return cmp(float64(bucket)/float64(total), c.Op, c.Pct)
	case StreakCond:
		var days int
		if c.Which == "current" {
			days = currentStreak(p.DailyTotal)
		} else {
			days = longestStreakInRange(p.DailyTotal)
		}
		return cmp(float64(days), c.Op, float64(c.Days))
	case DailyAvgCond:
		hours := p.DailyAvg / 3600.0
		return cmp(hours, c.Op, c.Hours)
	case TrendCond:
		ratio, ok := last7VsPrior7Ratio(p)
		if !ok {
			return false
		}
		return cmp(ratio, c.Op, c.Ratio)
	case AllCond:
		for _, sub := range c.Of {
			if !EvaluateCondition(sub, p) {
				return false
			}
		}
		return true
	case AnyCond:
		for _, sub := range c.Of {
			if EvaluateCondition(sub, p) {
				return true
			}
		}
		return false
	case NotCond:
		return !EvaluateCondition(c.Of, p)
	}
	return false
}

func intSet(xs []int) map[int]bool {
	m := make(map[int]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// EvaluateAll walks every spec in the catalog, dedupes tier collisions
// (highest tier per tierKey wins), sorts by rank desc (id asc secondary),
// returns the LabelAward list. Mirrors evaluator.ts evaluate().
func EvaluateAll(p *Payload, catalog []LabelSpec) []LabelAward {
	if len(catalog) == 0 {
		return nil
	}
	// Pass 1: filter to specs whose condition holds.
	passing := make([]LabelSpec, 0, len(catalog))
	for _, s := range catalog {
		if EvaluateCondition(s.Condition, p) {
			passing = append(passing, s)
		}
	}
	if len(passing) == 0 {
		return nil
	}
	// Pass 2: tier collision — keep highest per tierKey. Non-tier specs
	// pass through directly.
	byTierKey := make(map[string]LabelSpec)
	nonTier := make([]LabelSpec, 0, len(passing))
	for _, s := range passing {
		if s.Kind == KindTier && s.TierKey != "" {
			cur, exists := byTierKey[s.TierKey]
			if !exists {
				byTierKey[s.TierKey] = s
				continue
			}
			if tierStrength(s.Tier) > tierStrength(cur.Tier) {
				byTierKey[s.TierKey] = s
			}
			continue
		}
		nonTier = append(nonTier, s)
	}
	// Pass 3: sort by rank desc (higher first), stable secondary by id asc.
	winners := make([]LabelSpec, 0, len(byTierKey)+len(nonTier))
	for _, s := range byTierKey {
		winners = append(winners, s)
	}
	winners = append(winners, nonTier...)
	sort.SliceStable(winners, func(i, j int) bool {
		if winners[i].Rank != winners[j].Rank {
			return winners[i].Rank > winners[j].Rank
		}
		return winners[i].ID < winners[j].ID
	})

	awards := make([]LabelAward, 0, len(winners))
	for _, s := range winners {
		awards = append(awards, LabelAward{
			ID:          s.ID,
			Kind:        s.Kind,
			Label:       s.Label,
			Glyph:       s.Glyph,
			Description: s.Description,
			Rank:        s.Rank,
			Tier:        s.Tier,
			Condition:   s.Condition,
		})
	}
	return awards
}
