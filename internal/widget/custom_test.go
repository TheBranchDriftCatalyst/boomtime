package widget

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeDecodeDefRoundTrip(t *testing.T) {
	d := Def{
		Layout: Layout3Horz,
		Title:  "profile",
		Panels: []Panel{
			{Kind: PanelCalendar},
			{Kind: PanelTopLangs},
			{Kind: PanelGrade},
		},
	}
	enc, err := EncodeDef(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeDef(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Layout != d.Layout || got.Title != d.Title || len(got.Panels) != 3 {
		t.Errorf("round-trip lost data: %+v", got)
	}
	// Also accept std base64 for camo-friendliness (URL-safe copies sometimes
	// get re-encoded by tools).
	std := base64.StdEncoding.EncodeToString([]byte(mustJSON(d)))
	if _, err := DecodeDef(std); err != nil {
		t.Errorf("std base64 should be accepted: %v", err)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestDecodeDefRejectsBadInput(t *testing.T) {
	cases := []struct{ name, in, wantMsg string }{
		{"not b64", "!!!not base64!!!", "base64"},
		{"not json", base64.RawURLEncoding.EncodeToString([]byte("hello")), "JSON"},
		{"unknown layout", base64.RawURLEncoding.EncodeToString([]byte(`{"layout":"7-panel","panels":[]}`)), "unknown layout"},
		{"wrong panel count", base64.RawURLEncoding.EncodeToString([]byte(`{"layout":"3-panel-h","panels":[{"kind":"calendar"}]}`)), "3 panels"},
		{"unknown panel", base64.RawURLEncoding.EncodeToString([]byte(`{"layout":"1-panel","panels":[{"kind":"dance"}]}`)), "unknown panel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeDef(tc.in)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %q, want it to contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

// NeedsForDef aggregates panel requirements so the handler fetches only what
// the composition asks for.
func TestNeedsForDef(t *testing.T) {
	cases := []struct {
		name    string
		def     Def
		want    Requirements
	}{
		{"calendar only", Def{Layout: Layout1, Panels: []Panel{{Kind: PanelCalendar}}}, Requirements{}},
		{"grade only", Def{Layout: Layout1, Panels: []Panel{{Kind: PanelGrade}}}, Requirements{Grade: true}},
		{"punchcard + momentum", Def{Layout: Layout2Horz, Panels: []Panel{{Kind: PanelPunchcard}, {Kind: PanelMomentum}}}, Requirements{Punchcard: true, Momentum: true}},
		{"metrics needs Sessions", Def{Layout: Layout1, Panels: []Panel{{Kind: PanelMetrics}}}, Requirements{Sessions: true}},
		{"kitchen sink", Def{Layout: Layout3Horz, Panels: []Panel{{Kind: PanelGrade}, {Kind: PanelPunchcard}, {Kind: PanelMomentum}}}, Requirements{Grade: true, Punchcard: true, Momentum: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsForDef(tc.def); got != tc.want {
				t.Errorf("NeedsForDef = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A 3-panel composite renders each panel via its primitive; the SVG must be
// well-formed and contain the fingerprints of every dispatched primitive.
func TestRenderCustomThreePanelComposite(t *testing.T) {
	d := dataFixture()
	def := Def{
		Layout: Layout3Horz,
		Title:  "My widget",
		Panels: []Panel{
			{Kind: PanelCalendar},
			{Kind: PanelTopLangs},
			{Kind: PanelGrade},
		},
	}
	b, err := RenderCustom(d, def, Options{Theme: "dark", Subtitle: "last 30 days"})
	if err != nil {
		t.Fatal(err)
	}
	assertValidXML(t, b)
	s := string(b)
	// Header
	if !strings.Contains(s, "My widget") {
		t.Error("custom widget title missing")
	}
	// Panel 1 fingerprint: calendar cell tooltip has a date fragment
	if !strings.Contains(s, "2026") {
		t.Error("calendar panel absent")
	}
	// Panel 2 fingerprint: a language name from the fixture
	if !strings.Contains(s, "Go") {
		t.Error("top-langs panel absent")
	}
	// Panel 3 fingerprint: the grade letter is rendered as text
	if !strings.Contains(s, ">"+d.Grade.Level+"<") {
		t.Error("grade panel absent")
	}
}

// When a panel needs data the handler didn't fetch, the panel draws a
// placeholder instead of panicking — defense against a misconfigured Def.
func TestRenderCustomHandlesMissingData(t *testing.T) {
	d := &Data{Payload: dataFixture().Payload} // no Grade/Punchcard/Momentum
	def := Def{Layout: Layout2Horz, Panels: []Panel{
		{Kind: PanelGrade},
		{Kind: PanelPunchcard},
	}}
	b, err := RenderCustom(d, def, Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertValidXML(t, b)
	if !strings.Contains(string(b), "No grade") || !strings.Contains(string(b), "No punchcard") {
		t.Error("missing-data placeholders should render")
	}
}

func TestRenderCustomAllLayouts(t *testing.T) {
	d := dataFixture()
	layouts := []Layout{Layout1, Layout2Horz, Layout3Horz, Layout2Vert}
	panelSel := []Panel{{Kind: PanelCalendar}, {Kind: PanelTopLangs}, {Kind: PanelGrade}}
	for _, l := range layouts {
		t.Run(string(l), func(t *testing.T) {
			def := Def{Layout: l, Panels: panelSel[:layoutPanelCount(l)]}
			b, err := RenderCustom(d, def, Options{})
			if err != nil {
				t.Fatal(err)
			}
			assertValidXML(t, b)
			if !strings.Contains(string(b), "@keyframes") {
				t.Error("shared animations missing")
			}
		})
	}
}
