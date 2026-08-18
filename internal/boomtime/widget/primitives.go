// primitives.go: the viz building blocks. Each emit* function draws itself
// into a Frame at a caller-provided origin and size — no assumptions about
// what's around it. Composite widgets get 3-panels-in-a-card for free: just
// call several emit* with different origins. All user strings are xmlEscape'd
// inside the emitters so callers can pass raw ResourceStats names.
package widget

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// ---- top-list bars (used by stats-card body + top-langs / top-projects) ----

// BarsOpts controls the bar-list emitter. x/y is the top-left of the label
// column; barX starts the bar track; barW is the max bar length.
type BarsOpts struct {
	X, Y, YStep      int
	BarX, BarWMax    int
	LabelChars       int  // truncate labels to this many runes
	IncludeValueText bool // append "N.N%" at the end of each bar
}

// EmitBars draws one label + track + fill + pct-value row per entry, animated,
// with a hover-brighten and a native <title> tooltip carrying the exact
// duration. Percentages are renormalized over the SHOWN set (sum to ~100).
func EmitBars(f *Frame, entries []model.ResourceStats, opts BarsOpts) {
	if opts.LabelChars == 0 {
		opts.LabelChars = 18
	}
	th := f.Theme
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
	for i, e := range entries {
		w := int(float64(opts.BarWMax) * float64(e.TotalSeconds) / float64(maxSecs))
		if w < 2 {
			w = 2
		}
		pct := float64(e.TotalSeconds) / float64(shownTotal) * 100
		y := opts.Y + i*opts.YStep
		delay := fmt.Sprintf("%.2fs", 0.15*float64(i))
		f.Printf(`<g class="row" style="animation-delay: %s"><title>%s</title>`,
			delay, xmlEscape(fmt.Sprintf("%s — %s (%.1f%%)", e.Name, compound(e.TotalSeconds), pct)))
		f.Printf(`<text x="%d" y="%d" font-size="11" fill="%s" dominant-baseline="middle">%s</text>`,
			opts.X, y, th.Text, xmlEscape(truncate(e.Name, opts.LabelChars)))
		f.Printf(`<rect x="%d" y="%d" width="%d" height="8" rx="4" fill="%s" transform="translate(0,-4)"/>`,
			opts.BarX, y, opts.BarWMax, th.TrackBg)
		f.Printf(`<rect class="bar-fill" x="%d" y="%d" width="%d" height="8" rx="4" fill="%s" transform="translate(0,-4)" style="animation-delay: %s"/>`,
			opts.BarX, y, w, th.colorAt(i), delay)
		if opts.IncludeValueText {
			f.Printf(`<text x="%d" y="%d" font-size="10" fill="%s" dominant-baseline="middle" transform="translate(8,0)">%.1f%%</text>`,
				opts.BarX+opts.BarWMax, y, th.TextMuted, pct)
		}
		f.WriteString(`</g>`)
	}
}

// ---- grade ring (github-readme-stats' letter grade) ----

// EmitGradeRing draws a circle-shaped grade indicator centered at (cx, cy),
// with a partial ring filled proportionally to (100-percentile). The letter
// sits at the center. A <title> tooltip carries the level + percentile.
func EmitGradeRing(f *Frame, cx, cy, r int, g *stats.GradeResult) {
	if g == nil {
		return
	}
	th := f.Theme
	circ := 2 * math.Pi * float64(r)
	fill := circ * (100 - g.Percentile) / 100
	f.Printf(`<g class="fade" style="animation-delay: 0.3s"><title>%s</title>`,
		xmlEscape(fmt.Sprintf("Grade %s — %.1fth percentile (lower is better)", g.Level, g.Percentile)))
	f.Printf(`<circle cx="%d" cy="%d" r="%d" stroke="%s" stroke-width="6" fill="none"/>`,
		cx, cy, r, th.TrackBg)
	f.Printf(`<circle class="ring" cx="%d" cy="%d" r="%d" stroke="%s" stroke-width="6" fill="none" stroke-linecap="round" stroke-dasharray="%.2f %.2f" transform="rotate(-90 %d %d)"/>`,
		cx, cy, r, th.Title, fill, circ, cx, cy)
	f.Printf(`<text x="%d" y="%d" font-size="24" font-weight="700" fill="%s" text-anchor="middle" dominant-baseline="central">%s</text>`,
		cx, cy, th.Text, xmlEscape(g.Level))
	f.WriteString(`</g>`)
}

