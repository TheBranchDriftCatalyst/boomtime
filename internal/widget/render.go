// Package widget renders the public embeddable SVG stats widgets (gaka-hsj +
// gaka-unq.2). Every renderer is a pure function over a Data bundle — no DB,
// no network, no JS, no external resources (GitHub camo-safe). Chrome +
// styles are shared via Frame; each viz element is a primitive in
// primitives.go, so single-viz twins and composite (3-panel) cards use the
// same building blocks.
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
}

// Requirements declares which optional data blobs a kind consumes. The handler
// gates its DB queries on these so a badge render never fetches punchcard.
type Requirements struct {
	Grade, Punchcard, Momentum, Sessions bool
}

type renderFunc func(*Data, Theme, Options) ([]byte, error)

// kinds is the dispatch table AND the whitelist for the public :kind path
// param. The FE widget catalog (web/src/features/widgets/catalog.ts) must list
// exactly these kinds — TestKindsMatchFrontendCatalog guards the two lists
// against drift.
var kinds = map[string]struct {
	Render renderFunc
	Needs  Requirements
}{
	"stats-card":            {Render: renderStatsCard},
	"stats-card-with-grade": {Render: renderStatsCardWithGrade, Needs: Requirements{Grade: true}},
	"top-langs":             {Render: renderTopLangs},
	"top-projects":          {Render: renderTopProjects},
	"badge":                 {Render: renderBadge},
	// gaka-unq.2 — new twins + composite:
	"activity-heatmap": {Render: renderActivityHeatmap},
	"punchcard":        {Render: renderPunchcard, Needs: Requirements{Punchcard: true}},
	"momentum":         {Render: renderMomentum, Needs: Requirements{Momentum: true}},
	"profile-summary":  {Render: renderProfileSummary, Needs: Requirements{Grade: true}},
	// gaka-unq.3 — remaining chart twins (each ~30 LOC over the primitives):
	"cumulative-area":   {Render: renderCumulativeArea},
	"deep-work":         {Render: renderDeepWork, Needs: Requirements{Sessions: true}},
	"heatmap-projects":  {Render: renderHeatmapProjects},
	"heatmap-languages": {Render: renderHeatmapLanguages},
}

