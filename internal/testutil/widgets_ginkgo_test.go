// widgets_ginkgo_test.go — ginkgo mirror of widgets_test.go (gaka-0vp).
// 1:1 case map (8 stdlib TestXxx; the SVG public-render test's 5 kind
// subtests each become an Entry in a DescribeTable):
//   TestWidgetLinkMintIsIdempotent          → Widget Links > "mint is idempotent per scope"
//   TestWidgetLinkScopeOwnership            → Widget Links > "scope ownership + bad scopeType + unknown space"
//   TestWidgetSvgPublicRender               → Widget SVG > 5-entry DescribeTable per kind + 1 It for Go-in-langs
//   TestWidgetSvgErrors                     → Widget SVG > "bad uuid / unknown uuid / unknown kind"
//   TestWidgetSvgHiddenLeak                 → Widget SVG > "curation-hidden language never leaks"
//   TestWidgetLinkList                      → Widget Links > "list returns the minted link"
//   TestWidgetLinkTracksHitsAndOrigins      → Widget Links > "tracks last_used_at + origin counts"
//   TestWidgetLinkRoll                      → Widget Links > "roll mints new uuid, old id 404s, cross-owner 404"
//   TestWidgetSvgDaysClamped                → Widget SVG > "absurd days values clamp"
package testutil_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// widgetLinkRespG mirrors widgetLinkResp from the stdlib file.
type widgetLinkRespG struct {
	WidgetBaseURL string `json:"widgetBaseUrl"`
	LinkID        string `json:"linkId"`
}

// mintWidgetLinkG is the ginkgo variant of mintWidgetLink.
func mintWidgetLinkG(e http.Handler, token, scopeType, scopeRef string) widgetLinkRespG {
	GinkgoHelper()
	rec := doG(e, "GET",
		fmt.Sprintf("/api/v1/users/current/widgets/link?scopeType=%s&scopeRef=%s", scopeType, scopeRef),
		token, nil)
	Expect(rec.Code).To(Equal(http.StatusOK),
		"mint widget link: status %d body=%s", rec.Code, rec.Body.String())
	var out widgetLinkRespG
	decodeG(rec, &out)
	return out
}

