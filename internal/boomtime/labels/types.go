// Package labels is the Go port of the client-side label DSL that used to
// live in web/src/features/publicprofile/labels/. Mirrors conditions.ts +
// evaluator.ts + tierLabels.ts + types.ts byte-for-byte in semantics so a
// server-side evaluate produces the same awards a browser-side evaluate
// would produce for the same payload.
//
// Ticket: gaka-hc6.1 (part of the "move evaluator server-side" epic).
package labels

import (
	"encoding/json"
	"fmt"
)

// Axis names one of the five payload dimensions the evaluator can inspect.
// Matches the TS union exactly (no "machines" — public payload strips it).
type Axis string

const (
	AxisLanguages  Axis = "languages"
	AxisEditors    Axis = "editors"
	AxisProjects   Axis = "projects"
	AxisCategories Axis = "categories"
	AxisPlatforms  Axis = "platforms"
)

// CmpOp is >= or <=. Inclusive semantics at threshold — a Master condition
// at 100h fires on EXACTLY 100h too. Matches conditions.ts cmp().
type CmpOp string

const (
	OpGE CmpOp = ">="
	OpLE CmpOp = "<="
)

// LabelTier is the tier band for kind='tier' labels. Ordering (novice=0
// through legend=4) drives the "keep highest tier per tierKey" dedupe in
// EvaluateAll.
type LabelTier string

const (
	TierNovice     LabelTier = "novice"
	TierApprentice LabelTier = "apprentice"
	TierAdept      LabelTier = "adept"
	TierMaster     LabelTier = "master"
	TierLegend     LabelTier = "legend"
)

// tierStrength ranks tiers for the dedupe compare — same table as TS
// evaluator.ts's TIER_STRENGTH. -1 sentinel is used when a spec has no
// tier at all so the tier-labelled variant wins any collision.
func tierStrength(t LabelTier) int {
	switch t {
	case TierNovice:
		return 0
	case TierApprentice:
		return 1
	case TierAdept:
		return 2
	case TierMaster:
		return 3
	case TierLegend:
		return 4
	}
	return -1
}

// LabelKind is the display bucket a label lives in. Not evaluated —
// display-only. Kept exhaustive so an unknown kind flowing in from a
// migration that added a new bucket is loud, not silent.
type LabelKind string

const (
	KindTier      LabelKind = "tier"
	KindArchetype LabelKind = "archetype"
	KindTribe     LabelKind = "tribe"
	KindMeme      LabelKind = "meme"
	KindPatch     LabelKind = "patch"
)

// -- Condition primitives --------------------------------------------------
//
// Every Condition is a struct that carries its own kind literal for JSON
// round-tripping. Kind() returns the discriminator string; UnmarshalJSON
// on the Condition wrapper peeks at "kind" then decodes into the right
// concrete type. Encoding is trivial — the struct tags carry the shape.
//
// The interface stays minimal on purpose (just Kind()) — evaluation happens
// via a type switch in EvaluateCondition rather than a virtual method. Kept
// symmetric with the TS approach where the switch lives in one file that
// can be read end-to-end.

// Condition is the union of every predicate primitive plus the three
// composers. All concrete types embed a marker so the type switch in
// EvaluateCondition covers the exhaustive set.
type Condition interface {
	Kind() string
	isCondition()
}

// AxisTimeCond — hours on ONE (axis, value) crosses threshold.
type AxisTimeCond struct {
	Axis  Axis    `json:"axis"`
	Value string  `json:"value"`
	Op    CmpOp   `json:"op"`
	Hours float64 `json:"hours"`
}

func (AxisTimeCond) Kind() string { return "axis-time" }
func (AxisTimeCond) isCondition() {}

// AxisTimeSumCond — hours SUMMED across N (axis, value) pairs crosses
// threshold. Powers TERMINAL PURIST (vim+neovim+emacs) and similar.
type AxisTimeSumCond struct {
	Axis   Axis     `json:"axis"`
	Values []string `json:"values"`
	Op     CmpOp    `json:"op"`
	Hours  float64  `json:"hours"`
}

