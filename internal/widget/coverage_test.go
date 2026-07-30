// coverage_test.go — additional invariant-focused tests filling coverage gaps
// on primitives, custom-panel dispatch, theming, and frame plumbing (gaka-d6x).
//
// Each It pins a NAMED INVARIANT expressed as a defensive property of the
// public rendering contract:
//
//   - "unknown widget kind → Needs() returns zero-value Requirements"
//     Handler consults Needs() BEFORE dispatching so the render path never
//     panics on a typo'd kind — Needs on unknown must not accidentally
//     say "fetch punchcard".
//
//   - "IsCustomKind gates on the exact literal 'custom'"
//     A defensive true/false witness for the URL-path guard.
//
//   - "colorAt on an empty palette falls back to Accent"
//     Prevents an index-out-of-range from a theme that forgot to populate
//     Palette. The public endpoint must never panic.
//
//   - "layoutPanelCount + layoutSize default to sane values for an unknown Layout"
//     Belt to the ValidateDef braces: even if a malformed Def slips past
//     validation, geometry code must not divide-by-zero or panic.
//
//   - "renderCustom(unknown layout) still emits a well-formed SVG"
//     End-to-end guarantee that any Def (even one bypassing DecodeDef) yields
//     a parseable, camo-safe SVG.
//
//   - "renderPanel placeholders emit when data is missing per panel kind"
//     Every panel kind that has data-missing branches (Calendar w/o
//     DailyTotal, Punchcard w/o Punchcard, Metrics w/o Sessions) draws a
//     placeholder — the composition never leaves a blank hole.
//
//   - "EmitBars on empty entries emits nothing (no divide-by-zero, no rows)"
//     Empty top-N must be a no-op; the Frame stays clean for callers.
//
//   - "EmitGradeRing on nil Grade emits nothing (no panic, no partial glyph)"
//     Handler passes nil when Grade wasn't fetched — must be a no-op.
//
//   - "EmitCalendar / EmitPunchcard / EmitMomentum / EmitAreaLine on empty
//     inputs each emit the documented no-data text"
//     User-facing invariant: rather than a blank chart, users see a labeled
//     empty state.
//
//   - "EmitAreaLine with all-zero values emits 'No activity yet' (mx == 0)"
//     Distinct from the <2-points branch: some inputs have enough length but
//     nothing to show — the message differentiates the two.
//
//   - "mixHex clamps q into [0, 1] — negative → a; > 1 → b"
//     Color arithmetic must never over/undershoot the byte range.
//
//   - "parseHex returns (0,0,0) for malformed hex — mixHex still returns a
//     legal '#rrggbb'"
//     No-oracle guarantee: a bad theme hex never produces a crashy SVG
//     attribute like `fill="#-1--1--1"`.
//
//   - "Frame.Write accepts arbitrary bytes and preserves them in the output"
//     Primitives that implement io.Writer semantics (fmt.Fprintf) rely on
//     Frame satisfying io.Writer — the property is that bytes written flow
//     through verbatim.
package widget

