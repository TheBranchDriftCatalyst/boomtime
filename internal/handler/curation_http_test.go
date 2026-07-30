// curation_http_test.go — end-to-end HTTP coverage for the curation
// cluster (gaka-d6x.handler). Complements the small in-package
// curation_test.go (which pins whitelist + wire constants) with full
// request/response paths for every handler in curation.go:
//
//	ListCuration, CreateCuration, DeleteCuration,
//	ToggleCuration, CurationAffected,
//	ApplyRenamePreview, ApplyRename, PurgeHidden,
//	resolveCurationRule (indirect via the destructive paths)
//
// Named invariants pinned by each spec:
//
//   - "axis whitelist gates create"           → CreateCuration
//   - "action whitelist gates create"         → CreateCuration
//   - "regex validated up front"              → CreateCuration
//   - "template only on rename"               → CreateCuration
//   - "day axis cannot be renamed"            → CreateCuration
//   - "template backref count validated"      → CreateCuration
//   - "cross-user isolation on ListCuration"  → security
//   - "cross-user isolation on DeleteCuration"→ security
//   - "cross-user isolation on ToggleCuration"→ security
//   - "cross-user isolation on Affected"      → security
//   - "cross-user isolation on Preview"       → security
//   - "cross-user isolation on Apply/Purge"   → security
//   - "toggle body flip vs explicit"          → ToggleCuration
//   - "disabled rule blocks Apply/Purge"      → gaka-dfd guards
//   - "purge requires hide, apply requires rename"→ action gates
//   - "hide preview shape != rename preview"  → payload shape
//   - "affected exposes matched values"       → CurationAffected
package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// countHeartbeatsWithLanguage returns the number of heartbeat rows for `owner`
// whose `language` column exactly equals `lang`. Used by the destructive-path
// specs to verify the underlying raw data actually changed — not just that the
// handler returned a rowsAffected > 0.
func countHeartbeatsWithLanguage(hz *testutil.Harness, owner, lang string) int64 {
	var n int64
	err := hz.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM heartbeats WHERE sender=$1 AND language=$2`,
		owner, lang).Scan(&n)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(),
		"count heartbeats sender=%s language=%s: %v", owner, lang, err)
	return n
}

// createRule POSTs to /curation and returns the id from the response. On a
// non-2xx it fails the spec with the body for diagnosability.
func createRule(e http.Handler, token string, body map[string]any) int {
	rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, body)
	ExpectWithOffset(1, rec).To(testutil.HaveStatus(http.StatusOK),
		"create curation rule: body=%s", rec.Body.String())
	var out struct {
		Rule struct {
			ID int `json:"id"`
		} `json:"rule"`
	}
	ExpectWithOffset(1, json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
	ExpectWithOffset(1, out.Rule.ID).NotTo(BeZero(), "rule id was zero: %s", rec.Body.String())
	return out.Rule.ID
}

// seedRenameableHeartbeats seeds 3 heartbeats on `language="Python"` so a
// rename rule targeting Python has something to affect / rewrite.
func seedRenameableHeartbeats(hz *testutil.Harness, user string) {
	start := time.Now().UTC().Add(-6 * time.Hour).Truncate(time.Minute)
	hz.Seeder(user).Projects("p1").Block(testutil.HB{
		Project:  "p1",
		Language: "Python",
		Editor:   "vim",
		Platform: "linux",
		Category: "coding",
		Entity:   "main.py",
	}, start, 5, 60)
}

var _ = Describe("CreateCuration input validation (gaka-d6x.handler)", func() {
	It("rejects unknown axis with 400 (whitelist gate)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_bad_axis")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "sender", "action": "hide", "matchType": "exact", "matchValue": "foo",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"unknown axis must 400: body=%s", rec.Body.String())
	})

	It("rejects unknown action with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_bad_action")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "purge", "matchType": "exact", "matchValue": "Go",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("rejects empty matchValue with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_empty_mv")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("rejects unknown matchType with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_bad_mt")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "glob", "matchValue": "*",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("rejects template matchType on a hide rule (template is rename-only)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_tpl_hide")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "template", "matchValue": "^(.*)$",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"hide + template must 400 — template's semantics require a rename target")
	})

	It("rejects rename without newValue with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_rn_no_nv")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "rename", "matchType": "exact", "matchValue": "py",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("rejects rename on the day axis with 400 (dates are not remappable)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_day_rn")
		nv := "2025-01-01"

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "day", "action": "rename", "matchType": "exact", "matchValue": "2024-12-31", "newValue": nv,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("rejects a regex rule whose pattern does not compile", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_bad_re")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "regex", "matchValue": "[",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"unclosed [ must be a compile-time reject, not a runtime blow-up")
	})

	It("rejects a template with an out-of-range backref (\\9 with 1 group)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_bad_tpl")
		nv := `\9`

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "rename", "matchType": "template",
			"matchValue": "^(.*)$", "newValue": nv,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("normalizes $1 → \\1 in a template newValue and persists the canonical form", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_tpl_norm")
		nv := `$1-suffix`

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "rename", "matchType": "template",
			"matchValue": "^(.+)$", "newValue": nv,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		var out struct {
			Rule struct {
				NewValue *string `json:"newValue"`
			} `json:"rule"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Rule.NewValue).NotTo(BeNil())
		Expect(*out.Rule.NewValue).To(Equal(`\1-suffix`),
			"$1 must normalize to \\1 before persistence — Postgres regexp_replace only groks \\N backrefs")
	})
})

