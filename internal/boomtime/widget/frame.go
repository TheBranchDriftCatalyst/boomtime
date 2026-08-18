// frame.go: the shared card chrome — outer SVG frame, CSS animation styles,
// header (title + subtitle), and empty-state fallback. Every renderer opens a
// Frame, emits its primitives inside, then closes. Adding a new widget kind
// starts here: Frame does the boring bits so the kind only writes its viz.
package widget

import (
	"bytes"
	_ "embed"
	"fmt"
)

// frameStyleCSS is the shared <style> block (keyframes + class rules) that
// every card frame emits inline. Extracted from a raw-string constant in the
// package source (gaka-8tn.1) so an editor / diff viewer can highlight it
// as CSS. The bytes are served verbatim inside each SVG — do NOT re-indent
// or reformat this file or the golden-hash test in openapi will need to move
// its widget cousin too.
//
//go:embed frame_style.css
var frameStyleCSS []byte

// Frame is a card renderer that any widget kind can compose into.
type Frame struct {
	buf     bytes.Buffer
	W, H    int
	Theme   Theme
	closed  bool
	titleY  int
	bodyTop int // safe y-baseline the kind should start drawing from
}

// OpenFrame emits the outer SVG, the card background+border, the shared CSS
// keyframe animations, the title and (optional) subtitle. It returns the y
// coordinate the kind should treat as the top of its drawable body.
func OpenFrame(w, h int, th Theme, title, subtitle string) *Frame {
	return OpenFrameWith(w, h, th, title, subtitle, "")
}

// OpenFrameWith is OpenFrame with an optional named backdrop. background=""
// draws the flat card fill every card uses (OpenFrame's behaviour, unchanged);
// "synthwave" draws the richer gradient-wash + glow + perspective-grid backdrop
// used by the social-card OG image. The title/subtitle are drawn ON TOP of the
// backdrop so they stay crisp.
func OpenFrameWith(w, h int, th Theme, title, subtitle, background string) *Frame {
	f := &Frame{W: w, H: h, Theme: th, titleY: 30}
	fmt.Fprintf(&f.buf,
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" fill="none" role="img" aria-label="%s">`,
		w, h, w, h, xmlEscape(title))
	// Shared CSS: fade-in-up entrance, bar scale-x grow, grade-ring dasharray
	// reveal. Delays are staggered per row/panel. transform-box=fill-box lets
	// the bar grow anchor to the rect's own left edge. Body extracted to
	// frame_style.css so a syntax highlighter can help — served verbatim,
	// byte-identical to the pre-extraction inline literal.
	f.buf.Write(frameStyleCSS)
	if background == "synthwave" {
		f.drawSynthwaveBackdrop()
	} else {
		fmt.Fprintf(&f.buf,
			`<rect x="0.5" y="0.5" width="%d" height="%d" rx="4.5" fill="%s" stroke="%s"/>`,
			w, h, th.Background, th.Border)
	}
	fmt.Fprintf(&f.buf,
		`<text x="20" y="%d" font-size="16" font-weight="600" fill="%s">%s</text>`,
		f.titleY, th.Title, xmlEscape(truncate(title, 34)))
	f.bodyTop = f.titleY + 20
	if subtitle != "" {
		fmt.Fprintf(&f.buf,
			`<text x="20" y="%d" font-size="10" fill="%s">%s</text>`,
			f.titleY+15, th.TextMuted, xmlEscape(truncate(subtitle, 40)))
		f.bodyTop = f.titleY + 30
	}
	return f
}

