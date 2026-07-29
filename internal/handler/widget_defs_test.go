// widget_defs_ginkgo_test.go — ginkgo mirror of widget_defs_test.go (gaka-6jm.13).
// 1:1 case map (4 stdlib TestXxx):
//
//	TestRenderCustomWidget_ScrubberFiltersHiddenLang_Gaka6jm13Regression
//	  → RenderCustomWidget scrubber > "top-langs: hidden language absent from SVG body"
//	TestRenderCustomWidget_ScrubberFiltersMomentumProjectName_Gaka6jm13Regression
//	  → RenderCustomWidget scrubber > "momentum: hidden project absent from SVG body"
//	TestRenderCustomWidget_ScopeProjectHidden_Returns404_Gaka6jm13Regression
//	  → RenderCustomWidget scope gate > "unknown def-id → 404; valid def + unrelated hide → 200"
//	TestRenderCustomWidget_ScrubberFiltersHiddenLangInPayload
//	  → RenderCustomWidget scrubber > "top-langs: hidden language absent from bar rows (>Label< tighter scan)"
package handler_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/widget"
)

// routerWithWidgetDefsG — mirror of the stdlib routerWithWidgetDefs.
type createDefRespG struct {
	DefID string `json:"defId"`
	URL   string `json:"url"`
}

// mustMarshalDefG mirrors mustMarshalDef.
func mustMarshalDefG(d widget.Def) []byte {
	b, err := json.Marshal(d)
	Expect(err).NotTo(HaveOccurred())
	return b
}

// mintTopLangsDefG / mintMomentumDefG mirror the stdlib helpers.
func mintTopLangsDefG(e http.Handler, token, name string) createDefRespG {
	spec := mustMarshalDefG(widget.Def{
		Layout: widget.Layout1,
		Title:  "langs",
		Panels: []widget.Panel{{Kind: widget.PanelTopLangs}},
	})
	rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token,
		map[string]any{"name": name, "spec": json.RawMessage(spec)})
	Expect(rec).To(testutil.HaveStatus(http.StatusOK), "create widget-def %q: body=%s", name, rec.Body.String())
	var out createDefRespG
	Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
	Expect(out.DefID).NotTo(BeEmpty(), "empty defId: %s", rec.Body.String())
	return out
}

func mintMomentumDefG(e http.Handler, token, name string) createDefRespG {
	spec := mustMarshalDefG(widget.Def{
		Layout: widget.Layout1,
		Title:  "momentum",
		Panels: []widget.Panel{{Kind: widget.PanelMomentum}},
	})
	rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token,
		map[string]any{"name": name, "spec": json.RawMessage(spec)})
	Expect(rec).To(testutil.HaveStatus(http.StatusOK), "create momentum def: body=%s", rec.Body.String())
	var out createDefRespG
	Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
	return out
}

// fetchDefSvgG returns the SVG body string (unauthenticated public GET).
func fetchDefSvgG(e http.Handler, defID string, params string) string {
	target := "/widget/svg/" + defID + "/named"
	if params != "" {
		target += "?" + params
	}
	rec := doJSONReqG(e, http.MethodGet, target, "", nil)
	Expect(rec).To(testutil.HaveStatus(http.StatusOK), "fetch widget-def svg: body=%s", rec.Body.String())
	ct := rec.Header().Get("Content-Type")
	Expect(strings.HasPrefix(ct, "image/svg+xml")).To(BeTrue(),
		"Content-Type = %q, want image/svg+xml", ct)
	return rec.Body.String()
}

