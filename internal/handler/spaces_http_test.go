// spaces_http_test.go — end-to-end HTTP coverage for the spaces cluster
// (gaka-d6x.handler). Covers every handler in spaces.go:
//
//	ListSpaces, CreateSpace, UpdateSpace, DeleteSpace,
//	GetSpace, AddSpaceRule, DeleteSpaceRule, SpacePreview
//
// Named invariants:
//
//   - "name required on create"          → CreateSpace
//   - "duplicate name is a 400"          → CreateSpace (DB uniq)
//   - "PATCH updates name / position"    → UpdateSpace
//   - "PATCH 404 on missing"             → UpdateSpace
//   - "DELETE cascades rules"            → DeleteSpace
//   - "GetSpace returns id/name/rules"   → GetSpace
//   - "GetSpace 404 cross-user"          → GetSpace (isolation)
//   - "AddSpaceRule axis + matchType gates"→ AddSpaceRule
//   - "AddSpaceRule 404 cross-user"      → AddSpaceRule (isolation)
//   - "DeleteSpaceRule 404 cross-user"   → DeleteSpaceRule (isolation)
//   - "SpacePreview axis + matchType"    → SpacePreview
//   - "SpacePreview regex compile guard" → SpacePreview
//   - "ListSpaces isolation"             → cross-user
package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// createSpaceH creates a space + returns its id (fails the spec on non-2xx).
func createSpaceH(e http.Handler, token, name string) int {
	rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/spaces", token,
		map[string]any{"name": name})
	ExpectWithOffset(1, rec).To(testutil.HaveStatus(http.StatusOK),
		"create space %q: body=%s", name, rec.Body.String())
	var out struct {
		Space struct {
			ID int `json:"id"`
		} `json:"space"`
	}
	ExpectWithOffset(1, json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
	ExpectWithOffset(1, out.Space.ID).NotTo(BeZero())
	return out.Space.ID
}

var _ = Describe("CreateSpace input + isolation", func() {
	It("rejects an empty name with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_no_name")
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/spaces", token,
			map[string]any{"name": ""})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("400s on a duplicate name (per-owner uniqueness)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_dupe")
		name := "backend-work"
		createSpaceH(e, token, name)
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/spaces", token,
			map[string]any{"name": name})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"duplicate name must 400, got %d body=%s", rec.Code, rec.Body.String())
	})

	It("returns 401 without an auth token", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/spaces", "", nil)
		Expect(rec.Code).To(BeNumerically(">=", 400),
			"unauth GET spaces must be a 4xx, got %d", rec.Code)
	})
})

var _ = Describe("ListSpaces isolation + shape", func() {
	It("returns ONLY the caller's spaces (never leaks another user's)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("sp_iso_a")
		_, tokenB := hz.MintUser("sp_iso_b")
		aID := createSpaceH(e, tokenA, "alice-space")
		bID := createSpaceH(e, tokenB, "bob-space")

		recB := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/spaces", tokenB, nil)
		Expect(recB).To(testutil.HaveStatus(http.StatusOK))
		var outB struct {
			Spaces []db.Space `json:"spaces"`
		}
		Expect(json.Unmarshal(recB.Body.Bytes(), &outB)).To(Succeed())
		for _, s := range outB.Spaces {
			Expect(s.ID).NotTo(Equal(aID), "user B leaked user A's space id=%d", aID)
			Expect(s.Name).NotTo(Equal("alice-space"))
		}
		// Positive: B sees its own.
		found := false
		for _, s := range outB.Spaces {
			if s.ID == bID {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "user B's own space missing from its own list")
	})
})

var _ = Describe("GetSpace shape + isolation", func() {
	It("returns id/name/position/rules shape and lists membership rules", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_get_shape")
		id := createSpaceH(e, token, "work")
		// Add one rule.
		addRec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id)+"/rules", token,
			map[string]any{"axis": "project", "matchType": "exact", "matchValue": "boomtime"})
		Expect(addRec).To(testutil.HaveStatus(http.StatusOK), "body=%s", addRec.Body.String())

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id), token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		for _, k := range []string{"id", "name", "position", "rules"} {
			Expect(got).To(HaveKey(k), "GetSpace shape missing %q; got=%v", k, got)
		}
		rules, ok := got["rules"].([]any)
		Expect(ok).To(BeTrue(), "rules field must be a JSON array")
		Expect(rules).To(HaveLen(1))
	})

	It("404s cross-user (never leak that another user's space exists)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("sp_get_a")
		_, tokenB := hz.MintUser("sp_get_b")
		id := createSpaceH(e, tokenA, "aOnly")
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id), tokenB, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("400s on a non-integer id", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_get_bad")
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/spaces/notint", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("404s on a numeric id that does not exist", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_get_ghost")
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/spaces/9999999", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})
})

