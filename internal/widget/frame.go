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
	fmt.Fprintf(&f.buf,
		`<rect x="0.5" y="0.5" width="%d" height="%d" rx="4.5" fill="%s" stroke="%s"/>`,
		w, h, th.Background, th.Border)
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