import (
	"encoding/xml"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

var _ = Describe("public dispatch guards", func() {
	// The handler consults Needs() BEFORE dispatching to Render(). A typo'd
	// kind must not accidentally trigger a Grade / Punchcard / Momentum /
	// Sessions fetch — the zero-value Requirements gates every optional fetch
	// off.
	It("Needs(unknown kind) returns zero-value Requirements", func() {
		r := Needs("not-a-real-kind")
		Expect(r).To(Equal(Requirements{}),
			"Needs must default to no-fetch for unknown kinds")
	})

	// IsCustomKind is the URL-path guard that decides whether to parse ?spec=.
	// It must fire ONLY on the exact literal "custom" — no substring match,
	// no case-insensitive slack.
	It("IsCustomKind gates on the exact literal 'custom'", func() {
		Expect(IsCustomKind("custom")).To(BeTrue(),
			"IsCustomKind(\"custom\") must be true")
		Expect(IsCustomKind("Custom")).To(BeFalse(),
			"IsCustomKind must be case-sensitive")
		Expect(IsCustomKind("customx")).To(BeFalse(),
			"IsCustomKind must not substring-match")
		Expect(IsCustomKind("stats-card")).To(BeFalse(),
			"a real kind must not be misdetected as custom")
		Expect(IsCustomKind("")).To(BeFalse(),
			"empty kind must not be misdetected as custom")
	})
})

var _ = Describe("theme fallback safety", func() {
	// A theme with an empty Palette must not crash the bar renderer — it falls
	// back to Accent. Simulates a config-file theme override that forgot
	// Palette. This is a no-oracle test: we verify the returned color is
	// exactly Accent, not just "non-empty".
	It("colorAt on an empty palette falls back to Accent", func() {
		t := Theme{Accent: "#deadbe", Palette: nil}
		for i := 0; i < 20; i++ {
			Expect(t.colorAt(i)).To(Equal("#deadbe"),
				"empty palette must always return Accent, got %s at i=%d", t.colorAt(i), i)
		}
	})
})

var _ = Describe("layout defaults for unknown Layout", func() {
	// A Def that bypasses ValidateDef (e.g. constructed in code) must not
	// panic when it lands in the geometry primitives.
	It("layoutPanelCount defaults to 0 for an unknown Layout", func() {
		Expect(layoutPanelCount(Layout("hologram-7"))).To(Equal(0),
			"unknown layout must not claim any panels")
	})
	It("layoutSize defaults to a sane 495x195 for an unknown Layout", func() {
		w, h := layoutSize(Layout("hologram-7"))
		Expect(w).To(Equal(495))
		Expect(h).To(Equal(195))
	})
	// panelRects returns nil for unknown layouts — the loop over panels in
	// renderCustom is bounded by len(rects), so an unknown layout results in
	// a frame with a title and no panels drawn (no panic).
	It("panelRects returns nil for an unknown Layout", func() {
		Expect(panelRects(Layout("hologram-7"), 500, 200, 50)).To(BeNil())
	})
})

var _ = Describe("renderCustom robustness", func() {
	// End-to-end: even a Def with an unknown layout (bypassing DecodeDef's
	// validation) yields a well-formed SVG rather than a panic. This is the
	// last-line-of-defense against internal callers minting Defs directly.
	It("renderCustom with an unknown Layout still emits well-formed SVG (no panels drawn)", func() {
		def := Def{Layout: Layout("hologram-7"), Panels: []Panel{{Kind: PanelCalendar}}}
		b, err := RenderCustom(dataFixture(), def, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(xml.Unmarshal(b, new(any))).To(Succeed(),
			"unknown-layout output must still be well-formed XML")
	})

	// A Def with more panels than the layout can hold silently drops the
	// overflow — the invariant is "no panic, no leak past the frame".
	It("renderCustom silently drops panels past the layout's capacity", func() {
		def := Def{Layout: Layout1, Panels: []Panel{
			{Kind: PanelCalendar}, // this one draws
			{Kind: PanelGrade},    // dropped by the len-guard
		}}
		b, err := RenderCustom(dataFixture(), def, Options{})
		Expect(err).NotTo(HaveOccurred())
		assertValidXMLG(b)
	})

	// The 2-vertical layout draws a horizontal divider between rows; the
	// horizontal layouts draw vertical ones. This asserts BOTH divider paths
	// are exercised — a coverage gap in the earlier suite.
	It("Layout2Vert emits a horizontal divider between panels", func() {
		def := Def{Layout: Layout2Vert, Panels: []Panel{
			{Kind: PanelCalendar}, {Kind: PanelTopLangs},
		}}
		b, err := RenderCustom(dataFixture(), def, Options{})
		Expect(err).NotTo(HaveOccurred())
		assertValidXMLG(b)
		// The vert-layout divider is height=1 (horizontal), not width=1
		// (vertical). Its presence proves the Layout2Vert branch fired.
		Expect(strings.Contains(string(b), `height="1"`)).To(BeTrue(),
			"expected horizontal (height=1) divider from Layout2Vert branch")
	})

	// Def.Title falls back to opts.Title, then to the literal "Custom widget".
	// This is a hierarchy test — pins the three-level fallback.
	It("Def with no title and no opts.Title falls back to 'Custom widget'", func() {
		def := Def{Layout: Layout1, Panels: []Panel{{Kind: PanelCalendar}}}
		b, err := RenderCustom(dataFixture(), def, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(b), "Custom widget")).To(BeTrue(),
			"missing Def.Title AND opts.Title should fall back to 'Custom widget'")
	})

	It("Def.Title is used when opts.Title is empty", func() {
		def := Def{Layout: Layout1, Title: "MyDefTitle", Panels: []Panel{{Kind: PanelCalendar}}}
		b, err := RenderCustom(dataFixture(), def, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(b), "MyDefTitle")).To(BeTrue(),
			"Def.Title should be used when opts.Title is empty")
	})
})

