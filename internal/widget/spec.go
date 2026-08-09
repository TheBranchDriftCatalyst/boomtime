// spec.go: the canonical WidgetSpec model + generic render engine (Part B
// Stage 2; the ONLY render path as of the Part B Stage 5 cutover). ONE
// committed specs.json describes every catalog kind — Go embeds it via the
// directive below (see the var block), and the FE imports the SAME file
// (see web/src/features/widgets/specs.ts) so there is exactly one source of
// truth, no codegen, no drift. renderSpec is renderCustom/renderPanel's
// generalization: same OpenFrame + panelRect + Emit* vocabulary, but driven
// by data (a Spec) instead of a hand-written renderer per kind.
//
// Pre-cutover this engine sat behind BOOM_WIDGET_SPEC_ENGINE
// (config.Config.FeatureWidgetSpecEngine), coexisting with a hand-written
// per-kind renderer in render.go. That flag and the legacy renderers are
// gone: the widgets handler always calls RenderSpec/NeedsForSpec now.
package widget

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
)

//go:embed specs.json
var specsJSON []byte

// SpecTarget classifies a spec. "both" is backend-renderable (this engine)
// AND FE-renderable (the composable dashboard); "fe-only" kinds carry no
// panels — they exist purely so the cross-language guard (spec_test.go +
// web/src/features/widgets/specs.test.ts) can prove every catalog kind is
// classified one way or the other, with none silently unaccounted for.
type SpecTarget string

const (
	TargetBoth   SpecTarget = "both"
	TargetFEOnly SpecTarget = "fe-only"
)

// Spec is the canonical per-kind widget description. One entry per catalog
// kind, all in specs.json — see the package doc above for why there's only
// one file.
type Spec struct {
	Kind   string     `json:"kind"`
	Target SpecTarget `json:"target"`
	// Title is the card headline renderSpec opens the Frame with when the
	// request didn't pass ?title= — the spec-engine's counterpart to each
	// legacy renderer's `defaultString(opts.Title, "...")` literal. Every
	// target:"both" spec except "badge" (which has no Frame/title at all —
	// see renderSpec's doc comment) MUST set this, or a cutover to the spec
	// engine would show the raw kind slug ("stats-card") instead of a real
	// headline ("Coding Stats") on every prod embed that doesn't pass an
	// explicit title.
	Title string `json:"title,omitempty"`
	// Reason documents WHY an fe-only kind has no backend renderer (in-page
	// identity chrome, self-fetching overview/GitHub data, private-by-default
	// goals, …). Pure documentation — read by the guard tests and reviewers,
	// not by renderSpec.
	Reason string `json:"reason,omitempty"`
	// Size is the canvas renderSpec opens the Frame at. Unused for target
	// "fe-only" and for the "badge" primitive (badge is not a Frame/card at
	// all — see renderSpec below).
	Size *SpecSize `json:"size,omitempty"`
	// DefaultView mirrors WidgetCatalogEntry.defaultView (catalog.ts) —
	// carried here so a future FE reader doesn't need a second lookup for a
	// kind's default chart-toggle view. Not consumed by the Go renderer.
	DefaultView string      `json:"defaultView,omitempty"`
	Panels      []SpecPanel `json:"panels,omitempty"`
}

// SpecSize is a spec's canvas dimensions.
type SpecSize struct {
	W int `json:"w"`
	H int `json:"h"`
}

// SpecPanel is one drawn element: a primitive (the Emit* vocabulary) bound
// to a payload source. Rect is optional ONLY when a spec has exactly one
// panel — renderSpec then fills the whole card body automatically (mirrors
// custom.go's Layout1). A spec with more than one panel (a composite) MUST
// give every panel an explicit, non-overlapping Rect; there is no implicit
// multi-panel layout in this engine (unlike renderCustom's 4 fixed
// Layout2Horz/3Horz/2Vert grids) — composites commit their exact geometry in
// specs.json instead.
type SpecPanel struct {
	Primitive string `json:"primitive"`
	Binding   string `json:"binding"`
	// Field disambiguates a sub-value on a binding whose blob carries more
	// than one renderable number. Today only the "sessions" binding needs it
	// (count|median|longest) — every other binding maps 1:1 to what its
	// primitive draws.
	Field string     `json:"field,omitempty"`
	Rect  *panelRect `json:"rect,omitempty"`
	Title string     `json:"title,omitempty"`
}

var (
	specRegistry []Spec
	specByKind   map[string]Spec
)

