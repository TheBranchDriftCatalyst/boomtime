package importer

import (
	"encoding/json"
	"fmt"
	"sort"
)

// gaka-unq.1: wakatime.com API schema-drift detection.
//
// Design choice: two-pass decode (raw map + typed struct + set-diff of keys)
// per-endpoint against a hand-authored schemaSpec, rather than
// json.Decoder.DisallowUnknownFields.
//
// Rationale:
//   - DisallowUnknownFields aborts on the FIRST unknown field and turns decode
//     into a hard error. That is the opposite of "warn, don't fail", the
//     ergonomic requirement for the import path (see plan gaka-unq.1). It also
//     gives no structured findings and cannot express a benign-field allowlist
//     so wakatime.com routinely shipping additive fields would break imports.
//   - Raw-message byte comparison against a golden payload is too brittle
//     (ordering, values change every response).
//
// So for each response we decode the envelope as {Data []json.RawMessage} in
// parallel with the existing typed decode, then diff a small sample of items
// against a schemaSpec (known-field set / baseline-allowlist / required set /
// per-known-field type check). Findings are deduped by (endpoint, kind, field)
// with a running count; capped total.

// DriftFinding is one schema mismatch detected during an import run. The JSON
// shape is the FE contract (see web/src/types/import.ts DriftFinding).
type DriftFinding struct {
	Endpoint     string `json:"endpoint"`
	Kind         string `json:"kind"`   // unknown_field | missing_required | type_changed | envelope_changed
	Field        string `json:"field"`  // "" when kind == envelope_changed
	Detail       string `json:"detail"` // human-readable extra ("expected number, got string" etc.)
	Severity     string `json:"severity"`
	FirstSeenDay string `json:"firstSeenDay,omitempty"` // "" for lookups
	Count        int    `json:"count"`
}

// Drift kind constants.
const (
	driftKindUnknown         = "unknown_field"
	driftKindMissingRequired = "missing_required"
	driftKindTypeChanged     = "type_changed"
	driftKindEnvelopeChanged = "envelope_changed"

	driftSeverityWarning = "warning"
	driftSeverityError   = "error"

	// Reasonable caps: schemas are small so dedupe naturally bounds this, but
	// guard against pathological payloads that inject arbitrarily many keys.
	driftMaxFindings   = 50
	driftSampleSizeDay = 5 // items to sample per heartbeat day (uniform schema)
)

// jsonType is a small enum for shallow type-checking of a raw JSON fragment.
type jsonType int

const (
	jtAny jsonType = iota // "don't check" — always passes
	jtString
	jtNumber
	jtBool
	jtArray
	jtObject
	jtStringOrNumber
	jtStringOrNull
	jtNumberOrNull
	jtBoolOrNull
	jtArrayOrNull
	jtObjectOrNull
)

// schemaSpec describes the expected shape of ONE object type (a single
// heartbeat item, a user_agents entry, an envelope, etc.).
type schemaSpec struct {
	// known is the full set of json-tag names the typed struct maps. Any field
	// in the payload NOT in known AND NOT in baseline is an unknown_field.
	known map[string]jsonType
	// baseline is the allowlist of fields wakatime.com returns today that we
	// deliberately don't map. Without this, unknown-field detection fires on
	// 100% of imports. Populated conservatively; expand as we observe real
	// payloads.
	baseline map[string]struct{}
	// required is the subset of `known` that must be present with a non-null
	// value for the row to be safe to insert (missing_required = data-mangling
	// risk).
	required []string
	// requiredSeverity chooses the severity for missing_required (warning by
	// default, error for heartbeats where the field is load-bearing).
	requiredSeverity string
}