var _ = Describe("renderPanel placeholders", func() {
	// Every panel that has a data-missing branch must draw its labeled
	// placeholder — the composition never leaves a blank hole.

	It("PanelCalendar with empty DailyTotal draws 'No days'", func() {
		d := &Data{Payload: &model.StatsPayload{}}
		def := Def{Layout: Layout1, Panels: []Panel{{Kind: PanelCalendar}}}
		b, err := RenderCustom(d, def, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(b), "No days")).To(BeTrue(),
			"missing DailyTotal must draw the 'No days' placeholder")
	})

	It("PanelPunchcard with nil Punchcard draws 'No punchcard'", func() {
		d := &Data{Payload: dataFixture().Payload}
		def := Def{Layout: Layout1, Panels: []Panel{{Kind: PanelPunchcard}}}
		b, err := RenderCustom(d, def, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(b), "No punchcard")).To(BeTrue())
	})

	It("PanelGrade with nil Grade draws 'No grade'", func() {
		d := &Data{Payload: dataFixture().Payload}
		def := Def{Layout: Layout1, Panels: []Panel{{Kind: PanelGrade}}}
		b, err := RenderCustom(d, def, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(b), "No grade")).To(BeTrue())
	})

	// TopLangs/TopProjects with empty axes: the bars panel wraps
	// panelPlaceholder("No data") when there are no entries.
	It("PanelTopLangs with empty Languages draws 'No data'", func() {
		d := &Data{Payload: &model.StatsPayload{DailyTotal: []int64{1, 2}}}
		def := Def{Layout: Layout1, Panels: []Panel{{Kind: PanelTopLangs}}}
		b, err := RenderCustom(d, def, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(b), "No data")).To(BeTrue(),
			"empty top-langs axis must draw 'No data' placeholder")
	})

	It("PanelTopProjects with empty Projects draws 'No data'", func() {
		d := &Data{Payload: &model.StatsPayload{DailyTotal: []int64{1, 2}}}
		def := Def{Layout: Layout1, Panels: []Panel{{Kind: PanelTopProjects}}}
		b, err := RenderCustom(d, def, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(b), "No data")).To(BeTrue())
	})

	// PanelMetrics with a Sessions payload including Count > 0 renders the
	// third "Sessions" metric row — covers the Sessions-branch of renderPanel.
	It("PanelMetrics with Sessions.Count > 0 emits the 'Sessions' metric row", func() {
		d := dataFixture()
		def := Def{Layout: Layout1, Panels: []Panel{{Kind: PanelMetrics}}}
		b, err := RenderCustom(d, def, Options{})
		Expect(err).NotTo(HaveOccurred())
		s := string(b)
		Expect(strings.Contains(s, "Sessions")).To(BeTrue(),
			"PanelMetrics with non-zero Sessions.Count must render 'Sessions' label")
		Expect(strings.Contains(s, "Total")).To(BeTrue())
		Expect(strings.Contains(s, "Daily avg")).To(BeTrue())
	})

	It("PanelMetrics with nil Sessions omits the 'Sessions' label but still emits Total/Daily avg", func() {
		d := &Data{Payload: dataFixture().Payload}
		def := Def{Layout: Layout1, Panels: []Panel{{Kind: PanelMetrics}}}
		b, err := RenderCustom(d, def, Options{})
		Expect(err).NotTo(HaveOccurred())
		s := string(b)
		Expect(strings.Contains(s, "Sessions")).To(BeFalse(),
			"nil Sessions must not render the 'Sessions' metric row")
		Expect(strings.Contains(s, "Total")).To(BeTrue())
	})

	// PanelArea uses the cumulative-area primitive; assert it renders.
	It("PanelArea emits an area-line path", func() {
		d := dataFixture()
		def := Def{Layout: Layout1, Panels: []Panel{{Kind: PanelArea}}}
		b, err := RenderCustom(d, def, Options{})
		Expect(err).NotTo(HaveOccurred())
		s := string(b)
		Expect(strings.Contains(s, `<path`)).To(BeTrue(),
			"PanelArea must emit an SVG path")
	})

	// PanelMomentum with a real momentum payload; happy path — covers the
	// non-placeholder branch of the case.
	It("PanelMomentum with a non-empty Momentum emits project labels", func() {
		d := dataFixture()
		def := Def{Layout: Layout1, Panels: []Panel{{Kind: PanelMomentum}}}
		b, err := RenderCustom(d, def, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(b), "boomtime")).To(BeTrue(),
			"PanelMomentum must emit project labels from Momentum.Projects")
	})
})

