// raster.go: SVG → PNG rasterization for the OpenGraph social card
// (gaka social-card). The public /api/public/profile/:slug/og.png endpoint
// renders the "social-card" spec to SVG via renderSpec (like every other
// widget) and then rasterizes it here to a 1200×630 PNG suitable as an
// og:image.
//
// Rasterization uses github.com/kanrichan/resvg-go — a pure-Go WASM port of
// resvg driven by wazero, so it builds and runs under CGO_ENABLED=0 (the
// Docker/CI build). No system libraries, no cgo. Fonts are embedded (the Go
// font family from golang.org/x/image) so text renders identically whether
// the process runs on a dev Mac or the fontless alpine runtime image.
package widget

import (
	"bytes"
	"context"
	"fmt"
	"regexp"

	resvg "github.com/kanrichan/resvg-go"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

// styleBlockRe matches the shared <style>…</style> chrome OpenFrame embeds
// (frame_style.css). It is stripped before rasterization — see
// StripAnimationStyle.
var styleBlockRe = regexp.MustCompile(`(?s)<style>.*?</style>`)

// StripAnimationStyle removes the inline <style> block from a rendered widget
// SVG. That block sets the ENTRANCE-ANIMATION initial state (`.fade { opacity:
// 0 }`, `.bar-fill { transform: scaleX(0) }`, `.row { opacity: 0 }`) and
// relies on CSS @keyframes to reveal the content. resvg is a STATIC renderer —
// it does not run animations, so without this strip every animated element
// would rasterize at its hidden initial state (an all-but-blank PNG). Removing
// the block leaves each element at its natural, fully-drawn state (the bars are
// already emitted at their final width, the ring at its final dasharray, etc.),
// which is exactly the final animation frame. The leftover inline
// `style="animation-delay: …"` attributes are inert with no @keyframes present.
func StripAnimationStyle(svg []byte) []byte {
	return styleBlockRe.ReplaceAll(svg, nil)
}

// RenderPNG rasterizes a widget SVG to a width×height PNG using resvg-go
// (pure-Go WASM, CGO-free). The animation <style> block is stripped first (see
// StripAnimationStyle) so the static rasterizer captures the final frame. Two
// embedded Go fonts (regular + bold) are loaded and set as the default family
// so text renders even on a fontless runtime image; the widget SVGs don't pin
// a served font-family on their text nodes, so this default applies uniformly.
func RenderPNG(ctx context.Context, svg []byte, width, height int) ([]byte, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("widget: RenderPNG needs positive dimensions, got %dx%d", width, height)
	}
	flat := StripAnimationStyle(svg)

	worker, err := resvg.NewContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("widget: resvg context: %w", err)
	}
	defer worker.Close()
	r, err := worker.NewRenderer()
	if err != nil {
		return nil, fmt.Errorf("widget: resvg renderer: %w", err)
	}
	defer r.Close()

	if err := r.LoadFontData(goregular.TTF); err != nil {
		return nil, fmt.Errorf("widget: load regular font: %w", err)
	}
	if err := r.LoadFontData(gobold.TTF); err != nil {
		return nil, fmt.Errorf("widget: load bold font: %w", err)
	}
	// The Go fonts report family name "Go"; make it the fallback so text nodes
	// with no explicit font-family (all of ours, once the <style> is stripped)
	// resolve to it.
	if err := r.SetFontFamily("Go"); err != nil {
		return nil, fmt.Errorf("widget: set font family: %w", err)
	}

	png, err := r.RenderWithSize(flat, uint32(width), uint32(height))
	if err != nil {
		return nil, fmt.Errorf("widget: rasterize svg: %w", err)
	}
	if !bytes.HasPrefix(png, pngSignature) {
		return nil, fmt.Errorf("widget: rasterizer returned non-PNG output")
	}
	return png, nil
}

// pngSignature is the 8-byte PNG magic header — a cheap sanity check that the
// WASM rasterizer actually produced a PNG.
var pngSignature = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