// ---- hero (social-card identity banner) ----

// EmitHero draws the big identity banner at the top of the social-card OG
// image: a "@username" headline in the title accent + an optional tagline
// line beneath it in muted text. x/y is the top-left of the block; the
// username baseline sits at y+52, the tagline at y+86. Both strings are
// xmlEscape'd here. Empty username falls back to "boomtime" so a stray render
// without Identity still produces a branded card rather than a blank hero.
func EmitHero(f *Frame, x, y, w int, username, tagline string) {
	th := f.Theme
	name := strings.TrimSpace(username)
	if name == "" {
		name = "boomtime"
	}
	f.Printf(`<g class="fade">`)
	f.Printf(`<text x="%d" y="%d" font-size="60" font-weight="700" fill="%s">%s</text>`,
		x, y+58, th.Title, xmlEscape("@"+truncate(name, 22)))
	line := strings.TrimSpace(tagline)
	if line == "" {
		line = "coding activity"
	}
	f.Printf(`<text x="%d" y="%d" font-size="21" fill="%s">%s</text>`,
		x, y+94, th.TextMuted, xmlEscape(truncate(line, 52)))
	f.WriteString(`</g>`)
}

// ---- metric row (two-line label + value) ----

// EmitMetric writes a small "LABEL: value" pair — used as summary stats on
// composites where a full StatCard would be overkill.
func EmitMetric(f *Frame, x, y int, label, value string) {
	f.Printf(`<text class="fade" x="%d" y="%d" font-size="12" fill="%s">%s</text>`,
		x, y, f.Theme.TextMuted, xmlEscape(label))
	f.Printf(`<text class="fade" x="%d" y="%d" font-size="14" font-weight="600" fill="%s" style="animation-delay: 0.1s">%s</text>`,
		x, y+16, f.Theme.Text, xmlEscape(value))
}

// ---- contribution calendar (GitHub-style year-of-day cells) ----

// EmitCalendar draws a per-day activity heatmap within (x, y, w, h). Cells are
// bucketed into 5 intensity levels; each carries a native <title> tooltip
// with the date and duration. `daily` is aligned to a startDate.
func EmitCalendar(f *Frame, x, y, w, h int, startDate time.Time, daily []int64) {
	if len(daily) == 0 {
		f.Printf(`<text x="%d" y="%d" font-size="11" fill="%s">No days in range</text>`,
			x, y+h/2, f.Theme.TextMuted)
		return
	}
	th := f.Theme
	// Align start to the Sunday-of-week for clean columns.
	off := int(startDate.Weekday())
	rows := 7
	cols := (len(daily) + off + rows - 1) / rows
	if cols < 1 {
		cols = 1
	}
	// Fit cells + gap into the box.
	gap := 2
	cell := (h - (rows-1)*gap) / rows
	if maxCellW := (w - (cols-1)*gap) / cols; maxCellW < cell {
		cell = maxCellW
	}
	if cell < 3 {
		cell = 3
	}
	// Intensity buckets: 0 = empty; 1..4 = quartiles of nonzero values.
	var mx int64
	for _, v := range daily {
		if v > mx {
			mx = v
		}
	}
	bucket := func(v int64) int {
		if v <= 0 || mx == 0 {
			return 0
		}
		q := float64(v) / float64(mx)
		switch {
		case q > 0.75:
			return 4
		case q > 0.5:
			return 3
		case q > 0.25:
			return 2
		default:
			return 1
		}
	}
	levels := []string{th.TrackBg, mixHex(th.TrackBg, th.Accent, 0.25), mixHex(th.TrackBg, th.Accent, 0.5), mixHex(th.TrackBg, th.Accent, 0.75), th.Accent}
	f.WriteString(`<g class="fade" style="animation-delay: 0.2s">`)
	for i, v := range daily {
		idx := i + off
		col := idx / rows
		row := idx % rows
		cx := x + col*(cell+gap)
		cy := y + row*(cell+gap)
		day := startDate.AddDate(0, 0, i)
		tip := fmt.Sprintf("%s — %s", day.Format("Mon 2 Jan 2006"), compound(v))
		f.Printf(`<rect class="cell" x="%d" y="%d" width="%d" height="%d" rx="1.5" fill="%s"><title>%s</title></rect>`,
			cx, cy, cell, cell, levels[bucket(v)], xmlEscape(tip))
	}
	f.WriteString(`</g>`)
}