var _ = Describe("Widget Links", func() {
	It("mint is idempotent per (owner, scope): repeated mints return the same uuid", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		_, token := hz.MintUser("widget_mint")

		a := mintWidgetLinkG(e, token, "user", "")
		b := mintWidgetLinkG(e, token, "user", "")
		Expect(b.LinkID).To(Equal(a.LinkID), "re-mint must not change the uuid")
		Expect(a.WidgetBaseURL).To(ContainSubstring("/widget/svg/" + a.LinkID))
	})

	It("scope ownership: cross-owner 404 + bad scopeType 400 + unknown space 404", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		userA, tokenA := hz.MintUser("widget_owner_a")
		_, tokenB := hz.MintUser("widget_owner_b")

		hz.Seeder(userA).Projects("secret-proj")

		// B cannot mint a link for A's project.
		rec := doG(e, "GET",
			"/api/v1/users/current/widgets/link?scopeType=project&scopeRef=secret-proj",
			tokenB, nil)
		Expect(rec.Code).To(Equal(http.StatusNotFound), "cross-owner project mint")

		// A can.
		mintWidgetLinkG(e, tokenA, "project", "secret-proj")

		// Unknown scopeType → 400.
		rec = doG(e, "GET",
			"/api/v1/users/current/widgets/link?scopeType=galaxy&scopeRef=x",
			tokenA, nil)
		Expect(rec.Code).To(Equal(http.StatusBadRequest), "bad scopeType")

		// Unknown space id → 404.
		rec = doG(e, "GET",
			"/api/v1/users/current/widgets/link?scopeType=space&scopeRef=999999",
			tokenA, nil)
		Expect(rec.Code).To(Equal(http.StatusNotFound), "unknown space mint")
	})

	It("list returns exactly the one minted link (user-scope has empty scopeName)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		_, token := hz.MintUser("widget_list")

		link := mintWidgetLinkG(e, token, "user", "")

		var list struct {
			Links []struct {
				LinkID    string `json:"linkId"`
				ScopeType string `json:"scopeType"`
				ScopeName string `json:"scopeName"`
			} `json:"links"`
		}
		rec := doG(e, "GET", "/api/v1/users/current/widgets/links", token, nil)
		decodeG(rec, &list)
		Expect(list.Links).To(HaveLen(1))
		Expect(list.Links[0].LinkID).To(Equal(link.LinkID))
		Expect(list.Links[0].ScopeName).To(BeEmpty(), "user-scope has no scopeName")
	})

	It("tracks last_used_at + merges origins (Referer or 'direct') into bounded set", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		_, token := hz.MintUser("widget_hits")

		link := mintWidgetLinkG(e, token, "user", "")

		fire := func(ref string) {
			req := httptest.NewRequest("GET", "/widget/svg/"+link.LinkID+"/stats-card", nil)
			if ref != "" {
				req.Header.Set("Referer", ref)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(200), "SVG hit")
		}
		fire("https://github.com/DJ/repo")
		fire("https://github.com/DJ/repo")
		fire("https://blog.example.com/post")
		fire("") // direct

		var list struct {
			Links []struct {
				LinkID     string     `json:"linkId"`
				LastUsedAt *time.Time `json:"lastUsedAt"`
				Origins    []struct {
					Origin string `json:"origin"`
					Count  int    `json:"count"`
				} `json:"origins"`
			} `json:"links"`
		}
		rec := doG(e, "GET", "/api/v1/users/current/widgets/links", token, nil)
		decodeG(rec, &list)
		Expect(list.Links).To(HaveLen(1))
		got := list.Links[0]
		Expect(got.LastUsedAt).NotTo(BeNil(), "last_used_at should be set after a fetch")
		origins := map[string]int{}
		for _, o := range got.Origins {
			origins[o.Origin] = o.Count
		}
		Expect(origins["https://github.com/DJ/repo"]).To(Equal(2))
		Expect(origins["https://blog.example.com/post"]).To(Equal(1))
		Expect(origins["direct"]).To(Equal(1))
	})

	It("roll mints a new uuid for the same scope; old id 404s on public endpoint; cross-owner roll 404s", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		_, tokenA := hz.MintUser("widget_roll_a")
		_, tokenB := hz.MintUser("widget_roll_b")

		orig := mintWidgetLinkG(e, tokenA, "user", "")

		// B cannot roll A's link.
		rec := doG(e, "POST", "/api/v1/users/current/widgets/link/"+orig.LinkID+"/roll", tokenB, nil)
		Expect(rec.Code).To(Equal(http.StatusNotFound), "cross-owner roll")

		rec = doG(e, "POST", "/api/v1/users/current/widgets/link/"+orig.LinkID+"/roll", tokenA, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), "body=%s", rec.Body.String())
		var rolled widgetLinkRespG
		decodeG(rec, &rolled)
		Expect(rolled.LinkID).NotTo(Equal(orig.LinkID), "roll must mint a new uuid")

		// Old id → 404 on the public endpoint.
		rec = doG(e, "GET", "/widget/svg/"+orig.LinkID+"/stats-card", "", nil)
		Expect(rec.Code).To(Equal(http.StatusNotFound), "old link post-roll")

		// New id → 200.
		rec = doG(e, "GET", "/widget/svg/"+rolled.LinkID+"/stats-card", "", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), "new link post-roll")

		// List still shows exactly one link, the new id.
		var list struct {
			Links []struct{ LinkID, ScopeType, ScopeRef string } `json:"links"`
		}
		rec = doG(e, "GET", "/api/v1/users/current/widgets/links", tokenA, nil)
		decodeG(rec, &list)
		Expect(list.Links).To(HaveLen(1))
		Expect(list.Links[0].LinkID).To(Equal(rolled.LinkID))
	})
})

