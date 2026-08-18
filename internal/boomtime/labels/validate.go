package labels

import (
	"encoding/json"
	"fmt"
)

// MaxConditionDepth is the maximum composer nesting depth accepted by
// ValidateCondition. Matches the FE ConditionBuilder depth cap so a tree
// built through the UI always round-trips through the server without a
// mid-save rejection. A malicious client that bypasses the FE and posts a
// deeply-nested tree gets a 400 with the offending path instead of a
// stack-blowout at evaluate time.
const MaxConditionDepth = 5

// ValidationError is returned by ValidateCondition when a condition JSONB
// blob fails schema checks. Path is a JSON Pointer (RFC 6901) style pointer
// into the source document — e.g. "/of/0/hours" for the hours field of the
// first sub-condition of a composer. Empty path means the error is at the
// root of the condition.
type ValidationError struct {
	Path    string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

// ValidateCondition parses a raw condition JSONB blob and enforces the full
// DSL schema: enum values (op, axis, which, window), required-non-empty
// fields per kind, pct ∈ [0,1], numeric ranges, and depth cap on composers.
// Returns nil when the blob is a valid condition tree; otherwise a
// *ValidationError with a JSON-pointer path into the offending field.
//
// This is the write-side counterpart to UnmarshalCondition. UnmarshalCondition
// alone would happily accept `{"kind":"axis-time","op":"===","axis":"foo"}`
// because the Go zero values on `Hours` / etc. pass through silently — the
// evaluator would then treat the condition as "≥ 0h in foo (foo)" and either
// always-fire or never-fire depending on the payload. This validator rejects
// such input at the API boundary so a malformed condition never lands in
// the labels table.
func ValidateCondition(raw json.RawMessage) error {
	return validateAt(raw, "", 0)
}

func validateAt(raw json.RawMessage, path string, depth int) error {
	if len(raw) == 0 {
		return &ValidationError{Path: path, Message: "condition body is empty"}
	}
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return &ValidationError{Path: path, Message: "not a JSON object with a `kind` field: " + err.Error()}
	}
	if probe.Kind == "" {
		return &ValidationError{Path: joinPath(path, "kind"), Message: "missing discriminator `kind`"}
	}
	switch probe.Kind {
	case "axis-time":
		var c AxisTimeCond
		if err := json.Unmarshal(raw, &c); err != nil {
			return &ValidationError{Path: path, Message: "decode axis-time: " + err.Error()}
		}
		if err := requireAxis(c.Axis, path); err != nil {
			return err
		}
		if err := requireOp(c.Op, path); err != nil {
			return err
		}
		if c.Value == "" {
			return &ValidationError{Path: joinPath(path, "value"), Message: "axis-time requires a non-empty `value`"}
		}
		if !(c.Hours > 0) {
			return &ValidationError{Path: joinPath(path, "hours"), Message: "axis-time requires `hours` > 0"}
		}
		return nil
	case "axis-time-sum":
		var c AxisTimeSumCond
		if err := json.Unmarshal(raw, &c); err != nil {
			return &ValidationError{Path: path, Message: "decode axis-time-sum: " + err.Error()}
		}
		if err := requireAxis(c.Axis, path); err != nil {
			return err
		}
		if err := requireOp(c.Op, path); err != nil {
			return err
		}
		if len(c.Values) == 0 {
			return &ValidationError{Path: joinPath(path, "values"), Message: "axis-time-sum requires a non-empty `values` array"}
		}
		for i, v := range c.Values {
			if v == "" {
				return &ValidationError{Path: joinPath(path, "values", fmt.Sprintf("%d", i)), Message: "`values` entries must be non-empty strings"}
			}
		}
		if !(c.Hours > 0) {
			return &ValidationError{Path: joinPath(path, "hours"), Message: "axis-time-sum requires `hours` > 0"}
		}
		return nil
	case "axis-pct":
		var c AxisPctCond
		if err := json.Unmarshal(raw, &c); err != nil {
			return &ValidationError{Path: path, Message: "decode axis-pct: " + err.Error()}
		}
		if err := requireAxis(c.Axis, path); err != nil {
			return err
		}
		if err := requireOp(c.Op, path); err != nil {
			return err
		}
		if c.Value == "" {
			return &ValidationError{Path: joinPath(path, "value"), Message: "axis-pct requires a non-empty `value`"}
		}
		if err := requirePct(c.Pct, path); err != nil {
			return err
		}
		return nil
	case "top-share":
		var c TopShareCond
		if err := json.Unmarshal(raw, &c); err != nil {
			return &ValidationError{Path: path, Message: "decode top-share: " + err.Error()}
		}
		if err := requireAxis(c.Axis, path); err != nil {
			return err
		}
		if err := requireOp(c.Op, path); err != nil {
			return err
		}
		if err := requirePct(c.Pct, path); err != nil {
			return err
		}
		return nil
	case "distinct-count":
		var c DistinctCountCond
		if err := json.Unmarshal(raw, &c); err != nil {
			return &ValidationError{Path: path, Message: "decode distinct-count: " + err.Error()}
		}
		if err := requireAxis(c.Axis, path); err != nil {
			return err
		}
		if err := requireOp(c.Op, path); err != nil {
			return err
		}
		if !(c.MinHoursEach >= 0) {
			return &ValidationError{Path: joinPath(path, "minHoursEach"), Message: "distinct-count requires `minHoursEach` >= 0"}
		}
		if c.N <= 0 {
			return &ValidationError{Path: joinPath(path, "n"), Message: "distinct-count requires `n` > 0"}
		}
		return nil
	case "punchcard-hour-pct":
		var c PunchcardHourPctCond
		if err := json.Unmarshal(raw, &c); err != nil {
			return &ValidationError{Path: path, Message: "decode punchcard-hour-pct: " + err.Error()}
		}
		if err := requireOp(c.Op, path); err != nil {
			return err
		}
		if len(c.HoursIn) == 0 {
			return &ValidationError{Path: joinPath(path, "hoursIn"), Message: "punchcard-hour-pct requires a non-empty `hoursIn` array"}
		}
		for i, h := range c.HoursIn {
			if h < 0 || h > 23 {
				return &ValidationError{Path: joinPath(path, "hoursIn", fmt.Sprintf("%d", i)), Message: fmt.Sprintf("hour %d out of range [0,23]", h)}
			}
		}
		if err := requirePct(c.Pct, path); err != nil {
			return err
		}
		return nil
	case "punchcard-dow-pct":
		var c PunchcardDowPctCond
		if err := json.Unmarshal(raw, &c); err != nil {
			return &ValidationError{Path: path, Message: "decode punchcard-dow-pct: " + err.Error()}
		}
		if err := requireOp(c.Op, path); err != nil {
			return err
		}
		if len(c.DowIn) == 0 {
			return &ValidationError{Path: joinPath(path, "dowIn"), Message: "punchcard-dow-pct requires a non-empty `dowIn` array"}
		}
		for i, d := range c.DowIn {
			if d < 0 || d > 6 {
				return &ValidationError{Path: joinPath(path, "dowIn", fmt.Sprintf("%d", i)), Message: fmt.Sprintf("dow %d out of range [0,6] (0=Sun..6=Sat)", d)}
			}
		}
		if err := requirePct(c.Pct, path); err != nil {
			return err
		}
		return nil
	case "streak":
		var c StreakCond
		if err := json.Unmarshal(raw, &c); err != nil {
			return &ValidationError{Path: path, Message: "decode streak: " + err.Error()}
		}
		if c.Which != "current" && c.Which != "longest" {
			return &ValidationError{Path: joinPath(path, "which"), Message: `streak requires "which" to be "current" or "longest"`}
		}
		if err := requireOp(c.Op, path); err != nil {
			return err
		}
		if c.Days <= 0 {
			return &ValidationError{Path: joinPath(path, "days"), Message: "streak requires `days` > 0"}
		}
		return nil
	case "daily-avg":
		var c DailyAvgCond
		if err := json.Unmarshal(raw, &c); err != nil {
			return &ValidationError{Path: path, Message: "decode daily-avg: " + err.Error()}
		}
		if err := requireOp(c.Op, path); err != nil {
			return err
		}
		if !(c.Hours > 0) {
			return &ValidationError{Path: joinPath(path, "hours"), Message: "daily-avg requires `hours` > 0"}
		}
		return nil
	case "trend":
		var c TrendCond
		if err := json.Unmarshal(raw, &c); err != nil {
			return &ValidationError{Path: path, Message: "decode trend: " + err.Error()}
		}
		if c.Window != "last7-vs-prior7" {
			return &ValidationError{Path: joinPath(path, "window"), Message: `trend requires "window" to be "last7-vs-prior7" (the only supported window today)`}
		}
		if err := requireOp(c.Op, path); err != nil {
			return err
		}
		if !(c.Ratio > 0) {
			return &ValidationError{Path: joinPath(path, "ratio"), Message: "trend requires `ratio` > 0"}
		}
		return nil
	case "all", "any":
		if depth+1 > MaxConditionDepth {
			return &ValidationError{Path: path, Message: fmt.Sprintf("composer depth exceeds cap (%d)", MaxConditionDepth)}
		}
		var group struct {
			Of []json.RawMessage `json:"of"`
		}
		if err := json.Unmarshal(raw, &group); err != nil {
			return &ValidationError{Path: path, Message: "decode " + probe.Kind + ": " + err.Error()}
		}
		if len(group.Of) == 0 {
			return &ValidationError{Path: joinPath(path, "of"), Message: probe.Kind + " requires a non-empty `of` array"}
		}
		for i, sub := range group.Of {
			if err := validateAt(sub, joinPath(path, "of", fmt.Sprintf("%d", i)), depth+1); err != nil {
				return err
			}
		}
		return nil
	case "not":
		if depth+1 > MaxConditionDepth {
			return &ValidationError{Path: path, Message: fmt.Sprintf("composer depth exceeds cap (%d)", MaxConditionDepth)}
		}
		var group struct {
			Of json.RawMessage `json:"of"`
		}
		if err := json.Unmarshal(raw, &group); err != nil {
			return &ValidationError{Path: path, Message: "decode not: " + err.Error()}
		}
		if len(group.Of) == 0 {
			return &ValidationError{Path: joinPath(path, "of"), Message: "not requires an `of` sub-condition"}
		}
		return validateAt(group.Of, joinPath(path, "of"), depth+1)
	}
	return &ValidationError{Path: joinPath(path, "kind"), Message: fmt.Sprintf("unknown kind %q", probe.Kind)}
}