func init() {
	if err := json.Unmarshal(specsJSON, &specRegistry); err != nil {
		panic(fmt.Sprintf("widget: specs.json is invalid: %v", err))
	}
	specByKind = make(map[string]Spec, len(specRegistry))
	for _, s := range specRegistry {
		specByKind[s.Kind] = s
	}
}

// Specs returns every registered spec (both "both" and "fe-only" kinds), in
// specs.json declaration order.
func Specs() []Spec { return specRegistry }

// SpecFor looks up a kind's spec.
func SpecFor(kind string) (Spec, bool) {
	s, ok := specByKind[kind]
	return s, ok
}

// IsGoalKind reports whether kind's spec reads the account-wide Goals data
// (any panel bound to "goals" — see NeedsForSpec). This used to be exactly
// IsAlwaysSpecKind's set pre-cutover (goal-* kinds had no legacy renderer to
// fall back to, so they always rendered via the spec engine); now that
// renderSpec is the only path for every kind, that distinction is gone and
// this predicate is purely about the PRIVACY property goals have that no
// other "both" kind does: goals are account-wide, never project/space-
// scoped. The widgets handler uses this to 404 a goal-* embed minted under a
// non-user-scoped widget link (see WidgetSvg's scope gate) — if a future
// kind legitimately needs the same account-wide-only treatment for a
// different reason, narrow this to a dedicated predicate instead of
// overloading it.
func IsGoalKind(kind string) bool {
	spec, ok := SpecFor(kind)
	return ok && spec.Target == TargetBoth && NeedsForSpec(spec).Goals
}

// NeedsForSpec derives Requirements from the union of every panel's
// binding — the spec-engine's counterpart to the hand-written `Needs` map in
// render.go. spec_test.go pins NeedsForSpec(spec) == Needs(kind) for every
// "both" kind: the two must agree exactly, or the handler would fetch either
// too little (nil-pointer risk) or too much (wasted DB round-trips) under
// the flag.
func NeedsForSpec(spec Spec) Requirements {
	var r Requirements
	for _, p := range spec.Panels {
		switch p.Binding {
		case "grade":
			r.Grade = true
		case "punchcard":
			r.Punchcard = true
		case "momentum":
			r.Momentum = true
		case "sessions":
			r.Sessions = true
		case "categories":
			r.Categories = true
		case "goals":
			r.Goals = true
		}
	}
	return r
}

// RenderSpec renders a "both"-target kind through the generic spec engine —
// the ONLY render path (Part B Stage 5). Callers (the widgets handler, plus
// tests) look up SpecFor(kind) themselves when they also need NeedsForSpec,
// but RenderSpec re-resolves it so it stays a self-sufficient one-call API.
func RenderSpec(kind string, d *Data, opts Options) ([]byte, error) {
	spec, ok := SpecFor(kind)
	if !ok || spec.Target != TargetBoth {
		return nil, fmt.Errorf("unknown spec-engine widget kind %q", kind)
	}
	return renderSpec(spec, d, themeFor(opts.Theme), opts)
}

// renderSpec is renderCustom's generalization: open a Frame at the spec's
// size, dispatch every panel to its primitive via the SAME Emit* vocabulary
// + panelRect math renderPanel uses, close, return.
//
// The one kind that doesn't fit this per-panel/rect model: "badge". A badge
// is not a titled card at all — no OpenFrame, no title, a totally different
// tiny pill SVG (see renderBadge in render.go). specs.json names it as a
// single panel with primitive "badge" so the cross-language guard still
// finds a spec entry for it, but renderSpec special-cases that primitive to
// call the existing renderBadge directly rather than treating "badge" as a
// primitive with rect math — badge output is therefore byte-identical
// between the legacy and spec-engine paths (see spec_test.go).
func renderSpec(spec Spec, d *Data, th Theme, opts Options) ([]byte, error) {
	if len(spec.Panels) == 1 && spec.Panels[0].Primitive == "badge" {
		return renderBadge(d, th, opts)
	}

	w, h := 495, 195
	if spec.Size != nil {
		w, h = spec.Size.W, spec.Size.H
	}
	// ?title= always wins (mirrors every legacy renderer's
	// defaultString(opts.Title, "...")); spec.Title is the card's real
	// headline; the kind slug is only a last-resort fallback for a spec that
	// somehow shipped without one (the "every both spec has a title" guard
	// in spec_test.go should make that unreachable in practice).
	title := opts.Title
	if title == "" {
		title = spec.Title
	}
	if title == "" {
		title = spec.Kind
	}
	f := OpenFrame(w, h, th, title, opts.Subtitle)
	// Part B Stage 5 cutover fix: the deleted legacy renderers each ran a
	// kind-specific isEmpty check BEFORE drawing anything, short-circuiting
	// to a single whole-card placeholder (f.Empty(msg)) instead of a card
	// full of zero-value panels (a bars panel with nothing to show, a
	// compound(0)-formatted "no data" METRIC VALUE sitting under a "Total"
	// label, etc.). The generic per-panel dispatch below has no way to
	// derive "is there anything meaningful to show" purely from a spec's
	// panel bindings — see emptyStateOverride's doc comment for why this
	// stays a small kind-keyed table rather than a binding-derived rule.
	if empty, msg := emptyStateOverride(spec.Kind, d); empty {
		f.Empty(msg)
		return f.Close(), nil
	}
	for _, p := range spec.Panels {
		if err := renderSpecPanel(f, d, w, h, len(spec.Panels), p); err != nil {
			return nil, err
		}
	}
	return f.Close(), nil
}

