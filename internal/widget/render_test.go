package widget

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
)

func payloadFixture() *model.StatsPayload {
	return &model.StatsPayload{
		TotalSeconds: 3 * 3600,
		DailyAvg:     1543,
		DailyTotal:   []int64{3600, 0, 3600, 3600, 0, 0, 0},
		Languages: []model.ResourceStats{
			{Name: "Go", TotalSeconds: 7200, TotalPct: 66.7},
			{Name: "TypeScript", TotalSeconds: 3600, TotalPct: 33.3},
		},
		Projects: []model.ResourceStats{
			{Name: "boomtime", TotalSeconds: 10800, TotalPct: 100},
		},
		LanguagesCount: 2,
		ProjectsCount:  1,
	}
}

// assertValidXML round-trips the SVG through encoding/xml — a malformed escape
// or unbalanced tag fails here.
func assertValidXML(t *testing.T, b []byte) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(string(b)))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				return
			}
			t.Fatalf("SVG is not well-formed XML: %v\n%s", err, b)
		}
	}
}

func TestRenderAllKindsWellFormed(t *testing.T) {
	p := payloadFixture()
	g := stats.Grade(p)
	for _, kind := range Kinds() {
		t.Run(kind, func(t *testing.T) {
			b, err := Render(kind, p, &g, Options{Theme: "dark", Subtitle: "last 30 days"})
			if err != nil {
				t.Fatalf("Render(%s): %v", kind, err)
			}
			assertValidXML(t, b)
			s := string(b)
			if !strings.HasPrefix(strings.TrimSpace(s), "<svg") {
				t.Errorf("%s: output does not start with <svg", kind)
			}
			// Camo-safety: no scripts, no external references.
			for _, banned := range []string{"<script", "http://", "https://", "url(http", "@import"} {
				if strings.Contains(s, banned) && banned != "http://" {
					t.Errorf("%s: output contains banned token %q", kind, banned)
				}
			}
			// The xmlns is the one allowed URL-ish string.
			if strings.Count(s, "http://www.w3.org/2000/svg") != strings.Count(s, "http://") {
				t.Errorf("%s: contains an http:// reference beyond the svg xmlns", kind)
			}
		})
	}
}

func TestRenderEscapesUserStrings(t *testing.T) {
	p := payloadFixture()
	p.Languages = []model.ResourceStats{
		{Name: `<script>alert(1)</script>`, TotalSeconds: 7200, TotalPct: 50},
		{Name: `A&B "quoted" <lang>`, TotalSeconds: 3600, TotalPct: 25},
	}
	p.Projects = []model.ResourceStats{
		{Name: `evil<img onerror=alert(1)>`, TotalSeconds: 3600, TotalPct: 100},
	}
	for _, kind := range []string{"stats-card", "top-langs", "top-projects"} {
		b, err := Render(kind, p, nil, Options{Theme: "dark", Title: `T<i>tle & "stuff"`})
		if err != nil {
			t.Fatalf("Render(%s): %v", kind, err)
		}
		assertValidXML(t, b)
		s := string(b)
		if strings.Contains(s, "<script") || strings.Contains(s, "<img") || strings.Contains(s, "<i>") {
			t.Errorf("%s: unescaped user markup leaked into SVG:\n%s", kind, s)
		}
	}
}

func TestRenderTruncatesLongNames(t *testing.T) {
	long := strings.Repeat("verylongname", 10)
	p := payloadFixture()
	p.Languages = []model.ResourceStats{{Name: long, TotalSeconds: 3600, TotalPct: 100}}
	b, err := Render("top-langs", p, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), long) {
		t.Error("long name was not truncated")
	}
	if !strings.Contains(string(b), "…") {
		t.Error("expected an ellipsis after truncation")
	}
}

func TestRenderEmptyPayload(t *testing.T) {
	empty := &model.StatsPayload{}
	for _, kind := range Kinds() {
		b, err := Render(kind, empty, nil, Options{})
		if err != nil {
			t.Fatalf("Render(%s) on empty payload: %v", kind, err)
		}
		assertValidXML(t, b)
	}
	b, _ := Render("stats-card", empty, nil, Options{})
	if !strings.Contains(string(b), "No coding activity") {
		t.Error("empty stats-card should render the no-data message")
	}
}