// requireAxis fails when a isn't one of the five whitelisted axes.
func requireAxis(a Axis, path string) error {
	switch a {
	case AxisLanguages, AxisEditors, AxisProjects, AxisCategories, AxisPlatforms:
		return nil
	}
	return &ValidationError{Path: joinPath(path, "axis"),
		Message: fmt.Sprintf("axis must be one of languages|editors|projects|categories|platforms (got %q)", string(a))}
}

// requireOp fails when op isn't >= or <=.
func requireOp(op CmpOp, path string) error {
	if op == OpGE || op == OpLE {
		return nil
	}
	return &ValidationError{Path: joinPath(path, "op"),
		Message: fmt.Sprintf("op must be one of >=|<= (got %q)", string(op))}
}

// requirePct fails when pct isn't in [0, 1]. DSL uses 0..1, not 0..100 —
// a common author mistake is to send `50` meaning 50%.
func requirePct(pct float64, path string) error {
	if pct < 0 || pct > 1 {
		return &ValidationError{Path: joinPath(path, "pct"),
			Message: fmt.Sprintf("pct must be in [0, 1] — DSL uses 0..1 fractions, not 0..100 (got %g)", pct)}
	}
	return nil
}

// joinPath appends `segments` to `base` using JSON Pointer syntax. Empty
// base + one segment yields "/segment". Nested lookups produce "/a/b/0".
func joinPath(base string, segments ...string) string {
	out := base
	for _, s := range segments {
		out += "/" + s
	}
	return out
}
