// custom.go: the "build your own widget" renderer (gaka-567). Takes a Def
// (composition of primitives + layout) that the caller passes inline via URL
// query param — no saved-defs table for v1; a user-mints-a-def endpoint can
// be added later without touching this file. Layouts do the panel-origin
// arithmetic; each panel dispatches to the same emit* primitives the fixed
// kinds use, so a builder-composed widget looks pixel-identical to the same
// composition wired as a hardcoded kind.
package widget

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// PanelKind is one primitive slot the builder can drop into a layout.
type PanelKind string

const (
	PanelCalendar    PanelKind = "calendar"     // EmitCalendar over Payload.DailyTotal
	PanelTopLangs    PanelKind = "top-langs"    // EmitBars over Payload.Languages
	PanelTopProjects PanelKind = "top-projects" // EmitBars over Payload.Projects
	PanelGrade       PanelKind = "grade"        // EmitGradeRing (needs Grade)
	PanelArea        PanelKind = "area"         // EmitAreaLine (cumulative Payload.DailyTotal)
	PanelPunchcard   PanelKind = "punchcard"    // EmitPunchcard (needs Punchcard)
	PanelMomentum    PanelKind = "momentum"     // EmitMomentum (needs Momentum)
	PanelMetrics     PanelKind = "metrics"      // EmitMetric x3 (total, daily avg, sessions if present)
)

// Layout picks the panel-count + orientation.
type Layout string

const (
	Layout1     Layout = "1-panel"
	Layout2Horz Layout = "2-panel-h"
	Layout3Horz Layout = "3-panel-h"
	Layout2Vert Layout = "2-panel-v"
)

// Panel is one slot in a Def — a primitive + optional per-panel title.
type Panel struct {
	Kind  PanelKind `json:"kind"`
	Title string    `json:"title,omitempty"`
}

// Def is the builder's serialized composition. Carried inline in the widget
// URL as `?spec=<base64(json)>`.
type Def struct {
	Layout Layout  `json:"layout"`
	Title  string  `json:"title,omitempty"`
	Panels []Panel `json:"panels"`
}

// EncodeDef base64-urlencodes a Def for embedding in a widget URL.
func EncodeDef(d Def) (string, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeDef parses a base64-urlencoded Def. Rejects unknown layouts and any
// panel with an unknown kind (defense-in-depth: even though renderPanel has
// its own default, the whitelist stops probing).
func DecodeDef(encoded string) (Def, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		// Also accept std base64 for camo-friendliness (some renderers escape
		// URL-safe chars during copy).
		raw, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return Def{}, fmt.Errorf("spec is not base64: %w", err)
		}
	}
	var d Def
	if err := json.Unmarshal(raw, &d); err != nil {
		return Def{}, fmt.Errorf("spec is not JSON: %w", err)
	}
	switch d.Layout {
	case Layout1, Layout2Horz, Layout3Horz, Layout2Vert:
	default:
		return Def{}, fmt.Errorf("unknown layout %q", d.Layout)
	}
	want := layoutPanelCount(d.Layout)
	if len(d.Panels) != want {
		return Def{}, fmt.Errorf("layout %s wants %d panels, got %d", d.Layout, want, len(d.Panels))
	}
	for _, p := range d.Panels {
		if !isKnownPanel(p.Kind) {
			return Def{}, fmt.Errorf("unknown panel kind %q", p.Kind)
		}
	}
	return d, nil
}

func layoutPanelCount(l Layout) int {
	switch l {
	case Layout1:
		return 1
	case Layout2Horz, Layout2Vert:
		return 2
	case Layout3Horz:
		return 3
	}
	return 0
}

func isKnownPanel(k PanelKind) bool {
	switch k {
	case PanelCalendar, PanelTopLangs, PanelTopProjects,
		PanelGrade, PanelArea, PanelPunchcard, PanelMomentum, PanelMetrics:
		return true
	}
	return false
}

// NeedsForDef aggregates the data requirements a Def implies — the union of
// each panel's needs. The handler uses this to decide which optional DB
// fetches to make for a custom render.
func NeedsForDef(d Def) Requirements {
	var r Requirements
	for _, p := range d.Panels {
		switch p.Kind {
		case PanelGrade:
			r.Grade = true
		case PanelPunchcard:
			r.Punchcard = true
		case PanelMomentum:
			r.Momentum = true
		case PanelMetrics:
			r.Sessions = true // metrics shows session count when present
		}
	}
	return r
}

// panelRect is one panel's origin + size within the card body.
type panelRect struct{ X, Y, W, H int }

// panelRects computes each panel's rectangle for a layout, sized to (w × h)
// body area starting at bodyTop inside a `total` height frame.
func panelRects(layout Layout, w, h, bodyTop int) []panelRect {
	pad := 20
	inner := h - bodyTop - pad
	switch layout {
	case Layout1:
		return []panelRect{{X: pad, Y: bodyTop, W: w - 2*pad, H: inner}}
	case Layout2Horz:
		colW := (w - 3*pad) / 2
		return []panelRect{
			{X: pad, Y: bodyTop, W: colW, H: inner},
			{X: 2*pad + colW, Y: bodyTop, W: colW, H: inner},
		}
	case Layout3Horz:
		colW := (w - 4*pad) / 3
		return []panelRect{
			{X: pad, Y: bodyTop, W: colW, H: inner},
			{X: 2*pad + colW, Y: bodyTop, W: colW, H: inner},
			{X: 3*pad + 2*colW, Y: bodyTop, W: colW, H: inner},
		}
	case Layout2Vert:
		rowH := (inner - pad) / 2
		return []panelRect{
			{X: pad, Y: bodyTop, W: w - 2*pad, H: rowH},
			{X: pad, Y: bodyTop + rowH + pad, W: w - 2*pad, H: rowH},
		}
	}
	return nil
}