// emptyStateOverride reports whether kind should short-circuit to a
// whole-card "nothing to show" placeholder instead of drawing its panels for
// the given Data, and if so, the message to show — restoring the exact
// per-kind isEmpty short-circuits the deleted legacy renderers ran (see
// render.go's history: renderStatsCard/renderProfileSummary checked
// TotalSeconds==0; renderTotalTime/renderDailyAvg the same; renderCurrentStreak/
// renderLongestStreak/renderActiveDays checked len(DailyTotal)==0;
// renderCategories/renderEditors/renderPlatforms checked their own segment's
// length). Each kind's emptiness hinges on a DIFFERENT field — deep-work's
// on Sessions.Summary.Count, the stat tiles on TotalSeconds vs DailyTotal —
// so this can't be derived generically from a spec's panel bindings; it's a
// deliberately small, explicit table. Kinds not listed here rely on their
// primitives' own empty-state fallbacks (EmitBars/EmitCalendar/EmitChips/
// etc. each already draw a reasonable "no data" placeholder) — those weren't
// pinned to exact legacy wording by any test and are left alone.
func emptyStateOverride(kind string, d *Data) (empty bool, msg string) {
	switch kind {
	case "stats-card", "stats-card-with-grade", "profile-summary":
		return d.Payload.TotalSeconds == 0, "No coding activity in this range yet"
	case "total-time-stat", "daily-avg-stat":
		return d.Payload.TotalSeconds == 0, "No activity yet"
	case "current-streak-stat", "longest-streak-stat", "active-days-stat":
		return len(d.Payload.DailyTotal) == 0, "No days in range yet"
	case "categories-chart":
		return len(d.Payload.Categories) == 0, "No category data yet"
	case "editors-chips":
		return len(d.Payload.Editors) == 0, "No editor data yet"
	case "platforms-chips":
		return len(d.Payload.Platforms) == 0, "No platform data yet"
	}
	return false, ""
}

// renderSpecPanel dispatches one panel to its primitive. rect defaults to
// the whole card body when the panel omits one (only valid for single-panel
// specs — see SpecPanel's doc comment).
func renderSpecPanel(f *Frame, d *Data, w, h, panelCount int, p SpecPanel) error {
	r := panelRect{}
	if p.Rect != nil {
		r = *p.Rect
	} else {
		if panelCount > 1 {
			return fmt.Errorf("widget: multi-panel spec panel %q/%q is missing an explicit rect", p.Primitive, p.Binding)
		}
		pad := 20
		r = panelRect{X: pad, Y: f.BodyTop(), W: w - 2*pad, H: h - f.BodyTop() - pad}
	}

	switch p.Primitive {
	case "bars":
		res, err := resolveResources(d, p.Binding)
		if err != nil {
			return err
		}
		emitBarsPanel(f, r, topEntries(res, panelRowCount(r.H)))
	case "chips":
		res, err := resolveResources(d, p.Binding)
		if err != nil {
			return err
		}
		EmitChips(f, r.X, r.Y, r.W, r.H, topEntries(res, 8))
	case "calendar":
		series, err := resolveSeries(d, p.Binding)
		if err != nil {
			return err
		}
		EmitCalendar(f, r.X, r.Y, r.W, r.H, d.Payload.StartDate, series)
	case "area":
		series, err := resolveSeries(d, p.Binding)
		if err != nil {
			return err
		}
		EmitAreaLine(f, r.X, r.Y, r.W, r.H, series)
	case "day-heatmap":
		res, err := resolveResources(d, p.Binding)
		if err != nil {
			return err
		}
		top := topEntries(res, 6)
		rows := make([]DayRow, 0, len(top))
		for _, e := range top {
			rows = append(rows, DayRow{Name: e.Name, Daily: e.TotalDaily})
		}
		EmitDayHeatmap(f, r.X, r.Y, r.W, r.H, d.Payload.StartDate, rows)
	case "punchcard":
		var cells []model.PunchcardCell
		if d.Punchcard != nil {
			cells = d.Punchcard.Cells
		}
		EmitPunchcard(f, r.X, r.Y, r.W, r.H, cells)
	case "momentum":
		EmitMomentum(f, r.X, r.Y, r.W, r.H, d.Momentum)
	case "grade-ring":
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		radius := r.W / 2
		if r.H/2 < radius {
			radius = r.H / 2
		}
		if radius > 42 {
			radius = 42
		}
		EmitGradeRing(f, cx, cy, radius, d.Grade)
	case "metric":
		label, value := metricLabelValue(d, p)
		EmitMetric(f, r.X, r.Y, label, value)
	case "stat-numeral":
		label, value := statNumeralLabelValue(d, p)
		EmitStatNumeral(f, r.X, r.Y, label, value)
	case "ratio":
		active, total := stats.ActiveDays(d.Payload.DailyTotal)
		EmitRatio(f, r.X, r.Y, active, total)
	case "goal-bar":
		goals, err := resolveGoals(d, p.Binding)
		if err != nil {
			return err
		}
		emitGoalBarPanel(f, r, goals, 1)
	case "goal-list":
		goals, err := resolveGoals(d, p.Binding)
		if err != nil {
			return err
		}
		emitGoalBarPanel(f, r, goals, 6)
	case "goal-ring":
		goals, err := resolveGoals(d, p.Binding)
		if err != nil {
			return err
		}
		emitGoalRingPanel(f, r, goals)
	default:
		return fmt.Errorf("widget: unknown spec primitive %q", p.Primitive)
	}
	return nil
}