// ---- punchcard (7 dow × 24 hour) ----

// EmitPunchcard draws the dow-x-hour intensity grid. Cells are sized to fit
// (w, h). dowLabels shown at the left; hour ticks at the bottom.
func EmitPunchcard(f *Frame, x, y, w, h int, cells []model.PunchcardCell) {
	th := f.Theme
	const rows = 7
	const cols = 24
	labelW := 30
	tickH := 12
	gap := 2
	gridW := w - labelW
	gridH := h - tickH
	cellW := (gridW - (cols-1)*gap) / cols
	cellH := (gridH - (rows-1)*gap) / rows
	if cellW < 3 {
		cellW = 3
	}
	if cellH < 3 {
		cellH = 3
	}
	if len(cells) == 0 {
		f.Printf(`<text x="%d" y="%d" font-size="11" fill="%s">No punchcard data</text>`,
			x, y+h/2, th.TextMuted)
		return
	}
	// Lookup: (dow*24 + hour) -> seconds.
	byKey := make(map[int]int64, len(cells))
	var mx int64
	for _, c := range cells {
		byKey[c.Dow*24+c.Hour] += c.Seconds
		if s := byKey[c.Dow*24+c.Hour]; s > mx {
			mx = s
		}
	}
	if mx == 0 {
		mx = 1
	}
	dowLabels := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	f.WriteString(`<g class="fade" style="animation-delay: 0.2s">`)
	for r := 0; r < rows; r++ {
		f.Printf(`<text x="%d" y="%d" font-size="10" fill="%s" dominant-baseline="middle">%s</text>`,
			x, y+r*(cellH+gap)+cellH/2, th.TextMuted, dowLabels[r])
	}
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			v := byKey[r*24+c]
			q := float64(v) / float64(mx)
			// Mix track→accent by intensity.
			fill := mixHex(th.TrackBg, th.Accent, q)
			cx := x + labelW + c*(cellW+gap)
			cy := y + r*(cellH+gap)
			f.Printf(`<rect class="cell" x="%d" y="%d" width="%d" height="%d" rx="1.5" fill="%s"><title>%s %02d:00 — %s</title></rect>`,
				cx, cy, cellW, cellH, fill, dowLabels[r], c, xmlEscape(compound(v)))
		}
	}
	// Hour ticks every 6 hours.
	for c := 0; c <= cols; c += 6 {
		tx := x + labelW + c*(cellW+gap)
		f.Printf(`<text x="%d" y="%d" font-size="9" fill="%s" text-anchor="middle">%02dh</text>`,
			tx, y+gridH+10, th.TextMuted, c%24)
	}
	f.WriteString(`</g>`)
}

// ---- momentum (weeks x projects mini-heatmap) ----

