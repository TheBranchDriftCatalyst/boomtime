// drift_ginkgo_test.go — ginkgo mirror of drift_test.go (gaka-0vp).
// 1:1 case map (9 stdlib TestXxx):
//
//	TestDriftCurrentSchemaNoFindings                              → driftCollector > "baseline heartbeats payload yields zero findings"
//	TestDriftUnknownField                                         → driftCollector > "unknown field → warning-severity unknown_field"
//	TestDriftMissingRequiredIsError                               → driftCollector > "missing required field → error severity"
//	TestDriftTypeChangedWarns                                     → driftCollector > "type-changed field → type_changed with detail"
//	TestDriftDedupeAcrossItems                                    → driftCollector > "duplicate unknown field across items → dedupes with count=3"
//	TestDriftEnvelopeMissingData                                  → driftCollector envelope > "missing 'data' key → error-severity envelope_changed"
//	TestDriftEnvelopeWrongDataType                                → driftCollector envelope > "wrong 'data' type → error-severity finding"
//	TestDriftLookupSpecRejectsMissingValue                        → driftCollector > "user_agents missing required 'value' → error severity"
//	TestDriftLookupSpec_KnowsAiModelFields_Wakatime20260723Regression → driftCollector > "user_agents baseline knows ai_model_* fields (regression)"
//	TestDriftJSONRoundTrip                                        → DriftFinding > "JSON round-trip preserves camelCase contract"
//	TestDriftCap                                                  → driftCollector > "cap at driftMaxFindings and set capped flag"
package importer

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// heartbeatsBaselineJSONGinkgo mirrors the const in drift_test.go — kept
// separate to avoid name collisions across the two files (both compile into
// the same test binary).
const heartbeatsBaselineJSONGinkgo = `{
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

var _ = Describe("driftCollector", func() {
	It("baseline heartbeats payload yields zero findings", func() {
		c := newDriftCollector()
		data, ok := c.checkEnvelope("heartbeats", []byte(heartbeatsBaselineJSONGinkgo), jtArray)
		Expect(ok).To(BeTrue(), "envelope should be OK, findings=%+v", c.findings())
		c.checkList("heartbeats", "2025-01-01", data, heartbeatSpec, driftSampleSizeDay)
		Expect(c.findings()).To(BeNil())
		Expect(c.hasError()).To(BeFalse())
	})

	It("unknown field → warning-severity unknown_field", func() {
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
		Expect(ok).To(BeTrue(), "envelope not OK: %+v", c.findings())
		c.checkList("heartbeats", "2025-01-02", data, heartbeatSpec, driftSampleSizeDay)
		f := c.findings()
		Expect(f).To(HaveLen(1))
		Expect(f[0].Kind).To(Equal(driftKindUnknown))
		Expect(f[0].Field).To(Equal("brand_new_field"))
		Expect(f[0].Severity).To(Equal(driftSeverityWarning))
		Expect(c.hasError()).To(BeFalse())
	})

	It("missing required field → error severity", func() {
		// heartbeat missing `entity` — required for a valid row.
		body := `{"data":[{"user_agent_id":"ua-1","type":"file","time":1.0}]}`
		c := newDriftCollector()
		data, ok := c.checkEnvelope("heartbeats", []byte(body), jtArray)
		Expect(ok).To(BeTrue(), "envelope not OK")
		c.checkList("heartbeats", "d", data, heartbeatSpec, driftSampleSizeDay)
		f := c.findings()
		Expect(f).To(HaveLen(1))
		Expect(f[0].Kind).To(Equal(driftKindMissingRequired))
		Expect(f[0].Field).To(Equal("entity"))
		Expect(c.hasError()).To(BeTrue())
	})

	It("type-changed field → type_changed with detail mentioning expected number", func() {
		// time is documented as number; wakatime returns it as string.
		body := `{"data":[{"user_agent_id":"ua-1","entity":"/x","type":"file","time":"1735689600"}]}`
		c := newDriftCollector()
		data, ok := c.checkEnvelope("heartbeats", []byte(body), jtArray)
		Expect(ok).To(BeTrue(), "envelope not OK")
		c.checkList("heartbeats", "d", data, heartbeatSpec, driftSampleSizeDay)
		f := c.findings()
		Expect(f).To(HaveLen(1))
		Expect(f[0].Kind).To(Equal(driftKindTypeChanged))
		Expect(f[0].Field).To(Equal("time"))
		Expect(f[0].Detail).To(ContainSubstring("expected number"))
	})

	It("duplicate unknown field across 3 items → dedupes with count=3", func() {
		body := `{"data":[
      {"user_agent_id":"ua-1","entity":"/a","type":"file","time":1.0,"new_field":1},
      {"user_agent_id":"ua-1","entity":"/b","type":"file","time":2.0,"new_field":2},
      {"user_agent_id":"ua-1","entity":"/c","type":"file","time":3.0,"new_field":3}
    ]}`
		c := newDriftCollector()
		data, ok := c.checkEnvelope("heartbeats", []byte(body), jtArray)
		Expect(ok).To(BeTrue(), "envelope not OK")
		c.checkList("heartbeats", "d", data, heartbeatSpec, driftSampleSizeDay)
		f := c.findings()
		Expect(f).To(HaveLen(1))
		Expect(f[0].Count).To(Equal(3))
	})
})

var _ = Describe("driftCollector envelope", func() {
	It("missing 'data' key → error-severity envelope_changed", func() {
		body := `{"meta":"hi"}`
		c := newDriftCollector()
		_, ok := c.checkEnvelope("heartbeats", []byte(body), jtArray)
		Expect(ok).To(BeFalse(), "envelope should not be OK when 'data' is missing")
		f := c.findings()
		Expect(f).To(HaveLen(1))
		Expect(f[0].Kind).To(Equal(driftKindEnvelopeChanged))
		Expect(c.hasError()).To(BeTrue())
	})

	It("wrong 'data' type → error-severity finding", func() {
		body := `{"data":"not-an-array"}`
		c := newDriftCollector()
		_, ok := c.checkEnvelope("heartbeats", []byte(body), jtArray)
		Expect(ok).To(BeFalse(), "envelope should not be OK when 'data' is a string")
		Expect(c.hasError()).To(BeTrue())
	})
})

var _ = Describe("driftCollector lookup spec (user_agents)", func() {
	It("user_agents missing required 'value' → error severity", func() {
		body := `{"data":[{"id":"ua-1"}]}`
		c := newDriftCollector()
		data, ok := c.checkEnvelope("user_agents", []byte(body), jtArray)
		Expect(ok).To(BeTrue(), "envelope OK check failed")
		c.checkList("user_agents", "", data, lookupSpec, -1)
		Expect(c.hasError()).To(BeTrue(),
			"missing 'value' on user_agents should be error-severity, got %+v", c.findings())
	})

	// Regression pin: gaka-awh's report — user_agents entries with the three
	// ai_model_* fields must NOT raise unknown_field drift. Removing them
	// from lookupSpec.baseline would produce warning noise on every import.
	It("baseline knows ai_model_* fields (wakatime 2026-07-23 regression)", func() {
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
		Expect(ok).To(BeTrue(), "envelope OK failed: %+v", c.findings())
		c.checkList("user_agents", "", data, lookupSpec, -1)

		for _, f := range c.findings() {
			if f.Kind == driftKindUnknown &&
				(f.Field == "ai_model" || f.Field == "ai_model_version" || f.Field == "ai_model_complexity") {
				Fail("ai_model_* field " + f.Field + " raised unknown_field drift — did someone remove it from lookupSpec.baseline?")
			}
		}
	})
})

var _ = Describe("DriftFinding JSON contract", func() {
	It("marshals with camelCase keys matching FE type expectations", func() {
		// The JSON contract used to persist findings into import_jobs.drift and
		// ship over WS: endpoint, kind, field, detail, severity, firstSeenDay, count.
		c := newDriftCollector()
		c.add(DriftFinding{Endpoint: "heartbeats", Kind: driftKindUnknown, Field: "x", Severity: driftSeverityWarning, FirstSeenDay: "2025-01-01"})
		buf, err := json.Marshal(c.findings())
		Expect(err).NotTo(HaveOccurred())
		for _, key := range []string{`"endpoint"`, `"kind"`, `"field"`, `"severity"`, `"firstSeenDay"`, `"count"`} {
			Expect(strings.Contains(string(buf), key)).To(BeTrue(),
				"missing key %s in %s", key, buf)
		}
	})
})

var _ = Describe("driftCollector cap", func() {
	It("caps findings at driftMaxFindings and sets capped flag", func() {
		c := newDriftCollector()
		for i := 0; i < driftMaxFindings+10; i++ {
			c.add(DriftFinding{
				Endpoint: "heartbeats",
				Kind:     driftKindUnknown,
				Field:    "f" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
				Severity: driftSeverityWarning,
			})
		}
		Expect(c.findings()).To(HaveLen(driftMaxFindings))
		Expect(c.capped).To(BeTrue())
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
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
