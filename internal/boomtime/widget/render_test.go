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
//	TestRenderUnknownKind               → It "RenderSpec + IsKind reject unknown kinds"
//	TestKindsMatchFrontendCatalog       → It "Kinds() matches the FE catalog verbatim"
//
// Part B Stage 5 cutover: every RenderSpec/Render(...) call in this file was
// repointed from the deleted legacy Render() to RenderSpec() — renderSpec is
// now the only render path. Kinds() also grew the 3 goal-* kinds (they used
// to render outside Kinds() via the now-deleted IsAlwaysSpecKind).
package widget

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
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
			b, err := RenderSpec(kind, d, Options{Theme: "dark", Subtitle: "last 30 days"})
			Expect(err).NotTo(HaveOccurred(), "RenderSpec(%s)", kind)
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
			b, err := RenderSpec(kind, d, Options{Theme: "dark", Title: `T<i>tle & "stuff"`})
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
		b, err := RenderSpec("top-langs", d, Options{})
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
			b, err := RenderSpec(kind, empty, Options{})
			Expect(err).NotTo(HaveOccurred(), "RenderSpec(%s) on empty payload", kind)
			assertValidXMLG(b)
		}
		b, _ := RenderSpec("stats-card", empty, Options{})
		Expect(strings.Contains(string(b), "No coding activity")).To(BeTrue(),
			"empty stats-card should render the no-data message")
		// The composite is defensive about missing Grade — nil Grade must not panic.
		b, _ = RenderSpec("profile-summary", empty, Options{})
		Expect(strings.Contains(string(b), "No coding activity")).To(BeTrue(),
			"empty profile-summary should render the no-data message")
	})

	It("theme selection picks known themes and falls back to dark for unknown ones", func() {
		d := dataFixture()
		dark, _ := RenderSpec("stats-card", d, Options{Theme: "dark"})
		light, _ := RenderSpec("stats-card", d, Options{Theme: "light"})
		unknown, _ := RenderSpec("stats-card", d, Options{Theme: "hotdog-stand"})
		Expect(strings.Contains(string(dark), themes["dark"].Background)).To(BeTrue(), "dark theme background missing")
		Expect(strings.Contains(string(light), themes["light"].Background)).To(BeTrue(), "light theme background missing")
		Expect(string(unknown)).To(Equal(string(dark)), "unknown theme should fall back to dark")
	})

	It("stats-card-with-grade emits the grade level + ring; nil Grade self-computes without panic", func() {
		d := dataFixture()
		b, err := RenderSpec("stats-card-with-grade", d, Options{})
		Expect(err).NotTo(HaveOccurred())
		s := string(b)
		Expect(strings.Contains(s, ">"+d.Grade.Level+"<")).To(BeTrue(), "grade level %q not rendered", d.Grade.Level)
		Expect(strings.Contains(s, "stroke-dasharray")).To(BeTrue(), "grade ring missing")
		// nil Grade must self-compute (renderer falls back to stats.Grade), not panic.
		d2 := &Data{Payload: d.Payload}
		b2, err := RenderSpec("stats-card-with-grade", d2, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(b2), "stroke-dasharray")).To(BeTrue(), "nil-grade render failed")
	})

	It("top-langs excludes the synthesized Other row from top lists", func() {
		d := dataFixture()
		d.Payload.Languages = append(d.Payload.Languages, model.ResourceStats{
			Name: "Other (5 more)", TotalSeconds: 999999, TotalPct: 90, OtherCount: 5,
		})
		b, err := RenderSpec("top-langs", d, Options{})
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
		b, err := RenderSpec("top-langs", d, Options{})
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
			b, err := RenderSpec(kind, d, Options{})
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
		b, err := RenderSpec("profile-summary", d, Options{Subtitle: "last 30 days"})
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
		// Part B Stage 1 — StatsPayload-derived, except categories-chart which
		// gates the extra category-rows fetch:
		Entry("total-time-stat", "total-time-stat", Requirements{}),
		Entry("daily-avg-stat", "daily-avg-stat", Requirements{}),
		Entry("current-streak-stat", "current-streak-stat", Requirements{}),
		Entry("longest-streak-stat", "longest-streak-stat", Requirements{}),
		Entry("active-days-stat", "active-days-stat", Requirements{}),
		Entry("categories-chart", "categories-chart", Requirements{Categories: true}),
		Entry("editors-chips", "editors-chips", Requirements{}),
		Entry("platforms-chips", "platforms-chips", Requirements{}),
		// Part B Stage 5 — goal-* kinds moved into Kinds()/Needs() proper
		// (they used to render outside the legacy `kinds` map entirely).
		Entry("goal-progress", "goal-progress", Requirements{Goals: true}),
		Entry("goal-ring", "goal-ring", Requirements{Goals: true}),
		Entry("goal-list", "goal-list", Requirements{Goals: true}),
	)
})

