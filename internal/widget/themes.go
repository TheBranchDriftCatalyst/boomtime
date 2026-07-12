package widget

// Theme is a hardcoded palette for server-rendered SVG widgets. The values
// mirror the SPA's theme tokens (web/src/theme/theme.css :root / .dark) and
// CHART_COLORS (web/src/lib/config.ts) — hex approximations of the oklch
// tokens, since SVG wants concrete colors and GitHub camo strips external CSS.
type Theme struct {
	Background string
	Border     string
	Title      string // headline accent
	Text       string
	TextMuted  string
	Accent     string // bars/highlights fallback
	TrackBg    string // bar track (unfilled part)
	Palette    []string
}

// chartPalette mirrors CHART_COLORS in web/src/lib/config.ts, order-for-order.
var chartPalette = []string{
	"#05d9e8", "#ff2d95", "#a3ff3c", "#b967ff", "#ffb13d", "#3b7bff",
	"#ff5e7e", "#2dffb3", "#ff8f1f", "#e94bff", "#00f0ff", "#ffe94d",
	"#7a5cff", "#ff3caa",
}

var themes = map[string]Theme{
	// Synthwave dark — the app default (theme.css .dark block).
	"dark": {
		Background: "#16121f",
		Border:     "#2b2140",
		Title:      "#ff2d95",
		Text:       "#e8e6f0",
		TextMuted:  "#8b86a0",
		Accent:     "#05d9e8",
		TrackBg:    "#2b2140",
		Palette:    chartPalette,
	},
	"light": {
		Background: "#ffffff",
		Border:     "#e4e2ea",
		Title:      "#3b7bff",
		Text:       "#1a1a24",
		TextMuted:  "#6b6880",
		Accent:     "#0aa8b4",
		TrackBg:    "#eceaf2",
		Palette:    chartPalette,
	},
}

// themeFor resolves a theme name, defaulting to dark (the app default) for
// unknown/empty values — never an error on a public endpoint.
func themeFor(name string) Theme {
	if t, ok := themes[name]; ok {
		return t
	}
	return themes["dark"]
}

// colorAt cycles the palette like the FE's colorAt.
func (t Theme) colorAt(i int) string {
	if len(t.Palette) == 0 {
		return t.Accent
	}
	return t.Palette[i%len(t.Palette)]
}