// EmitMomentum draws a projects×weeks grid: rows are projects (top ~6), cols
// are weeks. Cell intensity = week-total seconds normalized against the
// project's own peak. Each cell carries a <title> with project + week + total.
func EmitMomentum(f *Frame, x, y, w, h int, m *model.MomentumPayload) {
	th := f.Theme
	if m == nil || len(m.Projects) == 0 || len(m.Weeks) == 0 {
		f.Printf(`<text x="%d" y="%d" font-size="11" fill="%s">No momentum data</text>`,
			x, y+h/2, th.TextMuted)
		return
	}
	rows := len(m.Projects)
	if rows > 6 {
		rows = 6
	}
	cols := len(m.Weeks)
	labelW := 84
	gap := 2
	gridW := w - labelW
	cellW := (gridW - (cols-1)*gap) / cols
	cellH := (h - (rows-1)*gap) / rows
	if cellW < 3 {
		cellW = 3
	}
	if cellH < 6 {
		cellH = 6
	}
	f.WriteString(`<g class="fade" style="animation-delay: 0.2s">`)
	for r := 0; r < rows; r++ {
		p := m.Projects[r]
		f.Printf(`<text x="%d" y="%d" font-size="10" fill="%s" dominant-baseline="middle">%s</text>`,
			x, y+r*(cellH+gap)+cellH/2, th.Text, xmlEscape(truncate(p.Name, 12)))
		// project-local max so a busy project doesn't wash out a small one
		var mx int64
		for _, v := range p.Weekly {
			if v > mx {
				mx = v
			}
		}
		if mx == 0 {
			mx = 1
		}
		for c := 0; c < cols && c < len(p.Weekly); c++ {
			v := p.Weekly[c]
			q := float64(v) / float64(mx)
			fill := mixHex(th.TrackBg, th.colorAt(r), q)
			cx := x + labelW + c*(cellW+gap)
			cy := y + r*(cellH+gap)
			f.Printf(`<rect class="cell" x="%d" y="%d" width="%d" height="%d" rx="1.5" fill="%s"><title>%s — %s: %s</title></rect>`,
				cx, cy, cellW, cellH, fill, xmlEscape(p.Name), xmlEscape(m.Weeks[c]), xmlEscape(compound(v)))
		}
	}
	f.WriteString(`</g>`)
}

// ---- area line (cumulative-area, sparkline-style) ----

// EmitAreaLine draws a monotonically-non-decreasing cumulative series as a
// filled area under a stroked line, fitting the (x, y, w, h) box. Reads as
// "how the total grew" at a glance — the shape of a coder's momentum.
func EmitAreaLine(f *Frame, x, y, w, h int, values []int64) {
	th := f.Theme
	if len(values) < 2 {
		f.Printf(`<text x="%d" y="%d" font-size="11" fill="%s">Not enough data</text>`,
			x, y+h/2, th.TextMuted)
		return
	}
	cum := make([]int64, len(values))
	var s int64
	for i, v := range values {
		s += v
		cum[i] = s
	}
	mx := cum[len(cum)-1]
	if mx == 0 {
		f.Printf(`<text x="%d" y="%d" font-size="11" fill="%s">No activity yet</text>`,
			x, y+h/2, th.TextMuted)
		return
	}
	nSpan := len(cum) - 1
	pt := func(i int, v int64) (int, int) {
		px := x + i*w/nSpan
		py := y + h - int(float64(v)/float64(mx)*float64(h-4))
		return px, py
	}
	var pathLine, pathArea strings.Builder
	startX, startY := pt(0, cum[0])
	fmt.Fprintf(&pathLine, "M %d %d", startX, startY)
	fmt.Fprintf(&pathArea, "M %d %d L %d %d", startX, y+h, startX, startY)
	for i := 1; i < len(cum); i++ {
		px, py := pt(i, cum[i])
		fmt.Fprintf(&pathLine, " L %d %d", px, py)
		fmt.Fprintf(&pathArea, " L %d %d", px, py)
	}
	endX := x + w
	fmt.Fprintf(&pathArea, " L %d %d Z", endX, y+h)
	f.Printf(`<g class="fade" style="animation-delay: 0.2s"><title>Cumulative — %s total</title>`,
		xmlEscape(compound(mx)))
	f.Printf(`<path d="%s" fill="%s" opacity="0.35"/>`, pathArea.String(), th.Accent)
	f.Printf(`<path d="%s" fill="none" stroke="%s" stroke-width="2"/>`, pathLine.String(), th.Accent)
	f.WriteString(`</g>`)
}

// ---- day × row heatmap (heatmap-projects / heatmap-languages) ----