var _ = Describe("ListCuration + DeleteCuration cross-user isolation", func() {
	It("does NOT return user A's rules to user B and prevents delete-across-owners", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("cur_iso_a")
		_, tokenB := hz.MintUser("cur_iso_b")

		// User A creates a hide rule.
		ruleID := createRule(e, tokenA, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "SecretLang",
		})

		// User B lists → does NOT see A's rule.
		recB := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/curation", tokenB, nil)
		Expect(recB).To(testutil.HaveStatus(http.StatusOK))
		var outB struct {
			Rules []db.CurationRule `json:"rules"`
		}
		Expect(json.Unmarshal(recB.Body.Bytes(), &outB)).To(Succeed())
		for _, r := range outB.Rules {
			Expect(r.ID).NotTo(Equal(ruleID),
				"cross-user leak: user B's list contains user A's rule %d", ruleID)
		}

		// User B tries to DELETE A's rule → 404 (never leak existence).
		recDel := doJSONReqG(e, http.MethodDelete,
			"/api/v1/users/current/curation/"+strconv.Itoa(ruleID), tokenB, nil)
		Expect(recDel).To(testutil.HaveStatus(http.StatusNotFound),
			"cross-user delete must 404, got %d body=%s", recDel.Code, recDel.Body.String())

		// User A's rule is STILL there.
		recA := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/curation", tokenA, nil)
		Expect(recA).To(testutil.HaveStatus(http.StatusOK))
		var outA struct {
			Rules []db.CurationRule `json:"rules"`
		}
		Expect(json.Unmarshal(recA.Body.Bytes(), &outA)).To(Succeed())
		found := false
		for _, r := range outA.Rules {
			if r.ID == ruleID {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "user A's own rule vanished after user B's failed delete")
	})

	It("returns 400 on non-integer rule id in the DELETE path", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_bad_del_id")
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/curation/not-an-int", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("returns 404 when deleting an id that no user owns", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_del_ghost")
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/curation/9999999", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("204s on a successful owner-scoped delete and removes the rule from ListCuration", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_del_own")
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "TypeScript",
		})
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/curation/"+strconv.Itoa(id), token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent))

		listRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/curation", token, nil)
		var out struct {
			Rules []db.CurationRule `json:"rules"`
		}
		Expect(json.Unmarshal(listRec.Body.Bytes(), &out)).To(Succeed())
		for _, r := range out.Rules {
			Expect(r.ID).NotTo(Equal(id), "rule still visible after successful delete")
		}
	})
})

