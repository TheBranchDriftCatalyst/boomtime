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

// alwaysSpecKinds are target:"both" spec kinds with NO legacy renderer at
// all (Part B Stage 4: the goal-* kinds) — see IsAlwaysSpecKind's doc
// comment. They render via renderSpec/NeedsForSpec unconditionally, so they
// are "both" in specs.json without being part of Kinds() (the legacy
// whitelist render.go's `kinds` map derives).
var alwaysSpecKinds = []string{"goal-list", "goal-progress", "goal-ring"}

// catalogKinds mirrors every kind declared in
// web/src/features/widgets/catalog.ts (WIDGET_CATALOG), split the same way
// the FE file documents it: the 21 legacy-renderable kinds (== Kinds()) plus
// the 3 always-spec-engine goal-* kinds, and the 16 FE-only kinds. Keep BOTH
// this list and specs.json in sync with catalog.ts by hand — same
// discipline TestKindsMatchFrontendCatalog (render_test.go) already asks of
// Kinds().
var catalogBothKinds = append(append([]string{}, Kinds()...), alwaysSpecKinds...)

var catalogFEOnlyKinds = []string{
	"ai-assistance",
	"category-breakdown",
	"category-streamgraph",
	"github-commits",
	"github-languages",
	"github-repos",
	"github-stats",
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

	It("the 'both' spec kinds equal Kinds() plus alwaysSpecKinds exactly (no drift either direction)", func() {
		var got []string
		for _, s := range Specs() {
			if s.Target == TargetBoth {
				got = append(got, s.Kind)
			}
		}
		sort.Strings(got)
		want := append(append([]string{}, Kinds()...), alwaysSpecKinds...)
		sort.Strings(want)
		Expect(got).To(Equal(want))
	})

	It("alwaysSpecKinds are 'both' but absent from Kinds() (the Part B Stage 4 decoupling)", func() {
		for _, kind := range alwaysSpecKinds {
			Expect(IsKind(kind)).To(BeFalse(), "%s must stay OUT of the legacy kinds map", kind)
			Expect(IsAlwaysSpecKind(kind)).To(BeTrue(), "%s should be classified always-spec-engine", kind)
			spec, ok := SpecFor(kind)
			Expect(ok).To(BeTrue())
			Expect(spec.Target).To(Equal(TargetBoth))
		}
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

// goal-* kinds (Part B Stage 4) aren't in Kinds(), so they're absent from
// the loops above — dedicated coverage here for EmitGoalBar/EmitGoalRing
// well-formedness, xmlEscape on a hostile goal name, and the privacy
// no-oracle empty state (an empty/nil Data.Goals — which is what the
// handler sends both for "zero goals at all" and "zero PUBLIC goals" —
// must render the exact same placeholder).
var _ = Describe("goal-* kinds (always-spec-engine, Part B Stage 4)", func() {
	It("render well-formed, camo-safe SVG with populated goals", func() {
		d := dataFixture()
		d.Goals = []GoalProgressLite{
			{Name: "Weekly Go", Progress: 0.6, Hit: false},
			{Name: "Daily streak", Progress: 1, Hit: true},
			{Name: "Cap browsing", Progress: 0.2, Hit: false},
		}
		for _, kind := range alwaysSpecKinds {
			b, err := RenderSpec(kind, d, Options{Theme: "dark", Subtitle: "last 30 days"})
			Expect(err).NotTo(HaveOccurred(), "RenderSpec(%s)", kind)
			assertValidXMLG(b)
			s := string(b)
			Expect(strings.HasPrefix(strings.TrimSpace(s), "<svg")).To(BeTrue(), "%s: output does not start with <svg", kind)
			for _, banned := range []string{"<script", "https://", "url(http", "@import"} {
				Expect(strings.Contains(s, banned)).To(BeFalse(), "%s: output contains banned token %q", kind, banned)
			}
			Expect(s).To(ContainSubstring("Weekly Go"), "%s: expected the first goal's name to render", kind)
		}
	})

	It("render the SAME empty-state placeholder for a nil/empty Goals slice (privacy no-oracle)", func() {
		empty := &Data{Payload: &model.StatsPayload{}}
		for _, kind := range alwaysSpecKinds {
			b, err := RenderSpec(kind, empty, Options{})
			Expect(err).NotTo(HaveOccurred(), "RenderSpec(%s) on empty goals", kind)
			assertValidXMLG(b)
			Expect(string(b)).To(ContainSubstring("No goals yet"), "%s: expected the empty-goals placeholder", kind)
		}
	})

	It("xmlEscape's a hostile goal name (script tag + ampersand) so the SVG stays well-formed", func() {
		d := dataFixture()
		d.Goals = []GoalProgressLite{
			{Name: `<script>alert(1)</script> & "friends"`, Progress: 0.5, Hit: false},
		}
		for _, kind := range alwaysSpecKinds {
			b, err := RenderSpec(kind, d, Options{})
			Expect(err).NotTo(HaveOccurred(), "RenderSpec(%s)", kind)
			assertValidXMLG(b)
			Expect(string(b)).NotTo(ContainSubstring("<script>"), "%s: raw <script> leaked into SVG", kind)
		}
	})
})
