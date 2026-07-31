// render_ginkgo_test.go — ginkgo mirror of render_test.go (gaka-0vp).
// 1:1 case map (12 stdlib TestXxx → 12 top-level nodes; some table-heavy tests
// become DescribeTables to preserve per-case reporting):
//
//	TestRenderAllKindsWellFormed        → It "every kind renders well-formed, camo-safe SVG" (iterates Kinds() via By())
//	TestRenderEscapesUserStrings        → DescribeTable "escapes user-supplied strings" (one Entry per relevant kind)
//	TestRenderTruncatesLongNames        → It "top-langs truncates long labels + keeps full name in <title>"
//	TestRenderEmptyPayload              → It "every kind survives an empty payload + emits the no-data message"
//	TestRenderThemeSelection            → It "theme selection picks known themes and falls back to dark"
//	TestRenderGradeRing                 → It "stats-card-with-grade emits the grade level + ring; nil Grade self-computes"
//	TestRenderSkipsSynthesizedOtherRow  → It "top-langs excludes the synthesized Other row"
//	TestRenderPercentagesSumTo100       → It "top-langs renormalizes shown percentages to ~100"
//	TestRenderAnimationsAndTooltips     → DescribeTable "each kind ships @keyframes + <title> tooltips"
//	TestRenderProfileSummaryPanels      → It "profile-summary renders every panel + metric labels"
//	TestNeedsMatchesRendererUsage       → DescribeTable "Needs() declares what each renderer consumes"
//	TestRenderUnknownKind               → It "Render + IsKind reject unknown kinds"
//	TestKindsMatchFrontendCatalog       → It "Kinds() matches the FE catalog verbatim"
package widget

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
)

// hashHex is the pinning-test helper: sha256 hex of the raw render bytes.
// Kept in this file (not testutil) because the pinning invariant is scoped to
// this package — an internal API drift, not an HTTP-layer one.
func hashHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

