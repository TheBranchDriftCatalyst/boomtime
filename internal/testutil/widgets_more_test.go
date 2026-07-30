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
		It("requires auth: NO header → 400 MissingAuth (pinned per apierr.MissingAuth); INVALID token → 403 InvalidToken", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()

			// Case 1: no Authorization header at all → tokenFromHeader
			// returns apierr.MissingAuth() which is 400 (see handler.go:194-199
			// and apierr/apierr.go:35). Pinning EXACTLY 400 catches a silent
			// status-code refactor (a change to 401 would still be "4xx" but
			// would break the documented contract).
			rec := doG(e, http.MethodGet, "/api/v1/users/current/widgets/links", "", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"MissingAuth must return exactly 400 (per apierr.MissingAuth); got %d body=%s",
				rec.Code, rec.Body.String())
			Expect(rec.Body.String()).NotTo(ContainSubstring("linkId"),
				"AUTH LEAK: unauthenticated response contained link data: body=%s", rec.Body.String())
			// The MissingAuth message text is also load-bearing — the FE keys
			// off it to prompt for login.
			Expect(rec.Body.String()).To(ContainSubstring("Authorization"),
				"expected MissingAuth message referencing 'Authorization'; got %s", rec.Body.String())

			// Case 2: syntactically-present but garbage token → InvalidToken
			// (403 per apierr.InvalidToken and handler.go:214). Different code
			// path (GetUserByToken returns ok=false) — pinning it catches a
			// broken auth pipeline that would 500 or silently 200.
			rec = doG(e, http.MethodGet, "/api/v1/users/current/widgets/links", "garbage-token", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusForbidden),
				"InvalidToken must return exactly 403 (per apierr.InvalidToken); got %d body=%s",
				rec.Code, rec.Body.String())
			Expect(rec.Body.String()).NotTo(ContainSubstring("linkId"),
				"AUTH LEAK: invalid-token response contained link data: body=%s", rec.Body.String())
		})

		It("CROSS-USER: B's link list does NOT include A's user-link OR project-link ids; B cannot roll A's project link", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			userA, tokenA := hz.MintUser("wc_iso_a")
			_, tokenB := hz.MintUser("wc_iso_b")

			// A mints a user-scope link.
			linkAUser := mintLinkG(e, tokenA, "user", "")

			// A also mints a PROJECT-scope link — this is the case most likely
			// to leak because scopeRef is user-supplied and echoed in URLs.
			// Seed A's actual username with a project so ProjectExists returns
			// true and the mint succeeds.
			hz.Seeder(userA).Projects("A-secret-project")
			linkAProj := mintLinkG(e, tokenA, "project", "A-secret-project")
			Expect(linkAProj.LinkID).NotTo(Equal(linkAUser.LinkID),
				"same owner, different scopes must get distinct link ids")

			// B mints a user link — must have a different id from A's user link.
			linkB := mintLinkG(e, tokenB, "user", "")
			Expect(linkB.LinkID).NotTo(Equal(linkAUser.LinkID),
				"different owners must get different link ids for the same scope")

			// B's list must contain B's link only — no leak of EITHER of A's links.
			rec := doG(e, http.MethodGet, "/api/v1/users/current/widgets/links", tokenB, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))
			var listB struct {
				Links []struct {
					LinkID    string `json:"linkId"`
					ScopeType string `json:"scopeType"`
					ScopeName string `json:"scopeName"`
				} `json:"links"`
			}
			decodeG(rec, &listB)
			for _, l := range listB.Links {
				Expect(l.LinkID).NotTo(Equal(linkAUser.LinkID),
					"CROSS-USER LEAK: B's list contains A's user-link id %s", linkAUser.LinkID)
				Expect(l.LinkID).NotTo(Equal(linkAProj.LinkID),
					"CROSS-USER LEAK: B's list contains A's project-link id %s", linkAProj.LinkID)
				// Also assert B's list never carries A's project name in the
				// scopeName field — a load-bearing check because a bad JOIN
				// might carry the string across even without the id.
				Expect(l.ScopeName).NotTo(Equal("A-secret-project"),
					"CROSS-USER LEAK: B's list contains A's project name via scopeName")
			}
			// Positive control — B's own id is in B's list.
			foundB := false
			for _, l := range listB.Links {
				if l.LinkID == linkB.LinkID {
					foundB = true
				}
			}
			Expect(foundB).To(BeTrue(), "B's own link missing from B's list; body=%s", rec.Body.String())

			// B cannot ROLL A's project link — must 404 (owner-keyed).
			rec = doG(e, http.MethodPost,
				"/api/v1/users/current/widgets/link/"+linkAProj.LinkID+"/roll", tokenB, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
				"CROSS-USER LEAK: B could roll A's project link: body=%s", rec.Body.String())

			// B cannot RENDER A's project link data — the public endpoint
			// resolves the link and applies A's curation, so B is not blocked
			// by auth here (it's a public URL), but the endpoint should still
			// work only for A's actual content. Verify the SVG succeeds (public)
			// but only A can see it in her list — the list is the isolation surface.

			// A's list must contain both of A's links, and NOT B's link.
			rec = doG(e, http.MethodGet, "/api/v1/users/current/widgets/links", tokenA, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))
			var listA struct {
				Links []struct {
					LinkID    string `json:"linkId"`
					ScopeType string `json:"scopeType"`
					ScopeName string `json:"scopeName"`
				} `json:"links"`
			}
			decodeG(rec, &listA)
			foundAUser := false
			foundAProj := false
			for _, l := range listA.Links {
				Expect(l.LinkID).NotTo(Equal(linkB.LinkID),
					"CROSS-USER LEAK: A's list contains B's link id %s", linkB.LinkID)
				if l.LinkID == linkAUser.LinkID {
					foundAUser = true
				}
				if l.LinkID == linkAProj.LinkID {
					foundAProj = true
					Expect(l.ScopeName).To(Equal("A-secret-project"),
						"A's project link should report its scopeName")
				}
			}
			Expect(foundAUser).To(BeTrue(), "A's own user-link missing from A's list")
			Expect(foundAProj).To(BeTrue(), "A's own project-link missing from A's list")
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

		It("accepts a rename-target project scope AND the minted link renders src-name data under the renamed label (gaka-xuc: LoadRenameSets fallback + expansion)", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			user, token := hz.MintUser("wc_rename_scope")

			// Seed raw project 'src-name' with a known-precise 3-hour block so
			// we can assert the renderer resolved the rename target back to
			// the source project's activity. If LoadRenameSets always returned
			// empty (breaking the gaka-xuc fallback) the DB row might still
			// be created but the SVG would render empty (no rows match the
			// scope's expanded member set) — the ContainSubstring assertions
			// below would then fail.
			start := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Hour)
			sd := hz.Seeder(user)
			// each=60 stays under widgetTimeLimit(15min=900s); 20 beats gives
			// a measurable 20-minute block that will render as a bar row.
			sd.Block(testutil.HB{Project: "src-name", Language: "Go", Editor: "vim"},
				start, 20, 60)
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
			rec := doG(e, http.MethodGet,
				"/api/v1/users/current/widgets/link?scopeType=project&scopeRef=dst-name",
				token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK),
				"rename-target scope should be accepted (gaka-xuc): body=%s", rec.Body.String())
			var minted minLinkResp
			decodeG(rec, &minted)
			Expect(minted.LinkID).NotTo(BeEmpty(), "mint returned empty linkId: %s", rec.Body.String())

			// NOW the real invariant: render the widget and confirm that
			// src-name's heartbeat data actually flowed through. If the
			// rename map is properly resolved, the top-projects panel must
			// contain the renamed label (dst-name) as the bar row for the
			// scope's data. A regression that dropped the expansion would
			// render an empty top-projects panel (member set = ["dst-name"]
			// literal, but no heartbeats stored under that raw name).
			svg := doG(e, http.MethodGet,
				"/widget/svg/"+minted.LinkID+"/top-projects", "", nil)
			Expect(svg).To(testutil.HaveStatus(http.StatusOK),
				"minted rename-target link must render: body=%s", svg.Body.String())
			Expect(svg.Header().Get("Content-Type")).To(HavePrefix("image/svg+xml"))
			body := svg.Body.String()
			// The renamed label MUST appear — the RenameSets translation
			// remaps src-name → dst-name on the outbound payload, and the
			// scope's member set (expanded via rename) matched the src-name
			// heartbeats. Both branches must have fired for this substring
			// to appear.
			Expect(body).To(ContainSubstring(">dst-name<"),
				"rename resolution broken: expected 'dst-name' bar row, got body=%s", body)
			// The raw source name must NOT leak — the outbound rename must
			// have translated it, or the widget is exposing pre-rename data.
			Expect(body).NotTo(ContainSubstring(">src-name<"),
				"rename leak: raw 'src-name' surfaced in rendered widget; body=%s", body)
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

		It("scopeType=user WITH a non-empty scopeRef silently ZEROS the ref — mint is idempotent regardless of what's passed (widgets.go:70-71 normalization)", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			_, token := hz.MintUser("wc_user_scope_ref")

			// First: mint with no scopeRef — canonical form.
			bareLink := mintLinkG(e, token, "user", "")

			// Second: mint scopeType=user with a garbage scopeRef. The code
			// (widgets.go:70-71) OVERRIDES scopeRef to "" for user scope. This
			// means the mint MUST return the SAME link id — one link per user,
			// full stop. A regression that started respecting scopeRef for user
			// scope would create multiple links (bareLink.LinkID != withRef.LinkID)
			// and the mint-idempotence invariant would break.
			withRef := mintLinkG(e, token, "user", "some-garbage-ref")
			Expect(withRef.LinkID).To(Equal(bareLink.LinkID),
				"user-scope mint with garbage scopeRef must return the SAME id — normalization broken; got bare=%s withRef=%s",
				bareLink.LinkID, withRef.LinkID)

			// Third: verify the list has EXACTLY ONE link and the scopeName is
			// empty (never the garbage ref).
			rec := doG(e, http.MethodGet, "/api/v1/users/current/widgets/links", token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))
			var list struct {
				Links []struct {
					LinkID    string `json:"linkId"`
					ScopeType string `json:"scopeType"`
					ScopeName string `json:"scopeName"`
				} `json:"links"`
			}
			decodeG(rec, &list)
			Expect(list.Links).To(HaveLen(1),
				"user-scope must produce exactly one link, no matter how many times mint is called with different refs; got %d",
				len(list.Links))
			Expect(list.Links[0].ScopeName).To(BeEmpty(),
				"user-scope link.scopeName must be empty (widgets.go:71 zeros scopeRef); got %q — GARBAGE LEAK",
				list.Links[0].ScopeName)
		})
	})

	Describe("public-render cache-key owner-prefix invariant (gaka-6jm.3 cache correctness)", func() {
		It("curation change BUSTS cached SVG bytes — same URL renders fresh output after a hide rule (owner-prefixed cache sweep)", func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			user, token := hz.MintUser("wc_cache_bust")

			// Seed two languages so top-langs is non-trivial.
			start := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Hour)
			sd := hz.Seeder(user)
			sd.Block(testutil.HB{Project: "p1", Language: "TypeScript", Editor: "vim"}, start, 20, 60)
			sd.Block(testutil.HB{Project: "p2", Language: "Go", Editor: "vim"},
				start.Add(time.Hour), 10, 60)
			sd.RefreshRollup(start.Add(-time.Hour))

			link := mintLinkG(e, token, "user", "")

			// Render 1: baseline. Cached under (owner|widget|<id>|top-langs|...).
			r1 := doG(e, http.MethodGet, "/widget/svg/"+link.LinkID+"/top-langs", "", nil)
			Expect(r1).To(testutil.HaveStatus(http.StatusOK))
			body1 := r1.Body.String()
			Expect(body1).To(ContainSubstring("TypeScript"),
				"baseline: TypeScript must be present pre-hide; body=%s", body1)

			// Render 2: same URL. If cache is working, this is byte-identical to r1.
			// (This is not a required invariant — but it warms us that a fresh
			// GET on the same URL is stable.)
			r2 := doG(e, http.MethodGet, "/widget/svg/"+link.LinkID+"/top-langs", "", nil)
			Expect(r2).To(testutil.HaveStatus(http.StatusOK))

			// Curate: hide TypeScript. curation.go:115 calls invalidateOwnerCache
			// which sweeps owner|* — including the widget cache entries above.
			cr := doG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
				"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "TypeScript",
			})
			Expect(cr.Code).To(BeNumerically("<", 300),
				"create hide rule: body=%s", cr.Body.String())

			// Render 3: SAME URL as r1/r2. If the cache key were NOT owner-
			// prefixed (or invalidateOwnerCache didn't run on curation POST),
			// the stale bytes with TypeScript would be served — a PRIVACY LEAK.
			// The correct behavior is: TypeScript is scrubbed out.
			r3 := doG(e, http.MethodGet, "/widget/svg/"+link.LinkID+"/top-langs", "", nil)
			Expect(r3).To(testutil.HaveStatus(http.StatusOK))
			body3 := r3.Body.String()
			Expect(body3).NotTo(ContainSubstring("TypeScript"),
				"CACHE-KEY LEAK: TypeScript still visible after hide rule — cache sweep broken; body=%s", body3)
			// Positive control — Go still there.
			Expect(body3).To(ContainSubstring(">Go<"),
				"positive control: Go should still render; body=%s", body3)
			// The bytes MUST differ from the pre-curation render — proves
			// fresh render happened (not a stale cache hit).
			Expect(r3.Body.Bytes()).NotTo(Equal(r1.Body.Bytes()),
				"CACHE-KEY LEAK: post-hide render bytes-equal to pre-hide — stale cache served across curation boundary")
		})
	})
})

// unused var; keep encoding/json referenced when only used in helpers.
var _ = json.RawMessage(nil)