var _ = Describe("ToggleCuration (gaka-dfd) - flip, set, isolation", func() {
	It("flips enabled with an empty body and returns the new value", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_tog_flip")
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Ruby",
		})
		// Empty-body toggle → newly-created rule (enabled=true) flips to false.
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/toggle", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var out struct {
			Enabled bool `json:"enabled"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Enabled).To(BeFalse(), "empty-body toggle on enabled rule should produce enabled=false")
	})

	It("sets enabled to an EXACT value (idempotent no-op on second call)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_tog_set")
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Rust",
		})
		// Explicit set to false.
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/toggle", token,
			map[string]any{"enabled": false})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var out struct {
			Enabled bool `json:"enabled"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Enabled).To(BeFalse())

		// Idempotent second call (already false).
		rec2 := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/toggle", token,
			map[string]any{"enabled": false})
		Expect(rec2).To(testutil.HaveStatus(http.StatusOK),
			"idempotent no-op set must NOT 404: body=%s", rec2.Body.String())
	})

	It("rejects a bad numeric rule id with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_tog_badid")
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/abc/toggle", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("404s cross-user (never leaks that another user's rule exists)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("cur_tog_a")
		_, tokenB := hz.MintUser("cur_tog_b")
		id := createRule(e, tokenA, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Elixir",
		})
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/toggle", tokenB, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
			"cross-user toggle MUST 404 — anything else leaks rule existence to another user")
	})
})

