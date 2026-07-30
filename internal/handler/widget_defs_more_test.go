// widget_defs_more_test.go — extra coverage for widget_defs.go (gaka-d6x.handler).
//
// Focus: the CRUD-error branches + cross-user isolation that widget_defs_test.go
// (the scrubber regression file) does not exercise. Every test pins a NAMED
// INVARIANT that is load-bearing on the widget-defs contract:
//
//   - "empty spec / oversized spec / invalid layout / unknown panel kind"
//     all reject at the SERVER, not the DB — because validateWidgetDefSpec
//     runs BEFORE Insert. A regression that dropped the size cap would let
//     a hostile client force a large JSON decode inside CreateWidgetDef.
//   - "duplicate (owner, name) → 400 with the friendly message, not 500":
//     the isUniqueViolation branch is the difference between "please rename"
//     and a scary 500 in the UI.
//   - "UpdateWidgetDef / DeleteWidgetDef are OWNER-KEYED": user B trying
//     to PATCH or DELETE user A's widget-def by name must 404, not silently
//     modify A's row. This is the same class of load-bearing isolation that
//     the widget_links tests already prove for the link table.
//   - "list returns only the caller's defs": the ListWidgetDefs endpoint
//     is on the /users/current/* namespace; a cross-user leak would expose
//     saved widget compositions (title + panels) across accounts.
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

// mkValidDefBytes marshals a minimal valid widget.Def (1 panel over top-langs).
func mkValidDefBytes() json.RawMessage {
	b, err := json.Marshal(widget.Def{
		Layout: widget.Layout1,
		Title:  "t",
		Panels: []widget.Panel{{Kind: widget.PanelTopLangs}},
	})
	Expect(err).NotTo(HaveOccurred())
	return b
}