var _ = Describe("primitive no-op / empty-state invariants", func() {
	// EmitBars on empty entries: the for-range never iterates, no <g class="row">
	// tags land in the buffer. Prevents divide-by-zero from maxSecs==0 in the
	// downstream width math.
	It("EmitBars on empty entries writes no bar rows", func() {
		f := OpenFrame(200, 100, themes["dark"], "T", "")
		before := len(f.Close())
		f2 := OpenFrame(200, 100, themes["dark"], "T", "")
		EmitBars(f2, nil, BarsOpts{X: 10, Y: 10, YStep: 10, BarX: 50, BarWMax: 100})
		after := len(f2.Close())
		Expect(after).To(Equal(before),
			"EmitBars(nil) must be a no-op — got %d extra bytes", after-before)
	})

	// EmitBars where every entry has zero seconds: the maxSecs<1 guard sets it
	// to 1 so the width math yields 0-ish rounded to a min-width of 2. Assert
	// we get the fallback bar and no NaN/panic.
	It("EmitBars with all-zero seconds falls back to min-width bars", func() {
		f := OpenFrame(200, 100, themes["dark"], "T", "")
		EmitBars(f, []model.ResourceStats{
			{Name: "z1", TotalSeconds: 0},
			{Name: "z2", TotalSeconds: 0},
		}, BarsOpts{X: 10, Y: 10, YStep: 20, BarX: 50, BarWMax: 100, IncludeValueText: true})
		out := f.Close()
		assertValidXMLG(out)
		// Guaranteed by the `if w < 2 { w = 2 }` fallback — the fill rects
		// must have width="2".
		Expect(strings.Contains(string(out), `width="2"`)).To(BeTrue(),
			"all-zero entries must fall back to the min-width bar (width=2)")
	})

	It("EmitGradeRing on nil Grade emits nothing", func() {
		f := OpenFrame(200, 100, themes["dark"], "T", "")
		before := len(f.Close())
		f2 := OpenFrame(200, 100, themes["dark"], "T", "")
		EmitGradeRing(f2, 50, 50, 20, nil)
		after := len(f2.Close())
		Expect(after).To(Equal(before),
			"EmitGradeRing(nil Grade) must be a no-op")
	})

	It("EmitCalendar on empty daily emits 'No days in range'", func() {
		f := OpenFrame(200, 100, themes["dark"], "T", "")
		EmitCalendar(f, 10, 10, 180, 80, dataFixture().Payload.StartDate, nil)
		out := f.Close()
		Expect(strings.Contains(string(out), "No days in range")).To(BeTrue())
	})

	It("EmitPunchcard on empty cells emits 'No punchcard data'", func() {
		f := OpenFrame(600, 200, themes["dark"], "T", "")
		EmitPunchcard(f, 10, 10, 580, 180, nil)
		out := f.Close()
		Expect(strings.Contains(string(out), "No punchcard data")).To(BeTrue())
	})

	It("EmitMomentum on nil / empty payload emits 'No momentum data'", func() {
		f := OpenFrame(600, 200, themes["dark"], "T", "")
		EmitMomentum(f, 10, 10, 580, 180, nil)
		out := f.Close()
		Expect(strings.Contains(string(out), "No momentum data")).To(BeTrue())

		f2 := OpenFrame(600, 200, themes["dark"], "T", "")
		EmitMomentum(f2, 10, 10, 580, 180, &model.MomentumPayload{})
		Expect(strings.Contains(string(f2.Close()), "No momentum data")).To(BeTrue())
	})

	It("EmitAreaLine with <2 values emits 'Not enough data'", func() {
		f := OpenFrame(200, 100, themes["dark"], "T", "")
		EmitAreaLine(f, 10, 10, 180, 80, []int64{7})
		Expect(strings.Contains(string(f.Close()), "Not enough data")).To(BeTrue())

		f2 := OpenFrame(200, 100, themes["dark"], "T", "")
		EmitAreaLine(f2, 10, 10, 180, 80, nil)
		Expect(strings.Contains(string(f2.Close()), "Not enough data")).To(BeTrue())
	})

	// Distinct from <2: enough length but all-zero → mx==0 branch draws the
	// "No activity yet" message. Two separate branches, two separate messages.
	It("EmitAreaLine with enough all-zero values emits 'No activity yet'", func() {
		f := OpenFrame(200, 100, themes["dark"], "T", "")
		EmitAreaLine(f, 10, 10, 180, 80, []int64{0, 0, 0, 0})
		out := string(f.Close())
		Expect(strings.Contains(out, "No activity yet")).To(BeTrue(),
			"enough-length all-zero data must draw the 'No activity yet' message, got:\n%s", out)
		Expect(strings.Contains(out, "Not enough data")).To(BeFalse(),
			"'No activity yet' and 'Not enough data' are distinct messages for distinct branches")
	})

	// EmitDayHeatmap with no rows OR empty row.Daily → 'No data'.
	It("EmitDayHeatmap on empty rows emits 'No data'", func() {
		f := OpenFrame(600, 200, themes["dark"], "T", "")
		EmitDayHeatmap(f, 10, 10, 580, 180, dataFixture().Payload.StartDate, nil)
		Expect(strings.Contains(string(f.Close()), "No data")).To(BeTrue())

		f2 := OpenFrame(600, 200, themes["dark"], "T", "")
		EmitDayHeatmap(f2, 10, 10, 580, 180, dataFixture().Payload.StartDate,
			[]DayRow{{Name: "x", Daily: nil}})
		Expect(strings.Contains(string(f2.Close()), "No data")).To(BeTrue())
	})

	// A DayRow with all-zero Daily still draws the row header (its label) and
	// cells; the mx==0 fallback keeps the color math safe.
	It("EmitDayHeatmap with all-zero DayRow draws label + cells without panic", func() {
		f := OpenFrame(600, 200, themes["dark"], "T", "")
		EmitDayHeatmap(f, 10, 10, 580, 180, dataFixture().Payload.StartDate,
			[]DayRow{{Name: "quiet", Daily: []int64{0, 0, 0}}})
		out := f.Close()
		assertValidXMLG(out)
		Expect(strings.Contains(string(out), "quiet")).To(BeTrue(),
			"all-zero row must still emit its label")
	})

	// EmitPunchcard with a single cell but max==0 (Seconds==0) exercises the
	// mx==0→1 fallback.
	It("EmitPunchcard with a zero-seconds cell falls back without divide-by-zero", func() {
		f := OpenFrame(600, 200, themes["dark"], "T", "")
		EmitPunchcard(f, 10, 10, 580, 180, []model.PunchcardCell{
			{Dow: 1, Hour: 9, Seconds: 0},
		})
		out := f.Close()
		assertValidXMLG(out)
	})

	// EmitMomentum with a project whose Weekly is all-zero exercises the
	// per-project mx==0→1 fallback.
	It("EmitMomentum with an all-zero project falls back without divide-by-zero", func() {
		f := OpenFrame(600, 200, themes["dark"], "T", "")
		EmitMomentum(f, 10, 10, 580, 180, &model.MomentumPayload{
			Weeks: []string{"2026-01-05", "2026-01-12"},
			Projects: []model.MomentumProject{
				{Name: "quiet", Weekly: []int64{0, 0}},
			},
		})
		out := f.Close()
		assertValidXMLG(out)
		Expect(strings.Contains(string(out), "quiet")).To(BeTrue(),
			"all-zero project must still get a label")
	})
})