var _ = Describe("RenderSpec + IsKind reject unknown kinds", func() {
	It("unknown kind → RenderSpec errors and IsKind returns false", func() {
		_, err := RenderSpec("nope", dataFixture(), Options{})
		Expect(err).To(HaveOccurred(), "unknown kind should error")
		Expect(IsKind("nope")).To(BeFalse(), "IsKind(nope) should be false")
	})
})

// Byte-identical render invariant (gaka-hsj). The public /widget/svg endpoint
// serves these bytes through GitHub camo and other aggressive HTTP caches; any
// non-determinism inside RenderSpec (e.g. someone reaches for time.Now, a
// map-range leak, or a rand.Float call in a primitive) silently invalidates
// every camo snapshot and burns cache entries. Pinning the SHA256 of the
// output for a fixed payload catches that class of regression on the first
// run.
//
// stats-card and top-langs were re-baselined at Part B cutover (Stage 5,
// 2026-08-09) — renderSpec is now the only path; output visually verified
// equivalent to legacy (composite/panel geometry differs from the deleted
// hand-written renderStatsCard/renderTopList, so the bytes changed even
// though the picture didn't). badge is UNCHANGED: renderSpec special-cases
// "badge" to call renderBadge directly (see spec.go), so its bytes are
// byte-for-byte identical to the pre-cutover pin.
//
// If you INTENTIONALLY change the SVG output for any of the three below,
// update BOTH the hash AND this comment's date so the next reviewer knows
// it's a deliberate re-baseline, not drift.
var _ = Describe("RenderSpec bytes are stable for a fixed payload (gaka-hsj)", func() {
	// payloadFixture (defined at the bottom of this file) is intentionally
	// small + deterministic — no time.Now, no random data. Adding TotalDaily
	// series (as dataFixture does for the heatmap twins) would change the
	// hash; the pinned kinds below all render from the plain payload only.
	It("stats-card renders byte-identically across runs (fixed payload)", func() {
		d := &Data{Payload: payloadFixture()}
		want := "e619cd76d559c62764449f5cce8d675afd41f4523698e0a8bdd94ecfe5595aef"
		a, err := RenderSpec("stats-card", d, Options{Theme: "dark", Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		b, err := RenderSpec("stats-card", d, Options{Theme: "dark", Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal(b), "stats-card render is non-deterministic between calls")
		Expect(hashHex(a)).To(Equal(want),
			"stats-card SVG bytes drifted from the pinned SHA256 — either an intentional visual change (update the hash + timestamp above) or an accidental regression. body:\n%s", string(a))
	})

	It("top-langs renders byte-identically across runs (fixed payload)", func() {
		d := &Data{Payload: payloadFixture()}
		want := "940e3787b885177522c6e78135b1b3c4be5e427375a57ee41011c41676b6a7d0"
		a, err := RenderSpec("top-langs", d, Options{Theme: "dark", Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		b, err := RenderSpec("top-langs", d, Options{Theme: "dark", Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal(b), "top-langs render is non-deterministic between calls")
		Expect(hashHex(a)).To(Equal(want),
			"top-langs SVG bytes drifted from the pinned SHA256. body:\n%s", string(a))
	})

	It("badge renders byte-identically across runs (fixed payload)", func() {
		d := &Data{Payload: payloadFixture()}
		want := "71768648b56b832b72c72a633b982a17a543413cdfba0a869a5fd156ccae2438"
		a, err := RenderSpec("badge", d, Options{Theme: "dark", Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		b, err := RenderSpec("badge", d, Options{Theme: "dark", Subtitle: "last 30 days"})
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal(b), "badge render is non-deterministic between calls")
		Expect(hashHex(a)).To(Equal(want),
			"badge SVG bytes drifted from the pinned SHA256. body:\n%s", string(a))
	})
})

// Part B Stage 1 — the stat-tile + chip twins render the same numbers the FE
// tiles compute (streaks/active-days pinned in internal/stats/streaks_test.go
// against grade.ts) and the chip clouds carry the segment entries.
var _ = Describe("Stat-tile + chip twins (Part B Stage 1)", func() {
	It("stat tiles render the payload-derived values", func() {
		d := dataFixture() // DailyTotal {3600,0,3600,3600,0,0,0}
		// compound() drops the last unit (hakatime parity), so an exact-hours
		// total renders "" — use 3h02m so the tile shows "3 hrs".
		d.Payload.TotalSeconds = 3*3600 + 120
		b, _ := RenderSpec("total-time-stat", d, Options{})
		Expect(string(b)).To(ContainSubstring("TOTAL TIME"))
		Expect(string(b)).To(ContainSubstring("3 hrs"))
		b, _ = RenderSpec("daily-avg-stat", d, Options{})
		Expect(string(b)).To(ContainSubstring("DAILY AVG"))
		Expect(string(b)).To(ContainSubstring("25 min")) // compound(1543)
		b, _ = RenderSpec("current-streak-stat", d, Options{})
		Expect(string(b)).To(ContainSubstring(">0D<"), "trailing zeros → current streak 0")
		b, _ = RenderSpec("longest-streak-stat", d, Options{})
		Expect(string(b)).To(ContainSubstring(">2D<"), "longest run is days 3-4")
		b, _ = RenderSpec("active-days-stat", d, Options{})
		Expect(string(b)).To(ContainSubstring(">3/7<"))
		Expect(string(b)).To(ContainSubstring("43% of days active"))
	})

	It("chip clouds render each segment entry with a duration tooltip", func() {
		d := dataFixture()
		b, _ := RenderSpec("editors-chips", d, Options{})
		Expect(string(b)).To(ContainSubstring("vscode"))
		Expect(string(b)).To(ContainSubstring("neovim"))
		Expect(string(b)).To(ContainSubstring("<title>"))
		b, _ = RenderSpec("platforms-chips", d, Options{})
		Expect(string(b)).To(ContainSubstring("darwin"))
		b, _ = RenderSpec("categories-chart", d, Options{})
		Expect(string(b)).To(ContainSubstring("coding"))
		Expect(string(b)).To(ContainSubstring("debugging"))
	})

	It("every Stage-1 kind renders the clean empty state on an empty payload", func() {
		empty := &Data{Payload: &model.StatsPayload{}}
		for kind, msg := range map[string]string{
			"total-time-stat":     "No activity yet",
			"daily-avg-stat":      "No activity yet",
			"current-streak-stat": "No days in range yet",
			"longest-streak-stat": "No days in range yet",
			"active-days-stat":    "No days in range yet",
			"categories-chart":    "No category data yet",
			"editors-chips":       "No editor data yet",
			"platforms-chips":     "No platform data yet",
		} {
			b, err := RenderSpec(kind, empty, Options{})
			Expect(err).NotTo(HaveOccurred(), "RenderSpec(%s) on empty payload", kind)
			Expect(string(b)).To(ContainSubstring(msg), "%s: empty-state message missing", kind)
		}
	})
})

// Drift guard: the BE whitelist must match the FE catalog
// (web/shared/features/widgets/catalog.ts) — update BOTH when adding a kind.
var _ = Describe("Kinds() matches the FE catalog verbatim", func() {
	It("returns the same ordered list as web/shared/features/widgets/catalog.ts", func() {
		want := []string{
			"active-days-stat",
			"activity-heatmap",
			"badge",
			"categories-chart",
			"cumulative-area",
			"current-streak-stat",
			"daily-avg-stat",
			"deep-work",
			"editors-chips",
			"goal-list",
			"goal-progress",
			"goal-ring",
			"heatmap-languages",
			"heatmap-projects",
			"longest-streak-stat",
			"momentum",
			"platforms-chips",
			"profile-summary",
			"punchcard",
			"social-card",
			"stats-card",
			"stats-card-with-grade",
			"top-langs",
			"top-projects",
			"total-time-stat",
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
	// Part B Stage 1: segments for the chip-cloud twins. Added here (NOT in
	// payloadFixture) so the SHA-pinned kinds keep rendering from the exact
	// pre-Stage-1 payload bytes.
	p.Editors = []model.ResourceStats{
		{Name: "vscode", TotalSeconds: 7200, TotalPct: 66.7},
		{Name: "neovim", TotalSeconds: 3600, TotalPct: 33.3},
	}
	p.Platforms = []model.ResourceStats{
		{Name: "darwin", TotalSeconds: 10800, TotalPct: 100},
	}
	p.Categories = []model.ResourceStats{
		{Name: "coding", TotalSeconds: 9000, TotalPct: 83.3},
		{Name: "debugging", TotalSeconds: 1800, TotalPct: 16.7},
	}
	return &Data{Payload: p, Grade: &g, Punchcard: &pc, Momentum: &m, Sessions: &s}
}
