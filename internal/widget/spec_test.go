// spec_test.go — Part B Stage 2: the canonical WidgetSpec registry + the
// generic renderSpec engine (spec.go). Three invariant classes:
//
//   - Cross-language guard: every WIDGET_CATALOG kind (web/src/features/
//     widgets/catalog.ts) has a spec entry, classified "both" or "fe-only",
//     with the "both" set matching Kinds() exactly. The TS-side twin
//     (web/src/features/widgets/specs.test.ts) pins the same list from the
//     FE's own catalog.ts import.
//   - NeedsForSpec(spec) == Needs(kind) EXACTLY for every "both" kind — the
//     spec-engine's derived requirements must not fetch more or less than
//     the hand-written legacy map, or the handler either fetches
//     unnecessarily or leaves a renderer looking at a nil blob.
//   - RenderSpec produces well-formed, camo-safe SVG for every "both" kind on
//     both a populated fixture AND an empty payload — including a
//     side-by-side render against the legacy Render() (NOT byte-identical;
//     re-baseline is allowed for this stage, see spec.go's package doc).
package widget

import (
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// catalogKinds mirrors every kind declared in
// web/src/features/widgets/catalog.ts (WIDGET_CATALOG), split the same way
// the FE file documents it: the 21 backend-renderable kinds (== Kinds()) and
// the 19 FE-only kinds. Keep BOTH this list and specs.json in sync with
// catalog.ts by hand — same discipline TestKindsMatchFrontendCatalog
// (render_test.go) already asks of Kinds().
var catalogBothKinds = Kinds() // the 21 "both" kinds, straight from the legacy whitelist

var catalogFEOnlyKinds = []string{
	"ai-assistance",
	"category-breakdown",
	"category-streamgraph",
	"github-commits",
	"github-languages",
	"github-repos",
	"github-stats",
	"goal-list",
	"goal-progress",
	"goal-ring",
	"grade-badge",
	"hero-identity",
	"labels-showcase",
	"loc",
	"overview-stats",
	"overview-timeline",
	"overview-total-activity",
	"streak-banner",
	"wellness",
}

var _ = Describe("Spec registry mirrors the FE catalog (web/src/features/widgets/catalog.ts)", func() {
	It("every catalog kind has a spec entry classified both|fe-only", func() {
		for _, kind := range catalogBothKinds {
			spec, ok := SpecFor(kind)
			Expect(ok).To(BeTrue(), "missing spec entry for %q (declared both-renderable via Kinds())", kind)
			Expect(spec.Target).To(Equal(TargetBoth), "%q should be classified both", kind)
		}
		for _, kind := range catalogFEOnlyKinds {
			spec, ok := SpecFor(kind)
			Expect(ok).To(BeTrue(), "missing spec entry for %q (an FE-only catalog kind)", kind)
			Expect(spec.Target).To(Equal(TargetFEOnly), "%q should be classified fe-only", kind)
		}
		// No unclassified stragglers, no unexpected extras: the registry's
		// total size is EXACTLY the catalog's.
		Expect(Specs()).To(HaveLen(len(catalogBothKinds)+len(catalogFEOnlyKinds)),
			"specs.json entry count drifted from the catalog kind count")
	})

	It("the 'both' spec kinds equal Kinds() exactly (no drift either direction)", func() {
		var got []string
		for _, s := range Specs() {
			if s.Target == TargetBoth {
				got = append(got, s.Kind)
			}
		}
		sort.Strings(got)
		Expect(got).To(Equal(Kinds()))
	})

	It("every fe-only spec carries a reason and NO panels (it's a leaf, not a renderer)", func() {
		for _, s := range Specs() {
			if s.Target != TargetFEOnly {
				continue
			}
			Expect(s.Reason).NotTo(BeEmpty(), "%q: fe-only spec is missing a reason", s.Kind)
			Expect(s.Panels).To(BeEmpty(), "%q: fe-only spec should not declare panels", s.Kind)
		}
	})

	It("every both spec carries at least one panel, and a size unless it's the badge special-case", func() {
		for _, s := range Specs() {
			if s.Target != TargetBoth {
				continue
			}
			Expect(s.Panels).NotTo(BeEmpty(), "%q: both spec has no panels", s.Kind)
			isBadge := len(s.Panels) == 1 && s.Panels[0].Primitive == "badge"
			if !isBadge {
				// badge bypasses OpenFrame/Size entirely — see renderSpec's doc
				// comment on why it's the one primitive that isn't a panel/rect.
				Expect(s.Size).NotTo(BeNil(), "%q: non-badge both spec is missing a size", s.Kind)
			}
		}
	})
})

// NeedsForSpec is the spec-engine's counterpart to the hand-written Needs
// map in render.go. The handler swaps one for the other under
// BOOM_WIDGET_SPEC_ENGINE — if these ever disagree, flipping the flag either
// starves a renderer of data it needs or burns an extra DB round-trip it
// doesn't.
var _ = Describe("NeedsForSpec matches the legacy Needs(kind) exactly", func() {
	It("agrees with Needs() for every both-target kind", func() {
		for _, kind := range Kinds() {
			spec, ok := SpecFor(kind)
			Expect(ok).To(BeTrue(), "kind %q has no spec entry", kind)
			Expect(NeedsForSpec(spec)).To(Equal(Needs(kind)), "NeedsForSpec/Needs disagree for kind %q", kind)
		}
	})
})

var _ = Describe("RenderSpec", func() {
	It("every both-target kind renders well-formed, camo-safe SVG (fixture payload)", func() {
		d := dataFixture()
		for _, kind := range Kinds() {
			By("RenderSpec " + kind)
			b, err := RenderSpec(kind, d, Options{Theme: "dark", Subtitle: "last 30 days"})
			Expect(err).NotTo(HaveOccurred(), "RenderSpec(%s)", kind)
			assertValidXMLG(b)
			s := string(b)
			Expect(strings.HasPrefix(strings.TrimSpace(s), "<svg")).To(BeTrue(), "%s: output does not start with <svg", kind)
			for _, banned := range []string{"<script", "https://", "url(http", "@import"} {
				Expect(strings.Contains(s, banned)).To(BeFalse(), "%s: output contains banned token %q", kind, banned)
			}

			// Parity-ish: the legacy path must ALSO render well-formed SVG for
			// the same fixture — a side-by-side sanity check, NOT a
			// byte-identity assertion (re-baseline is allowed for Stage 2; see
			// spec.go's package doc).
			legacy, err := Render(kind, d, Options{Theme: "dark", Subtitle: "last 30 days"})
			Expect(err).NotTo(HaveOccurred(), "legacy Render(%s)", kind)
			assertValidXMLG(legacy)
		}
	})

	It("every both-target kind survives an empty payload without panicking", func() {
		empty := &Data{Payload: &model.StatsPayload{}}
		for _, kind := range Kinds() {
			b, err := RenderSpec(kind, empty, Options{})
			Expect(err).NotTo(HaveOccurred(), "RenderSpec(%s) on empty payload", kind)
			assertValidXMLG(b)
		}
	})

	It("badge is byte-identical between Render and RenderSpec (both call renderBadge directly)", func() {
		d := dataFixture()
		legacy, err := Render("badge", d, Options{Theme: "dark", Title: "boomtime"})
		Expect(err).NotTo(HaveOccurred())
		spec, err := RenderSpec("badge", d, Options{Theme: "dark", Title: "boomtime"})
		Expect(err).NotTo(HaveOccurred())
		Expect(spec).To(Equal(legacy), "badge should bypass panel/rect dispatch entirely and match legacy byte-for-byte")
	})

	It("errors on an unknown kind and on a fe-only kind (no spec-engine renderer exists)", func() {
		d := dataFixture()
		_, err := RenderSpec("nope", d, Options{})
		Expect(err).To(HaveOccurred(), "unknown kind should error")

		_, err = RenderSpec("grade-badge", d, Options{})
		Expect(err).To(HaveOccurred(), "fe-only kind has no backend renderer and should error")
	})
})