// resolveResources is the binding resolver for the "bars" / "chips" /
// "day-heatmap" primitives — every binding that names a []model.ResourceStats
// segment on the payload.
func resolveResources(d *Data, binding string) ([]model.ResourceStats, error) {
	switch binding {
	case "languages":
		return d.Payload.Languages, nil
	case "projects":
		return d.Payload.Projects, nil
	case "platforms":
		return d.Payload.Platforms, nil
	case "editors":
		return d.Payload.Editors, nil
	case "categories":
		return d.Payload.Categories, nil
	case "machines":
		return d.Payload.Machines, nil
	}
	return nil, fmt.Errorf("widget: binding %q is not a resource list", binding)
}

// resolveSeries is the binding resolver for the "calendar" / "area"
// primitives — a []int64 day-aligned series.
func resolveSeries(d *Data, binding string) ([]int64, error) {
	switch binding {
	case "daily-total":
		return d.Payload.DailyTotal, nil
	case "sessions":
		// The only series a SessionsPayload carries: daily total_seconds,
		// same axis as Payload.DailyTotal — the deep-work area panel reads
		// "how deep-work time grew/shrank" day by day.
		if d.Sessions == nil {
			return nil, nil
		}
		daily := make([]int64, len(d.Sessions.Daily))
		for i, day := range d.Sessions.Daily {
			daily[i] = day.TotalSeconds
		}
		return daily, nil
	}
	return nil, fmt.Errorf("widget: binding %q is not a series", binding)
}

// resolveGoals is the binding resolver for the "goal-bar"/"goal-ring"/
// "goal-list" primitives. The only binding is "goals" — the handler has
// ALREADY privacy-filtered d.Goals down to the link owner's enabled &&
// public set (see internal/widgets.publicGoalsFor) before this package ever
// sees it, so there is no further filtering here.
func resolveGoals(d *Data, binding string) ([]GoalProgressLite, error) {
	if binding != "goals" {
		return nil, fmt.Errorf("widget: binding %q is not a goals list", binding)
	}
	return d.Goals, nil
}

// emitGoalBarPanel draws up to maxN goal rows (one EmitGoalBar call per
// row) stacked vertically within r. Shared by goal-progress (maxN=1, the
// FE's "first enabled goal" convention) and goal-list (maxN=6, capped like
// every other top-N list primitive). An empty goals slice draws the SAME
// placeholder every other empty panel uses — this is the privacy
// no-oracle: the handler already collapsed "owner has zero public goals"
// and "owner has zero goals at all" into the identical empty/nil slice, so
// this function structurally cannot (and must not) tell them apart.
func emitGoalBarPanel(f *Frame, r panelRect, goals []GoalProgressLite, maxN int) {
	if len(goals) == 0 {
		panelPlaceholder(f, r, "No goals yet")
		return
	}
	n := len(goals)
	if n > maxN {
		n = maxN
	}
	yStep := 34
	if avail := r.H / n; avail < yStep {
		yStep = avail
	}
	if yStep < 20 {
		yStep = 20
	}
	for i := 0; i < n; i++ {
		g := goals[i]
		pct := int(math.Round(g.Progress * 100))
		EmitGoalBar(f, r.X, r.Y+18+i*yStep, r.W, g.Name, pct, g.Hit)
	}
}

