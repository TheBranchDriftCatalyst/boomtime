package widget

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
)

func payloadFixture() *model.StatsPayload {
	return &model.StatsPayload{
		StartDate:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:      time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
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

// dataFixture bundles a stats payload + freshly computed grade + a synthetic
// punchcard/momentum so every renderer has usable input.
func dataFixture() *Data {
	p := payloadFixture()
	g := stats.Grade(p)
	pc := model.PunchcardPayload{
		Cells: []model.PunchcardCell{
			{Dow: 1, Hour: 9, Seconds: 3600},
			{Dow: 1, Hour: 10, Seconds: 5400},
			{Dow: 3, Hour: 14, Seconds: 1800},
		},
		MaxSeconds:   5400,
		TotalSeconds: 10800,
	}
	m := model.MomentumPayload{
		Weeks: []string{"2026-06-01", "2026-06-08", "2026-06-15"},
		Projects: []model.MomentumProject{
			{Name: "boomtime", Weekly: []int64{3600, 5400, 1800}, TotalSeconds: 10800},
		},
	}
	return &Data{Payload: p, Grade: &g, Punchcard: &pc, Momentum: &m}
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
	d := dataFixture()
	for _, kind := range Kinds() {
		t.Run(kind, func(t *testing.T) {
			b, err := Render(kind, d, Options{Theme: "dark", Subtitle: "last 30 days"})
			if err != nil {
				t.Fatalf("Render(%s): %v", kind, err)
			}
			assertValidXML(t, b)
			s := string(b)
			if !strings.HasPrefix(strings.TrimSpace(s), "<svg") {
				t.Errorf("%s: output does not start with <svg", kind)
			}
			// Camo-safety: no scripts, no external references.
			for _, banned := range []string{"<script", "https://", "url(http", "@import"} {
				if strings.Contains(s, banned) {
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
	d := dataFixture()
	d.Payload.Languages = []model.ResourceStats{
		{Name: `<script>alert(1)</script>`, TotalSeconds: 7200, TotalPct: 50},
		{Name: `A&B "quoted" <lang>`, TotalSeconds: 3600, TotalPct: 25},
	}
	d.Payload.Projects = []model.ResourceStats{
		{Name: `evil<img onerror=alert(1)>`, TotalSeconds: 3600, TotalPct: 100},
	}
	for _, kind := range []string{"stats-card", "top-langs", "top-projects", "profile-summary"} {
		b, err := Render(kind, d, Options{Theme: "dark", Title: `T<i>tle & "stuff"`})
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
	d := dataFixture()
	d.Payload.Languages = []model.ResourceStats{{Name: long, TotalSeconds: 3600, TotalPct: 100}}
	b, err := Render("top-langs", d, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// The label is truncated → ellipsis present. The FULL name is intentionally
	// kept inside the <title> hover tooltip so users can see it on hover — so
	// it appears exactly ONCE (in the tooltip), not repeated in the rendered
	// label.
	if !strings.Contains(s, "…") {
		t.Error("expected an ellipsis after label truncation")
	}
	if n := strings.Count(s, long); n != 1 {
		t.Errorf("long name should appear exactly once (in the <title> tooltip), got %d occurrences", n)
	}
}

func TestRenderEmptyPayload(t *testing.T) {
	empty := &Data{Payload: &model.StatsPayload{}}
	for _, kind := range Kinds() {
		b, err := Render(kind, empty, Options{})
		if err != nil {
			t.Fatalf("Render(%s) on empty payload: %v", kind, err)
		}
		assertValidXML(t, b)
	}
	b, _ := Render("stats-card", empty, Options{})
	if !strings.Contains(string(b), "No coding activity") {
		t.Error("empty stats-card should render the no-data message")
	}
	// The composite is defensive about missing Grade — nil Grade must not panic.
	b, _ = Render("profile-summary", empty, Options{})
	if !strings.Contains(string(b), "No coding activity") {
		t.Error("empty profile-summary should render the no-data message")
	}
}

func TestRenderThemeSelection(t *testing.T) {
	d := dataFixture()
	dark, _ := Render("stats-card", d, Options{Theme: "dark"})
	light, _ := Render("stats-card", d, Options{Theme: "light"})
	unknown, _ := Render("stats-card", d, Options{Theme: "hotdog-stand"})
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
	d := dataFixture()
	b, err := Render("stats-card-with-grade", d, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, ">"+d.Grade.Level+"<") {
		t.Errorf("grade level %q not rendered", d.Grade.Level)
	}
	if !strings.Contains(s, "stroke-dasharray") {
		t.Error("grade ring missing")
	}
	// nil Grade must self-compute (renderer falls back to stats.Grade), not panic.
	d2 := &Data{Payload: d.Payload}
	b2, err := Render("stats-card-with-grade", d2, Options{})
	if err != nil || !strings.Contains(string(b2), "stroke-dasharray") {
		t.Errorf("nil-grade render failed: %v", err)
	}
}

func TestRenderSkipsSynthesizedOtherRow(t *testing.T) {
	d := dataFixture()
	d.Payload.Languages = append(d.Payload.Languages, model.ResourceStats{
		Name: "Other (5 more)", TotalSeconds: 999999, TotalPct: 90, OtherCount: 5,
	})
	b, err := Render("top-langs", d, Options{})
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
	d := dataFixture()
	d.Payload.Languages = []model.ResourceStats{
		// Global pcts sum to 60 (a dropped tail holds the rest) — the card must
		// renormalize over what it shows: 75.0% + 25.0%.
		{Name: "Go", TotalSeconds: 9000, TotalPct: 45},
		{Name: "TypeScript", TotalSeconds: 3000, TotalPct: 15},
	}
	b, err := Render("top-langs", d, Options{})
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
	d := dataFixture()
	for _, kind := range []string{"stats-card", "top-langs", "top-projects", "profile-summary", "activity-heatmap", "punchcard", "momentum"} {
		b, err := Render(kind, d, Options{})
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

// The composite renders all three panels — calendar cells + language bars +
// grade ring. If any one disappears the layout has silently regressed.
func TestRenderProfileSummaryPanels(t *testing.T) {
	d := dataFixture()
	b, err := Render("profile-summary", d, Options{Subtitle: "last 30 days"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// Panel 1: calendar cells carry per-day <title> tooltips with date labels.
	if !strings.Contains(s, "2026") {
		t.Error("profile-summary: calendar panel missing date tooltips")
	}
	// Panel 2: language names inside bar rows.
	if !strings.Contains(s, "Go") {
		t.Error("profile-summary: top-langs panel missing language name")
	}
	// Panel 3: grade level letter + Total metric label.
	if !strings.Contains(s, ">"+d.Grade.Level+"<") {
		t.Error("profile-summary: grade ring missing")
	}
	if !strings.Contains(s, "Total") || !strings.Contains(s, "Daily avg") {
		t.Error("profile-summary: metric labels missing")
	}
}

// Needs() gates the handler's DB fetches — kinds MUST accurately declare
// what optional Data they consume, or fetches get skipped and the renderer
// hits nil.
func TestNeedsMatchesRendererUsage(t *testing.T) {
	cases := []struct {
		kind string
		want Requirements
	}{
		{"stats-card", Requirements{}},
		{"stats-card-with-grade", Requirements{Grade: true}},
		{"top-langs", Requirements{}},
		{"top-projects", Requirements{}},
		{"badge", Requirements{}},
		{"activity-heatmap", Requirements{}},
		{"punchcard", Requirements{Punchcard: true}},
		{"momentum", Requirements{Momentum: true}},
		{"profile-summary", Requirements{Grade: true}},
	}
	for _, tc := range cases {
		if got := Needs(tc.kind); got != tc.want {
			t.Errorf("Needs(%s) = %+v, want %+v", tc.kind, got, tc.want)
		}
	}
}

func TestRenderUnknownKind(t *testing.T) {
	if _, err := Render("nope", dataFixture(), Options{}); err == nil {
		t.Error("unknown kind should error")
	}
	if IsKind("nope") {
		t.Error("IsKind(nope) should be false")
	}
}

// Drift guard: the BE whitelist must match the FE catalog
// (web/src/features/widgets/catalog.ts) — update BOTH when adding a kind.
func TestKindsMatchFrontendCatalog(t *testing.T) {
	want := []string{
		"activity-heatmap",
		"badge",
		"momentum",
		"profile-summary",
		"punchcard",
		"stats-card",
		"stats-card-with-grade",
		"top-langs",
		"top-projects",
	}
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
