// spec_test.go — Part B Stage 2: the canonical WidgetSpec registry + the
// generic renderSpec engine (spec.go); Part B Stage 5 cutover made renderSpec
// the ONLY render path (the legacy hand-written Render() this file used to
// sanity-check against is gone). Three invariant classes:
//
//   - Cross-language guard: every WIDGET_CATALOG kind (web/src/features/
//     widgets/catalog.ts) has a spec entry, classified "both" or "fe-only",
//     with the "both" set matching Kinds() exactly. The TS-side twin
//     (web/src/features/widgets/specs.test.ts) pins the same list from the
//     FE's own catalog.ts import.
//   - NeedsForSpec(spec) == Needs(kind) for every "both" kind — trivially
//     true post-cutover since Needs(kind) is now DEFINED as
//     NeedsForSpec(SpecFor(kind)) (render.go), but kept as an explicit
//     regression guard: a future refactor that reintroduces a kind-specific
//     Needs override would trip it.
//   - RenderSpec produces well-formed, camo-safe SVG for every "both" kind on
//     both a populated fixture AND an empty payload.
package widget

import (
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// goalKinds are the target:"both" spec kinds that read the account-wide
// Goals data (see widget.IsGoalKind) — used below for dedicated goal-*
// coverage (privacy no-oracle, xmlEscape on a hostile name). Pre-cutover
// these were also the exact set IsAlwaysSpecKind identified (kinds with no
// legacy renderer); that distinction is gone now that renderSpec is the only
// path for every kind, so this list exists purely for test organization.
var goalKinds = []string{"goal-list", "goal-progress", "goal-ring"}

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
		for _, kind := range Kinds() {
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
		Expect(Specs()).To(HaveLen(len(Kinds())+len(catalogFEOnlyKinds)),
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
		want := append([]string{}, Kinds()...)
		sort.Strings(want)
		Expect(got).To(Equal(want))
	})

	It("goal-* kinds are 'both', part of Kinds(), and the only kinds IsGoalKind reports true for", func() {
		for _, kind := range goalKinds {
			Expect(IsKind(kind)).To(BeTrue(), "%s must be a renderable (target:\"both\") kind", kind)
			Expect(IsGoalKind(kind)).To(BeTrue(), "%s should be classified a goal kind", kind)
			spec, ok := SpecFor(kind)
			Expect(ok).To(BeTrue())
			Expect(spec.Target).To(Equal(TargetBoth))
		}
		goalSet := map[string]bool{"goal-list": true, "goal-progress": true, "goal-ring": true}
		for _, kind := range Kinds() {
			if goalSet[kind] {
				continue
			}
			Expect(IsGoalKind(kind)).To(BeFalse(), "%s should NOT be classified a goal kind", kind)
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

	It("every both spec carries at least one panel, and a size + title unless it's the badge special-case", func() {
		for _, s := range Specs() {
			if s.Target != TargetBoth {
				continue
			}
			Expect(s.Panels).NotTo(BeEmpty(), "%q: both spec has no panels", s.Kind)
			isBadge := len(s.Panels) == 1 && s.Panels[0].Primitive == "badge"
			if !isBadge {
				// badge bypasses OpenFrame/Size/Title entirely — see renderSpec's
				// doc comment on why it's the one primitive that isn't a
				// panel/rect/card at all.
				Expect(s.Size).NotTo(BeNil(), "%q: non-badge both spec is missing a size", s.Kind)
				// Pre-cutover regression guard: a "both" spec with no Title falls
				// back to the raw kind slug ("stats-card") as the card headline —
				// every spec MUST carry a real one so prod embeds (which never
				// pass ?title=) show "Coding Stats", not the slug.
				Expect(s.Title).NotTo(BeEmpty(), "%q: non-badge both spec is missing a title", s.Kind)
			}
		}
	})
})

// NeedsForSpec is the spec-engine's requirements deriver; Needs(kind)
// (render.go) is now literally defined as NeedsForSpec(SpecFor(kind)), so
// this is a regression guard against a future kind-specific override
// silently diverging from the spec-derived truth.
var _ = Describe("NeedsForSpec matches Needs(kind) exactly", func() {
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

	// badge is the one primitive renderSpec special-cases: it bypasses the
	// generic OpenFrame/panel/rect dispatch entirely and calls renderBadge
	// directly (see spec.go's renderSpec doc comment + render.go's
	// renderBadge). Pinned here against a direct renderBadge call rather than
	// against the (now-deleted) legacy Render() — the invariant under test
	// hasn't changed, just what it's compared to.
	It("badge bypasses the generic panel/rect engine — calls renderBadge directly", func() {
		d := dataFixture()
		opts := Options{Theme: "dark", Title: "boomtime"}
		want, err := renderBadge(d, themeFor(opts.Theme), opts)
		Expect(err).NotTo(HaveOccurred())
		got, err := RenderSpec("badge", d, opts)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(want), "badge should call renderBadge directly, not the panel/rect dispatch")
	})

	It("errors on an unknown kind and on a fe-only kind (no spec-engine renderer exists)", func() {
		d := dataFixture()
		_, err := RenderSpec("nope", d, Options{})
		Expect(err).To(HaveOccurred(), "unknown kind should error")

		_, err = RenderSpec("grade-badge", d, Options{})
		Expect(err).To(HaveOccurred(), "fe-only kind has no backend renderer and should error")
	})

	// Pre-cutover fix: renderSpec used to fall back to the raw kind slug
	// ("stats-card") as the card title whenever the request omitted
	// ?title=, which is EVERY prod embed URL (widgetSvgUrl never sets it).
	// spec.Title now sits between opts.Title and the slug in the fallback
	// chain — this pins that every non-badge "both" kind renders its real
	// headline (not the slug) with Title unset, and that ?title= still wins
	// when the caller does pass one.
	It("uses spec.Title (not the kind slug) as the card headline when opts.Title is empty", func() {
		d := dataFixture()
		for _, kind := range Kinds() {
			if kind == "badge" {
				continue // no Frame/title at all — see renderSpec's doc comment
			}
			spec, ok := SpecFor(kind)
			Expect(ok).To(BeTrue(), "kind %q has no spec entry", kind)
			Expect(spec.Title).NotTo(BeEmpty(), "kind %q: spec has no title", kind)

			b, err := RenderSpec(kind, d, Options{Theme: "dark"})
			Expect(err).NotTo(HaveOccurred(), "RenderSpec(%s)", kind)
			s := string(b)
			Expect(s).To(ContainSubstring(xmlEscape(spec.Title)),
				"kind %q: expected spec.Title %q in the rendered SVG", kind, spec.Title)
			Expect(strings.Contains(s, ">"+kind+"<")).To(BeFalse(),
				"kind %q: raw kind slug leaked into the SVG as the title", kind)
		}
	})

	It("?title= (opts.Title) still overrides spec.Title", func() {
		d := dataFixture()
		b, err := RenderSpec("top-langs", d, Options{Title: "My Custom Title"})
		Expect(err).NotTo(HaveOccurred())
		s := string(b)
		Expect(s).To(ContainSubstring("My Custom Title"))
		Expect(s).NotTo(ContainSubstring("Top Languages"))
	})
})

// goal-* kinds (Part B Stage 4 privacy gate) are covered generically by the
// loops above (they're part of Kinds() since Part B Stage 5), but get
// dedicated coverage here for EmitGoalBar/EmitGoalRing well-formedness,
// xmlEscape on a hostile goal name, and the privacy no-oracle empty state
// (an empty/nil Data.Goals — which is what the handler sends both for "zero
// goals at all" and "zero PUBLIC goals" — must render the exact same
// placeholder).
var _ = Describe("goal-* kinds (Part B Stage 4 privacy gate)", func() {
	It("render well-formed, camo-safe SVG with populated goals", func() {
		d := dataFixture()
		d.Goals = []GoalProgressLite{
			{Name: "Weekly Go", Progress: 0.6, Hit: false},
			{Name: "Daily streak", Progress: 1, Hit: true},
			{Name: "Cap browsing", Progress: 0.2, Hit: false},
		}
		for _, kind := range goalKinds {
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
		for _, kind := range goalKinds {
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
		for _, kind := range goalKinds {
			b, err := RenderSpec(kind, d, Options{})
			Expect(err).NotTo(HaveOccurred(), "RenderSpec(%s)", kind)
			assertValidXMLG(b)
			Expect(string(b)).NotTo(ContainSubstring("<script>"), "%s: raw <script> leaked into SVG", kind)
		}
	})
})