var _ = Describe("UpdateSpace (PATCH name / position)", func() {
	It("updates name and returns 204", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_pat_ok")
		id := createSpaceH(e, token, "original-name")
		newName := "renamed"
		rec := doJSONReqG(e, http.MethodPatch,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id), token,
			map[string]any{"name": newName})
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent))

		// Confirm by GET.
		getRec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id), token, nil)
		var got map[string]any
		Expect(json.Unmarshal(getRec.Body.Bytes(), &got)).To(Succeed())
		Expect(got["name"]).To(Equal(newName))
	})

	It("updates position (int) and returns 204", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_pat_pos")
		id := createSpaceH(e, token, "reorder-me")
		pos := 99
		rec := doJSONReqG(e, http.MethodPatch,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id), token,
			map[string]any{"position": pos})
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent))
	})

	It("404s on a missing id", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_pat_404")
		newName := "wont-stick"
		rec := doJSONReqG(e, http.MethodPatch,
			"/api/v1/users/current/spaces/9999999", token, map[string]any{"name": newName})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("404s cross-user (user B cannot rename user A's space)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("sp_pat_a")
		_, tokenB := hz.MintUser("sp_pat_b")
		id := createSpaceH(e, tokenA, "aOnly-pat")
		newName := "hijacked"
		rec := doJSONReqG(e, http.MethodPatch,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id), tokenB,
			map[string]any{"name": newName})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("400s on a non-integer id", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_pat_bad_id")
		newName := "x"
		rec := doJSONReqG(e, http.MethodPatch,
			"/api/v1/users/current/spaces/notint", token, map[string]any{"name": newName})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})
})

var _ = Describe("DeleteSpace + isolation", func() {
	It("204s on a successful owner-scoped delete", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_del_ok")
		id := createSpaceH(e, token, "to-remove")
		rec := doJSONReqG(e, http.MethodDelete,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id), token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent))

		// Now returns 404 on the follow-up GET.
		getRec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id), token, nil)
		Expect(getRec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("404s cross-user (user B cannot delete user A's space)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("sp_del_a")
		_, tokenB := hz.MintUser("sp_del_b")
		id := createSpaceH(e, tokenA, "a-not-deleteable")
		rec := doJSONReqG(e, http.MethodDelete,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id), tokenB, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
		// User A's space still there.
		getRec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id), tokenA, nil)
		Expect(getRec).To(testutil.HaveStatus(http.StatusOK))
	})

	It("400s on a non-integer id", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_del_bad")
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/spaces/nope", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("404s on a numeric id that does not exist", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_del_ghost")
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/spaces/9999999", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})
})

var _ = Describe("AddSpaceRule input validation + isolation", func() {
	It("rejects an unknown axis with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_ar_bad_axis")
		id := createSpaceH(e, token, "with-bad-axis")
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id)+"/rules", token,
			map[string]any{"axis": "not_an_axis", "matchType": "exact", "matchValue": "x"})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("rejects empty matchValue with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_ar_empty")
		id := createSpaceH(e, token, "with-empty")
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id)+"/rules", token,
			map[string]any{"axis": "project", "matchType": "exact", "matchValue": ""})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("rejects matchType=template on a space rule (only exact|regex for spaces)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_ar_tpl")
		id := createSpaceH(e, token, "no-tpl")
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id)+"/rules", token,
			map[string]any{"axis": "project", "matchType": "template", "matchValue": "^(.*)$"})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"template is a curation-only match type; spaces reject it")
	})

	It("rejects a regex whose pattern does not compile", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_ar_badre")
		id := createSpaceH(e, token, "bad-regex-space")
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id)+"/rules", token,
			map[string]any{"axis": "project", "matchType": "regex", "matchValue": "["})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("404s when adding a rule to a space owned by another user", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("sp_ar_a")
		_, tokenB := hz.MintUser("sp_ar_b")
		id := createSpaceH(e, tokenA, "a-scope")
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id)+"/rules", tokenB,
			map[string]any{"axis": "project", "matchType": "exact", "matchValue": "sneak"})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
			"cross-user AddSpaceRule must 404 — otherwise user B can enumerate user A's space ids")
	})

	It("400s when the space id is not an integer", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_ar_bad_id")
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/spaces/notint/rules", token,
			map[string]any{"axis": "project", "matchType": "exact", "matchValue": "x"})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("accepts a valid exact rule and returns it in the rule envelope", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_ar_ok")
		id := createSpaceH(e, token, "ok-space")
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id)+"/rules", token,
			map[string]any{"axis": "language", "matchType": "exact", "matchValue": "Go"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var out struct {
			Rule db.SpaceRule `json:"rule"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Rule.ID).NotTo(BeZero())
		Expect(out.Rule.Axis).To(Equal("language"))
		Expect(out.Rule.MatchValue).To(Equal("Go"))
		Expect(out.Rule.MatchType).To(Equal(db.MatchExact))
	})

	It("defaults matchType to 'exact' when omitted", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_ar_default")
		id := createSpaceH(e, token, "default-mt")
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id)+"/rules", token,
			map[string]any{"axis": "project", "matchValue": "boomtime"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var out struct {
			Rule db.SpaceRule `json:"rule"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Rule.MatchType).To(Equal(db.MatchExact),
			"empty matchType must default to 'exact' (mirrors curation shape)")
	})
})