var _ = Describe("Render", func() {
	It("every kind renders well-formed, camo-safe SVG", func() {
		d := dataFixture()
		for _, kind := range Kinds() {
			By("rendering " + kind)
			b, err := Render(kind, d, Options{Theme: "dark", Subtitle: "last 30 days"})
			Expect(err).NotTo(HaveOccurred(), "Render(%s)", kind)
			assertValidXMLG(b)
			s := string(b)
			Expect(strings.HasPrefix(strings.TrimSpace(s), "<svg")).To(BeTrue(), "%s: output does not start with <svg", kind)
			// Camo-safety: no scripts, no external references.
			for _, banned := range []string{"<script", "https://", "url(http", "@import"} {
				Expect(strings.Contains(s, banned)).To(BeFalse(), "%s: output contains banned token %q", kind, banned)
			}
			// The xmlns is the one allowed URL-ish string.
			Expect(strings.Count(s, "http://www.w3.org/2000/svg")).To(Equal(strings.Count(s, "http://")),
				"%s: contains an http:// reference beyond the svg xmlns", kind)
		}
	})

	DescribeTable("escapes user-supplied strings so no <script>/<img>/<i> leaks into the SVG",
		func(kind string) {
			d := dataFixture()
			d.Payload.Languages = []model.ResourceStats{
				{Name: `<script>alert(1)</script>`, TotalSeconds: 7200, TotalPct: 50},
				{Name: `A&B "quoted" <lang>`, TotalSeconds: 3600, TotalPct: 25},
			}
			d.Payload.Projects = []model.ResourceStats{
				{Name: `evil<img onerror=alert(1)>`, TotalSeconds: 3600, TotalPct: 100},
			}
			b, err := Render(kind, d, Options{Theme: "dark", Title: `T<i>tle & "stuff"`})
			Expect(err).NotTo(HaveOccurred())
			assertValidXMLG(b)
			s := string(b)
			Expect(strings.Contains(s, "<script")).To(BeFalse(), "%s: unescaped <script> leaked", kind)
			Expect(strings.Contains(s, "<img")).To(BeFalse(), "%s: unescaped <img> leaked", kind)
			Expect(strings.Contains(s, "<i>")).To(BeFalse(), "%s: unescaped <i> leaked", kind)
		},
		Entry("stats-card", "stats-card"),
		Entry("top-langs", "top-langs"),
		Entry("top-projects", "top-projects"),
		Entry("profile-summary", "profile-summary"),
	)

	It("top-langs truncates long labels + keeps full name in <title> tooltip", func() {
		long := strings.Repeat("verylongname", 10)
		d := dataFixture()
		d.Payload.Languages = []model.ResourceStats{{Name: long, TotalSeconds: 3600, TotalPct: 100}}
		b, err := Render("top-langs", d, Options{})
		Expect(err).NotTo(HaveOccurred())
		s := string(b)
		// The label is truncated → ellipsis present. The FULL name is kept
		// inside the <title> tooltip so users can see it on hover — appears
		// exactly ONCE (in the tooltip), never repeated in the rendered label.
		Expect(strings.Contains(s, "…")).To(BeTrue(), "expected an ellipsis after label truncation")
		Expect(strings.Count(s, long)).To(Equal(1), "long name should appear exactly once (in the <title> tooltip)")
	})

	It("every kind survives an empty payload + emits the no-data message where expected", func() {
		empty := &Data{Payload: &model.StatsPayload{}}
		for _, kind := range Kinds() {
			b, err := Render(kind, empty, Options{})
			Expect(err).NotTo(HaveOccurred(), "Render(%s) on empty payload", kind)
			assertValidXMLG(b)
		}
		b, _ := Render("stats-card", empty, Options{})
		Expect(strings.Contains(string(b), "No coding activity")).To(BeTrue(),
			"empty stats-card should render the no-data message")
		// The composite is defensive about missing Grade — nil Grade must not panic.
		b, _ = Render("profile-summary", empty, Options{})
		Expect(strings.Contains(string(b), "No coding activity")).To(BeTrue(),
			"empty profile-summary should render the no-data message")
	})

	It("theme selection picks known themes and falls back to dark for unknown ones", func() {
		d := dataFixture()
		dark, _ := Render("stats-card", d, Options{Theme: "dark"})
		light, _ := Render("stats-card", d, Options{Theme: "light"})
		unknown, _ := Render("stats-card", d, Options{Theme: "hotdog-stand"})
		Expect(strings.Contains(string(dark), themes["dark"].Background)).To(BeTrue(), "dark theme background missing")
		Expect(strings.Contains(string(light), themes["light"].Background)).To(BeTrue(), "light theme background missing")
		Expect(string(unknown)).To(Equal(string(dark)), "unknown theme should fall back to dark")
	})

	It("stats-card-with-grade emits the grade level + ring; nil Grade self-computes without panic", func() {
		d := dataFixture()
		b, err := Render("stats-card-with-grade", d, Options{})
		Expect(err).NotTo(HaveOccurred())
		s := string(b)
		Expect(strings.Contains(s, ">"+d.Grade.Level+"<")).To(BeTrue(), "grade level %q not rendered", d.Grade.Level)
		Expect(strings.Contains(s, "stroke-dasharray")).To(BeTrue(), "grade ring missing")
		// nil Grade must self-compute (renderer falls back to stats.Grade), not panic.
		d2 := &Data{Payload: d.Payload}
		b2, err := Render("stats-card-with-grade", d2, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(b2), "stroke-dasharray")).To(BeTrue(), "nil-grade render failed")
	})

	It("top-langs excludes the synthesized Other row from top lists", func() {
		d := dataFixture()
		d.Payload.Languages = append(d.Payload.Languages, model.ResourceStats{
			Name: "Other (5 more)", TotalSeconds: 999999, TotalPct: 90, OtherCount: 5,
		})
		b, err := Render("top-langs", d, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(b), "Other (5 more)")).To(BeFalse(),
			"synthesized Other row should be excluded from top lists")
	})

	// Percentages on a card are normalized over the SHOWN entries (sum ~100),
	// not the payload-global TotalPct (which sums short when the tail is cut).
	It("top-langs renormalizes shown percentages so they sum to ~100", func() {
		d := dataFixture()
		d.Payload.Languages = []model.ResourceStats{
			// Global pcts sum to 60 (a dropped tail holds the rest) — the card must
			// renormalize over what it shows: 75.0% + 25.0%.
			{Name: "Go", TotalSeconds: 9000, TotalPct: 45},
			{Name: "TypeScript", TotalSeconds: 3000, TotalPct: 15},
		}
		b, err := Render("top-langs", d, Options{})
		Expect(err).NotTo(HaveOccurred())
		s := string(b)
		Expect(strings.Contains(s, "75.0%")).To(BeTrue(), "expected renormalized 75.0%%")
		Expect(strings.Contains(s, "25.0%")).To(BeTrue(), "expected renormalized 25.0%%")
		Expect(strings.Contains(s, "45.0%")).To(BeFalse(), "payload-global TotalPct leaked into the card")
	})

	// Embeds animate (CSS keyframes play inside <img>/camo) and carry native
	// <title> tooltips (hover on direct view / <object> embeds).
	DescribeTable("each kind ships @keyframes + <title> tooltips",
		func(kind string) {
			d := dataFixture()
			b, err := Render(kind, d, Options{})
			Expect(err).NotTo(HaveOccurred())
			s := string(b)
			Expect(strings.Contains(s, "@keyframes")).To(BeTrue(), "%s: missing entrance animations", kind)
			Expect(strings.Contains(s, "<title>")).To(BeTrue(), "%s: missing native <title> hover tooltips", kind)
		},
		Entry("stats-card", "stats-card"),
		Entry("top-langs", "top-langs"),
		Entry("top-projects", "top-projects"),
		Entry("profile-summary", "profile-summary"),
		Entry("activity-heatmap", "activity-heatmap"),
		Entry("punchcard", "punchcard"),
		Entry("momentum", "momentum"),
	)

	// The composite renders all three panels — calendar cells + language bars +
	// grade ring. If any one disappears the layout has silently regressed.
	It("profile-summary renders every panel + metric labels", func() {
		d := dataFixture()
		b, err := Render("profile-summary", d, Options{Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		s := string(b)
		// Panel 1: calendar cells carry per-day <title> tooltips with date labels.
		Expect(strings.Contains(s, "2026")).To(BeTrue(), "profile-summary: calendar panel missing date tooltips")
		// Panel 2: language names inside bar rows.
		Expect(strings.Contains(s, "Go")).To(BeTrue(), "profile-summary: top-langs panel missing language name")
		// Panel 3: grade level letter + Total metric label.
		Expect(strings.Contains(s, ">"+d.Grade.Level+"<")).To(BeTrue(), "profile-summary: grade ring missing")
		Expect(strings.Contains(s, "Total")).To(BeTrue(), "profile-summary: 'Total' metric label missing")
		Expect(strings.Contains(s, "Daily avg")).To(BeTrue(), "profile-summary: 'Daily avg' metric label missing")
	})
})