var _ = Describe("CurationAffected", func() {
	It("returns matched raw values + counts (owner-scoped)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_aff_own")
		seedRenameableHeartbeats(hz, user)

		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Python",
		})
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/affected", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		var out struct {
			Values    []db.AffectedValue `json:"values"`
			Truncated bool               `json:"truncated"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Values).NotTo(BeEmpty(), "hide-rule on Python must find the seeded rows")
		found := false
		for _, v := range out.Values {
			if v.Value == "Python" && v.Count > 0 {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "expected Python to appear with count>0, got %+v", out.Values)
	})

	It("404s cross-user (rule owner-scoped)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		userA, tokenA := hz.MintUser("cur_aff_a")
		_, tokenB := hz.MintUser("cur_aff_b")
		seedRenameableHeartbeats(hz, userA)
		id := createRule(e, tokenA, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Python",
		})
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/affected", tokenB, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("400s on a non-integer rule id", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_aff_bad_id")
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/curation/foo/affected", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})
})

var _ = Describe("ApplyRenamePreview - dispatches on action", func() {
	It("returns rename-shaped payload for a rename rule", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_prv_rn")
		seedRenameableHeartbeats(hz, user)
		newVal := "python"
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "rename", "matchType": "exact",
			"matchValue": "Python", "newValue": newVal,
		})
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/preview", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got["action"]).To(Equal("rename"),
			"rename preview must carry action=rename discriminator for FE dispatch")
		for _, k := range []string{"sqlUpdate", "sqlDelete", "sqlPlanned", "totalAffected", "affectedRows", "rule"} {
			Expect(got).To(HaveKey(k), "rename preview must include %q; keys=%v", k, mapKeys(got))
		}
		// sqlDeleteRows/sqlDeleteRule are hide-only keys.
		Expect(got).NotTo(HaveKey("sqlDeleteRows"), "hide-only key leaked into rename preview")
	})

	It("returns hide-shaped payload for a hide rule", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_prv_hd")
		seedRenameableHeartbeats(hz, user)
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Python",
		})
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/preview", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got["action"]).To(Equal("hide"),
			"hide preview must carry action=hide discriminator")
		for _, k := range []string{"sqlDeleteRows", "sqlDeleteRule", "sqlPlanned", "totalAffected", "affectedRows", "rule"} {
			Expect(got).To(HaveKey(k), "hide preview must include %q; keys=%v", k, mapKeys(got))
		}
		// sqlUpdate is rename-only.
		Expect(got).NotTo(HaveKey("sqlUpdate"), "rename-only key leaked into hide preview")
	})

	It("404s cross-user (never leaks another user's rule id)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		userA, tokenA := hz.MintUser("cur_prv_a")
		_, tokenB := hz.MintUser("cur_prv_b")
		seedRenameableHeartbeats(hz, userA)
		id := createRule(e, tokenA, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Python",
		})
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/preview", tokenB, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("400s on a non-integer rule id", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_prv_bad")
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/curation/nope/preview", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})
})

var _ = Describe("ApplyRename destructive path", func() {
	It("rewrites matched heartbeats + removes the rule (transactional)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_apl_ok")
		seedRenameableHeartbeats(hz, user)
		// Baseline: seeded rows are on the OLD label ("Python").
		Expect(countHeartbeatsWithLanguage(hz, user, "Python")).To(BeNumerically(">", 0),
			"precondition: fixture must have Python rows before apply")
		Expect(countHeartbeatsWithLanguage(hz, user, "python")).To(BeZero(),
			"precondition: fixture must NOT already contain the new label")

		newVal := "python"
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "rename", "matchType": "exact",
			"matchValue": "Python", "newValue": newVal,
		})
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/apply", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got).To(HaveKey("rowsAffected"))
		Expect(got).To(HaveKey("sqlRun"))
		Expect(got).To(HaveKey("sqlUpdate"))
		Expect(got).To(HaveKey("sqlDelete"))
		// Should have touched >0 rows on our seeded fixture.
		Expect(got["rowsAffected"]).To(BeNumerically(">", 0),
			"apply on seeded Python rows must rewrite >0 rows: %+v", got)

		// Non-tautological invariant: the RAW heartbeat rows now carry the new
		// value. A DB layer that reported rowsAffected=N + cleared the rule row
		// without actually rewriting heartbeats would pass the shape check
		// above but fail here — silently orphaning the raw data claim in the
		// handler comment. Assert the whole seeded set flipped: old label is
		// gone, new label carries every original row.
		Expect(countHeartbeatsWithLanguage(hz, user, "Python")).To(BeZero(),
			"apply MUST rewrite every seeded 'Python' row — none may remain")
		Expect(countHeartbeatsWithLanguage(hz, user, "python")).To(BeNumerically(">", 0),
			"apply MUST land the new label 'python' on the rewritten rows")

		// Rule must be gone from the DB.
		listRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/curation", token, nil)
		var listOut struct {
			Rules []db.CurationRule `json:"rules"`
		}
		Expect(json.Unmarshal(listRec.Body.Bytes(), &listOut)).To(Succeed())
		for _, r := range listOut.Rules {
			Expect(r.ID).NotTo(Equal(id), "apply must have deleted the rule row atomically")
		}
	})

	It("400s when the rule is a HIDE (only rename is apply-able)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_apl_hide")
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Ruby",
		})
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/apply", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("400s when the rule is disabled (gaka-dfd guard)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_apl_disabled")
		newVal := "python"
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "rename", "matchType": "exact",
			"matchValue": "Python", "newValue": newVal,
		})
		// Toggle it off, then attempt apply.
		tog := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/toggle", token,
			map[string]any{"enabled": false})
		Expect(tog).To(testutil.HaveStatus(http.StatusOK))

		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/apply", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"disabled rename rule must NOT be applyable; body=%s", rec.Body.String())
	})

	It("404s cross-user + 400 on a bad id", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("cur_apl_iso_a")
		_, tokenB := hz.MintUser("cur_apl_iso_b")
		newVal := "python"
		id := createRule(e, tokenA, map[string]any{
			"axis": "language", "action": "rename", "matchType": "exact",
			"matchValue": "Python", "newValue": newVal,
		})

		// bad id
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/notanint/apply", tokenA, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))

		// cross-user
		rec2 := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/apply", tokenB, nil)
		Expect(rec2).To(testutil.HaveStatus(http.StatusNotFound),
			"cross-user apply must 404, not 400/403 (never leak existence)")
	})
})

var _ = Describe("PurgeHidden destructive path", func() {
	It("deletes matched heartbeats + removes the rule (transactional)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_pge_ok")
		seedRenameableHeartbeats(hz, user)
		// Baseline: seeded rows exist on the target language.
		Expect(countHeartbeatsWithLanguage(hz, user, "Python")).To(BeNumerically(">", 0),
			"precondition: fixture must have Python rows before purge")

		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Python",
		})
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/purge", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got).To(HaveKey("rowsAffected"))
		Expect(got).To(HaveKey("sqlRun"))
		Expect(got).To(HaveKey("sqlDeleteRows"))
		Expect(got).To(HaveKey("sqlDeleteRule"))
		Expect(got["rowsAffected"]).To(BeNumerically(">", 0),
			"purge on seeded Python rows must delete >0 rows: %+v", got)

		// Non-tautological invariant: the RAW heartbeat rows are gone from the
		// DB — a delete-that-doesn't-delete would still pass the shape check
		// above (this is the scariest endpoint in the family — the handler
		// comment says so, and the test must match).
		Expect(countHeartbeatsWithLanguage(hz, user, "Python")).To(BeZero(),
			"purge MUST have removed every seeded Python heartbeat — none may remain")

		listRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/curation", token, nil)
		var listOut struct {
			Rules []db.CurationRule `json:"rules"`
		}
		Expect(json.Unmarshal(listRec.Body.Bytes(), &listOut)).To(Succeed())
		for _, r := range listOut.Rules {
			Expect(r.ID).NotTo(Equal(id), "purge must have deleted the rule row atomically")
		}
	})

	It("400s when the rule is a RENAME (only hide is purge-able)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_pge_rn")
		newVal := "python"
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "rename", "matchType": "exact",
			"matchValue": "Python", "newValue": newVal,
		})
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/purge", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("400s when the hide rule is disabled (gaka-dfd guard)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_pge_disabled")
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Python",
		})
		tog := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/toggle", token,
			map[string]any{"enabled": false})
		Expect(tog).To(testutil.HaveStatus(http.StatusOK))

		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/purge", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"disabled hide rule must NOT be purgeable; body=%s", rec.Body.String())
	})

	It("404s cross-user + 400 on a bad id", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("cur_pge_iso_a")
		_, tokenB := hz.MintUser("cur_pge_iso_b")
		id := createRule(e, tokenA, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Python",
		})
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/badid/purge", tokenA, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))

		rec2 := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/purge", tokenB, nil)
		Expect(rec2).To(testutil.HaveStatus(http.StatusNotFound),
			"cross-user purge must 404")
	})
})

var _ = Describe("CreateCuration re-insert reactivates a disabled rule (dedup ON CONFLICT)", func() {
	It("re-POSTing the same rule flips enabled back to true (gaka-dfd)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_re_ins")
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Perl",
		})
		// Disable it.
		tog := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/toggle", token,
			map[string]any{"enabled": false})
		Expect(tog).To(testutil.HaveStatus(http.StatusOK))

		// Re-create identical rule — the ON CONFLICT branch flips enabled=true.
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Perl",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		var out struct {
			Rule struct {
				ID      int  `json:"id"`
				Enabled bool `json:"enabled"`
			} `json:"rule"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Rule.ID).To(Equal(id), "dedup must reuse the SAME id, not orphan the old row")
		Expect(out.Rule.Enabled).To(BeTrue(),
			"re-adding a rule you previously paused must clearly express 'on again'")
	})
})

// mapKeys returns a sorted slice of keys for diag messages.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