func (AxisTimeSumCond) Kind() string { return "axis-time-sum" }
func (AxisTimeSumCond) isCondition() {}

// AxisPctCond — one axis-value's share of the axis total (0..1) crosses
// threshold. Note: payload totalPct is 0..100, DSL is 0..1; divide by 100.
type AxisPctCond struct {
	Axis  Axis    `json:"axis"`
	Value string  `json:"value"`
	Op    CmpOp   `json:"op"`
	Pct   float64 `json:"pct"`
}

func (AxisPctCond) Kind() string { return "axis-pct" }
func (AxisPctCond) isCondition() {}

// TopShareCond — the top entry's share of the axis total. Payload is
// pre-sorted desc by TotalSeconds so list[0] IS the top entry.
type TopShareCond struct {
	Axis Axis    `json:"axis"`
	Op   CmpOp   `json:"op"`
	Pct  float64 `json:"pct"`
}

func (TopShareCond) Kind() string { return "top-share" }
func (TopShareCond) isCondition() {}

// DistinctCountCond — number of axis entries with ≥ minHoursEach hours
// crosses threshold. Powers Polyglot ("5 languages each ≥ 20h").
type DistinctCountCond struct {
	Axis         Axis    `json:"axis"`
	MinHoursEach float64 `json:"minHoursEach"`
	Op           CmpOp   `json:"op"`
	N            int     `json:"n"`
}

func (DistinctCountCond) Kind() string { return "distinct-count" }
func (DistinctCountCond) isCondition() {}

// PunchcardHourPctCond — % of punchcard time inside a set of hours-of-day
// crosses threshold. 0..23.
type PunchcardHourPctCond struct {
	HoursIn []int   `json:"hoursIn"`
	Op      CmpOp   `json:"op"`
	Pct     float64 `json:"pct"`
}

func (PunchcardHourPctCond) Kind() string { return "punchcard-hour-pct" }
func (PunchcardHourPctCond) isCondition() {}

// PunchcardDowPctCond — % of punchcard time inside a set of days-of-week
// (0=Sun..6=Sat) crosses threshold.
type PunchcardDowPctCond struct {
	DowIn []int   `json:"dowIn"`
	Op    CmpOp   `json:"op"`
	Pct   float64 `json:"pct"`
}

func (PunchcardDowPctCond) Kind() string { return "punchcard-dow-pct" }
func (PunchcardDowPctCond) isCondition() {}

// StreakCond — current OR longest consecutive-day streak crosses threshold.
type StreakCond struct {
	Which string `json:"which"` // "current" | "longest"
	Op    CmpOp  `json:"op"`
	Days  int    `json:"days"`
}

func (StreakCond) Kind() string { return "streak" }
func (StreakCond) isCondition() {}

// DailyAvgCond — hours-per-day average crosses threshold. Payload's
// dailyAvg field is seconds; convert to hours.
type DailyAvgCond struct {
	Op    CmpOp   `json:"op"`
	Hours float64 `json:"hours"`
}

func (DailyAvgCond) Kind() string { return "daily-avg" }
func (DailyAvgCond) isCondition() {}

// TrendCond — last-7-day avg / prior-7-day avg crosses threshold. Insufficient
// history (< 14 days) → doesn't fire (matches TS: returns false, no award).
type TrendCond struct {
	Window string  `json:"window"` // "last7-vs-prior7"
	Op     CmpOp   `json:"op"`
	Ratio  float64 `json:"ratio"`
}

func (TrendCond) Kind() string { return "trend" }
func (TrendCond) isCondition() {}

// AllCond — every sub-condition must hold.
type AllCond struct {
	Of []Condition `json:"of"`
}

func (AllCond) Kind() string { return "all" }
func (AllCond) isCondition() {}

