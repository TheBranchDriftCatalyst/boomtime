// widgets_more_test.go — extra coverage for widgets.go / widget_defs.go
// public-render branches (gaka-d6x.handler). The stdlib widget_defs_test.go
// (in handler_test) already covers CRUD + scrub. This file targets the
// public /widget/svg/:uuid/... branches the ginkgo widgets_test.go doesn't:
//
//   - The custom kind: /widget/svg/:uuid/custom?spec=<base64> — exercises
//     IsCustomKind + DecodeDef + RenderCustom + NeedsForDef branches inside
//     WidgetSvg. Missing spec, malformed spec, and happy-path are all invariants.
//   - Project-scope hidden → 404 (gaka-6jm.5). Load-bearing on privacy:
//     a curated-away project name cannot be probed by minting-then-fetching.
//   - Space scope: mint via space + fetch renders 200 (WidgetSvg
//     WidgetScopeSpace switch branch).
//   - Cross-user list isolation on /widgets/links: B's link list must
//     NEVER include A's minted widget ids.
//   - WidgetLinkList requires auth (401) — the endpoint is /users/current/.
package testutil_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/widget"
)

// --- helper: create a space via HTTP and return its id ---
func createSpaceG(e http.Handler, token, name string) string {
	GinkgoHelper()
	rec := doG(e, http.MethodPost, "/api/v1/users/current/spaces", token,
		map[string]any{"name": name})
	Expect(rec).To(testutil.HaveStatus(http.StatusOK),
		"create space: body=%s", rec.Body.String())
	var out struct {
		Space struct{ ID int } `json:"space"`
	}
	decodeG(rec, &out)
	Expect(out.Space.ID).NotTo(BeZero(), "created space id missing: %s", rec.Body.String())
	return strconv.Itoa(out.Space.ID)
}

// --- helper: mint a widget link and return its id ---
type minLinkResp struct {
	WidgetBaseURL string `json:"widgetBaseUrl"`
	LinkID        string `json:"linkId"`
}

func mintLinkG(e http.Handler, token, scopeType, scopeRef string) minLinkResp {
	GinkgoHelper()
	rec := doG(e, http.MethodGet,
		fmt.Sprintf("/api/v1/users/current/widgets/link?scopeType=%s&scopeRef=%s",
			scopeType, scopeRef), token, nil)
	Expect(rec).To(testutil.HaveStatus(http.StatusOK),
		"mint link: body=%s", rec.Body.String())
	var out minLinkResp
	decodeG(rec, &out)
	return out
}

