// Package widget renders the public embeddable SVG stats widgets (gaka-hsj +
// gaka-unq.2). Every kind renders through spec.go's renderSpec engine, driven
// by the canonical internal/widget/specs.json — see spec.go's package doc for
// the engine itself (Part B Stage 5 cutover: renderSpec is now the ONLY
// render path; the hand-written per-kind renderers this file used to hold
// are gone).
//
// What's left in this file: the kind-registry accessors (Kinds/IsKind/Needs
// — thin wrappers over the spec registry, kept here for API continuity), the
// "custom" builder dispatch (RenderCustom/IsCustomKind — custom.go's
// Def-based composition is untouched by the cutover), renderBadge (the one
// primitive renderSpec special-cases rather than treating as a panel/rect —
// see spec.go's renderSpec doc comment), and the small string/formatting
// helpers (xmlEscape, truncate, compound, topEntries) that primitives.go,
// custom.go, and spec.go all still lean on.
package widget

import (
	"fmt"
	"sort"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
)

// Options are the per-request render knobs (from URL params on the public
// endpoint). Title overrides the kind's default headline; Subtitle is the
// range hint ("last 30 days").
type Options struct {
	Theme    string
	Title    string
	Subtitle string
}

// Data is the input bundle for renderers. Only fields the requested kind
// declares in Needs() are populated by the handler; everything else is nil.
type Data struct {
	Payload   *model.StatsPayload
	Grade     *stats.GradeResult
	Punchcard *model.PunchcardPayload
	Momentum  *model.MomentumPayload
	Sessions  *model.SessionsPayload
	// Goals is the PRIVACY-FILTERED set the widgets handler builds for the
	// goal-progress/goal-ring/goal-list kinds (Part B Stage 4): only the
	// link owner's enabled && public goals ever land here — see
	// internal/widgets.publicGoalsFor. This package never sees a goal's
	// spec tree, sub-conditions, or private/disabled siblings, only the
	// name + progress fraction + hit flag the primitives draw.
	Goals []GoalProgressLite
}

// GoalProgressLite is the thin, already-privacy-filtered projection of a
// goal + its cached progress that EmitGoalBar/EmitGoalRing draw. Deliberately
// minimal — see the Data.Goals doc comment for why this package never sees
// more than name/progress/hit.
type GoalProgressLite struct {
	Name     string
	Progress float64 // 0..1, already clamped by the evaluator
	Hit      bool
}

// Requirements declares which optional data blobs a kind consumes. The handler
// gates its DB queries on these so a badge render never fetches punchcard.
// Categories gates the category-rows fetch that folds the Categories segment
// into the StatsPayload (Part B Stage 1: only categories-chart wants it).
// Goals gates the owner's public-goal fetch (Part B Stage 4).
type Requirements struct {
	Grade, Punchcard, Momentum, Sessions, Categories, Goals bool
}

// Kinds returns the sorted whitelist of renderable widget kinds — every
// target:"both" spec in specs.json (Part B Stage 5: this now includes the
// goal-* kinds, which pre-cutover rendered via IsAlwaysSpecKind outside the
// legacy `kinds` map). The FE widget catalog
// (web/src/features/widgets/catalog.ts SVG_RENDERABLE_KINDS) must list
// exactly these kinds — TestKindsMatchFrontendCatalog guards the two lists
// against drift.
func Kinds() []string {
	out := make([]string, 0, len(specRegistry))
	for _, s := range specRegistry {
		if s.Target == TargetBoth {
			out = append(out, s.Kind)
		}
	}
	sort.Strings(out)
	return out
}

// IsKind reports whether kind is renderable — i.e. has a target:"both" spec.
func IsKind(kind string) bool {
	spec, ok := SpecFor(kind)
	return ok && spec.Target == TargetBoth
}

// Needs reports which optional Data fields a kind wants populated — the
// handler uses this to skip expensive fetches (punchcard/momentum/sessions
// each own their own DB round-trip). Thin wrapper over NeedsForSpec; kept as
// a kind-keyed entry point for callers (and tests) that don't already have
// the kind's Spec in hand.
func Needs(kind string) Requirements {
	spec, ok := SpecFor(kind)
	if !ok || spec.Target != TargetBoth {
		return Requirements{}
	}
	return NeedsForSpec(spec)
}

// RenderCustom dispatches the builder-composed "custom" widget (gaka-567).
// The Def is passed inline in the URL — no saved-def table for v1. Kept
// separate from RenderSpec so a caller can't mint a custom widget without
// providing the spec.
func RenderCustom(d *Data, def Def, opts Options) ([]byte, error) {
	return renderCustom(d, themeFor(opts.Theme), opts, def)
}

// IsCustomKind reports whether the URL kind is the builder-driven custom
// composition. The handler branches on it to parse the ?spec= param.
func IsCustomKind(kind string) bool { return kind == "custom" }

// ---- shared string helpers ----

var xmlEscaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;",
)

func xmlEscape(s string) string { return xmlEscaper.Replace(s) }

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func compound(seconds int64) string { return stats.CompoundDuration(&seconds) }

// topEntries sorts a (possibly capped) resource list by seconds desc, drops
// the synthesized "Other (N more)" row, and returns up to n entries.
func topEntries(list []model.ResourceStats, n int) []model.ResourceStats {
	sorted := make([]model.ResourceStats, 0, len(list))
	for _, r := range list {
		if r.OtherCount > 0 {
			continue
		}
		sorted = append(sorted, r)
	}
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].TotalSeconds > sorted[b].TotalSeconds })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

// ---- badge (native flat pill; the shields.io proxy at /badge/svg stays) ----

const badgeCharW = 6.6

func renderBadge(d *Data, th Theme, opts Options) ([]byte, error) {
	label := opts.Title
	if label == "" {
		label = "boomtime"
	}
	label = truncate(label, 24)
	msg := compound(d.Payload.TotalSeconds)
	labelW := int(badgeCharW*float64(len([]rune(label)))) + 20
	msgW := int(badgeCharW*float64(len([]rune(msg)))) + 20
	total := labelW + msgW
	var b []byte
	b = append(b, []byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">`,
		total, xmlEscape(label), xmlEscape(msg)))...)
	b = append(b, []byte(`<linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>`)...)
	b = append(b, []byte(fmt.Sprintf(`<clipPath id="r"><rect width="%d" height="20" rx="3" fill="#fff"/></clipPath><g clip-path="url(#r)">`, total))...)
	b = append(b, []byte(fmt.Sprintf(`<rect width="%d" height="20" fill="#555"/><rect x="%d" width="%d" height="20" fill="%s"/><rect width="%d" height="20" fill="url(#s)"/></g>`,
		labelW, labelW, msgW, th.Accent, total))...)
	b = append(b, []byte(fmt.Sprintf(`<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11"><text x="%d" y="14">%s</text><text x="%d" y="14">%s</text></g></svg>`,
		labelW/2, xmlEscape(label), labelW+msgW/2, xmlEscape(msg)))...)
	return b, nil
}