// keyOf turns a schemaSpec's known map into a sorted, comma-separated key
// (used only in debug messages, not stored).
func (s schemaSpec) knownKeys() []string {
	out := make([]string, 0, len(s.known))
	for k := range s.known {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// heartbeatSpec mirrors the typed importHeartbeat struct. Optional fields
// (branch, category, etc.) are `known` but not `required` — JSON absence is
// not drift, only present-with-wrong-type is.
var heartbeatSpec = schemaSpec{
	known: map[string]jsonType{
		"machine_name_id": jtStringOrNull,
		"user_agent_id":   jtString,
		"branch":          jtStringOrNull,
		"category":        jtStringOrNull,
		"cursorpos":       jtNumberOrNull,
		"dependencies":    jtArrayOrNull,
		"entity":          jtString,
		"is_write":        jtBoolOrNull,
		"language":        jtStringOrNull,
		"lineno":          jtNumberOrNull,
		"lines":           jtNumberOrNull,
		"project":         jtStringOrNull,
		"type":            jtString,
		"time":            jtNumber,
		// gaka-1l9: wakatime.com's AI-assistance heartbeat fields (first seen
		// 2026-07-03). Persisted to matching heartbeats columns.
		"ai_input_tokens":      jtNumberOrNull,
		"ai_output_tokens":     jtNumberOrNull,
		"ai_line_changes":      jtNumberOrNull,
		"human_line_changes":   jtNumberOrNull,
		"ai_prompt_length":     jtNumberOrNull,
		"ai_session":           jtStringOrNull,
		"ai_subscription_plan": jtStringOrNull,
	},
	// Baseline: fields wakatime.com currently returns on heartbeats that we
	// deliberately don't map. Seeded conservatively so a real response wouldn't
	// generate noise on day 1; expand as we see live payloads. The critical
	// ones are `id` (present on every heartbeat) and `created_at`.
	baseline: map[string]struct{}{
		"id":                    {},
		"created_at":            {},
		"project_root_count":    {},
		"user_id":               {},
		"machine_name":          {}, // sometimes returned alongside machine_name_id
		"line_additions":        {},
		"line_deletions":        {},
		"line_deletions_count":  {},
		"line_additions_count":  {},
	},
	required:         []string{"entity", "type", "time", "user_agent_id"},
	requiredSeverity: driftSeverityError, // heartbeat rows are load-bearing
}

// lookupSpec is used for user_agents and machine_names (id + value shape).
var lookupSpec = schemaSpec{
	known: map[string]jsonType{
		"id":    jtString,
		"value": jtString,
	},
	baseline: map[string]struct{}{
		"created_at":       {},
		"last_seen_at":     {},
		"user_id":          {},
		"editor":           {}, // ua parses these locally; still baseline OK
		"version":          {},
		"os":               {},
		"language":         {},
		"is_browser_extension": {},
		"is_desktop_app":       {},
		// gaka-1l9: per-lookup metadata added by wakatime.com. Not persisted
		// per-heartbeat — the important AI signal is on the heartbeat itself.
		// Listed here to silence unknown_field warnings.
		"cli_version":          {}, // user_agents
		"ai_agent":             {}, // user_agents
		"ai_agent_version":     {}, // user_agents
		"ai_agent_complexity":  {}, // user_agents
		"go_version":           {}, // user_agents
		"name":                 {}, // machine_names
		"ip":                   {}, // machine_names
		"timezone":             {}, // machine_names
	},
	required:         []string{"id", "value"},
	requiredSeverity: driftSeverityError, // ua/machine resolution is load-bearing
}

// allTimeSpec is the `data` object of /all_time_since_today. total_seconds +
// text + range{start_date,end_date}. Nested types keep it flat here.
var allTimeSpec = schemaSpec{
	known: map[string]jsonType{
		"total_seconds": jtNumber,
		"text":          jtString,
		"range":         jtObject,
		"is_up_to_date": jtAny,
		"decimal":       jtAny,
		"digital":       jtAny,
		"daily_average": jtAny,
		"percent_calculated": jtAny,
	},
	baseline: map[string]struct{}{
		"timeout":            {},
		"writes_only":        {},
	},
	// No required-severity for this endpoint: range emptiness already handled
	// by the caller via HasData.
	required:         []string{"total_seconds", "range"},
	requiredSeverity: driftSeverityWarning,
}

// driftCollector accumulates findings during one job. Single-goroutine: the
// job worker runs sequentially, so no locking.
type driftCollector struct {
	byKey  map[string]*DriftFinding // dedupe key -> pointer
	order  []string                 // insertion order for stable output
	newLog []string                 // messages to log at "warn" on new findings
	capped bool                     // set once we hit driftMaxFindings
}

func newDriftCollector() *driftCollector {
	return &driftCollector{byKey: map[string]*DriftFinding{}}
}

// hasNew returns any newly-added log messages since the last call and clears
// the buffer. The importer emits them at "warn" so LogTerminal sees them live.
func (c *driftCollector) drainNewLogs() []string {
	if len(c.newLog) == 0 {
		return nil
	}
	out := c.newLog
	c.newLog = nil
	return out
}

// findings returns a stable-ordered snapshot; nil when empty. Callers marshal
// this to JSON for persistence and WS delivery.
func (c *driftCollector) findings() []DriftFinding {
	if len(c.order) == 0 {
		return nil
	}
	out := make([]DriftFinding, 0, len(c.order))
	for _, k := range c.order {
		out = append(out, *c.byKey[k])
	}
	return out
}

// hasError reports whether any finding is severity==error. Callers use this
// to decide whether to fail a fetch (e.g. missing user_agent id / value).
func (c *driftCollector) hasError() bool {
	for _, f := range c.byKey {
		if f.Severity == driftSeverityError {
			return true
		}
	}
	return false
}

// add records or increments a finding. First occurrence returns true so the
// caller can emit a warn log line.
func (c *driftCollector) add(f DriftFinding) bool {
	key := f.Endpoint + "|" + f.Kind + "|" + f.Field
	if existing, ok := c.byKey[key]; ok {
		existing.Count++
		return false
	}
	if len(c.byKey) >= driftMaxFindings {
		c.capped = true
		return false
	}
	f.Count = 1
	c.byKey[key] = &f
	c.order = append(c.order, key)
	// Build a log line, including type-change detail if present.
	msg := fmt.Sprintf("schema drift: %s.%s %s", f.Endpoint, f.Field, f.Kind)
	if f.Field == "" {
		msg = fmt.Sprintf("schema drift: %s %s", f.Endpoint, f.Kind)
	}
	if f.Detail != "" {
		msg += " (" + f.Detail + ")"
	}
	c.newLog = append(c.newLog, msg)
	return true
}

// checkEnvelope validates that `raw` is an object containing a `data` field of
// the expected kind (array for lookups/heartbeats, object for all_time). It
// returns the raw `data` bytes (for follow-up per-item checks) and an ok flag.
// A missing/wrong-shape envelope is an error-severity envelope_changed.
func (c *driftCollector) checkEnvelope(endpoint string, raw []byte, expectData jsonType) (json.RawMessage, bool) {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		c.add(DriftFinding{
			Endpoint: endpoint,
			Kind:     driftKindEnvelopeChanged,
			Detail:   "response is not a JSON object: " + err.Error(),
			Severity: driftSeverityError,
		})
		return nil, false
	}
	data, ok := env["data"]
	if !ok {
		c.add(DriftFinding{
			Endpoint: endpoint,
			Kind:     driftKindEnvelopeChanged,
			Field:    "data",
			Detail:   "envelope missing 'data'",
			Severity: driftSeverityError,
		})
		return nil, false
	}
	if !checkJSONType(data, expectData) {
		c.add(DriftFinding{
			Endpoint: endpoint,
			Kind:     driftKindEnvelopeChanged,
			Field:    "data",
			Detail:   fmt.Sprintf("expected %s, got %s", typeName(expectData), rawTypeName(data)),
			Severity: driftSeverityError,
		})
		return data, false
	}
	return data, true
}

// checkItem diffs a single JSON object fragment against spec. Returns whether
// all required fields were present with a valid type — the caller can use
// this to decide whether to skip the row (heartbeats).
func (c *driftCollector) checkItem(endpoint, day string, item json.RawMessage, spec schemaSpec) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(item, &obj); err != nil {
		// A non-object item where an object is expected is envelope-ish drift.
		c.add(DriftFinding{
			Endpoint: endpoint, Kind: driftKindEnvelopeChanged,
			Detail: "list item is not a JSON object: " + err.Error(),
			Severity: driftSeverityError, FirstSeenDay: day,
		})
		return false
	}
	// unknown_field: keys not in known nor in baseline.
	for k := range obj {
		if _, isKnown := spec.known[k]; isKnown {
			continue
		}
		if _, isBaseline := spec.baseline[k]; isBaseline {
			continue
		}
		c.add(DriftFinding{
			Endpoint: endpoint, Kind: driftKindUnknown, Field: k,
			Severity: driftSeverityWarning, FirstSeenDay: day,
		})
	}
	// type_changed on present known fields.
	for k, expected := range spec.known {
		raw, present := obj[k]
		if !present {
			continue
		}
		if !checkJSONType(raw, expected) {
			c.add(DriftFinding{
				Endpoint: endpoint, Kind: driftKindTypeChanged, Field: k,
				Detail:   fmt.Sprintf("expected %s, got %s", typeName(expected), rawTypeName(raw)),
				Severity: driftSeverityWarning,
				FirstSeenDay: day,
			})
		}
	}
	// missing_required: absent OR present-but-null.
	rowOK := true
	for _, k := range spec.required {
		raw, present := obj[k]
		if !present || isJSONNull(raw) {
			c.add(DriftFinding{
				Endpoint: endpoint, Kind: driftKindMissingRequired, Field: k,
				Severity: spec.requiredSeverity, FirstSeenDay: day,
			})
			rowOK = false
		}
	}
	return rowOK
}