// DayRow is one row of the day×row heatmap: a label + per-day-of-range
// seconds aligned to a shared date axis. Callers hand a resource's Name +
// TotalDaily.
type DayRow struct {
	Name  string
	Daily []int64
}

// EmitDayHeatmap draws rows × days of intensity cells within (x, y, w, h),
// with per-row labels at the left. Each row is scaled against its OWN peak
// so a busy row doesn't wash out a quiet one — same convention EmitMomentum
// uses. Cells carry <title> hover with row + day label + duration.
func EmitDayHeatmap(f *Frame, x, y, w, h int, startDate time.Time, rows []DayRow) {
	th := f.Theme
	if len(rows) == 0 || len(rows[0].Daily) == 0 {
		f.Printf(`<text x="%d" y="%d" font-size="11" fill="%s">No data</text>`,
			x, y+h/2, th.TextMuted)
		return
	}
	labelW := 90
	gap := 2
	cols := len(rows[0].Daily)
	gridW := w - labelW
	cellW := (gridW - (cols-1)*gap) / cols
	cellH := (h - (len(rows)-1)*gap) / len(rows)
	if cellW < 2 {
		cellW = 2
	}
	if cellH < 8 {
		cellH = 8
	}
	f.WriteString(`<g class="fade" style="animation-delay: 0.2s">`)
	for r, row := range rows {
		f.Printf(`<text x="%d" y="%d" font-size="10" fill="%s" dominant-baseline="middle">%s</text>`,
			x, y+r*(cellH+gap)+cellH/2, th.Text, xmlEscape(truncate(row.Name, 14)))
		var mx int64
		for _, v := range row.Daily {
			if v > mx {
				mx = v
			}
		}
		if mx == 0 {
			mx = 1
		}
		for c := 0; c < cols && c < len(row.Daily); c++ {
			v := row.Daily[c]
			q := float64(v) / float64(mx)
			fill := mixHex(th.TrackBg, th.colorAt(r), q)
			cx := x + labelW + c*(cellW+gap)
			cy := y + r*(cellH+gap)
			day := startDate.AddDate(0, 0, c)
			f.Printf(`<rect class="cell" x="%d" y="%d" width="%d" height="%d" rx="1.5" fill="%s"><title>%s — %s: %s</title></rect>`,
				cx, cy, cellW, cellH, fill, xmlEscape(row.Name), day.Format("2 Jan"), xmlEscape(compound(v)))
		}
	}
	f.WriteString(`</g>`)
}

// ---- stat numeral (Part B Stage 1 — SVG twin of the FE BigStat tile) ----

// EmitStatNumeral draws a small uppercase tracking label over a large bold
// value — the big-numeral style of the FE dossier stat tiles
// (WidgetRenderer.tsx BigStat). x/y anchor the label baseline; the value
// baseline sits 40px below. A <title> tooltip carries "label — value".
func EmitStatNumeral(f *Frame, x, y int, label, value string) {
	th := f.Theme
	f.Printf(`<g class="fade"><title>%s</title>`,
		xmlEscape(fmt.Sprintf("%s — %s", label, value)))
	f.Printf(`<text x="%d" y="%d" font-size="10" letter-spacing="2" fill="%s">%s</text>`,
		x, y, th.TextMuted, xmlEscape(strings.ToUpper(label)))
	f.Printf(`<text class="fade" x="%d" y="%d" font-size="34" font-weight="700" fill="%s" style="animation-delay: 0.1s">%s</text>`,
		x, y+40, th.Title, xmlEscape(value))
	f.WriteString(`</g>`)
}

// ---- ratio (active-days-stat: "N/M" + pct) ----

