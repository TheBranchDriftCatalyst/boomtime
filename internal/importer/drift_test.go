package importer

import (
	"encoding/json"
	"strings"
	"testing"
)

// baseline heartbeat payload with ALL known and ALL baseline fields present
// and correctly typed. Should produce zero findings.
const heartbeatsBaselineJSON = `{
  "data": [
    {
      "id": "hb-1",
      "created_at": "2025-01-01T00:00:00Z",
      "machine_name_id": "mn-1",
      "user_agent_id": "ua-1",
      "branch": "main",
      "category": "coding",
      "cursorpos": 42,
      "dependencies": ["json"],
      "entity": "/tmp/foo.go",
      "is_write": true,
      "language": "Go",
      "lineno": 12,
      "lines": 200,
      "project": "boomtime",
      "type": "file",
      "time": 1735689600.0,
      "project_root_count": 1,
      "user_id": "u-1"
    }
  ]
}`

func TestDriftCurrentSchemaNoFindings(t *testing.T) {
	c := newDriftCollector()
	data, ok := c.checkEnvelope("heartbeats", []byte(heartbeatsBaselineJSON), jtArray)
	if !ok {
		t.Fatalf("envelope should be OK, findings=%+v", c.findings())
	}
	c.checkList("heartbeats", "2025-01-01", data, heartbeatSpec, driftSampleSizeDay)
	if f := c.findings(); f != nil {
		t.Fatalf("expected zero findings, got %+v", f)
	}
	if c.hasError() {
		t.Fatal("hasError=true on clean payload")
	}
}

func TestDriftUnknownField(t *testing.T) {
	body := `{
      "data": [{
        "id": "hb-1",
        "user_agent_id": "ua-1",
        "entity": "/x",
        "type": "file",
        "time": 1.0,
        "brand_new_field": "surprise"
      }]
    }`
	c := newDriftCollector()
	data, ok := c.checkEnvelope("heartbeats", []byte(body), jtArray)
	if !ok {
		t.Fatalf("envelope not OK: %+v", c.findings())
	}
	c.checkList("heartbeats", "2025-01-02", data, heartbeatSpec, driftSampleSizeDay)
	f := c.findings()
	if len(f) != 1 || f[0].Kind != driftKindUnknown || f[0].Field != "brand_new_field" {
		t.Fatalf("expected one unknown_field on 'brand_new_field', got %+v", f)
	}
	if f[0].Severity != driftSeverityWarning {
		t.Fatalf("expected warning severity, got %q", f[0].Severity)
	}
	if c.hasError() {
		t.Fatal("unknown field should not be error-severity")
	}
}

func TestDriftMissingRequiredIsError(t *testing.T) {
	// heartbeat missing `entity` — required for a valid row.
	body := `{"data":[{"user_agent_id":"ua-1","type":"file","time":1.0}]}`
	c := newDriftCollector()
	data, ok := c.checkEnvelope("heartbeats", []byte(body), jtArray)
	if !ok {
		t.Fatalf("envelope not OK")
	}
	c.checkList("heartbeats", "d", data, heartbeatSpec, driftSampleSizeDay)
	f := c.findings()
	if len(f) != 1 || f[0].Kind != driftKindMissingRequired || f[0].Field != "entity" {
		t.Fatalf("expected missing_required entity, got %+v", f)
	}
	if !c.hasError() {
		t.Fatal("missing required heartbeat field should be error-severity")
	}
}

func TestDriftTypeChangedWarns(t *testing.T) {
	// time is documented as number; wakatime returns it as string.
	body := `{"data":[{"user_agent_id":"ua-1","entity":"/x","type":"file","time":"1735689600"}]}`
	c := newDriftCollector()
	data, ok := c.checkEnvelope("heartbeats", []byte(body), jtArray)
	if !ok {
		t.Fatalf("envelope not OK")
	}
	c.checkList("heartbeats", "d", data, heartbeatSpec, driftSampleSizeDay)
	f := c.findings()
	if len(f) != 1 || f[0].Kind != driftKindTypeChanged || f[0].Field != "time" {
		t.Fatalf("expected type_changed on 'time', got %+v", f)
	}
	if !strings.Contains(f[0].Detail, "expected number") {
		t.Fatalf("detail should mention expected number, got %q", f[0].Detail)
	}
}

func TestDriftDedupeAcrossItems(t *testing.T) {
	// 3 heartbeats all with the same unknown field.
	body := `{"data":[
      {"user_agent_id":"ua-1","entity":"/a","type":"file","time":1.0,"new_field":1},
      {"user_agent_id":"ua-1","entity":"/b","type":"file","time":2.0,"new_field":2},
      {"user_agent_id":"ua-1","entity":"/c","type":"file","time":3.0,"new_field":3}
    ]}`
	c := newDriftCollector()
	data, ok := c.checkEnvelope("heartbeats", []byte(body), jtArray)
	if !ok {
		t.Fatalf("envelope not OK")
	}
	c.checkList("heartbeats", "d", data, heartbeatSpec, driftSampleSizeDay)
	f := c.findings()
	if len(f) != 1 {
		t.Fatalf("expected one deduped finding, got %d: %+v", len(f), f)
	}
	if f[0].Count != 3 {
		t.Fatalf("expected count=3 across 3 items, got %d", f[0].Count)
	}
}