// Needs() gates the handler's DB fetches — kinds MUST accurately declare
// what optional Data they consume, or fetches get skipped and the renderer
// hits nil.
var _ = Describe("Needs", func() {
	DescribeTable("declares what each renderer consumes",
		func(kind string, want Requirements) {
			Expect(Needs(kind)).To(Equal(want))
		},
		Entry("stats-card", "stats-card", Requirements{}),
		Entry("stats-card-with-grade", "stats-card-with-grade", Requirements{Grade: true}),
		Entry("top-langs", "top-langs", Requirements{}),
		Entry("top-projects", "top-projects", Requirements{}),
		Entry("badge", "badge", Requirements{}),
		Entry("activity-heatmap", "activity-heatmap", Requirements{}),
		Entry("punchcard", "punchcard", Requirements{Punchcard: true}),
		Entry("momentum", "momentum", Requirements{Momentum: true}),
		Entry("profile-summary", "profile-summary", Requirements{Grade: true}),
		Entry("cumulative-area", "cumulative-area", Requirements{}),
		Entry("deep-work", "deep-work", Requirements{Sessions: true}),
		Entry("heatmap-projects", "heatmap-projects", Requirements{}),
		Entry("heatmap-languages", "heatmap-languages", Requirements{}),
	)
})

var _ = Describe("Render + IsKind reject unknown kinds", func() {
	It("unknown kind → Render errors and IsKind returns false", func() {
		_, err := Render("nope", dataFixture(), Options{})
		Expect(err).To(HaveOccurred(), "unknown kind should error")
		Expect(IsKind("nope")).To(BeFalse(), "IsKind(nope) should be false")
	})
})