var _ = Describe("RenderCustomWidget scrubber (gaka-6jm.13 regression)", func() {
	It("filters hidden language from the top-langs SVG anywhere in body", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("wd_scrub_lang_g")

		start := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
		sd := hz.Seeder(user)
		sd.Block(testutil.HB{Project: "proj-a", Language: "TypeScript", Editor: "vim"}, start, 20, 60)
		sd.Block(testutil.HB{Project: "proj-b", Language: "Go", Editor: "vim"}, start.Add(time.Hour), 10, 60)
		sd.Block(testutil.HB{Project: "proj-c", Language: "Python", Editor: "vim"}, start.Add(2*time.Hour), 5, 60)
		sd.RefreshRollup(start.Add(-time.Hour))

		def := mintTopLangsDefG(e, token, "langs-scrub-g")

		// Baseline visibility.
		body := fetchDefSvgG(e, def.DefID, "days=30")
		Expect(body).To(ContainSubstring("TypeScript"),
			"baseline: TypeScript should be visible before hide rule")

		// Curate TypeScript hidden.
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "TypeScript",
		})
		Expect(rec.Code).To(BeNumerically("<", 300), "create hide rule: body=%s", rec.Body.String())

		body = fetchDefSvgG(e, def.DefID, "days=30")
		Expect(body).NotTo(ContainSubstring("TypeScript"),
			"PRIVACY LEAK (gaka-6jm.13): WidgetDefSvg missed widget.Scrub — body=\n%s", body)
		Expect(body).To(ContainSubstring(">Go<"),
			"positive control: 'Go' should still render — body=\n%s", body)
	})

	It("filters hidden project from the momentum SVG (ScrubMomentum wiring)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("wd_scrub_mom_g")

		start := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
		sd := hz.Seeder(user)
		sd.Block(testutil.HB{Project: "hakatime", Language: "Go", Editor: "vim"}, start, 20, 60)
		sd.Block(testutil.HB{Project: "public-proj", Language: "Go", Editor: "vim"}, start.Add(time.Hour), 10, 60)
		sd.RefreshRollup(start.Add(-time.Hour))

		def := mintMomentumDefG(e, token, "mom-scrub-g")

		body := fetchDefSvgG(e, def.DefID, "days=30")
		Expect(body).To(ContainSubstring("hakatime"),
			"baseline: hakatime should be visible before hide rule")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "hide", "matchType": "exact", "matchValue": "hakatime",
		})
		Expect(rec.Code).To(BeNumerically("<", 300), "create hide rule: body=%s", rec.Body.String())

		body = fetchDefSvgG(e, def.DefID, "days=30")
		Expect(body).NotTo(ContainSubstring("hakatime"),
			"PRIVACY LEAK (gaka-6jm.13): WidgetDefSvg missed widget.ScrubMomentum — body=\n%s", body)
	})

	It("filters hidden language from the top-langs bar rows (stricter >Label< scan)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("wd_scrub_lang_rows_g")

		start := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
		sd := hz.Seeder(user)
		sd.Block(testutil.HB{Project: "p1", Language: "TypeScript", Editor: "vim"}, start, 20, 60)
		sd.Block(testutil.HB{Project: "p2", Language: "Go", Editor: "vim"}, start.Add(time.Hour), 10, 60)
		sd.Block(testutil.HB{Project: "p3", Language: "Python", Editor: "vim"}, start.Add(2*time.Hour), 5, 60)
		sd.RefreshRollup(start.Add(-time.Hour))

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "TypeScript",
		})
		Expect(rec.Code).To(BeNumerically("<", 300), "create hide rule: body=%s", rec.Body.String())

		def := mintTopLangsDefG(e, token, "langs-rows-g")
		body := fetchDefSvgG(e, def.DefID, "days=30")
		Expect(body).NotTo(ContainSubstring(">TypeScript<"),
			"PRIVACY LEAK (gaka-6jm.13): TypeScript rendered as a bar row — body=\n%s", body)
		Expect(body).To(ContainSubstring(">Go<"),
			"positive control: 'Go' should render as a bar row — body=\n%s", body)
	})
})

var _ = Describe("RenderCustomWidget scope gate (v1 invariants)", func() {
	It("404s on unknown def-id and 200s on a valid def even with an unrelated hide rule", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("wd_scope_gate_g")

		// (1) Unknown def-id → 404.
		rec := doJSONReqG(e, http.MethodGet,
			"/widget/svg/00000000-0000-0000-0000-000000000000/named", "", nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound), "unknown def-id: body=%s", rec.Body.String())

		// (2) Valid def renders even with an unrelated hide rule.
		start := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Hour)
		sd := hz.Seeder(user)
		sd.Block(testutil.HB{Project: "proj-visible", Language: "Go", Editor: "vim"}, start, 5, 60)
		sd.RefreshRollup(start.Add(-time.Hour))

		def := mintTopLangsDefG(e, token, "scope-gate-g")

		rec = doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "hide", "matchType": "exact", "matchValue": "some-hidden-proj",
		})
		Expect(rec.Code).To(BeNumerically("<", 300), "create hide rule: body=%s", rec.Body.String())

		svg := doJSONReqG(e, http.MethodGet, "/widget/svg/"+def.DefID+"/named?days=30", "", nil)
		Expect(svg.Code).To(Equal(http.StatusOK),
			"v1 user-scoped def with unrelated hide rule: body=%s", svg.Body.String())
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
type createDefResp struct {
	DefID string `json:"defId"`
	URL   string `json:"url"`
}
