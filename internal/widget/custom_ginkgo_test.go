// custom_ginkgo_test.go — ginkgo mirror of custom_test.go (gaka-0vp).
// 1:1 case map (5 stdlib TestXxx → 1 It + 3 DescribeTables + 2 Its):
//   TestEncodeDecodeDefRoundTrip           → "encode/decode Def" > It "round-trips + accepts std base64"
//   TestDecodeDefRejectsBadInput/*         → "DecodeDef rejects bad input" > DescribeTable (5 entries)
//   TestNeedsForDef/*                      → "NeedsForDef" > DescribeTable (5 entries)
//   TestRenderCustomThreePanelComposite    → "RenderCustom" > It "3-panel composite renders every dispatched primitive"
//   TestRenderCustomHandlesMissingData     → "RenderCustom" > It "renders placeholders for missing data"
//   TestRenderCustomAllLayouts/*           → "RenderCustom" > DescribeTable (4 entries, one per layout)
package widget

import (
	"encoding/base64"
	"encoding/xml"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// assertValidXMLG is a ginkgo-friendly XML validator (the stdlib
// assertValidXML uses *testing.T so cannot be shared verbatim).
func assertValidXMLG(b []byte) {
	dec := xml.NewDecoder(strings.NewReader(string(b)))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				return
			}
			Expect(err).NotTo(HaveOccurred(), "SVG is not well-formed XML:\n%s", string(b))
			return
		}
	}
}

var _ = Describe("encode/decode Def", func() {
	It("round-trips + accepts std base64 alongside URL-safe", func() {
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
		Expect(err).NotTo(HaveOccurred())

		got, err := DecodeDef(enc)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Layout).To(Equal(d.Layout))
		Expect(got.Title).To(Equal(d.Title))
		Expect(got.Panels).To(HaveLen(3))

		// Also accept std base64 for camo-friendliness (URL-safe copies
		// sometimes get re-encoded by tools).
		std := base64.StdEncoding.EncodeToString([]byte(mustJSON(d)))
		_, err = DecodeDef(std)
		Expect(err).NotTo(HaveOccurred(), "std base64 should be accepted")
	})
})

var _ = Describe("DecodeDef rejects bad input", func() {
	DescribeTable("error message contains the diagnostic keyword",
		func(in, wantMsg string) {
			_, err := DecodeDef(in)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(wantMsg))
		},
		Entry("not b64", "!!!not base64!!!", "base64"),
		Entry("not json", base64.RawURLEncoding.EncodeToString([]byte("hello")), "JSON"),
		Entry("unknown layout", base64.RawURLEncoding.EncodeToString([]byte(`{"layout":"7-panel","panels":[]}`)), "unknown layout"),
		Entry("wrong panel count", base64.RawURLEncoding.EncodeToString([]byte(`{"layout":"3-panel-h","panels":[{"kind":"calendar"}]}`)), "3 panels"),
		Entry("unknown panel", base64.RawURLEncoding.EncodeToString([]byte(`{"layout":"1-panel","panels":[{"kind":"dance"}]}`)), "unknown panel"),
	)
})

// NeedsForDef aggregates panel requirements so the handler fetches only what
// the composition asks for.
var _ = Describe("NeedsForDef", func() {
	DescribeTable("aggregates panel requirements",
		func(def Def, want Requirements) {
			Expect(NeedsForDef(def)).To(Equal(want))
		},
		Entry("calendar only",
			Def{Layout: Layout1, Panels: []Panel{{Kind: PanelCalendar}}},
			Requirements{}),
		Entry("grade only",
			Def{Layout: Layout1, Panels: []Panel{{Kind: PanelGrade}}},
			Requirements{Grade: true}),
		Entry("punchcard + momentum",
			Def{Layout: Layout2Horz, Panels: []Panel{{Kind: PanelPunchcard}, {Kind: PanelMomentum}}},
			Requirements{Punchcard: true, Momentum: true}),
		Entry("metrics needs Sessions",
			Def{Layout: Layout1, Panels: []Panel{{Kind: PanelMetrics}}},
			Requirements{Sessions: true}),
		Entry("kitchen sink",
			Def{Layout: Layout3Horz, Panels: []Panel{{Kind: PanelGrade}, {Kind: PanelPunchcard}, {Kind: PanelMomentum}}},
			Requirements{Grade: true, Punchcard: true, Momentum: true}),
	)
})

var _ = Describe("RenderCustom", func() {
	// A 3-panel composite renders each panel via its primitive; the SVG must
	// be well-formed and contain the fingerprints of every dispatched
	// primitive.
	It("3-panel composite renders every dispatched primitive", func() {
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
		Expect(err).NotTo(HaveOccurred())
		assertValidXMLG(b)
		s := string(b)
		Expect(s).To(ContainSubstring("My widget"), "custom widget title missing")
		Expect(s).To(ContainSubstring("2026"), "calendar panel absent")
		Expect(s).To(ContainSubstring("Go"), "top-langs panel absent")
		Expect(s).To(ContainSubstring(">"+d.Grade.Level+"<"), "grade panel absent")
	})

	// When a panel needs data the handler didn't fetch, the panel draws a
	// placeholder instead of panicking — defense against a misconfigured Def.
	It("renders placeholders for missing data instead of panicking", func() {
		d := &Data{Payload: dataFixture().Payload} // no Grade/Punchcard/Momentum
		def := Def{Layout: Layout2Horz, Panels: []Panel{
			{Kind: PanelGrade},
			{Kind: PanelPunchcard},
		}}
		b, err := RenderCustom(d, def, Options{})
		Expect(err).NotTo(HaveOccurred())
		assertValidXMLG(b)
		s := string(b)
		Expect(s).To(ContainSubstring("No grade"), "missing-grade placeholder should render")
		Expect(s).To(ContainSubstring("No punchcard"), "missing-punchcard placeholder should render")
	})

	DescribeTable("every layout renders + emits shared @keyframes",
		func(l Layout) {
			d := dataFixture()
			panelSel := []Panel{{Kind: PanelCalendar}, {Kind: PanelTopLangs}, {Kind: PanelGrade}}
			def := Def{Layout: l, Panels: panelSel[:layoutPanelCount(l)]}
			b, err := RenderCustom(d, def, Options{})
			Expect(err).NotTo(HaveOccurred())
			assertValidXMLG(b)
			Expect(strings.Contains(string(b), "@keyframes")).To(BeTrue(), "shared animations missing")
		},
		Entry(string(Layout1), Layout1),
		Entry(string(Layout2Horz), Layout2Horz),
		Entry(string(Layout3Horz), Layout3Horz),
		Entry(string(Layout2Vert), Layout2Vert),
	)
})
