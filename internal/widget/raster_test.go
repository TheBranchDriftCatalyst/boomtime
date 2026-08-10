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