var _ = Describe("Widget SVG", func() {
	// Public render matrix: every kind must return SVG, correct content-type,
	// max-age=300 cache header, and a <svg> body. Mirrors the 5-way t.Run
	// loop in TestWidgetSvgPublicRender.
	DescribeTable("public render is 200 + image/svg+xml + max-age=300 + <svg body",
		func(kind string) {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			user, token := hz.MintUser("widget_render")

			start := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
			hz.Seeder(user).Block(
				testutil.HB{Project: "proj-x", Language: "Go", Editor: "vim"},
				start, 10, 60)
			hz.Seeder(user).RefreshRollup(start.Add(-time.Hour))

			link := mintWidgetLinkG(e, token, "user", "")
			rec := doG(e, "GET",
				"/widget/svg/"+link.LinkID+"/"+kind+"?days=30&theme=dark", "", nil)
			Expect(rec.Code).To(Equal(http.StatusOK), "body=%s", rec.Body.String())
			Expect(rec.Header().Get("Content-Type")).To(HavePrefix("image/svg+xml"))
			Expect(rec.Header().Get("Cache-Control")).To(ContainSubstring("max-age=300"))
			Expect(rec.Body.String()).To(ContainSubstring("<svg"))
		},
		Entry("stats-card", "stats-card"),
		Entry("stats-card-with-grade", "stats-card-with-grade"),
		Entry("top-langs", "top-langs"),
		Entry("top-projects", "top-projects"),
		Entry("badge", "badge"),
	)

	It("top-langs shows the seeded language (>Go<)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		user, token := hz.MintUser("widget_render_langs")

		start := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
		sdr := hz.Seeder(user)
		sdr.Block(testutil.HB{Project: "proj-x", Language: "Go", Editor: "vim"}, start, 10, 60)
		sdr.RefreshRollup(start.Add(-time.Hour))

		link := mintWidgetLinkG(e, token, "user", "")
		rec := doG(e, "GET", "/widget/svg/"+link.LinkID+"/top-langs", "", nil)
		Expect(rec.Body.String()).To(ContainSubstring(">Go<"),
			"top-langs must include seeded language Go")
	})

	It("errors on: bad uuid (400) / unknown uuid (404) / unknown kind (404)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		_, token := hz.MintUser("widget_err")
		link := mintWidgetLinkG(e, token, "user", "")

		rec := doG(e, "GET", "/widget/svg/not-a-uuid/stats-card", "", nil)
		Expect(rec.Code).To(Equal(http.StatusBadRequest), "bad uuid")

		rec = doG(e, "GET",
			"/widget/svg/00000000-0000-0000-0000-000000000000/stats-card", "", nil)
		Expect(rec.Code).To(Equal(http.StatusNotFound), "unknown uuid")

		rec = doG(e, "GET", "/widget/svg/"+link.LinkID+"/not-a-kind", "", nil)
		Expect(rec.Code).To(Equal(http.StatusNotFound), "unknown kind")
	})

	It("PRIVACY GATE: a curation-hidden language MUST NOT appear in the public SVG", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		user, token := hz.MintUser("widget_hidden")

		start := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
		sdr := hz.Seeder(user)
		sdr.Block(testutil.HB{Project: "proj-pub", Language: "Go", Editor: "vim"}, start, 10, 60)
		sdr.Block(testutil.HB{Project: "proj-sec", Language: "SecretLang", Editor: "vim"},
			start.Add(time.Hour), 10, 60)
		sdr.RefreshRollup(start.Add(-time.Hour))

		rec := doG(e, "POST", "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact",
			"matchValue": "SecretLang",
		})
		Expect(rec.Code).To(BeNumerically("<", 300),
			"create hide rule failed: status %d body=%s", rec.Code, rec.Body.String())

		link := mintWidgetLinkG(e, token, "user", "")
		svg := doG(e, "GET", "/widget/svg/"+link.LinkID+"/top-langs?days=30", "", nil)
		Expect(svg.Code).To(Equal(http.StatusOK))
		Expect(svg.Body.String()).NotTo(ContainSubstring("SecretLang"),
			"PRIVACY LEAK: curation-hidden language appears in the public widget SVG")
		Expect(svg.Body.String()).To(ContainSubstring(">Go<"),
			"non-hidden language should still render")
	})

	It("absurd days values (0, -5, 99999, abc) all clamp — endpoint stays 200", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		_, token := hz.MintUser("widget_clamp")
		link := mintWidgetLinkG(e, token, "user", "")

		for _, days := range []string{"0", "-5", "99999", "abc"} {
			rec := doG(e, "GET",
				"/widget/svg/"+link.LinkID+"/stats-card?days="+days, "", nil)
			Expect(rec.Code).To(Equal(http.StatusOK),
				"days=%s should clamp; status %d", days, rec.Code)
		}
	})
})

// silence unused-import when trimming
var _ = strings.Contains