var _ = Describe("mixHex / parseHex color arithmetic", func() {
	// mixHex is used pervasively for heatmap intensity ramps — an out-of-range
	// q must clamp so we never produce an invalid #rrggbb like "#-1--1--1".
	It("mixHex clamps q < 0 to 0 (returns a) and q > 1 to 1 (returns b)", func() {
		a := "#000000"
		b := "#ffffff"
		Expect(mixHex(a, b, -0.5)).To(Equal("#000000"),
			"q<0 must clamp to 0 → returns a verbatim")
		Expect(mixHex(a, b, 1.5)).To(Equal("#ffffff"),
			"q>1 must clamp to 1 → returns b verbatim")
	})

	It("mixHex at q=0.5 lerps midway between a and b", func() {
		Expect(mixHex("#000000", "#ffffff", 0.5)).To(Equal("#7f7f7f"))
	})

	// parseHex must return (0,0,0) for malformed inputs — safe fallback that
	// keeps mixHex from panicking on user-authored theme overrides.
	It("mixHex on non-hex inputs returns a legal '#rrggbb' (parseHex fallback)", func() {
		out := mixHex("not-a-hex", "also-bad", 0.5)
		Expect(out).To(HavePrefix("#"),
			"even bad hex inputs must yield a legal #rrggbb")
		Expect(len(out)).To(Equal(7),
			"result must be exactly 7 chars (#rrggbb)")
	})

	It("mixHex on short hex input returns a legal '#rrggbb'", func() {
		out := mixHex("#abc", "#def", 0.3)
		Expect(len(out)).To(Equal(7))
	})

	It("mixHex on invalid hex chars (parseHex Sscanf error) still returns legal '#rrggbb'", func() {
		// 7 chars, starts with #, but contains non-hex chars → Sscanf fails.
		out := mixHex("#zzzzzz", "#000000", 0.5)
		Expect(out).To(HavePrefix("#"))
		Expect(len(out)).To(Equal(7))
	})
})

