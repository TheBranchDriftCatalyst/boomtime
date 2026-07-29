package labels

import (
	"encoding/json"
	"testing"
)

func TestDeriveTierKey_Convention(t *testing.T) {
	cases := []struct {
		id, tier, want string
	}{
		{"languages-python-master", "master", "languages:python"},
		{"editors-vim-legend", "legend", "editors:vim"},
		{"languages-c++-adept", "adept", "languages:c++"},
		// No trailing tier match.
		{"languages-python-master", "novice", ""},
		// No dash to split axis from value.
		{"solo-master", "master", ""},
		// Empty tier → no key.
		{"whatever", "", ""},
	}
	for _, c := range cases {
		got := deriveTierKey(c.id, c.tier)
		if got != c.want {
			t.Errorf("deriveTierKey(%q, %q) = %q, want %q", c.id, c.tier, got, c.want)
		}
	}
}

func TestSpecFromDBRow_TierRowGetsTierKey(t *testing.T) {
	row := DBRow{
		ID:          "languages-python-master",
		Kind:        "tier",
		Label:       "PYTHON MASTER",
		Rank:        100,
		Tier:        "master",
		Condition:   json.RawMessage(`{"kind":"axis-time","axis":"languages","value":"python","op":">=","hours":100}`),
	}
	spec, err := SpecFromDBRow(row)
	if err != nil {
		t.Fatalf("SpecFromDBRow: %v", err)
	}
	if spec.TierKey != "languages:python" {
		t.Errorf("TierKey = %q, want %q", spec.TierKey, "languages:python")
	}
	if _, ok := spec.Condition.(AxisTimeCond); !ok {
		t.Errorf("Condition type = %T, want AxisTimeCond", spec.Condition)
	}
}

func TestSpecFromDBRow_NonTierLeavesTierKeyEmpty(t *testing.T) {
	row := DBRow{
		ID:        "night-watch",
		Kind:      "archetype",
		Label:     "NIGHT WATCH",
		Rank:      50,
		Condition: json.RawMessage(`{"kind":"punchcard-hour-pct","hoursIn":[22,23,0,1,2,3,4,5],"op":">=","pct":0.4}`),
	}
	spec, err := SpecFromDBRow(row)
	if err != nil {
		t.Fatalf("SpecFromDBRow: %v", err)
	}
	if spec.TierKey != "" {
		t.Errorf("non-tier spec should have empty TierKey, got %q", spec.TierKey)
	}
}

func TestSpecsFromDBRows_BadConditionRejects(t *testing.T) {
	rows := []DBRow{
		{ID: "ok", Kind: "archetype", Condition: json.RawMessage(`{"kind":"daily-avg","op":">=","hours":1}`)},
		{ID: "bad", Kind: "archetype", Condition: json.RawMessage(`{"kind":"UNKNOWN"}`)},
	}
	if _, err := SpecsFromDBRows(rows); err == nil {
		t.Error("expected error on unknown-kind condition, got nil")
	}
}