// emitGoalRingPanel draws up to 3 concentric EmitGoalRing rings (outer =
// first goal) centered in the top portion of r, with a small color-dot +
// name + pct legend row per goal underneath — the SVG twin of the FE
// GoalRing's stacked CircularGauges + legend list. Same empty/no-oracle
// contract as emitGoalBarPanel.
func emitGoalRingPanel(f *Frame, r panelRect, goals []GoalProgressLite) {
	if len(goals) == 0 {
		panelPlaceholder(f, r, "No goals yet")
		return
	}
	th := f.Theme
	n := len(goals)
	if n > 3 {
		n = 3
	}
	legendH := 18 * n
	ringAreaH := r.H - legendH - 10
	if ringAreaH < 60 {
		ringAreaH = 60
	}
	cx := r.X + r.W/2
	cy := r.Y + ringAreaH/2
	outer := ringAreaH / 2
	if maxW := r.W/2 - 4; outer > maxW {
		outer = maxW
	}
	if outer > 56 {
		outer = 56
	}
	radii := [3]int{outer, outer * 72 / 100, outer * 46 / 100}
	for i := 0; i < n; i++ {
		g := goals[i]
		pct := int(math.Round(g.Progress * 100))
		EmitGoalRing(f, cx, cy, radii[i], pct, g.Hit, i, g.Name)
	}
	legendTop := r.Y + ringAreaH + 16
	for i := 0; i < n; i++ {
		g := goals[i]
		pct := int(math.Round(g.Progress * 100))
		color := th.colorAt(i)
		if g.Hit {
			color = goalHitColor
		}
		y := legendTop + i*18
		f.Printf(`<circle cx="%d" cy="%d" r="4" fill="%s"/>`, r.X+8, y-4, color)
		f.Printf(`<text x="%d" y="%d" font-size="11" fill="%s">%s</text>`,
			r.X+20, y, th.Text, xmlEscape(truncate(g.Name, 22)))
		f.Printf(`<text x="%d" y="%d" font-size="11" fill="%s" text-anchor="end">%d%%</text>`,
			r.X+r.W, y, th.TextMuted, pct)
	}
}

// specLabel picks the panel's own title when set, else a fallback.
func specLabel(p SpecPanel, fallback string) string {
	if p.Title != "" {
		return p.Title
	}
	return fallback
}

// metricLabelValue resolves a "metric" panel's (label, value) pair. Scalar
// bindings read straight off the payload; "sessions" needs Field to pick
// which of the three summary numbers to show.
func metricLabelValue(d *Data, p SpecPanel) (label, value string) {
	switch p.Binding {
	case "total-seconds":
		return specLabel(p, "Total"), compound(d.Payload.TotalSeconds)
	case "daily-avg":
		return specLabel(p, "Daily avg"), compound(int64(d.Payload.DailyAvg))
	case "sessions":
		var sm model.SessionSummary
		if d.Sessions != nil {
			sm = d.Sessions.Summary
		}
		switch p.Field {
		case "median":
			return specLabel(p, "Median length"), compound(sm.MedianSeconds)
		case "longest":
			return specLabel(p, "Longest"), compound(sm.MaxSeconds)
		default: // "count" or unset
			return specLabel(p, "Sessions"), fmt.Sprintf("%d", sm.Count)
		}
	}
	return specLabel(p, p.Binding), "—"
}

// statNumeralLabelValue resolves a "stat-numeral" panel's (label, value)
// pair — the big-numeral tiles (total/daily-avg/streaks).
func statNumeralLabelValue(d *Data, p SpecPanel) (label, value string) {
	switch p.Binding {
	case "total-seconds":
		return specLabel(p, "TOTAL TIME"), compound(d.Payload.TotalSeconds)
	case "daily-avg":
		return specLabel(p, "DAILY AVG"), compound(int64(d.Payload.DailyAvg))
	case "streak-current":
		return specLabel(p, "CURRENT STREAK"), fmt.Sprintf("%dD", stats.CurrentStreak(d.Payload.DailyTotal))
	case "streak-longest":
		return specLabel(p, "LONGEST STREAK"), fmt.Sprintf("%dD", stats.LongestStreak(d.Payload.DailyTotal))
	}
	return specLabel(p, p.Binding), "—"
}
