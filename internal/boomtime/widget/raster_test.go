package widget

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The social-card spec must render well-formed SVG AND rasterize to a real
// 1200×630 PNG via the CGO-free resvg-go pipeline (gaka social-card).
func TestSocialCardRendersAndRasterizes(t *testing.T) {
	d := dataFixture()
	d.Payload.TotalSeconds = 1285200 // 357h — the big-hours flex
	d.Identity = &Identity{Username: "djdaniels", Tagline: "shipping <boomtime> & friends"}

	svg, err := RenderSpec("social-card", d, Options{Theme: "dark", Subtitle: "last 60 days"})
	if err != nil {
		t.Fatalf("RenderSpec(social-card): %v", err)
	}
	s := string(svg)
	if !strings.HasPrefix(strings.TrimSpace(s), "<svg") {
		t.Fatalf("social-card SVG does not start with <svg: %.60q", s)
	}
	// Hero username renders; a hostile tagline is escaped (no raw <boomtime>).
	if !strings.Contains(s, "@djdaniels") {
		t.Errorf("social-card missing @username hero")
	}
	if strings.Contains(s, "<boomtime>") {
		t.Errorf("social-card leaked an unescaped tagline: %q", s)
	}
	// TOTAL TRACKED uses the big-hours flex ("357h"), not the compound humanize.
	if !strings.Contains(s, ">357h<") {
		t.Errorf("social-card total should render big hours '357h'")
	}
	// Synthwave backdrop is present (scoped to this kind via spec.Background).
	if !strings.Contains(s, "url(#scclip)") {
		t.Errorf("social-card missing the synthwave backdrop")
	}
	for _, banned := range []string{"<script", "https://", "@import"} {
		if strings.Contains(s, banned) {
			t.Errorf("social-card SVG contains banned token %q", banned)
		}
	}

	png, err := RenderPNG(context.Background(), svg, 1200, 630)
	if err != nil {
		t.Fatalf("RenderPNG: %v", err)
	}
	if !bytes.HasPrefix(png, pngSignature) {
		t.Fatalf("RenderPNG output is not a PNG: first bytes %x", png[:8])
	}
	if len(png) < 1000 {
		t.Fatalf("RenderPNG output implausibly small (%d bytes)", len(png))
	}
}

// StripAnimationStyle must remove the animation <style> block (whose opacity:0
// / scaleX(0) initial states would rasterize blank) while leaving the drawn
// elements intact.
func TestStripAnimationStyle(t *testing.T) {
	svg := []byte(`<svg><style>@keyframes fadeInUp{}.fade{opacity:0}</style><text class="fade">hi</text></svg>`)
	got := StripAnimationStyle(svg)
	if bytes.Contains(got, []byte("<style>")) || bytes.Contains(got, []byte("opacity:0")) {
		t.Fatalf("style block not stripped: %s", got)
	}
	if !bytes.Contains(got, []byte(`<text class="fade">hi</text>`)) {
		t.Fatalf("content unexpectedly removed: %s", got)
	}
}

// formatHoursFlex: big whole hours, k-compacted past 1000h. Social-card only.
func TestFormatHoursFlex(t *testing.T) {
	cases := map[int64]string{
		0:         "0h",
		1285200:   "357h", // the flex fixture
		3600:      "1h",
		3_600_000: "1.0k h", // 1000h
		4_320_000: "1.2k h", // 1200h
	}
	for sec, want := range cases {
		if got := formatHoursFlex(sec); got != want {
			t.Errorf("formatHoursFlex(%d) = %q, want %q", sec, got, want)
		}
	}
}

// RenderBrandCard is the generic (no user data) card served for a non-public
// slug — it must render + rasterize like the real card.
func TestBrandCardRasterizes(t *testing.T) {
	svg, err := RenderBrandCard("dark")
	if err != nil {
		t.Fatalf("RenderBrandCard: %v", err)
	}
	png, err := RenderPNG(context.Background(), svg, 1200, 630)
	if err != nil {
		t.Fatalf("RenderPNG(brand): %v", err)
	}
	if !bytes.HasPrefix(png, pngSignature) {
		t.Fatalf("brand card is not a PNG")
	}
}
