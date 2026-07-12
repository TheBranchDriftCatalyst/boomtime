// Package widget renders the public embeddable SVG stats widgets (gaka-hsj).
// Every renderer is a pure function over a model.StatsPayload — no DB, no
// network, no JS, no external resources (GitHub camo-safe). All user strings
// (project/language names, titles) are XML-escaped in the view-model builders;
// the templates only ever see pre-escaped, pre-measured values.
package widget

import (
	"bytes"
	"embed"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
)

//go:embed templates/*.svg.tmpl
var templateFS embed.FS

var tmpl = template.Must(template.ParseFS(templateFS, "templates/*.svg.tmpl"))

// Options are the per-request render knobs (from URL params on the public
// endpoint). Title overrides the kind's default headline; Subtitle is the
// range hint ("last 30 days").
type Options struct {
	Theme    string
	Title    string
	Subtitle string
}

type renderFunc func(p *model.StatsPayload, g *stats.GradeResult, th Theme, opts Options) ([]byte, error)

// kinds is the dispatch table AND the whitelist for the public :kind path
// param. The FE widget catalog (web/src/features/widgets/catalog.ts) must list
// exactly these kinds — a drift-guard test asserts the set.
var kinds = map[string]renderFunc{
	"stats-card": func(p *model.StatsPayload, _ *stats.GradeResult, th Theme, o Options) ([]byte, error) {
		return renderStatsCard(p, nil, th, o)
	},
	"stats-card-with-grade": renderStatsCardWithGrade,
	"top-langs": func(p *model.StatsPayload, _ *stats.GradeResult, th Theme, o Options) ([]byte, error) {
		return renderTopList(p.Languages, "Top Languages", th, o)
	},
	"top-projects": func(p *model.StatsPayload, _ *stats.GradeResult, th Theme, o Options) ([]byte, error) {
		return renderTopList(p.Projects, "Top Projects", th, o)
	},
	"badge": renderBadge,
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

// NeedsGrade reports whether the kind consumes a stats.GradeResult (lets the
// handler skip the computation otherwise).
func NeedsGrade(kind string) bool { return kind == "stats-card-with-grade" }

// Render dispatches to the kind's renderer. Unknown kinds are the caller's
// responsibility to reject first (IsKind) — this returns an error defensively.
func Render(kind string, p *model.StatsPayload, g *stats.GradeResult, opts Options) ([]byte, error) {
	fn, ok := kinds[kind]
	if !ok {
		return nil, fmt.Errorf("unknown widget kind %q", kind)
	}
	return fn(p, g, themeFor(opts.Theme), opts)
}

// ---- shared view-model pieces ----

// xmlEscape escapes every XML metacharacter. ALL user-controlled strings pass
// through here before entering a template.
var xmlEscaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;",
)

func xmlEscape(s string) string { return xmlEscaper.Replace(s) }

// truncate cuts s to at most n runes, appending an ellipsis when cut. Escape
// AFTER truncation so an entity is never sliced in half.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// barRow is one pre-measured label/bar/value line.
type barRow struct {
	Y       int
	Label   string // escaped
	Value   string // escaped
	Tooltip string // escaped; native <title> hover (direct view / <object> embeds)
	BarX    int
	BarW    int
	BarWMax int
	Color   string
	// Delay staggers each row's entrance animation ("0.45s").
	Delay string
}

// topEntries sorts a (possibly capped) resource list by seconds desc, drops
// the synthesized "Other (N more)" row (identified by OtherCount — real rows
// never carry it), and returns up to n entries.
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

func compound(seconds int64) string { return stats.CompoundDuration(&seconds) }

// buildBarRows lays out rows for a top-list: label column, proportional bar,
// pct column. Bars scale against the largest shown entry. Percentages are
// normalized over the SHOWN set (they sum to ~100 across the card) — a
// top-6-of-12 list showing payload-global pcts would confusingly sum to ~70.
func buildBarRows(entries []model.ResourceStats, th Theme, yStart, yStep, barX, barWMax int) []barRow {
	var maxSecs, shownTotal int64
	for _, e := range entries {
		shownTotal += e.TotalSeconds
		if e.TotalSeconds > maxSecs {
			maxSecs = e.TotalSeconds
		}
	}
	if maxSecs < 1 {
		maxSecs = 1
	}
	if shownTotal < 1 {
		shownTotal = 1
	}
	rows := make([]barRow, 0, len(entries))
	for i, e := range entries {
		w := int(float64(barWMax) * float64(e.TotalSeconds) / float64(maxSecs))
		if w < 2 {
			w = 2
		}
		pct := float64(e.TotalSeconds) / float64(shownTotal) * 100
		rows = append(rows, barRow{
			Y:       yStart + i*yStep,
			Label:   xmlEscape(truncate(e.Name, 18)),
			Value:   xmlEscape(fmt.Sprintf("%.1f%%", pct)),
			Tooltip: xmlEscape(fmt.Sprintf("%s — %s (%.1f%%)", e.Name, compound(e.TotalSeconds), pct)),
			BarX:    barX,
			BarW:    w,
			BarWMax: barWMax,
			Color:   th.colorAt(i),
			Delay:   fmt.Sprintf("%.2fs", 0.15*float64(i)),
		})
	}
	return rows
}