func TestRenderThemeSelection(t *testing.T) {
	p := payloadFixture()
	dark, _ := Render("stats-card", p, nil, Options{Theme: "dark"})
	light, _ := Render("stats-card", p, nil, Options{Theme: "light"})
	unknown, _ := Render("stats-card", p, nil, Options{Theme: "hotdog-stand"})
	if !strings.Contains(string(dark), themes["dark"].Background) {
		t.Error("dark theme background missing")
	}
	if !strings.Contains(string(light), themes["light"].Background) {
		t.Error("light theme background missing")
	}
	if string(unknown) != string(dark) {
		t.Error("unknown theme should fall back to dark")
	}
}

func TestRenderGradeRing(t *testing.T) {
	p := payloadFixture()
	g := stats.Grade(p)
	b, err := Render("stats-card-with-grade", p, &g, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, ">"+g.Level+"<") {
		t.Errorf("grade level %q not rendered", g.Level)
	}
	if !strings.Contains(s, "stroke-dasharray") {
		t.Error("grade ring missing")
	}
	// nil grade must self-compute, not panic.
	b2, err := Render("stats-card-with-grade", p, nil, Options{})
	if err != nil || !strings.Contains(string(b2), "stroke-dasharray") {
		t.Errorf("nil-grade render failed: %v", err)
	}
}

func TestRenderSkipsSynthesizedOtherRow(t *testing.T) {
	p := payloadFixture()
	p.Languages = append(p.Languages, model.ResourceStats{
		Name: "Other (5 more)", TotalSeconds: 999999, TotalPct: 90, OtherCount: 5,
	})
	b, err := Render("top-langs", p, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Other (5 more)") {
		t.Error("synthesized Other row should be excluded from top lists")
	}
}

// Percentages on a card are normalized over the SHOWN entries (sum ~100),
// not the payload-global TotalPct (which sums short when the tail is cut).
func TestRenderPercentagesSumTo100(t *testing.T) {
	p := payloadFixture()
	p.Languages = []model.ResourceStats{
		// Global pcts sum to 60 (a dropped tail holds the rest) — the card must
		// renormalize over what it shows: 75.0% + 25.0%.
		{Name: "Go", TotalSeconds: 9000, TotalPct: 45},
		{Name: "TypeScript", TotalSeconds: 3000, TotalPct: 15},
	}
	b, err := Render("top-langs", p, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "75.0%") || !strings.Contains(s, "25.0%") {
		t.Errorf("expected renormalized 75.0%%/25.0%%, got:\n%s", s)
	}
	if strings.Contains(s, "45.0%") {
		t.Error("payload-global TotalPct leaked into the card")
	}
}

// Embeds animate (CSS keyframes play inside <img>/camo) and carry native
// <title> tooltips (hover on direct view / <object> embeds).
func TestRenderAnimationsAndTooltips(t *testing.T) {
	p := payloadFixture()
	for _, kind := range []string{"stats-card", "top-langs", "top-projects"} {
		b, err := Render(kind, p, nil, Options{})
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if !strings.Contains(s, "@keyframes") {
			t.Errorf("%s: missing entrance animations", kind)
		}
		if !strings.Contains(s, "<title>") {
			t.Errorf("%s: missing native <title> hover tooltips", kind)
		}
	}
}

func TestRenderUnknownKind(t *testing.T) {
	if _, err := Render("nope", payloadFixture(), nil, Options{}); err == nil {
		t.Error("unknown kind should error")
	}
	if IsKind("nope") {
		t.Error("IsKind(nope) should be false")
	}
}

// Drift guard: the BE whitelist must match the FE catalog
// (web/src/features/widgets/catalog.ts) — update BOTH when adding a kind.
func TestKindsMatchFrontendCatalog(t *testing.T) {
	want := []string{"badge", "stats-card", "stats-card-with-grade", "top-langs", "top-projects"}
	got := Kinds()
	if len(got) != len(want) {
		t.Fatalf("Kinds() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Kinds() = %v, want %v", got, want)
		}
	}
}