// EmitRatio draws a big "active/total" numeral with a muted percentage line
// under it — the SVG twin of the FE active-days-stat tile (value `N/M`,
// sub `NN%`). Percentage rounds like the FE (Math.round(active/total*100)).
func EmitRatio(f *Frame, x, y int, active, total int) {
	th := f.Theme
	denom := total
	if denom < 1 {
		denom = 1 // FE mirrors: `data.dailyTotal.length || 1`
	}
	pct := int(math.Round(float64(active) / float64(denom) * 100))
	f.Printf(`<g class="fade"><title>%s</title>`,
		xmlEscape(fmt.Sprintf("%d of %d days active (%d%%)", active, total, pct)))
	f.Printf(`<text x="%d" y="%d" font-size="34" font-weight="700" fill="%s">%d/%d</text>`,
		x, y+40, th.Title, active, total)
	f.Printf(`<text class="fade" x="%d" y="%d" font-size="12" fill="%s" style="animation-delay: 0.1s">%d%% of days active</text>`,
		x, y+62, th.TextMuted, pct)
	f.WriteString(`</g>`)
}

// ---- proportional chip cloud (categories / editors / platforms) ----

// EmitChips draws a wrapping cloud of label+pct chips within (x, y, w, h) —
// the SVG twin of the FE ChipList. Chip font size scales with the entry's
// share of the SHOWN set (10..14px, +1px per 10pct, like the FE), percentages
// are recomputed over the shown entries (never the payload-global TotalPct —
// same rationale as EmitBars), and chips that would overflow the box are
// dropped rather than clipped mid-glyph. Each chip carries a native <title>
// tooltip with the exact duration.
func EmitChips(f *Frame, x, y, w, h int, entries []model.ResourceStats) {
	th := f.Theme
	if len(entries) == 0 {
		f.Printf(`<text x="%d" y="%d" font-size="11" fill="%s">No data</text>`,
			x, y+h/2, th.TextMuted)
		return
	}
	var grand int64
	for _, e := range entries {
		grand += e.TotalSeconds
	}
	if grand < 1 {
		grand = 1
	}
	const gap = 8
	const rowStep = 30 // fixed row height so mixed font sizes still align
	cx, cy := x, y
	f.WriteString(`<g class="fade" style="animation-delay: 0.1s">`)
	for i, e := range entries {
		pct := float64(e.TotalSeconds) / float64(grand) * 100
		fs := 10 + pct/10 // FE: max(10, min(14, 10 + pct/10))
		if fs > 14 {
			fs = 14
		}
		label := truncate(e.Name, 16)
		text := fmt.Sprintf("%s %d%%", label, int(math.Round(pct)))
		// Approximate monospace-ish advance; +20 pads 10px each side.
		chipW := int(fs*0.62*float64(len([]rune(text)))) + 20
		chipH := int(fs) + 12
		if cx+chipW > x+w && cx > x { // wrap
			cx = x
			cy += rowStep
		}
		if cy+chipH > y+h {
			break // out of vertical room — drop the tail rather than clip
		}
		f.Printf(`<g class="row" style="animation-delay: %.2fs"><title>%s</title>`,
			0.1*float64(i), xmlEscape(fmt.Sprintf("%s — %s (%.1f%%)", e.Name, compound(e.TotalSeconds), pct)))
		f.Printf(`<rect x="%d" y="%d" width="%d" height="%d" rx="3" fill="%s" stroke="%s"/>`,
			cx, cy, chipW, chipH, th.TrackBg, th.colorAt(i))
		f.Printf(`<text x="%d" y="%d" font-size="%.0f" fill="%s" dominant-baseline="middle">%s</text>`,
			cx+10, cy+chipH/2+1, fs, th.Text, xmlEscape(text))
		f.WriteString(`</g>`)
		cx += chipW + gap
	}
	f.WriteString(`</g>`)
}

// ---- goal bar / goal ring (Part B Stage 4 — SVG twins of the FE
// GoalProgress/GoalList/GoalRing self-fetching components) ----

// goalHitColor tints a hit goal's fill/text — mirrors the FE's
// text-emerald-400 convention (GoalProgress.tsx / GoalList.tsx) rather than
// the theme's accent color, so a hit goal reads the same "done" green in
// both the in-page tile and the SVG embed.
const goalHitColor = "#34d399"