// Kinds returns the sorted whitelist of renderable widget kinds.
func Kinds() []string {
	out := make([]string, 0, len(kinds))
	for k := range kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// IsKind reports whether kind is renderable.
func IsKind(kind string) bool { _, ok := kinds[kind]; return ok }

// Needs reports which optional Data fields a kind wants populated — the
// handler uses this to skip expensive fetches (punchcard/momentum/sessions
// each own their own DB round-trip).
func Needs(kind string) Requirements {
	if k, ok := kinds[kind]; ok {
		return k.Needs
	}
	return Requirements{}
}

// Render dispatches to the kind's renderer.
func Render(kind string, d *Data, opts Options) ([]byte, error) {
	k, ok := kinds[kind]
	if !ok {
		return nil, fmt.Errorf("unknown widget kind %q", kind)
	}
	return k.Render(d, themeFor(opts.Theme), opts)
}

// RenderCustom dispatches the builder-composed "custom" widget (gaka-567).
// The Def is passed inline in the URL — no saved-def table for v1. Kept
// separate from Render so a caller can't mint a custom widget without
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

// ---- renderers: single-viz cards ----

func renderStatsCard(d *Data, th Theme, opts Options) ([]byte, error) {
	title := defaultString(opts.Title, "Coding Stats")
	f := OpenFrame(495, 195, th, title, opts.Subtitle)
	if d.Payload.TotalSeconds == 0 {
		f.Empty("No coding activity in this range yet")
		return f.Close(), nil
	}
	// Left column: total + daily avg.
	EmitMetric(f, 25, 78, "Total", compound(d.Payload.TotalSeconds))
	EmitMetric(f, 25, 118, "Daily avg", compound(int64(d.Payload.DailyAvg)))
	// Right column: top-5 languages bars.
	barWMax := 190
	if d.Grade != nil {
		barWMax = 150
	}
	EmitBars(f, topEntries(d.Payload.Languages, 5), BarsOpts{
		X: 150, Y: 82, YStep: 18, BarX: 260, BarWMax: barWMax, IncludeValueText: true,
	})
	// Grade circle (top-right).
	EmitGradeRing(f, 425, 60, 40, d.Grade)
	return f.Close(), nil
}

func renderStatsCardWithGrade(d *Data, th Theme, opts Options) ([]byte, error) {
	if d.Grade == nil {
		g := stats.Grade(d.Payload)
		d = &Data{Payload: d.Payload, Grade: &g}
	}
	return renderStatsCard(d, th, opts)
}

func renderTopLangs(d *Data, th Theme, opts Options) ([]byte, error) {
	return renderTopList(d.Payload.Languages, defaultString(opts.Title, "Top Languages"), th, opts)
}

func renderTopProjects(d *Data, th Theme, opts Options) ([]byte, error) {
	return renderTopList(d.Payload.Projects, defaultString(opts.Title, "Top Projects"), th, opts)
}

func renderTopList(list []model.ResourceStats, title string, th Theme, opts Options) ([]byte, error) {
	f := OpenFrame(300, 200, th, title, opts.Subtitle)
	entries := topEntries(list, 6)
	if len(entries) == 0 {
		f.Empty("No data in this range yet")
		return f.Close(), nil
	}
	EmitBars(f, entries, BarsOpts{
		X: 20, Y: 62, YStep: 22, BarX: 130, BarWMax: 120, IncludeValueText: true,
	})
	return f.Close(), nil
}

func renderActivityHeatmap(d *Data, th Theme, opts Options) ([]byte, error) {
	f := OpenFrame(720, 170, th,
		defaultString(opts.Title, "Coding activity"),
		opts.Subtitle)
	if len(d.Payload.DailyTotal) == 0 {
		f.Empty("No activity in this range yet")
		return f.Close(), nil
	}
	EmitCalendar(f, 20, 55, 680, 100, d.Payload.StartDate, d.Payload.DailyTotal)
	return f.Close(), nil
}

func renderPunchcard(d *Data, th Theme, opts Options) ([]byte, error) {
	f := OpenFrame(560, 220, th,
		defaultString(opts.Title, "Coding punchcard"),
		opts.Subtitle)
	if d.Punchcard == nil || len(d.Punchcard.Cells) == 0 {
		f.Empty("No punchcard data yet")
		return f.Close(), nil
	}
	EmitPunchcard(f, 20, 55, 520, 150, d.Punchcard.Cells)
	return f.Close(), nil
}

func renderMomentum(d *Data, th Theme, opts Options) ([]byte, error) {
	f := OpenFrame(560, 220, th,
		defaultString(opts.Title, "Project momentum"),
		opts.Subtitle)
	if d.Momentum == nil || len(d.Momentum.Projects) == 0 {
		f.Empty("No momentum data yet")
		return f.Close(), nil
	}
	EmitMomentum(f, 20, 55, 520, 150, d.Momentum)
	return f.Close(), nil
}

// ---- renderer: composite 3-panel card (gaka-unq.2) ----

// renderProfileSummary is the "3-graphs-in-a-card" composite — the profile-
// summary-cards direction. Panel 1: contribution calendar. Panel 2: top
// languages. Panel 3: grade + total-time summary. Each panel calls the same
// primitives the single-viz cards do — that's the whole point of the DRY
// refactor: adding a new composite means picking panels + coordinates.
func renderProfileSummary(d *Data, th Theme, opts Options) ([]byte, error) {
	f := OpenFrame(820, 240, th,
		defaultString(opts.Title, "Coding profile"),
		opts.Subtitle)
	if d.Payload.TotalSeconds == 0 {
		f.Empty("No coding activity in this range yet")
		return f.Close(), nil
	}
	// Panel 1: contribution calendar (left, 380 wide).
	EmitCalendar(f, 20, 60, 360, 160, d.Payload.StartDate, d.Payload.DailyTotal)
	// Divider.
	f.Printf(`<rect x="400" y="55" width="1" height="170" fill="%s"/>`, th.Border)
	// Panel 2: top-langs (middle, 250 wide).
	EmitBars(f, topEntries(d.Payload.Languages, 5), BarsOpts{
		X: 420, Y: 78, YStep: 18, BarX: 520, BarWMax: 130, IncludeValueText: true,
	})
	// Divider.
	f.Printf(`<rect x="680" y="55" width="1" height="170" fill="%s"/>`, th.Border)
	// Panel 3: grade + total (right).
	EmitGradeRing(f, 750, 100, 42, d.Grade)
	EmitMetric(f, 700, 160, "Total", compound(d.Payload.TotalSeconds))
	EmitMetric(f, 700, 195, "Daily avg", compound(int64(d.Payload.DailyAvg)))
	return f.Close(), nil
}

// ---- gaka-unq.3 twins ----

// renderCumulativeArea — filled-area line of accumulating coding time. Reads
// "how the total grew day-by-day" at a glance. Uses only StatsPayload.
func renderCumulativeArea(d *Data, th Theme, opts Options) ([]byte, error) {
	f := OpenFrame(560, 200, th,
		defaultString(opts.Title, "Cumulative coding time"),
		opts.Subtitle)
	if len(d.Payload.DailyTotal) < 2 {
		f.Empty("Not enough days in this range yet")
		return f.Close(), nil
	}
	EmitAreaLine(f, 20, 60, 520, 120, d.Payload.DailyTotal)
	// Also show the final cumulative in the corner as a strong metric label.
	var total int64
	for _, v := range d.Payload.DailyTotal {
		total += v
	}
	EmitMetric(f, 470, 55, "Total", compound(total))
	return f.Close(), nil
}

// renderDeepWork — session summary card: count + median + longest session,
// plus a mini area-line of the daily total_seconds series so the shape of
// deep-work time is visible. Consumes SessionsPayload.
func renderDeepWork(d *Data, th Theme, opts Options) ([]byte, error) {
	f := OpenFrame(495, 195, th,
		defaultString(opts.Title, "Deep-work sessions"),
		opts.Subtitle)
	if d.Sessions == nil || d.Sessions.Summary.Count == 0 {
		f.Empty("No sessions in this range yet")
		return f.Close(), nil
	}
	sm := d.Sessions.Summary
	EmitMetric(f, 25, 78, "Sessions", fmt.Sprintf("%d", sm.Count))
	EmitMetric(f, 25, 118, "Median length", compound(sm.MedianSeconds))
	EmitMetric(f, 25, 158, "Longest", compound(sm.MaxSeconds))
	// Right side: daily totals area line so growth-vs-decay reads at a glance.
	daily := make([]int64, len(d.Sessions.Daily))
	for i, day := range d.Sessions.Daily {
		daily[i] = day.TotalSeconds
	}
	EmitAreaLine(f, 190, 60, 285, 125, daily)
	return f.Close(), nil
}

// renderHeatmapProjects / renderHeatmapLanguages — day×resource intensity
// grid. Rows are the top-6 by TotalSeconds (dropping the synthesized "Other"
// bucket); columns are days aligned to StartDate. Same primitive powers both.
func renderHeatmapProjects(d *Data, th Theme, opts Options) ([]byte, error) {
	return renderDayHeatmap(d, th, opts, d.Payload.Projects,
		defaultString(opts.Title, "Activity per project"))
}

func renderHeatmapLanguages(d *Data, th Theme, opts Options) ([]byte, error) {
	return renderDayHeatmap(d, th, opts, d.Payload.Languages,
		defaultString(opts.Title, "Activity per language"))
}

func renderDayHeatmap(d *Data, th Theme, opts Options, list []model.ResourceStats, title string) ([]byte, error) {
	f := OpenFrame(720, 240, th, title, opts.Subtitle)
	top := topEntries(list, 6)
	if len(top) == 0 || len(d.Payload.DailyTotal) == 0 {
		f.Empty("No activity in this range yet")
		return f.Close(), nil
	}
	rows := make([]DayRow, 0, len(top))
	for _, e := range top {
		// ResourceStats.TotalDaily is the per-day series aligned to
		// StatsPayload.StartDate — same axis the calendar twin uses.
		rows = append(rows, DayRow{Name: e.Name, Daily: e.TotalDaily})
	}
	EmitDayHeatmap(f, 20, 55, 680, 170, d.Payload.StartDate, rows)
	return f.Close(), nil
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

func defaultString(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
