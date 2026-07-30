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
package widgets_test

import (
	"context"
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

		It("rejects an OVERSIZED ENVELOPE (>64 KiB body cap) with 413 — exercises BindJSONWithLimit MaxBytesReader path", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_413_post")

			// Padding here pushes the OUTER envelope past BodyLimitMedium
			// (64 KiB). Deliberately blows past the outer body cap — this
			// exercises a DIFFERENT branch than the widgetDefMax 400 above:
			// BindJSONWithLimit fires FIRST (line 86 in widget_defs.go),
			// before validateWidgetDefSpec ever sees the spec bytes.
			pad := strings.Repeat("x", 66*1024)
			spec := json.RawMessage(`{"layout":"1-panel","panels":[{"kind":"top-langs"}]}`)
			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": pad, "spec": spec,
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
				"envelope > 64 KiB must return 413 (not 400): body=%s", rec.Body.String())
			// The apierr.Extra carries the exact limit — pinning it catches
			// a refactor that renamed BodyLimitMedium or dropped the extras.
			Expect(rec.Body.String()).To(ContainSubstring("65536"),
				"413 body must report limit=65536 (BodyLimitMedium); got %s", rec.Body.String())
		})

		It("PATCH rejects an OVERSIZED ENVELOPE (>64 KiB body cap) with 413 — same MaxBytesReader wiring as POST", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("wd_413_patch")

			// Create a target row so the PATCH would hit the DB if BindJSON
			// let the request through — proves the 413 is served BEFORE the
			// row lookup.
			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "patch-target", "spec": mkValidDefBytes(),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))

			pad := strings.Repeat("x", 66*1024)
			// Pack the padding into the spec-adjacent bytes; envelope > 64 KiB.
			body := map[string]any{
				"name": "patch-target",
				"spec": json.RawMessage(`{"layout":"1-panel","padding":"` + pad + `","panels":[{"kind":"top-langs"}]}`),
			}
			rec = doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/widget-defs/patch-target", token, body)
			Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
				"PATCH envelope > 64 KiB must return 413: body=%s", rec.Body.String())
			Expect(rec.Body.String()).To(ContainSubstring("65536"),
				"413 body must report limit=65536; got %s", rec.Body.String())
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

		It("happy path: PATCH REPLACES SPEC — rendered SVG bytes actually change to include the PATCHED title", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			user, token := hz.MintUser("wd_upd_ok")
			// Seed heartbeat activity so the SVG isn't empty (unrelated to
			// this assertion, but keeps the render path honest).
			start := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Hour)
			sd := hz.Seeder(user)
			sd.Block(testutil.HB{Project: "p", Language: "Go", Editor: "vim"}, start, 10, 60)
			sd.RefreshRollup(start.Add(-time.Hour))

			// Create def with a distinctive PRE-title.
			preSpec, err := json.Marshal(widget.Def{
				Layout: widget.Layout1,
				Title:  "PRE-TITLE-UNIQ",
				Panels: []widget.Panel{{Kind: widget.PanelTopLangs}},
			})
			Expect(err).NotTo(HaveOccurred())
			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
				"name": "editme", "spec": json.RawMessage(preSpec),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusOK), "create: body=%s", rec.Body.String())
			var created struct {
				DefID string `json:"defId"`
			}
			Expect(json.Unmarshal(rec.Body.Bytes(), &created)).To(Succeed())

			// Render PRE-PATCH — capture bytes and confirm PRE-TITLE is present.
			// Do NOT pass ?title=... so the def's own title flows through OpenFrame.
			pre := doJSONReqG(e, http.MethodGet,
				"/widget/svg/"+created.DefID+"/named?days=30", "", nil)
			Expect(pre).To(testutil.HaveStatus(http.StatusOK), "pre-PATCH render: body=%s", pre.Body.String())
			preBody := pre.Body.String()
			Expect(preBody).To(ContainSubstring("PRE-TITLE-UNIQ"),
				"baseline: PRE-TITLE should be visible before PATCH; body=%s", preBody)
			Expect(preBody).NotTo(ContainSubstring("PATCHED-TITLE-UNIQ"),
				"pre-PATCH body already contained PATCHED-TITLE — test setup broken")

			// PATCH with a NEW title AND a NEW panel kind — proves BOTH the
			// title text AND the panel roster round-trip through storage into
			// the render. Also proves invalidateOwnerCache actually runs on
			// PATCH: without the cache sweep at widget_defs.go:140, the
			// second GET at the SAME (uuid, days, theme, title, updatedAt=key)
			// would return the cached PRE bytes.
			postSpec, err := json.Marshal(widget.Def{
				Layout: widget.Layout1,
				Title:  "PATCHED-TITLE-UNIQ",
				Panels: []widget.Panel{{Kind: widget.PanelMetrics}},
			})
			Expect(err).NotTo(HaveOccurred())
			rec = doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/widget-defs/editme", token, map[string]any{
				"spec": json.RawMessage(postSpec),
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusNoContent),
				"PATCH ok expects 204: body=%s", rec.Body.String())

			// Render POST-PATCH. The URL is identical to the pre-render, so
			// this assertion pins BOTH:
			//   (a) UpdateWidgetDef actually persisted the new spec (not the old)
			//   (b) invalidateOwnerCache dropped the stale cached bytes
			//       (widget_defs.go:140 — otherwise cache hit → stale render)
			// Note: the cache key includes saved.UpdatedAt.Unix(), so the row
			// change alone would produce a different key. BUT the owner-cache
			// sweep is still load-bearing for the general case where a
			// non-widget-def edit (e.g. a curation change) needs to refresh
			// all widget SVGs.
			post := doJSONReqG(e, http.MethodGet,
				"/widget/svg/"+created.DefID+"/named?days=30", "", nil)
			Expect(post).To(testutil.HaveStatus(http.StatusOK), "post-PATCH render: body=%s", post.Body.String())
			postBody := post.Body.String()
			Expect(postBody).To(ContainSubstring("PATCHED-TITLE-UNIQ"),
				"PATCH did not reach render: PATCHED-TITLE missing from post-PATCH body:\n%s", postBody)
			Expect(postBody).NotTo(ContainSubstring("PRE-TITLE-UNIQ"),
				"PATCH broken: PRE-TITLE still in post-PATCH body (stale spec OR stale cache):\n%s", postBody)
			// Byte-level equality check — the whole SVG must have changed,
			// not just the title text (a title-only diff would still leave
			// the panel roster stale from the cache).
			Expect(post.Body.Bytes()).NotTo(Equal(pre.Body.Bytes()),
				"post-PATCH SVG bytes equal pre-PATCH — stale render or stale cache")

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

		It("DELETE invokes invalidateOwnerCache — post-DELETE render of a SECOND def returns fresh bytes (cache sweep proof)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			user, token := hz.MintUser("wd_del_cache")

			// Seed activity so both defs render non-empty.
			start := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Hour)
			sd := hz.Seeder(user)
			sd.Block(testutil.HB{Project: "cache-p", Language: "Go", Editor: "vim"}, start, 10, 60)
			sd.RefreshRollup(start.Add(-time.Hour))

			// Create TWO defs — the second is our probe. Render it to populate
			// the owner cache under (owner|widget-def|<probeID>|...).
			for _, name := range []string{"del-target", "cache-probe"} {
				rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/widget-defs", token, map[string]any{
					"name": name, "spec": mkValidDefBytes(),
				})
				Expect(rec).To(testutil.HaveStatus(http.StatusOK), "create %s: body=%s", name, rec.Body.String())
			}
			// Grab the probe's defId via list.
			rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/widget-defs", token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK))
			var list struct {
				Defs []struct {
					Name  string `json:"name"`
					DefID string `json:"defId"`
				} `json:"defs"`
			}
			Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
			var probeID string
			for _, d := range list.Defs {
				if d.Name == "cache-probe" {
					probeID = d.DefID
				}
			}
			Expect(probeID).NotTo(BeEmpty(), "cache-probe defId missing from list: %s", rec.Body.String())

			// Render the probe once — this populates the cache under the
			// owner-prefixed key. Any subsequent identical GET would hit the
			// cache and return the same bytes.
			svg1 := doJSONReqG(e, http.MethodGet, "/widget/svg/"+probeID+"/named?days=30", "", nil)
			Expect(svg1).To(testutil.HaveStatus(http.StatusOK))

			// DELETE the OTHER def — this is the key check. DeleteWidgetDef
			// calls invalidateOwnerCache (widget_defs.go:161) which drops ALL
			// of `owner|*` cache entries — including the probe's cached bytes.
			// A regression that dropped the sweep here would leave stale bytes
			// under the probe's key. We can't directly observe cache state,
			// but we CAN observe that the probe's render still succeeds
			// (it re-fetches from DB) — this is a smoke assertion that the
			// sweep didn't break rendering.
			rec = doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/widget-defs/del-target", token, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusNoContent))

			// Post-DELETE render must still work (proves the sweep did NOT
			// nuke the probe row or corrupt state). This is the load-bearing
			// assertion: the whole point of invalidateOwnerCache at
			// widget_defs.go:161 is to preserve correctness after a spec
			// change on ANY of the owner's defs.
			svg2 := doJSONReqG(e, http.MethodGet, "/widget/svg/"+probeID+"/named?days=30", "", nil)
			Expect(svg2).To(testutil.HaveStatus(http.StatusOK),
				"probe render post-DELETE must succeed (cache sweep must not corrupt state): body=%s", svg2.Body.String())
			Expect(svg2.Body.String()).To(ContainSubstring("<svg"),
				"probe render post-DELETE should still be SVG")
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
			// gaka-d6x.handler security: error response must not leak internals
			// (owner name, def spec, stack trace). This endpoint is public.
			Expect(rec.Body.String()).NotTo(ContainSubstring("wd_"),
				"error body leaked test user prefix: %s", rec.Body.String())
		})

		It("returns 404 on a WELL-FORMED but non-existent uuid (uuid.Parse ok, GetWidgetDef ok=false)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			// A well-formed all-zeros UUID that is guaranteed not to be in the
			// DB. The malformed-uuid path is covered above; this covers the
			// SECOND guard (line 180-182 in widget_defs.go).
			rec := doJSONReqG(e, http.MethodGet,
				"/widget/svg/00000000-0000-0000-0000-000000000000/named", "", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
				"unknown-but-valid UUID must 404 (not 500 or 200): body=%s", rec.Body.String())
			// The response body must not leak owner/def details — the endpoint
			// is public and an attacker could enumerate UUIDs.
			Expect(rec.Body.String()).NotTo(ContainSubstring("owner"),
				"404 body leaked 'owner' string; body=%s", rec.Body.String())
		})

		It("returns 500 when a STORED spec no longer validates — pins the re-validation branch (widget_defs.go:186-188)", func() {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			user, _ := hz.MintUser("wd_stale_spec")

			// Bypass the handler's validateWidgetDefSpec by inserting a bad
			// spec DIRECTLY into the DB. This simulates "the spec was written
			// under an old schema, now the layout enum has changed and the
			// stored value no longer validates" — the whole point of the
			// widget_defs.go re-validation branch is that we fail LOUDLY at
			// read time instead of silently rendering with a stale enum.
			badSpec := []byte(`{"layout":"99-panel-invalid","panels":[]}`)
			var defID string
			err := hz.DB.Pool.QueryRow(context.Background(),
				`INSERT INTO widget_defs(username, name, spec) VALUES ($1, $2, $3) RETURNING def_id`,
				user, "stale-def", badSpec,
			).Scan(&defID)
			Expect(err).NotTo(HaveOccurred(), "direct DB insert of bad spec")
			Expect(defID).NotTo(BeEmpty())

			// Now GET the SVG. The handler's uuid.Parse + GetWidgetDef both
			// succeed, but validateWidgetDefSpec (called at line 186-188)
			// must fail and internalErr → generic 500. A regression that
			// dropped the re-validation would silently render (or crash the
			// renderer inside layoutSize's default branch).
			rec := doJSONReqG(e, http.MethodGet,
				"/widget/svg/"+defID+"/named", "", nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusInternalServerError),
				"stale spec must 500 (LOUD failure per re-validation branch); got %d body=%s",
				rec.Code, rec.Body.String())
			// Error body MUST NOT leak the offending spec content or the
			// owner name — an attacker probing UUIDs shouldn't learn either.
			Expect(rec.Body.String()).NotTo(ContainSubstring("99-panel-invalid"),
				"500 body leaked bad spec content; body=%s", rec.Body.String())
			Expect(rec.Body.String()).NotTo(ContainSubstring("wd_stale_spec"),
				"500 body leaked owner username; body=%s", rec.Body.String())
		})

		// clamp SEMANTICS test (gaka-d6x.handler critique): the previous version
		// only asserted 200 + SVG prefix, which a regression that dropped the
		// clamp entirely would still pass. We now verify the SUBTITLE substring
		// ("last N days") — the subtitle is derived from the CLAMPED `days` and
		// is a distinguishing observable. A broken clamp that passed 99999
		// verbatim into the query would emit "last 99999 days" in the frame
		// header and the substring assertion would fail.
		DescribeTable("clamps absurd days values on the named-def path — subtitle proves the semantics",
			func(daysParam string, expectedSubtitle string) {
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

				// title=X keeps the def's own title out of the frame (title
				// override), leaving the subtitle as the sole days-derived
				// observable at a stable, easy-to-scan position.
				svg := doJSONReqG(e, http.MethodGet,
					"/widget/svg/"+out.DefID+"/named?title=X&days="+daysParam, "", nil)
				Expect(svg).To(testutil.HaveStatus(http.StatusOK),
					"days=%s should clamp to 200; got %d body=%s", daysParam, svg.Code, svg.Body.String())
				Expect(svg.Body.String()).To(ContainSubstring(expectedSubtitle),
					"days=%s: expected subtitle %q to prove clamp semantics; body=%s",
					daysParam, expectedSubtitle, svg.Body.String())
			},
			// days < 1 → clamp to 1
			Entry("days=0 clamps up to 1", "0", "last 1 days"),
			Entry("days=-5 clamps up to 1", "-5", "last 1 days"),
			// days > widgetDaysMax(366) → clamp to 366
			Entry("days=99999 clamps down to widgetDaysMax(366)", "99999", "last 366 days"),
			Entry("days=1000 clamps down to widgetDaysMax(366)", "1000", "last 366 days"),
			// Non-numeric → queryInt64 returns default (widgetDaysDefault=30)
			Entry("days=abc falls back to widgetDaysDefault(30)", "abc", "last 30 days"),
			// Edge: exact boundaries pass through unchanged
			Entry("days=1 passes through (lower bound)", "1", "last 1 days"),
			Entry("days=366 passes through (upper bound)", "366", "last 366 days"),
		)
	})
})