// ---- stats card ----

type gradeVM struct {
	Level         string
	CX, CY, R     int
	Circumference float64
	// DashFill is the stroke-dasharray fill length: ring fill % = 100-percentile
	// (lower percentile = better = fuller ring), matching github-readme-stats.
	DashFill float64
}

type statsCardVM struct {
	Width, Height int
	Theme         Theme
	Title         string // escaped
	Subtitle      string // escaped
	TotalLabel    string // escaped
	AvgLabel      string // escaped
	Rows          []barRow
	Grade         *gradeVM
	Empty         bool
}

func renderStatsCard(p *model.StatsPayload, g *stats.GradeResult, th Theme, opts Options) ([]byte, error) {
	title := opts.Title
	if title == "" {
		title = "Coding Stats"
	}
	vm := statsCardVM{
		Width: 495, Height: 195, Theme: th,
		Title:    xmlEscape(truncate(title, 30)),
		Subtitle: xmlEscape(truncate(opts.Subtitle, 30)),
		Empty:    p.TotalSeconds == 0,
	}
	if !vm.Empty {
		avg := int64(p.DailyAvg)
		vm.TotalLabel = xmlEscape("Total: " + compound(p.TotalSeconds))
		vm.AvgLabel = xmlEscape("Daily avg: " + compound(avg))
		barWMax := 190
		if g != nil {
			barWMax = 150 // leave room for the grade ring
		}
		vm.Rows = buildBarRows(topEntries(p.Languages, 5), th, 100, 18, 150, barWMax)
	}
	if g != nil {
		const r = 40
		circ := 2 * 3.14159265 * r
		vm.Grade = &gradeVM{
			Level: xmlEscape(g.Level),
			CX:    vm.Width - 70, CY: 60, R: r,
			Circumference: circ,
			DashFill:      circ * (100 - g.Percentile) / 100,
		}
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "stats_card.svg.tmpl", vm); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func renderStatsCardWithGrade(p *model.StatsPayload, g *stats.GradeResult, th Theme, opts Options) ([]byte, error) {
	if g == nil {
		computed := stats.Grade(p)
		g = &computed
	}
	return renderStatsCard(p, g, th, opts)
}

// ---- top list (top-langs / top-projects) ----

type topListVM struct {
	Width, Height int
	Theme         Theme
	Title         string // escaped
	Subtitle      string // escaped
	Rows          []barRow
	Empty         bool
}

func renderTopList(list []model.ResourceStats, defaultTitle string, th Theme, opts Options) ([]byte, error) {
	title := opts.Title
	if title == "" {
		title = defaultTitle
	}
	entries := topEntries(list, 6)
	vm := topListVM{
		Width: 300, Height: 200, Theme: th,
		Title:    xmlEscape(truncate(title, 24)),
		Subtitle: xmlEscape(truncate(opts.Subtitle, 24)),
		Empty:    len(entries) == 0,
		Rows:     buildBarRows(entries, th, 62, 22, 120, 120),
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "top_list.svg.tmpl", vm); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// ---- badge (native flat pill; the shields.io proxy at /badge/svg stays) ----

type badgeVM struct {
	Width, LabelW, MsgW int
	Label, Msg          string // escaped
	LabelX, MsgX        int
	Theme               Theme
}

// badgeCharW approximates Verdana/DejaVu 11px average character width.
const badgeCharW = 6.6

func renderBadge(p *model.StatsPayload, _ *stats.GradeResult, th Theme, opts Options) ([]byte, error) {
	label := opts.Title
	if label == "" {
		label = "boomtime"
	}
	label = truncate(label, 24)
	msg := compound(p.TotalSeconds)
	labelW := int(badgeCharW*float64(len([]rune(label)))) + 20
	msgW := int(badgeCharW*float64(len([]rune(msg)))) + 20
	vm := badgeVM{
		Width: labelW + msgW, LabelW: labelW, MsgW: msgW,
		Label:  xmlEscape(label),
		Msg:    xmlEscape(msg),
		LabelX: labelW / 2, MsgX: labelW + msgW/2,
		Theme: th,
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "badge.svg.tmpl", vm); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