var _ = Describe("Frame plumbing", func() {
	// Frame.Write satisfies io.Writer — a required contract for fmt.Fprintf
	// (which is how primitives.go writes into the frame). This test locks the
	// io.Writer semantics: bytes-in equals bytes-appended, non-error return.
	It("Frame.Write appends the exact bytes and returns (len, nil)", func() {
		f := OpenFrame(100, 50, themes["dark"], "T", "")
		payload := []byte("<!--marker-->")
		n, err := f.Write(payload)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(len(payload)),
			"Frame.Write must return the exact byte-count written (io.Writer contract)")
		out := f.Close()
		Expect(strings.Contains(string(out), "<!--marker-->")).To(BeTrue(),
			"payload written via Frame.Write must appear verbatim in output")
	})

	// Close is documented as idempotent — a caller may Close inside a helper
	// and again at the top level. Double-close must not produce "</svg></svg>".
	It("Frame.Close is idempotent (double-close does not duplicate </svg>)", func() {
		f := OpenFrame(100, 50, themes["dark"], "T", "")
		first := f.Close()
		second := f.Close()
		// Both calls return the same byte slice.
		Expect(string(first)).To(Equal(string(second)),
			"double-close must return identical bytes")
		Expect(strings.Count(string(second), "</svg>")).To(Equal(1),
			"Close must emit exactly one </svg> regardless of call count")
	})

	// BodyTop bumps when a subtitle is provided — layout math depends on this.
	It("Frame.BodyTop advances when a subtitle is set", func() {
		fNo := OpenFrame(100, 50, themes["dark"], "T", "")
		fYes := OpenFrame(100, 50, themes["dark"], "T", "subtitle")
		Expect(fYes.BodyTop()).To(BeNumerically(">", fNo.BodyTop()),
			"subtitle must push BodyTop down to make room")
	})
})

