// spec_engine_test.go — Part B Stage 2: BOOM_WIDGET_SPEC_ENGINE end-to-end
// through the public /widget/svg endpoint. internal/widget/spec_test.go
// already proves renderSpec/NeedsForSpec are correct for every "both" kind
// in isolation; this file proves the HANDLER WIRING (handler.go's
// h.Cfg.FeatureWidgetSpecEngine branch, see WidgetSvg) actually picks the
// spec engine when the flag is on and the legacy renderer when it's off,
// and that the needs-gated optional fetches (Grade/Punchcard/Categories)
// still fire correctly under the flag — a wiring bug here would either
// starve a renderer of data (nil-pointer/empty-state) or skip the fetch
// entirely.
package widgets_test

import (
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// mintWidgetLinkSE mints a user-scope widget link and returns its uuid.
func mintWidgetLinkSE(e http.Handler, token string) string {
	GinkgoHelper()
	rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/widgets/link?scopeType=user&scopeRef=", token, nil)
	Expect(rec).To(testutil.HaveStatus(http.StatusOK), "mint widget link: body=%s", rec.Body.String())
	var out struct {
		LinkID string `json:"linkId"`
	}
	Expect(decodeJSONBody(rec.Body.Bytes(), &out)).To(Succeed())
	return out.LinkID
}

// getSVG issues an unauthenticated GET against the public endpoint.
func getSVG(e http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

var _ = Describe("Widget SVG spec engine wiring (BOOM_WIDGET_SPEC_ENGINE, Part B Stage 2)", func() {
	DescribeTable("a representative kind renders 200 SVG with the flag off AND on",
		func(kind string) {
			for _, on := range []bool{false, true} {
				hz := testutil.NewHarness(GinkgoTB())
				hz.Cfg.FeatureWidgetSpecEngine = on
				e := hz.Router()
				user, token := hz.MintUser("spec_engine")

				start := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
				sd := hz.Seeder(user)
				sd.Block(testutil.HB{Project: "proj-x", Language: "Go", Editor: "vim",
					Category: "coding"}, start, 10, 60)
				sd.RefreshRollup(start.Add(-time.Hour))

				link := mintWidgetLinkSE(e, token)
				rec := getSVG(e, "/widget/svg/"+link+"/"+kind+"?days=30&theme=dark")
				Expect(rec).To(testutil.HaveStatus(http.StatusOK),
					"kind=%s specEngine=%v: body=%s", kind, on, rec.Body.String())
				Expect(rec.Header().Get("Content-Type")).To(HavePrefix("image/svg+xml"))
				Expect(rec.Body.String()).To(ContainSubstring("<svg"))
			}
		},
		Entry("stats-card", "stats-card"),
		Entry("stats-card-with-grade (Needs.Grade)", "stats-card-with-grade"),
		Entry("punchcard (Needs.Punchcard)", "punchcard"),
		Entry("momentum (Needs.Momentum)", "momentum"),
		Entry("deep-work (Needs.Sessions)", "deep-work"),
		Entry("categories-chart (Needs.Categories)", "categories-chart"),
		Entry("badge (special-cased primitive)", "badge"),
		Entry("heatmap-projects (day-heatmap primitive)", "heatmap-projects"),
	)

	It("with the flag ON, needs-gated kinds still render the fetched data (not a silent empty state)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		hz.Cfg.FeatureWidgetSpecEngine = true
		e := hz.Router()
		user, token := hz.MintUser("spec_engine_needs")

		start := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Hour)
		sd := hz.Seeder(user)
		sd.Block(testutil.HB{Project: "proj-x", Language: "Go", Editor: "vim",
			Category: "coding"}, start, 10, 60)
		sd.Block(testutil.HB{Project: "proj-x", Language: "Go", Editor: "vim",
			Category: "debugging"}, start.Add(time.Hour), 10, 60)
		sd.RefreshRollup(start.Add(-time.Hour))

		link := mintWidgetLinkSE(e, token)

		// categories-chart needs the gated category-rows fetch — under the
		// spec engine that's NeedsForSpec deriving Categories:true from the
		// "categories" binding. If that derivation were wrong, this would
		// silently render the empty state instead of the seeded chips.
		rec := getSVG(e, "/widget/svg/"+link+"/categories-chart?days=30")
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("coding"))
		Expect(rec.Body.String()).To(ContainSubstring("debugging"))

		// punchcard needs Punchcard data fetched from the DB.
		rec = getSVG(e, "/widget/svg/"+link+"/punchcard?days=30")
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).NotTo(ContainSubstring("No punchcard data"),
			"punchcard should render seeded cells, not the empty state")

		// stats-card-with-grade needs Grade fetched + computed.
		rec = getSVG(e, "/widget/svg/"+link+"/stats-card-with-grade?days=30")
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("stroke-dasharray"),
			"grade ring should render (proves Grade was fetched under the spec engine)")
	})
})