func TestDriftEnvelopeMissingData(t *testing.T) {
	body := `{"meta":"hi"}`
	c := newDriftCollector()
	_, ok := c.checkEnvelope("heartbeats", []byte(body), jtArray)
	if ok {
		t.Fatal("envelope should not be OK when 'data' is missing")
	}
	f := c.findings()
	if len(f) != 1 || f[0].Kind != driftKindEnvelopeChanged || !c.hasError() {
		t.Fatalf("expected error-severity envelope_changed, got %+v", f)
	}
}

func TestDriftEnvelopeWrongDataType(t *testing.T) {
	body := `{"data":"not-an-array"}`
	c := newDriftCollector()
	_, ok := c.checkEnvelope("heartbeats", []byte(body), jtArray)
	if ok {
		t.Fatal("envelope should not be OK when 'data' is a string")
	}
	if !c.hasError() {
		t.Fatal("expected error-severity finding")
	}
}

func TestDriftLookupSpecRejectsMissingValue(t *testing.T) {
	// user_agents entry missing the `value` field — critical for UA resolution.
	body := `{"data":[{"id":"ua-1"}]}`
	c := newDriftCollector()
	data, ok := c.checkEnvelope("user_agents", []byte(body), jtArray)
	if !ok {
		t.Fatal("envelope OK check failed")
	}
	c.checkList("user_agents", "", data, lookupSpec, -1)
	if !c.hasError() {
		t.Fatalf("missing 'value' on user_agents should be error-severity, got %+v", c.findings())
	}
}

// TestDriftLookupSpec_KnowsAiModelFields_Wakatime20260723Regression pins the
// three ai_model_* baseline entries added on 2026-07-23 in response to a live
// drift report from a user's import. If someone deletes them from
// lookupSpec.baseline, wakatime's user_agents endpoint would produce
// warning-severity noise on every future import for anyone touching an AI
// model. Not a data-integrity bug, but it's noise we shipped a fix for.
func TestDriftLookupSpec_KnowsAiModelFields_Wakatime20260723Regression(t *testing.T) {
	// A user_agents response including all three new fields — the exact shape
	// the drift detector emitted in gaka-awh's report:
	// unknown_field ai_model, ai_model_version, ai_model_complexity.
	body := `{"data":[{
		"id":"ua-1",
		"value":"cursor/0.42.0 (darwin-arm64-24.5.0) cursor/0.42.0 cursor-wakatime/1.0.0",
		"ai_agent":"cursor",
		"ai_agent_version":"0.42.0",
		"ai_agent_complexity":"high",
		"ai_model":"claude-sonnet-4-5",
		"ai_model_version":"20250929",
		"ai_model_complexity":"high"
	}]}`
	c := newDriftCollector()
	data, ok := c.checkEnvelope("user_agents", []byte(body), jtArray)
	if !ok {
		t.Fatalf("envelope OK failed: %+v", c.findings())
	}
	c.checkList("user_agents", "", data, lookupSpec, -1)

	for _, f := range c.findings() {
		if f.Kind == driftKindUnknown &&
			(f.Field == "ai_model" || f.Field == "ai_model_version" || f.Field == "ai_model_complexity") {
			t.Fatalf("ai_model_* field %q raised unknown_field drift — did someone remove it from lookupSpec.baseline? full findings=%+v",
				f.Field, c.findings())
		}
	}
}

func TestDriftJSONRoundTrip(t *testing.T) {
	// Verify the JSON contract used to persist findings into import_jobs.drift
	// and ship over WS matches the FE type expectations (camelCase, no
	// nested-only fields).
	c := newDriftCollector()
	c.add(DriftFinding{Endpoint: "heartbeats", Kind: driftKindUnknown, Field: "x", Severity: driftSeverityWarning, FirstSeenDay: "2025-01-01"})
	buf, err := json.Marshal(c.findings())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Contract keys: endpoint, kind, field, detail, severity, firstSeenDay, count.
	for _, key := range []string{`"endpoint"`, `"kind"`, `"field"`, `"severity"`, `"firstSeenDay"`, `"count"`} {
		if !strings.Contains(string(buf), key) {
			t.Fatalf("missing key %s in %s", key, buf)
		}
	}
}

func TestDriftCap(t *testing.T) {
	c := newDriftCollector()
	for i := 0; i < driftMaxFindings+10; i++ {
		c.add(DriftFinding{
			Endpoint: "heartbeats",
			Kind:     driftKindUnknown,
			Field:    "f" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Severity: driftSeverityWarning,
		})
	}
	if len(c.findings()) != driftMaxFindings {
		t.Fatalf("expected cap at %d findings, got %d", driftMaxFindings, len(c.findings()))
	}
	if !c.capped {
		t.Fatal("capped flag should be set after exceeding max")
	}
}