// checkList decodes an array envelope and applies checkItem to sample of items.
// Sample of -1 checks all items (used for the small lookup lists).
func (c *driftCollector) checkList(endpoint, day string, dataArray json.RawMessage, spec schemaSpec, sample int) {
	var items []json.RawMessage
	if err := json.Unmarshal(dataArray, &items); err != nil {
		// checkEnvelope should have gated this; belt-and-suspenders.
		c.add(DriftFinding{
			Endpoint: endpoint, Kind: driftKindEnvelopeChanged,
			Detail:   "data is not a JSON array: " + err.Error(),
			Severity: driftSeverityError, FirstSeenDay: day,
		})
		return
	}
	limit := len(items)
	if sample >= 0 && limit > sample {
		limit = sample
	}
	for i := 0; i < limit; i++ {
		c.checkItem(endpoint, day, items[i], spec)
	}
}

// checkObject applies checkItem to a single object fragment (used for
// all_time's data field which is an object, not a list).
func (c *driftCollector) checkObject(endpoint string, obj json.RawMessage, spec schemaSpec) {
	c.checkItem(endpoint, "", obj, spec)
}

// --- helpers ---

// isJSONNull reports whether a raw fragment is the JSON literal `null`.
func isJSONNull(raw json.RawMessage) bool {
	// Trim ASCII whitespace to match json spec liberally.
	s := trimJSONWS(raw)
	return string(s) == "null"
}