// layoutSize picks a canvas size that fits the chosen layout comfortably.
func layoutSize(layout Layout) (w, h int) {
	switch layout {
	case Layout1:
		return 495, 195
	case Layout2Horz:
		return 720, 220
	case Layout3Horz:
		return 820, 240
	case Layout2Vert:
		return 495, 340
	}
	return 495, 195
}

// renderCustom is the dispatch for kind == "custom". opts.Title falls back to
// the def's own Title; missing data blobs draw an inline "no data" per panel.
func renderCustom(d *Data, th Theme, opts Options, def Def) ([]byte, error) {
	w, h := layoutSize(def.Layout)
	title := opts.Title
	if title == "" {
		title = def.Title
	}
	if title == "" {
		title = "Custom widget"
	}
	f := OpenFrame(w, h, th, title, opts.Subtitle)
	rects := panelRects(def.Layout, w, h, f.BodyTop())
	// Optional dividers between panels (visual separators; skipped on
	// 1-panel to keep the card clean).
	if def.Layout != Layout1 {
		for i := 1; i < len(rects); i++ {
			r := rects[i]
			if def.Layout == Layout2Vert {
				f.Printf(`<rect x="%d" y="%d" width="%d" height="1" fill="%s"/>`,
					r.X, r.Y-10, r.W, th.Border)
			} else {
				f.Printf(`<rect x="%d" y="%d" width="1" height="%d" fill="%s"/>`,
					r.X-10, r.Y, r.H, th.Border)
			}
		}
	}
	for i, panel := range def.Panels {
		if i >= len(rects) {
			break
		}
		renderPanel(f, d, rects[i], panel)
	}
	return f.Close(), nil
}

// renderPanel dispatches one panel slot to its primitive. Missing data
// (e.g. a Grade panel with no Grade fetched) draws a placeholder so the
// composition doesn't leave a blank hole.
func renderPanel(f *Frame, d *Data, r panelRect, p Panel) {
	switch p.Kind {
	case PanelCalendar:
		if len(d.Payload.DailyTotal) == 0 {
			panelPlaceholder(f, r, "No days")
			return
		}
		EmitCalendar(f, r.X, r.Y, r.W, r.H, d.Payload.StartDate, d.Payload.DailyTotal)
	case PanelTopLangs:
		emitBarsPanel(f, r, topEntries(d.Payload.Languages, panelRowCount(r.H)))
	case PanelTopProjects:
		emitBarsPanel(f, r, topEntries(d.Payload.Projects, panelRowCount(r.H)))
	case PanelGrade:
		if d.Grade == nil {
			panelPlaceholder(f, r, "No grade")
			return
		}
		// Center the ring in the panel; leave room for a Total metric under it.
		cx := r.X + r.W/2
		cy := r.Y + r.H/2 - 15
		radius := r.H / 4
		if radius > 42 {
			radius = 42
		}
		EmitGradeRing(f, cx, cy, radius, d.Grade)
		EmitMetric(f, r.X+10, r.Y+r.H-30, "Total", compound(d.Payload.TotalSeconds))
	case PanelArea:
		EmitAreaLine(f, r.X, r.Y, r.W, r.H, d.Payload.DailyTotal)
	case PanelPunchcard:
		if d.Punchcard == nil {
			panelPlaceholder(f, r, "No punchcard")
			return
		}
		EmitPunchcard(f, r.X, r.Y, r.W, r.H, d.Punchcard.Cells)
	case PanelMomentum:
		EmitMomentum(f, r.X, r.Y, r.W, r.H, d.Momentum)
	case PanelMetrics:
		EmitMetric(f, r.X, r.Y+10, "Total", compound(d.Payload.TotalSeconds))
		EmitMetric(f, r.X, r.Y+60, "Daily avg", compound(int64(d.Payload.DailyAvg)))
		if d.Sessions != nil && d.Sessions.Summary.Count > 0 {
			EmitMetric(f, r.X, r.Y+110, "Sessions",
				fmt.Sprintf("%d", d.Sessions.Summary.Count))
		}
	}
}

// panelRowCount picks how many bar rows fit vertically in a panel.
func panelRowCount(h int) int {
	n := (h - 20) / 22
	if n < 3 {
		n = 3
	}
	if n > 6 {
		n = 6
	}
	return n
}

// emitBarsPanel is the panel-scoped wrapper over EmitBars: positions the
// label column and bar track based on the panel's actual width.
func emitBarsPanel(f *Frame, r panelRect, entries []model.ResourceStats) {
	if len(entries) == 0 {
		panelPlaceholder(f, r, "No data")
		return
	}
	labelW := 90
	if r.W < 220 {
		labelW = 60
	}
	barWMax := r.W - labelW - 40
	if barWMax < 40 {
		barWMax = 40
	}
	yStep := 22
	if r.H/len(entries) < yStep {
		yStep = r.H / len(entries)
	}
	if yStep < 14 {
		yStep = 14
	}
	EmitBars(f, entries, BarsOpts{
		X:                r.X,
		Y:                r.Y + 18,
		YStep:            yStep,
		BarX:             r.X + labelW,
		BarWMax:          barWMax,
		LabelChars:       14,
		IncludeValueText: true,
	})
}

func panelPlaceholder(f *Frame, r panelRect, msg string) {
	f.Printf(`<text x="%d" y="%d" font-size="11" fill="%s" text-anchor="middle">%s</text>`,
		r.X+r.W/2, r.Y+r.H/2, f.Theme.TextMuted, xmlEscape(msg))
}