// AnyCond — at least one sub-condition must hold.
type AnyCond struct {
	Of []Condition `json:"of"`
}

func (AnyCond) Kind() string { return "any" }
func (AnyCond) isCondition() {}

// NotCond — negation of a single sub-condition.
type NotCond struct {
	Of Condition `json:"of"`
}

func (NotCond) Kind() string { return "not" }
func (NotCond) isCondition() {}

// -- JSON marshalling ------------------------------------------------------
//
// Encoding: each primitive marshals as its natural struct with a "kind"
// field added. Wrapping every MarshalJSON to inject "kind" is repetitive
// but keeps the round-trip byte-shape identical to the TS JSON.
//
// Decoding: JSON.Unmarshal on `*Condition` peeks at "kind" first, then
// decodes the payload into the concrete type. The composers (all/any/not)
// recursively unmarshal their `of` field(s) through the same wrapper.

// conditionEnvelope is only used during Unmarshal to peek at the
// discriminator. Fields for every primitive coexist as RawMessage so we
// don't have to reparse the whole blob after picking the concrete type.
type conditionEnvelope struct {
	Kind string          `json:"kind"`
	Raw  json.RawMessage `json:"-"`
}

// UnmarshalCondition decodes a single Condition from JSON. Exposed so
// LabelSpec.UnmarshalJSON can defer to it — the DB stores conditions as
// jsonb and rows scan them into json.RawMessage.
func UnmarshalCondition(data []byte) (Condition, error) {
	// Peek the kind first.
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("condition: peek kind: %w", err)
	}
	switch probe.Kind {
	case "axis-time":
		var c AxisTimeCond
		return c, json.Unmarshal(data, &c)
	case "axis-time-sum":
		var c AxisTimeSumCond
		return c, json.Unmarshal(data, &c)
	case "axis-pct":
		var c AxisPctCond
		return c, json.Unmarshal(data, &c)
	case "top-share":
		var c TopShareCond
		return c, json.Unmarshal(data, &c)
	case "distinct-count":
		var c DistinctCountCond
		return c, json.Unmarshal(data, &c)
	case "punchcard-hour-pct":
		var c PunchcardHourPctCond
		return c, json.Unmarshal(data, &c)
	case "punchcard-dow-pct":
		var c PunchcardDowPctCond
		return c, json.Unmarshal(data, &c)
	case "streak":
		var c StreakCond
		return c, json.Unmarshal(data, &c)
	case "daily-avg":
		var c DailyAvgCond
		return c, json.Unmarshal(data, &c)
	case "trend":
		var c TrendCond
		return c, json.Unmarshal(data, &c)
	case "all":
		// "of" is []Condition — need to unmarshal each recursively.
		var raw struct {
			Of []json.RawMessage `json:"of"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		out := AllCond{Of: make([]Condition, 0, len(raw.Of))}
		for i, r := range raw.Of {
			sub, err := UnmarshalCondition(r)
			if err != nil {
				return nil, fmt.Errorf("all/of[%d]: %w", i, err)
			}
			out.Of = append(out.Of, sub)
		}
		return out, nil
	case "any":
		var raw struct {
			Of []json.RawMessage `json:"of"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		out := AnyCond{Of: make([]Condition, 0, len(raw.Of))}
		for i, r := range raw.Of {
			sub, err := UnmarshalCondition(r)
			if err != nil {
				return nil, fmt.Errorf("any/of[%d]: %w", i, err)
			}
			out.Of = append(out.Of, sub)
		}
		return out, nil
	case "not":
		var raw struct {
			Of json.RawMessage `json:"of"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		sub, err := UnmarshalCondition(raw.Of)
		if err != nil {
			return nil, fmt.Errorf("not/of: %w", err)
		}
		return NotCond{Of: sub}, nil
	}
	return nil, fmt.Errorf("condition: unknown kind %q", probe.Kind)
}

// MarshalCondition encodes a Condition as JSON, injecting the "kind"
// discriminator (each primitive struct doesn't carry Kind as a field —
// that would duplicate the type identity).
func MarshalCondition(c Condition) ([]byte, error) {
	// Encode the struct, then splice "kind" into the resulting object.
	// Faster than reflection-based tagging and matches the TS output.
	inner, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	// Composers need their Of field re-encoded via MarshalCondition so the
	// discriminator lands on nested objects too.
	switch v := c.(type) {
	case AllCond:
		return marshalComposer("all", v.Of)
	case AnyCond:
		return marshalComposer("any", v.Of)
	case NotCond:
		subBytes, err := MarshalCondition(v.Of)
		if err != nil {
			return nil, err
		}
		return []byte(fmt.Sprintf(`{"kind":"not","of":%s}`, subBytes)), nil
	}
	// Primitive: inner is `{...}` — inject kind after the opening brace.
	if len(inner) < 2 || inner[0] != '{' {
		return nil, fmt.Errorf("condition: malformed inner encoding")
	}
	out := make([]byte, 0, len(inner)+16)
	out = append(out, '{')
	out = append(out, []byte(fmt.Sprintf(`"kind":%q`, c.Kind()))...)
	if len(inner) > 2 {
		out = append(out, ',')
		out = append(out, inner[1:]...)
	} else {
		out = append(out, '}')
	}
	return out, nil
}

func marshalComposer(kind string, of []Condition) ([]byte, error) {
	parts := make([]json.RawMessage, 0, len(of))
	for i, sub := range of {
		b, err := MarshalCondition(sub)
		if err != nil {
			return nil, fmt.Errorf("%s/of[%d]: %w", kind, i, err)
		}
		parts = append(parts, b)
	}
	inner, err := json.Marshal(parts)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf(`{"kind":%q,"of":%s}`, kind, inner)), nil
}

// -- Label spec + award ----------------------------------------------------

// LabelSpec is one row from the labels table (post-decoding). The
// discriminated Condition takes JSONB from labels.condition through
// UnmarshalCondition.
type LabelSpec struct {
	ID            string
	Kind          LabelKind
	Label         string
	Glyph         string
	Description   string
	Rank          int
	Tier          LabelTier // empty for non-tier kinds
	TierKey       string    // "languages:python" — collision key for dedupe
	PeriodDefault string    // "" = use kind default
	Condition     Condition
}

// LabelAward is what EvaluateAll returns per firing label. Mirrors the TS
// LabelAward — the FE (or a client of GET /awards) renders these directly.
type LabelAward struct {
	ID          string    `json:"id"`
	Kind        LabelKind `json:"kind"`
	Label       string    `json:"label"`
	Glyph       string    `json:"glyph,omitempty"`
	Description string    `json:"description"`
	Rank        int       `json:"rank"`
	Tier        LabelTier `json:"tier,omitempty"`
	// Condition is passed through so LabelChip's "Fires when: ..." tooltip
	// can format without a separate catalog lookup. Kept as `any` in the
	// TS shape — here it's the same interface, marshalled through
	// MarshalCondition.
	Condition Condition `json:"condition,omitempty"`
}

// MarshalJSON on LabelAward routes Condition through MarshalCondition so
// the "kind" discriminator lands in the JSON.
func (a LabelAward) MarshalJSON() ([]byte, error) {
	type alias LabelAward
	// Emit without Condition first; splice it in after so we can use the
	// discriminator-aware encoder.
	if a.Condition == nil {
		return json.Marshal(struct{ alias }{alias(a)})
	}
	condJSON, err := MarshalCondition(a.Condition)
	if err != nil {
		return nil, err
	}
	// Rebuild manually — replacing the field via reflection is more code
	// than just concatenating the parts and equally opaque.
	stub := struct {
		alias
		Condition json.RawMessage `json:"condition"`
	}{alias(a), condJSON}
	return json.Marshal(stub)
}