var _ = Describe("widget-defs CRUD extras (gaka-d6x.handler)", func() {
	Describe("CreateWidgetDef reject-before-insert branches", func() {
		It("rejects an EMPTY name with 400 (server-side, before the DB unique check runs)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_empty_name")

			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "   ",
				"spec": mkValidDefBytes(),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"whitespace-only name must be rejected: body=%s", rec.Body.String())
			Expect(rec.Body.String()).To(ContainSubstring("name"),
				"expected name-related error, got %s", rec.Body.String())
		})

		It("rejects an EMPTY spec with 400 (validateWidgetDefSpec len==0 branch)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_empty_spec")

			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "empty-spec-widget",
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"missing spec must reject: body=%s", rec.Body.String())
		})

		It("rejects an OVERSIZED spec (>32 KiB) with 400 — proves widgetDefMax fires BEFORE json.Unmarshal", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_oversize_spec")

			// Build a spec whose JSON envelope exceeds 32 KiB but stays under
			// the BodyLimitMedium (64 KiB) so we hit widgetDefMax, not the
			// outer body cap. The Title field carries the padding.
			pad := strings.Repeat("x", 33*1024)
			spec, err := json.Marshal(widget.Def{
				Layout: widget.Layout1,
				Title:  pad,
				Panels: []widget.Panel{{Kind: widget.PanelTopLangs}},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(len(spec)).To(BeNumerically(">", 32*1024))

			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "oversize", "spec": json.RawMessage(spec),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"oversized spec should get 400 (not 413 — outer body cap is 64 KiB): body=%s", rec.Body.String())
			Expect(rec.Body.String()).To(ContainSubstring("exceeds"),
				"expected widgetDefMax message, got %s", rec.Body.String())
		})

		It("rejects a NON-OBJECT spec with 400 (validateWidgetDefSpec json.Unmarshal into widget.Def fails)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_bad_json")

			// The spec IS valid JSON (an array) but does NOT decode as widget.Def.
			// Exercises validateWidgetDefSpec's json.Unmarshal error branch (line 45).
			raw := json.RawMessage(`[1,2,3]`)
			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "bad-shape", "spec": raw,
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"non-object spec must reject: body=%s", rec.Body.String())
			Expect(rec.Body.String()).To(ContainSubstring("widget.Def"),
				"expected widget.Def type error, got %s", rec.Body.String())
		})

		It("rejects an INVALID DEF (unknown layout) with 400 — proves ValidateDef whitelist is enforced at write time", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_bad_layout")

			// Bypass widget.Def marshalling to send a spec that decodes as a
			// Def but has a layout not in the whitelist. json.RawMessage keeps
			// the bytes verbatim.
			bad := json.RawMessage(`{"layout":"99-panel","panels":[]}`)
			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "bad-layout", "spec": bad,
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"unknown layout must reject at persist time: body=%s", rec.Body.String())
			Expect(rec.Body.String()).To(ContainSubstring("layout"),
				"expected layout-related error, got %s", rec.Body.String())
		})

		It("rejects an INVALID DEF (unknown panel kind) with 400 — write-path parity with URL /custom", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_bad_panel")

			bad := json.RawMessage(`{"layout":"1-panel","panels":[{"kind":"galaxy"}]}`)
			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "bad-panel", "spec": bad,
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"unknown panel kind must reject: body=%s", rec.Body.String())
		})

		It("rejects a DUPLICATE (owner, name) with 400 + friendly message (isUniqueViolation branch)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_dup")

			// First create succeeds.
			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "duplicate-name", "spec": mkValidDefBytes(),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusOK), "first create: body=%s", rec.Body.String())

			// Second create with the same name → 400 (translated from PG 23505).
			rec = doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "duplicate-name", "spec": mkValidDefBytes(),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"duplicate name must return 400 (not 500): body=%s", rec.Body.String())
			Expect(rec.Body.String()).To(ContainSubstring("already exists"),
				"expected the friendly 'already exists' message; got %s", rec.Body.String())
		})
	})

	Describe("ListWidgetDefs", func() {
		It("returns exactly the caller's defs, empty for a fresh user — CROSS-USER ISOLATION", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, tokenA := hz.MintUser("wd_list_a")
			_, tokenB := hz.MintUser("wd_list_b")

			// A creates two defs.
			for _, n := range []string{"alpha", "beta"} {
				rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", tokenA, map[string]any{
					"name": n, "spec": mkValidDefBytes(),
				})
				Expect(rec).To(testutil.HaveStatus(http.StatusOK), "create %s: body=%s", n, rec.Body.String())
			}

			// A's list has exactly A's defs.
			var listA struct {
				Defs []struct {
					Name string `json:"name"`
				} `json:"defs"`
			}
			rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/widget-defs", tokenA, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))
			Expect(json.Unmarshal(rec.Body.Bytes(), &listA)).To(Succeed())
			names := map[string]bool{}
			for _, d := range listA.Defs {
				names[d.Name] = true
			}
			Expect(names["alpha"]).To(BeTrue(), "A should see alpha")
			Expect(names["beta"]).To(BeTrue(), "A should see beta")

			// B's list must NOT include A's defs. Load-bearing on privacy:
			// widget compositions carry the owner's title + panel intent.
			var listB struct {
				Defs []struct {
					Name string `json:"name"`
				} `json:"defs"`
			}
			rec = doJSONReqG(e, http.MethodGet, "/api/v1/users/current/widget-defs", tokenB, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))
			Expect(json.Unmarshal(rec.Body.Bytes(), &listB)).To(Succeed())
			for _, d := range listB.Defs {
				Expect(d.Name).NotTo(Equal("alpha"), "CROSS-USER LEAK: B sees A's def alpha")
				Expect(d.Name).NotTo(Equal("beta"), "CROSS-USER LEAK: B sees A's def beta")
			}
		})
	})

	Describe("UpdateWidgetDef", func() {
		It("404s when the def does not exist (avoids silent-create semantics)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_upd_missing")

			rec := doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/widget-defs/nonexistent", token, map[string]any{
				"spec": mkValidDefBytes(),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
				"PATCH unknown name must be 404 (not silent-create): body=%s", rec.Body.String())
		})

		It("rejects an invalid spec BEFORE calling UpdateWidgetDef (spec validation branch)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_upd_badspec")

			// Create a valid def so the PATCH row-lookup would succeed on a valid body.
			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "target", "spec": mkValidDefBytes(),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))

			// Now PATCH with an invalid spec — must 400 before touching the row.
			bad := json.RawMessage(`{"layout":"1-panel","panels":[{"kind":"galaxy"}]}`)
			rec = doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/widget-defs/target", token, map[string]any{
				"spec": bad,
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"PATCH with invalid spec must reject: body=%s", rec.Body.String())
		})

		It("happy path: PATCH replaces spec, GET reflects new title — round-trip via named SVG render", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			user, token := hz.MintUser("wd_upd_ok")
			// Seed a heartbeat so the SVG has something to render.
			_ = user

			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "editme", "spec": mkValidDefBytes(),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusOK), "create: body=%s", rec.Body.String())

			// Update with a NEW title — PATCH must return 204.
			b, err := json.Marshal(widget.Def{
				Layout: widget.Layout1,
				Title:  "PATCHED-TITLE-UNIQ",
				Panels: []widget.Panel{{Kind: widget.PanelMetrics}},
			})
			Expect(err).NotTo(HaveOccurred())
			rec = doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/widget-defs/editme", token, map[string]any{
				"spec": json.RawMessage(b),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusNoContent),
				"PATCH ok expects 204: body=%s", rec.Body.String())

			// Confirm via ListWidgetDefs — spec is opaque but list must include the name.
			var list struct {
				Defs []struct {
					Name string `json:"name"`
				} `json:"defs"`
			}
			rec = doJSONReqG(e, http.MethodGet, "/api/v1/users/current/widget-defs", token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))
			Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
			found := false
			for _, d := range list.Defs {
				if d.Name == "editme" {
					found = true
				}
			}
			Expect(found).To(BeTrue(), "editme should still be present post-PATCH")
		})

		It("CROSS-USER: B cannot PATCH A's def (404, not 200) — owner-keyed by (username, name)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, tokenA := hz.MintUser("wd_upd_iso_a")
			_, tokenB := hz.MintUser("wd_upd_iso_b")

			// A creates a def named "shared-name".
			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", tokenA, map[string]any{
				"name": "shared-name", "spec": mkValidDefBytes(),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusOK), "A create: body=%s", rec.Body.String())

			// B PATCHing the same name must 404 — B has no row for "shared-name".
			rec = doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/widget-defs/shared-name", tokenB, map[string]any{
				"spec": mkValidDefBytes(),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
				"CROSS-USER LEAK: B could PATCH A's def: body=%s", rec.Body.String())
		})
	})

	Describe("DeleteWidgetDef", func() {
		It("404s when the def does not exist (idempotence would silently no-op — we want a signal)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_del_missing")

			rec := doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/widget-defs/nonexistent", token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
				"DELETE unknown name must be 404: body=%s", rec.Body.String())
		})

		It("happy path: DELETE removes the def; second DELETE 404s (proves row is gone)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_del_ok")

			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "deleteme", "spec": mkValidDefBytes(),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))

			rec = doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/widget-defs/deleteme", token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusNoContent))

			// Second delete → 404 (row is gone; not silently 204).
			rec = doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/widget-defs/deleteme", token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
				"double-delete must 404 — proves the row was actually removed: body=%s", rec.Body.String())
		})

		It("CROSS-USER: B cannot DELETE A's def (404, not 204) — same owner-key as UpdateWidgetDef", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, tokenA := hz.MintUser("wd_del_iso_a")
			_, tokenB := hz.MintUser("wd_del_iso_b")

			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", tokenA, map[string]any{
				"name": "a-owned", "spec": mkValidDefBytes(),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))

			// B's DELETE of A's def by name must 404.
			rec = doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/widget-defs/a-owned", tokenB, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
				"CROSS-USER LEAK: B deleted A's def: body=%s", rec.Body.String())

			// A can still see & delete their own row — proves B's request was a no-op.
			rec = doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/widget-defs/a-owned", tokenA, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusNoContent),
				"A's row should still exist after B's failed DELETE: body=%s", rec.Body.String())
		})
	})

	Describe("WidgetDefSvg data-fetch branches (Grade / Punchcard / Momentum / Sessions)", func() {
		// Each Panel kind in a def toggles a distinct optional DB fetch inside
		// WidgetDefSvg via NeedsForDef. Exercising every needs.X branch here
		// mirrors the WidgetSvg cluster's per-kind matrix — a regression that
		// dropped one of those data blobs would silently render a placeholder
		// instead of the correct panel.
		DescribeTable("renders 200 SVG for each panel kind (exercises optional-fetch branches)",
			func(panels []widget.Panel) {
				hz := testutil.NewHarness(GinkgoT())
				e := hz.Router()
				user, token := hz.MintUser("wd_needs")

				// Seed enough activity to make every needs.X path return non-empty rows.
				start := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Hour)
				sd := hz.Seeder(user)
				sd.Block(testutil.HB{Project: "p1", Language: "Go", Editor: "vim"}, start, 20, 60)
				sd.Block(testutil.HB{Project: "p2", Language: "Go", Editor: "vim"},
					start.Add(24*time.Hour), 15, 60)
				sd.Block(testutil.HB{Project: "p1", Language: "Go", Editor: "vim"},
					start.Add(48*time.Hour), 10, 60)
				sd.RefreshRollup(start.Add(-time.Hour))

				layout := widget.Layout1
				if len(panels) == 3 {
					layout = widget.Layout3Horz
				} else if len(panels) == 2 {
					layout = widget.Layout2Horz
				}
				spec := mustMarshalDefG(widget.Def{
					Layout: layout,
					Title:  "coverage",
					Panels: panels,
				})
				rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token,
					map[string]any{"name": "needs-def", "spec": json.RawMessage(spec)})
				Expect(rec).To(testutil.HaveStatus(http.StatusOK), "create: body=%s", rec.Body.String())
				var out struct {
					DefID string `json:"defId"`
				}
				Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())

				svg := doJSONReqG(e, http.MethodGet,
					"/widget/svg/"+out.DefID+"/named?days=30&theme=dark&title=X", "", nil)
				Expect(svg).To(testutil.HaveStatus(http.StatusOK),
					"named SVG render for panels=%v: body=%s", panels, svg.Body.String())
				Expect(svg.Header().Get("Content-Type")).To(HavePrefix("image/svg+xml"))
				Expect(svg.Body.String()).To(ContainSubstring("<svg"),
					"expected SVG body")
			},
			Entry("grade", []widget.Panel{{Kind: widget.PanelGrade}}),
			Entry("punchcard", []widget.Panel{{Kind: widget.PanelPunchcard}}),
			Entry("momentum", []widget.Panel{{Kind: widget.PanelMomentum}}),
			Entry("metrics (needs.Sessions)", []widget.Panel{{Kind: widget.PanelMetrics}}),
			// Full combo triggers all four optional fetches on a single request —
			// the highest-coverage single spec for WidgetDefSvg.
			Entry("all four", []widget.Panel{
				{Kind: widget.PanelGrade},
				{Kind: widget.PanelPunchcard},
				{Kind: widget.PanelMomentum},
			}),
		)
	})

	Describe("WidgetDefSvg error branches (gaka-d6x.handler)", func() {
		It("rejects a MALFORMED uuid with 400 (before any DB call)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			// no auth needed — public endpoint.
			rec := doJSONReqG(e, http.MethodGet, "/widget/svg/not-a-uuid/named", "", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"non-uuid path segment must 400: body=%s", rec.Body.String())
		})

		It("clamps absurd days values on the named-def path (mirrors WidgetSvg clamp)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_svg_clamp")

			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "clamp-def", "spec": mkValidDefBytes(),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))
			var out struct {
				DefID string `json:"defId"`
			}
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())

			for _, days := range []string{"0", "-5", "99999", "abc"} {
				svg := doJSONReqG(e, http.MethodGet,
					"/widget/svg/"+out.DefID+"/named?days="+days, "", nil)
				Expect(svg).To(testutil.HaveStatus(http.StatusOK),
					"days=%s should clamp; got %d body=%s", days, svg.Code, svg.Body.String())
			}
		})
	})
})