// trimJSONWS strips leading/trailing JSON whitespace.
func trimJSONWS(b json.RawMessage) json.RawMessage {
	i, j := 0, len(b)
	for i < j && isJSONWSByte(b[i]) {
		i++
	}
	for j > i && isJSONWSByte(b[j-1]) {
		j--
	}
	return b[i:j]
}

func isJSONWSByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// checkJSONType shallowly validates that `raw` matches the expected jsonType.
// For *OrNull variants, null passes.
func checkJSONType(raw json.RawMessage, want jsonType) bool {
	if want == jtAny {
		return true
	}
	s := trimJSONWS(raw)
	if len(s) == 0 {
		return false
	}
	first := s[0]
	isNull := string(s) == "null"
	switch want {
	case jtString:
		return first == '"'
	case jtNumber:
		return first == '-' || (first >= '0' && first <= '9')
	case jtBool:
		return string(s) == "true" || string(s) == "false"
	case jtArray:
		return first == '['
	case jtObject:
		return first == '{'
	case jtStringOrNumber:
		return first == '"' || first == '-' || (first >= '0' && first <= '9')
	case jtStringOrNull:
		return isNull || first == '"'
	case jtNumberOrNull:
		return isNull || first == '-' || (first >= '0' && first <= '9')
	case jtBoolOrNull:
		return isNull || string(s) == "true" || string(s) == "false"
	case jtArrayOrNull:
		return isNull || first == '['
	case jtObjectOrNull:
		return isNull || first == '{'
	}
	return false
}

func typeName(t jsonType) string {
	switch t {
	case jtString:
		return "string"
	case jtNumber:
		return "number"
	case jtBool:
		return "boolean"
	case jtArray:
		return "array"
	case jtObject:
		return "object"
	case jtStringOrNumber:
		return "string|number"
	case jtStringOrNull:
		return "string|null"
	case jtNumberOrNull:
		return "number|null"
	case jtBoolOrNull:
		return "boolean|null"
	case jtArrayOrNull:
		return "array|null"
	case jtObjectOrNull:
		return "object|null"
	}
	return "any"
}

func rawTypeName(raw json.RawMessage) string {
	s := trimJSONWS(raw)
	if len(s) == 0 {
		return "empty"
	}
	switch s[0] {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}