// EmitGoalBar draws one goal row: name (left) + pct% (right) on a single
// line, with a horizontal progress track+fill beneath — the SVG twin of the
// FE GoalProgress/GoalList row. `hit` tints the pct text and bar fill
// goalHitColor instead of the theme accent/muted colors (mirrors the FE's
// emerald-on-hit convention). name is user-controlled (the goal's own
// name) and is xmlEscape'd here, not by the caller.
func EmitGoalBar(f *Frame, x, y, w int, name string, pct int, hit bool) {
	th := f.Theme
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	fillColor, pctColor := th.Accent, th.TextMuted
	if hit {
		fillColor, pctColor = goalHitColor, goalHitColor
	}
	f.Printf(`<g class="row"><title>%s</title>`,
		xmlEscape(fmt.Sprintf("%s — %d%%", name, pct)))
	f.Printf(`<text x="%d" y="%d" font-size="12" fill="%s">%s</text>`,
		x, y, th.Text, xmlEscape(truncate(name, 28)))
	f.Printf(`<text x="%d" y="%d" font-size="12" fill="%s" text-anchor="end">%d%%</text>`,
		x+w, y, pctColor, pct)
	trackY := y + 8
	f.Printf(`<rect x="%d" y="%d" width="%d" height="8" rx="4" fill="%s"/>`,
		x, trackY, w, th.TrackBg)
	fillW := int(float64(w) * float64(pct) / 100)
	if fillW < 2 && pct > 0 {
		fillW = 2
	}
	f.Printf(`<rect class="bar-fill" x="%d" y="%d" width="%d" height="8" rx="4" fill="%s"/>`,
		x, trackY, fillW, fillColor)
	f.WriteString(`</g>`)
}

// EmitGoalRing draws one progress ring centered at (cx, cy) with radius r —
// the SVG twin of one CircularGauge in the FE GoalRing's concentric stack.
// colorIdx selects a palette color for the ring stroke (ignored when hit,
// which always tints goalHitColor — same convention as EmitGoalBar).
// Callers stack multiple calls at decreasing radii around a shared center
// for the concentric look (see emitGoalRingPanel in spec.go). name feeds
// the hover <title> only; it is xmlEscape'd here.
func EmitGoalRing(f *Frame, cx, cy, r, pct int, hit bool, colorIdx int, name string) {
	th := f.Theme
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	circ := 2 * math.Pi * float64(r)
	fillLen := circ * float64(pct) / 100
	color := th.colorAt(colorIdx)
	if hit {
		color = goalHitColor
	}
	f.Printf(`<g><title>%s</title>`, xmlEscape(fmt.Sprintf("%s — %d%%", name, pct)))
	f.Printf(`<circle cx="%d" cy="%d" r="%d" stroke="%s" stroke-width="8" fill="none"/>`,
		cx, cy, r, th.TrackBg)
	f.Printf(`<circle class="ring" cx="%d" cy="%d" r="%d" stroke="%s" stroke-width="8" fill="none" stroke-linecap="round" stroke-dasharray="%.2f %.2f" transform="rotate(-90 %d %d)"/>`,
		cx, cy, r, color, fillLen, circ, cx, cy)
	f.WriteString(`</g>`)
}

// ---- helpers ----

// mixHex linearly interpolates between two "#rrggbb" strings by q in [0, 1].
// Non-hex inputs fall back to `a`.
func mixHex(a, b string, q float64) string {
	ar, ag, ab := parseHex(a)
	br, bg, bb := parseHex(b)
	if q < 0 {
		q = 0
	}
	if q > 1 {
		q = 1
	}
	lerp := func(x, y int) int { return x + int(float64(y-x)*q) }
	return fmt.Sprintf("#%02x%02x%02x", lerp(ar, br), lerp(ag, bg), lerp(ab, bb))
}

// parseHex reads "#rrggbb" into three 0..255 components; unrecognised input
// yields (0, 0, 0) — safe fallback.
func parseHex(s string) (int, int, int) {
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0
	}
	var r, g, b int
	if _, err := fmt.Sscanf(s[1:], "%02x%02x%02x", &r, &g, &b); err != nil {
		return 0, 0, 0
	}
	return r, g, b
}