var _ = Describe("DeleteSpaceRule + isolation", func() {
	It("204s on a successful owner-scoped rule delete", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_dr_ok")
		id := createSpaceH(e, token, "with-a-rule")
		addRec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id)+"/rules", token,
			map[string]any{"axis": "language", "matchType": "exact", "matchValue": "Go"})
		var addOut struct {
			Rule db.SpaceRule `json:"rule"`
		}
		Expect(json.Unmarshal(addRec.Body.Bytes(), &addOut)).To(Succeed())

		delURL := fmt.Sprintf("/api/v1/users/current/spaces/%d/rules/%d", id, addOut.Rule.ID)
		rec := doJSONReqG(e, http.MethodDelete, delURL, token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent))
	})

	It("404s when the rule id does not exist under that space", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_dr_404")
		id := createSpaceH(e, token, "empty-rules-space")
		rec := doJSONReqG(e, http.MethodDelete,
			fmt.Sprintf("/api/v1/users/current/spaces/%d/rules/9999999", id), token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("404s cross-user (never lets user B delete user A's rule)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("sp_dr_a")
		_, tokenB := hz.MintUser("sp_dr_b")
		id := createSpaceH(e, tokenA, "a-with-a-rule")
		addRec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id)+"/rules", tokenA,
			map[string]any{"axis": "language", "matchType": "exact", "matchValue": "Go"})
		var addOut struct {
			Rule db.SpaceRule `json:"rule"`
		}
		Expect(json.Unmarshal(addRec.Body.Bytes(), &addOut)).To(Succeed())

		delURL := fmt.Sprintf("/api/v1/users/current/spaces/%d/rules/%d", id, addOut.Rule.ID)
		rec := doJSONReqG(e, http.MethodDelete, delURL, tokenB, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
			"cross-user rule-delete must 404")
	})

	It("400s on a non-integer space id or rule id", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_dr_bad")
		rec := doJSONReqG(e, http.MethodDelete,
			"/api/v1/users/current/spaces/notint/rules/1", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))

		rec2 := doJSONReqG(e, http.MethodDelete,
			"/api/v1/users/current/spaces/1/rules/notint", token, nil)
		Expect(rec2).To(testutil.HaveStatus(http.StatusBadRequest))
	})
})

var _ = Describe("SpacePreview validation + shape", func() {
	It("400s on unknown axis", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_prv_axis")
		q := url.Values{"axis": {"bogus"}, "matchType": {"exact"}, "matchValue": {"x"}}
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/preview?"+q.Encode(), token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("400s on empty matchValue", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_prv_mv")
		q := url.Values{"axis": {"project"}, "matchType": {"exact"}, "matchValue": {""}}
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/preview?"+q.Encode(), token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("400s on unknown matchType", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_prv_mt")
		q := url.Values{"axis": {"project"}, "matchType": {"glob"}, "matchValue": {"*"}}
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/preview?"+q.Encode(), token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("400s on a regex that does not compile", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_prv_badre")
		q := url.Values{"axis": {"project"}, "matchType": {"regex"}, "matchValue": {"["}}
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/preview?"+q.Encode(), token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("returns matched raw values for an owner-scoped exact preview", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("sp_prv_ok")
		// Seed 1 project row.
		start := time.Now().UTC().Add(-6 * time.Hour).Truncate(time.Minute)
		hz.Seeder(user).Projects("mprj").Block(testutil.HB{
			Project: "mprj", Language: "Go", Editor: "vim",
			Platform: "linux", Category: "coding", Entity: "a.go",
		}, start, 5, 60)

		q := url.Values{"axis": {"project"}, "matchType": {"exact"}, "matchValue": {"mprj"}}
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/preview?"+q.Encode(), token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		var out struct {
			Values    []db.AffectedValue `json:"values"`
			Truncated bool               `json:"truncated"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		found := false
		for _, v := range out.Values {
			if v.Value == "mprj" && v.Count > 0 {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "expected mprj in preview values: %+v", out.Values)
	})

	It("defaults matchType to exact when omitted", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_prv_default")
		// No matchType param.
		q := url.Values{"axis": {"project"}, "matchValue": {"nonexistent-proj"}}
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/preview?"+q.Encode(), token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK),
			"empty matchType should default to exact, not 400: body=%s", rec.Body.String())
	})

	It("scopes preview strictly to the caller — never returns another user's rows", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		userA, _ := hz.MintUser("sp_prv_iso_a")
		_, tokenB := hz.MintUser("sp_prv_iso_b")
		// Seed only for userA.
		start := time.Now().UTC().Add(-6 * time.Hour).Truncate(time.Minute)
		hz.Seeder(userA).Projects("secret").Block(testutil.HB{
			Project: "secret", Language: "Go", Editor: "vim",
			Platform: "linux", Category: "coding", Entity: "a.go",
		}, start, 5, 60)

		q := url.Values{"axis": {"project"}, "matchType": {"exact"}, "matchValue": {"secret"}}
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/preview?"+q.Encode(), tokenB, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var out struct {
			Values []db.AffectedValue `json:"values"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		for _, v := range out.Values {
			Expect(v.Value).NotTo(Equal("secret"),
				"user B saw user A's secret project via SpacePreview — owner-scope violation")
		}
	})
})