var _ = Describe("EncodeDef round-trip via std/URL base64", func() {
	// EncodeDef output MUST always be decodable by DecodeDef. This is a
	// closed-loop invariant: any Def we can encode we can also decode into an
	// equal Def. Complements the existing round-trip test with a broader
	// panel-kind sweep.
	It("every panel kind survives an Encode→Decode round-trip", func() {
		for _, k := range []PanelKind{
			PanelCalendar, PanelTopLangs, PanelTopProjects, PanelGrade,
			PanelArea, PanelPunchcard, PanelMomentum, PanelMetrics,
		} {
			d := Def{Layout: Layout1, Panels: []Panel{{Kind: k}}}
			enc, err := EncodeDef(d)
			Expect(err).NotTo(HaveOccurred(), "EncodeDef(%s)", k)
			got, err := DecodeDef(enc)
			Expect(err).NotTo(HaveOccurred(), "DecodeDef of encoded %s", k)
			Expect(got.Panels[0].Kind).To(Equal(k),
				"round-trip must preserve panel kind %s", k)
		}
	})
})

var _ = Describe("ScrubMomentum invariants — additional coverage", func() {
	// ScrubMomentum must be case-insensitive on the hide match: a stored
	// hide rule "HAKATIME" hides "hakatime" and vice versa. This is a
	// SECURITY property — case-toggling a project name must not exfiltrate it.
	It("ScrubMomentum hide match is case-insensitive (security property)", func() {
		mp := &model.MomentumPayload{
			Weeks: []string{"2026-01-05"},
			Projects: []model.MomentumProject{
				{Name: "HAKATIME", TotalSeconds: 100},
				{Name: "public", TotalSeconds: 50},
			},
		}
		got := ScrubMomentum(mp, model.HiddenSetsMap{"project": {"hakatime"}})
		names := make([]string, 0, len(got.Projects))
		for _, p := range got.Projects {
			names = append(names, p.Name)
		}
		Expect(names).NotTo(ContainElement("HAKATIME"),
			"case-toggled project name must not slip past the hide filter")
	})

	// Non-project axes in the hide set must NOT affect ScrubMomentum — it
	// only filters on the project axis by contract.
	It("ScrubMomentum ignores non-project axes in the hide set", func() {
		mp := &model.MomentumPayload{
			Weeks:    []string{"2026-01-05"},
			Projects: []model.MomentumProject{{Name: "public-a", TotalSeconds: 100}},
		}
		got := ScrubMomentum(mp, model.HiddenSetsMap{"language": {"public-a"}})
		Expect(got).To(BeIdenticalTo(mp),
			"non-project axis must be a no-op — return input pointer unchanged")
	})
})