// drawSynthwaveBackdrop paints the social-card OG backdrop: a dark base, a
// magenta top wash, a cyan glow behind the hero, and a receding perspective
// grid with a glowing horizon in the lower band — all low-contrast so the
// content drawn on top stays crisp. Everything is clipped to the rounded card.
// Static (no animation) so it rasterizes faithfully to PNG. Uses only theme
// colors + url(#id) refs (camo-safe: no external URLs).
func (f *Frame) drawSynthwaveBackdrop() {
	w, h := f.W, f.H
	th := f.Theme
	horizon := h * 60 / 100

	fmt.Fprintf(&f.buf, `<defs>`)
	fmt.Fprintf(&f.buf, `<clipPath id="scclip"><rect x="0.5" y="0.5" width="%d" height="%d" rx="4.5"/></clipPath>`, w, h)
	fmt.Fprintf(&f.buf, `<linearGradient id="scwash" x1="0" y1="0" x2="0" y2="1">`+
		`<stop offset="0" stop-color="%s" stop-opacity="0.22"/>`+
		`<stop offset="0.45" stop-color="%s" stop-opacity="0"/></linearGradient>`, th.Title, th.Title)
	fmt.Fprintf(&f.buf, `<radialGradient id="scglow" cx="0.15" cy="0.20" r="0.6">`+
		`<stop offset="0" stop-color="%s" stop-opacity="0.18"/>`+
		`<stop offset="1" stop-color="%s" stop-opacity="0"/></radialGradient>`, th.Accent, th.Accent)
	fmt.Fprintf(&f.buf, `<radialGradient id="scfloor" cx="0.5" cy="1" r="0.85">`+
		`<stop offset="0" stop-color="%s" stop-opacity="0.16"/>`+
		`<stop offset="1" stop-color="%s" stop-opacity="0"/></radialGradient>`, th.Title, th.Title)
	fmt.Fprintf(&f.buf, `</defs>`)

	fmt.Fprintf(&f.buf, `<g clip-path="url(#scclip)">`)
	// Base + washes.
	fmt.Fprintf(&f.buf, `<rect x="0" y="0" width="%d" height="%d" fill="%s"/>`, w, h, th.Background)
	fmt.Fprintf(&f.buf, `<rect x="0" y="0" width="%d" height="%d" fill="url(#scwash)"/>`, w, h)
	fmt.Fprintf(&f.buf, `<rect x="0" y="0" width="%d" height="%d" fill="url(#scglow)"/>`, w, h)
	fmt.Fprintf(&f.buf, `<rect x="0" y="0" width="%d" height="%d" fill="url(#scfloor)"/>`, w, h)

	// Perspective grid below the horizon: verticals fan to a vanishing point,
	// horizontals bunch up toward the horizon (t² spacing), fading with depth.
	vx := w / 2
	for x := -w; x <= 2*w; x += 88 {
		fmt.Fprintf(&f.buf, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1" opacity="0.16"/>`,
			x, h, vx, horizon, th.Accent)
	}
	for i := 1; i <= 12; i++ {
		t := float64(i) / 12.0
		y := horizon + int(float64(h-horizon)*t*t)
		op := 0.24 * (1 - t*0.55)
		fmt.Fprintf(&f.buf, `<line x1="0" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1" opacity="%.2f"/>`,
			y, w, y, th.Accent, op)
	}
	// Glowing horizon line (magenta) where the grid meets the wash.
	fmt.Fprintf(&f.buf, `<rect x="0" y="%d" width="%d" height="10" fill="%s" opacity="0.10"/>`, horizon-5, w, th.Title)
	fmt.Fprintf(&f.buf, `<rect x="0" y="%d" width="%d" height="2" fill="%s" opacity="0.55"/>`, horizon-1, w, th.Title)
	fmt.Fprintf(&f.buf, `</g>`)

	// Border on top of the backdrop (matches the flat-card path's stroke).
	fmt.Fprintf(&f.buf, `<rect x="0.5" y="0.5" width="%d" height="%d" rx="4.5" fill="none" stroke="%s"/>`, w, h, th.Border)
}

// BodyTop is the recommended y at which a kind starts drawing viz — below the
// header.
func (f *Frame) BodyTop() int { return f.bodyTop }

// Empty writes an empty-state message centered vertically in the body area.
func (f *Frame) Empty(msg string) {
	fmt.Fprintf(&f.buf,
		`<text x="20" y="%d" font-size="13" fill="%s">%s</text>`,
		f.bodyTop+40, f.Theme.TextMuted, xmlEscape(msg))
}

// Write exposes the internal buffer for primitives to append into.
func (f *Frame) Write(p []byte) (int, error) { return f.buf.Write(p) }

// WriteString appends s directly to the buffer.
func (f *Frame) WriteString(s string) { f.buf.WriteString(s) }

// Printf is a fmt.Fprintf shortcut into the buffer.
func (f *Frame) Printf(format string, a ...any) { fmt.Fprintf(&f.buf, format, a...) }

// Close emits </svg> and returns the rendered bytes. Idempotent.
func (f *Frame) Close() []byte {
	if !f.closed {
		f.buf.WriteString(`</svg>`)
		f.closed = true
	}
	return f.buf.Bytes()
}