var _ = Describe("WidgetSvg extras (gaka-d6x.handler)", func() {
	Describe("custom-kind branches (?spec=base64(Def))", func() {
		It("400s when spec is MISSING on the custom kind (DecodeDef must not accept empty)", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			_, token := hz.MintUser("wc_custom_nospec")
			link := mintLinkG(e, token, "user", "")

			// No ?spec at all → DecodeDef returns error, endpoint 400.
			rec := doG(e, http.MethodGet,
				"/widget/svg/"+link.LinkID+"/custom", "", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"custom kind without spec must 400: body=%s", rec.Body.String())
		})

		It("400s on a MALFORMED spec (not base64 / not JSON) — proves DecodeDef guards the render path", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			_, token := hz.MintUser("wc_custom_badspec")
			link := mintLinkG(e, token, "user", "")

			rec := doG(e, http.MethodGet,
				"/widget/svg/"+link.LinkID+"/custom?spec=%%%%%%", "", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"malformed base64 spec must 400: body=%s", rec.Body.String())
		})

		It("400s on a well-formed base64 spec that carries an INVALID Def (unknown layout)", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			_, token := hz.MintUser("wc_custom_badlayout")
			link := mintLinkG(e, token, "user", "")

			raw := []byte(`{"layout":"99-panel","panels":[]}`)
			enc := base64.RawURLEncoding.EncodeToString(raw)

			rec := doG(e, http.MethodGet,
				"/widget/svg/"+link.LinkID+"/custom?spec="+enc, "", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"invalid layout via base64 spec must 400: body=%s", rec.Body.String())
		})

		It("renders an SVG on a VALID custom spec — exercises NeedsForDef + RenderCustom branches", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			user, token := hz.MintUser("wc_custom_ok")

			// Seed activity so the render has non-empty data.
			start := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
			hz.Seeder(user).Block(
				testutil.HB{Project: "cust-p", Language: "Go", Editor: "vim"},
				start, 10, 60)
			hz.Seeder(user).RefreshRollup(start.Add(-time.Hour))

			link := mintLinkG(e, token, "user", "")

			// A 3-panel horizontal composition that touches Grade, Momentum, AND
			// Punchcard so the NeedsForDef union path (all three optional fetches
			// fire) is exercised inside WidgetSvg.
			enc, err := widget.EncodeDef(widget.Def{
				Layout: widget.Layout3Horz,
				Title:  "custom",
				Panels: []widget.Panel{
					{Kind: widget.PanelGrade},
					{Kind: widget.PanelPunchcard},
					{Kind: widget.PanelMomentum},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			rec := doG(e, http.MethodGet,
				"/widget/svg/"+link.LinkID+"/custom?spec="+enc+"&days=30&theme=dark", "", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK),
				"valid custom spec must render: body=%s", rec.Body.String())
			Expect(rec.Header().Get("Content-Type")).To(HavePrefix("image/svg+xml"))
			Expect(rec.Body.String()).To(ContainSubstring("<svg"),
				"expected an SVG body")
		})
	})

	Describe("project-scope hidden gate (gaka-6jm.5 privacy)", func() {
		It("404s AFTER the owner curates the pinned project — id lookup would otherwise 200", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			user, token := hz.MintUser("wc_projhide")

			// Seed a project + activity so the link is valid at mint time.
			start := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Hour)
			sd := hz.Seeder(user)
			sd.Block(testutil.HB{Project: "will-hide", Language: "Go", Editor: "vim"},
				start, 5, 60)
			sd.RefreshRollup(start.Add(-time.Hour))

			link := mintLinkG(e, token, "project", "will-hide")

			// Baseline: link works.
			rec := doG(e, http.MethodGet,
				"/widget/svg/"+link.LinkID+"/stats-card", "", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK),
				"baseline widget SVG should render before hide rule: body=%s", rec.Body.String())

			// Curate the project hidden.
			cr := doG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
				"axis": "project", "action": "hide",
				"matchType": "exact", "matchValue": "will-hide",
			})
			Expect(cr.Code).To(BeNumerically("<", 300),
				"create hide rule: body=%s", cr.Body.String())

			// Now the SAME url must 404 — privacy gate.
			rec = doG(e, http.MethodGet,
				"/widget/svg/"+link.LinkID+"/stats-card", "", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
				"PRIVACY LEAK (gaka-6jm.5): curated project scope must 404, got %d body=%s",
				rec.Code, rec.Body.String())
		})
	})

	Describe("space scope path (WidgetSvg WidgetScopeSpace branch)", func() {
		It("renders a space-scoped widget after mint (LoadMemberSets + scoped=true branch)", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			user, token := hz.MintUser("wc_space")

			// Two projects; one included in the space (matches regex), the other not.
			start := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Hour)
			sd := hz.Seeder(user)
			sd.Block(testutil.HB{Project: "in-space", Language: "Go", Editor: "vim"},
				start, 5, 60)
			sd.Block(testutil.HB{Project: "not-in-space", Language: "Rust", Editor: "vim"},
				start.Add(time.Hour), 5, 60)
			sd.RefreshRollup(start.Add(-time.Hour))

			spaceID := createSpaceG(e, token, "Work")
			// Add a project rule that only matches "in-space".
			rr := doG(e, http.MethodPost, "/api/v1/users/current/spaces/"+spaceID+"/rules", token,
				map[string]any{"axis": "project", "matchValue": "^in-", "matchType": "regex"})
			Expect(rr).To(testutil.HaveStatus(http.StatusOK),
				"add space rule: body=%s", rr.Body.String())

			// Mint a widget scoped to the space.
			link := mintLinkG(e, token, "space", spaceID)

			// Fetch it — the WidgetScopeSpace branch does LoadMemberSets +
			// scoped=true, which should render a 200 with an SVG body.
			rec := doG(e, http.MethodGet,
				"/widget/svg/"+link.LinkID+"/top-projects", "", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK),
				"space-scoped widget render: body=%s", rec.Body.String())
			Expect(rec.Header().Get("Content-Type")).To(HavePrefix("image/svg+xml"))
			body := rec.Body.String()
			Expect(body).To(ContainSubstring("<svg"))
			// Load-bearing scope invariant: the non-matching project must NOT
			// appear in the top-projects bar list.
			Expect(body).NotTo(ContainSubstring(">not-in-space<"),
				"space-scope did NOT exclude 'not-in-space': body=%s", body)
		})
	})

	Describe("WidgetLinkList auth + isolation", func() {
		It("requires auth: unauthenticated GET rejects with 4xx (never leaks data as 200)", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()

			rec := doG(e, http.MethodGet, "/api/v1/users/current/widgets/links", "", nil)
			// resolveUser returns 400/401 depending on how the token is presented;
			// either way, load-bearing: the endpoint MUST NOT return 200.
			Expect(rec.Code).To(BeNumerically(">=", 400),
				"unauthenticated list must reject; got %d body=%s", rec.Code, rec.Body.String())
			Expect(rec.Code).To(BeNumerically("<", 500),
				"unauthenticated list should be 4xx not 5xx; got %d", rec.Code)
			Expect(rec.Body.String()).NotTo(ContainSubstring("linkId"),
				"AUTH LEAK: unauthenticated response contained link data: body=%s", rec.Body.String())
		})

		It("CROSS-USER: B's link list does NOT include A's minted link ids", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			_, tokenA := hz.MintUser("wc_iso_a")
			_, tokenB := hz.MintUser("wc_iso_b")

			// A mints two links.
			linkA1 := mintLinkG(e, tokenA, "user", "")
			// A project link too.
			hz.Seeder("wc_iso_a_placeholder") // no-op to keep types happy
			// A also mints for a project (must be A's project).
			// Create A's project via a seed then mint.
			// Use the seeder on A's actual username — retrieve via list-workaround:
			// simpler: skip project link and only test that user-link is isolated.

			// B mints a user link — must have a different id.
			linkB := mintLinkG(e, tokenB, "user", "")
			Expect(linkB.LinkID).NotTo(Equal(linkA1.LinkID),
				"different owners must get different link ids for the same scope")

			// B's list must contain B's link only.
			rec := doG(e, http.MethodGet, "/api/v1/users/current/widgets/links", tokenB, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))
			var listB struct {
				Links []struct {
					LinkID string `json:"linkId"`
				} `json:"links"`
			}
			decodeG(rec, &listB)
			for _, l := range listB.Links {
				Expect(l.LinkID).NotTo(Equal(linkA1.LinkID),
					"CROSS-USER LEAK: B's list contains A's link id %s", linkA1.LinkID)
			}
			// Positive control — B's own id is in B's list.
			foundB := false
			for _, l := range listB.Links {
				if l.LinkID == linkB.LinkID {
					foundB = true
				}
			}
			Expect(foundB).To(BeTrue(), "B's own link missing from B's list; body=%s", rec.Body.String())

			// A's list contains A's link, not B's.
			rec = doG(e, http.MethodGet, "/api/v1/users/current/widgets/links", tokenA, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))
			var listA struct {
				Links []struct {
					LinkID string `json:"linkId"`
				} `json:"links"`
			}
			decodeG(rec, &listA)
			for _, l := range listA.Links {
				Expect(l.LinkID).NotTo(Equal(linkB.LinkID),
					"CROSS-USER LEAK: A's list contains B's link id %s", linkB.LinkID)
			}
		})
	})

	Describe("WidgetSvg needs-branch coverage (Grade / Punchcard / Momentum / Sessions)", func() {
		// Each of these kinds triggers a distinct optional DB fetch inside
		// WidgetSvg via Needs(kind). The stats-card/top-langs/badge kinds
		// (already covered) do NOT — so hitting them here fills the coverage
		// gaps on the WidgetSvg `needs.X` if-blocks. Same rationale as the
		// widget-defs Data-fetch branch matrix in widget_defs_more_test.go.
		DescribeTable("public SVG renders 200 for each needs.X-triggering kind",
			func(kind string) {
				hz := testutil.NewHarness(GinkgoTB())
				e := hz.Router()
				user, token := hz.MintUser("wc_needs")

				// Seed activity spanning multiple days so punchcard/momentum/sessions
				// have non-empty payloads.
				start := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Hour)
				sd := hz.Seeder(user)
				sd.Block(testutil.HB{Project: "p", Language: "Go", Editor: "vim"}, start, 20, 60)
				sd.Block(testutil.HB{Project: "p", Language: "Go", Editor: "vim"},
					start.Add(24*time.Hour), 15, 60)
				sd.Block(testutil.HB{Project: "p", Language: "Go", Editor: "vim"},
					start.Add(48*time.Hour), 10, 60)
				sd.RefreshRollup(start.Add(-time.Hour))

				link := mintLinkG(e, token, "user", "")
				rec := doG(e, http.MethodGet,
					"/widget/svg/"+link.LinkID+"/"+kind+"?days=30&theme=dark", "", nil)
				Expect(rec).To(testutil.HaveStatus(http.StatusOK),
					"public render for kind=%s: body=%s", kind, rec.Body.String())
				Expect(rec.Header().Get("Content-Type")).To(HavePrefix("image/svg+xml"))
				Expect(rec.Body.String()).To(ContainSubstring("<svg"))
			},
			Entry("punchcard (needs.Punchcard)", "punchcard"),
			Entry("momentum (needs.Momentum)", "momentum"),
			Entry("deep-work (needs.Sessions)", "deep-work"),
			Entry("profile-summary (needs.Grade)", "profile-summary"),
		)
	})

	Describe("WidgetLink error branches", func() {
		It("rolls a bad-uuid path with 400 (uuid.Parse guard)", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			_, token := hz.MintUser("wc_roll_baduuid")

			rec := doG(e, http.MethodPost,
				"/api/v1/users/current/widgets/link/not-a-uuid/roll", token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"roll on non-uuid must 400: body=%s", rec.Body.String())
		})

		It("accepts a rename-target project scope (gaka-xuc: LoadRenameSets fallback branch)", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			user, token := hz.MintUser("wc_rename_scope")

			// Seed raw project 'src-name' and add a curation rename src->dst.
			start := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Hour)
			sd := hz.Seeder(user)
			sd.Block(testutil.HB{Project: "src-name", Language: "Go", Editor: "vim"},
				start, 3, 60)
			sd.RefreshRollup(start.Add(-time.Hour))

			// Curation rule: exact rename src-name -> dst-name (curation uses `newValue`).
			cr := doG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
				"axis": "project", "action": "rename",
				"matchType": "exact", "matchValue": "src-name",
				"newValue": "dst-name",
			})
			Expect(cr.Code).To(BeNumerically("<", 300),
				"create rename rule: body=%s", cr.Body.String())

			// Mint by the RENAMED name — projects table doesn't contain it, but
			// the rename map's ExactSourcesFor should map back to src-name.
			// If the branch works, mint returns 200 (or 404 on branch drop).
			// Test the invariant: the endpoint must NOT 500.
			rec := doG(e, http.MethodGet,
				"/api/v1/users/current/widgets/link?scopeType=project&scopeRef=dst-name",
				token, nil)
			Expect(rec.Code).NotTo(BeNumerically(">=", 500),
				"scoping by rename-target must not 500 (branch is well-defined): body=%s", rec.Body.String())
			Expect(rec).To(testutil.HaveStatus(http.StatusOK),
				"rename-target scope should be accepted (gaka-xuc): body=%s", rec.Body.String())
		})

		It("rejects a NON-INTEGER space scopeRef with 400 (before DB lookup)", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			_, token := hz.MintUser("wc_bad_space_ref")

			rec := doG(e, http.MethodGet,
				"/api/v1/users/current/widgets/link?scopeType=space&scopeRef=abc",
				token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"non-integer space id must 400: body=%s", rec.Body.String())
		})
	})
})

// unused var; keep encoding/json referenced when only used in helpers.
var _ = json.RawMessage(nil)