// Byte-identical render invariant (gaka-hsj). The public /widget/svg endpoint
// serves these bytes through GitHub camo and other aggressive HTTP caches; any
// non-determinism inside Render (e.g. someone reaches for time.Now, a map-range
// leak, or a rand.Float call in a primitive) silently invalidates every camo
// snapshot and burns cache entries. Pinning the SHA256 of the output for a
// fixed payload catches that class of regression on the first run.
//
// The hashes below were captured on 2026-07-31; if you INTENTIONALLY change the
// SVG output for stats-card / top-langs / badge, update BOTH the hash AND the
// timestamp above so the next reviewer knows this is a deliberate re-baseline.
var _ = Describe("Render bytes are stable for a fixed payload (gaka-hsj)", func() {
	// payloadFixture (defined at the bottom of this file) is intentionally
	// small + deterministic — no time.Now, no random data. Adding TotalDaily
	// series (as dataFixture does for the heatmap twins) would change the
	// hash; the pinned kinds below all render from the plain payload only.
	It("stats-card renders byte-identically across runs (fixed payload)", func() {
		d := &Data{Payload: payloadFixture()}
		want := "506119adee7341d4cc5656adb190e8f08206bc562d486ec2e75b5997088dd57a"
		a, err := Render("stats-card", d, Options{Theme: "dark", Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		b, err := Render("stats-card", d, Options{Theme: "dark", Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal(b), "stats-card render is non-deterministic between calls")
		Expect(hashHex(a)).To(Equal(want),
			"stats-card SVG bytes drifted from the pinned SHA256 — either an intentional visual change (update the hash + timestamp above) or an accidental regression. body:\n%s", string(a))
	})

	It("top-langs renders byte-identically across runs (fixed payload)", func() {
		d := &Data{Payload: payloadFixture()}
		want := "75f22747f9d335ad284f63bf8b68f6b0e7bd223c87ead5f4260a49a3c6c720b4"
		a, err := Render("top-langs", d, Options{Theme: "dark", Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		b, err := Render("top-langs", d, Options{Theme: "dark", Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal(b), "top-langs render is non-deterministic between calls")
		Expect(hashHex(a)).To(Equal(want),
			"top-langs SVG bytes drifted from the pinned SHA256. body:\n%s", string(a))
	})

	It("badge renders byte-identically across runs (fixed payload)", func() {
		d := &Data{Payload: payloadFixture()}
		want := "71768648b56b832b72c72a633b982a17a543413cdfba0a869a5fd156ccae2438"
		a, err := Render("badge", d, Options{Theme: "dark", Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		b, err := Render("badge", d, Options{Theme: "dark", Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal(b), "badge render is non-deterministic between calls")
		Expect(hashHex(a)).To(Equal(want),
			"badge SVG bytes drifted from the pinned SHA256. body:\n%s", string(a))
	})
})

// Drift guard: the BE whitelist must match the FE catalog
// (web/src/features/widgets/catalog.ts) — update BOTH when adding a kind.
var _ = Describe("Kinds() matches the FE catalog verbatim", func() {
	It("returns the same ordered list as web/src/features/widgets/catalog.ts", func() {
		want := []string{
			"activity-heatmap",
			"badge",
			"cumulative-area",
			"deep-work",
			"heatmap-languages",
			"heatmap-projects",
			"momentum",
			"profile-summary",
			"punchcard",
			"stats-card",
			"stats-card-with-grade",
			"top-langs",
			"top-projects",
		}
		Expect(Kinds()).To(Equal(want))
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
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
	s := model.SessionsPayload{
		Summary: model.SessionSummary{
			Count: 12, TotalSeconds: 43200, AvgSeconds: 3600,
			MaxSeconds: 7200, MedianSeconds: 2400,
		},
		Daily: []model.SessionDaily{
			{Date: "2026-06-01", Sessions: 3, TotalSeconds: 5400, LongestSeconds: 3600},
			{Date: "2026-06-02", Sessions: 2, TotalSeconds: 3600, LongestSeconds: 2400},
			{Date: "2026-06-03", Sessions: 5, TotalSeconds: 12600, LongestSeconds: 4200},
		},
	}
	// Give the payload Languages/Projects per-day series so heatmap-* twins
	// have real cells to draw.
	p.Languages[0].TotalDaily = []int64{3600, 1800, 5400, 0, 3600, 2400, 0}
	p.Languages[1].TotalDaily = []int64{0, 1800, 3600, 1800, 0, 0, 3600}
	p.Projects[0].TotalDaily = []int64{3600, 3600, 3600, 1800, 3600, 2400, 3600}
	return &Data{Payload: p, Grade: &g, Punchcard: &pc, Momentum: &m, Sessions: &s}
}
