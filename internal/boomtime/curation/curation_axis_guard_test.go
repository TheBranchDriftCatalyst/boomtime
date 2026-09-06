// curation_axis_guard_test.go — boom-l827 (audit 2026-09-06) regressions for
// the two axis-shaped failures on the curation surface:
//
//  1. MEDIUM — a hide rule on a NON-TEXT explore axis (`day` is
//     time_sent::date, `isWrite` is boolean) was accepted, then blew up the
//     audit view: CurationAffectedValues builds `lower(<col>) = lower($2)`,
//     Postgres has no lower(date)/lower(boolean) (SQLSTATE 42883), the handler
//     funnels it through apierr.Generic() and the user gets a 500 for a rule
//     the API itself handed them. The rule was inert anyway — LoadHiddenSets
//     only reads the 8 registry axes — so the fix is to refuse to author it,
//     plus a 400 (not 500) for rows that predate the guard.
//
//  2. LOW — POST /curation/:id/purge (and /apply) on an axis with no raw
//     heartbeats column (entity, day, type, userAgent, isWrite) returned 500
//     via apierr.Generic() while GET /preview on the SAME rule returned a
//     clean 400 with the reason. Same class of client mistake, two different
//     answers, and monitoring counted the 500 as a server fault.
//
// The specs deliberately drive the HTTP surface (not the helpers) because the
// status code IS the defect.
package curation_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("curation non-text axis guard (boom-l827)", func() {
	It("rejects a hide rule on the day axis with 400 (it can never match)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_day_hide")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "day", "action": "hide", "matchType": "exact", "matchValue": "2026-01-01",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"a hide rule on time_sent::date is inert AND makes /affected 500 — it must not be storable; body=%s",
			rec.Body.String())
	})

	It("rejects a hide rule on the isWrite (boolean) axis with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_iswrite_hide")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "isWrite", "action": "hide", "matchType": "exact", "matchValue": "true",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
	})

	It("still accepts a hide rule on a text axis (guard must not over-reject)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_txt_hide")
		seedRenameableHeartbeats(hz, user)

		// language (registry axis) and entity (explore-only TEXT axis) both stay
		// authorable — only the two non-text axes are refused.
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Python",
		})
		Expect(id).NotTo(BeZero())
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "entity", "action": "hide", "matchType": "exact", "matchValue": "main.py",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
	})

	// A rule stored BEFORE the authoring guard (or by any non-HTTP writer) must
	// still not 500 the audit view. Insert it through the DB layer directly —
	// that is exactly the legacy-row shape.
	It("answers 400 (not 500) on /affected for a pre-existing day-axis rule", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_day_legacy")
		seedRenameableHeartbeats(hz, user)

		rule, err := hz.DB.CreateCurationRuleWithIngest(context.Background(), user,
			"day", db.CurationHide, db.MatchExact, "2026-01-01", nil, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(rule.ID).NotTo(BeZero())

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/curation/"+strconv.Itoa(rule.ID)+"/affected", token, nil)
		Expect(rec.Code).NotTo(Equal(http.StatusInternalServerError),
			"lower(time_sent::date) blew up as a 500 again — the /affected pre-check is gone; body=%s",
			rec.Body.String())
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
	})

	It("still returns matched values on /affected for a text axis", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_txt_affected")
		seedRenameableHeartbeats(hz, user)

		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Python",
		})
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/affected", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		var out struct {
			Values []db.AffectedValue `json:"values"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Values).NotTo(BeEmpty())
	})
})

var _ = Describe("curation purge/apply on an axis with no raw column (boom-l827)", func() {
	It("400s (not 500s) when purging a hide rule on the entity axis", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_pge_entity")
		seedRenameableHeartbeats(hz, user)

		// `entity` is whitelisted for curation (the FE offers it) but has no
		// column in the axis registry, so buildPurgeDeleteSQL cannot build a
		// DELETE for it. The PREVIEW of this exact rule already 400s.
		id := createRule(e, token, map[string]any{
			"axis": "entity", "action": "hide", "matchType": "exact", "matchValue": "main.py",
		})

		prev := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/preview", token, nil)
		Expect(prev).To(testutil.HaveStatus(http.StatusBadRequest),
			"precondition: preview already answers 400 for this rule; body=%s", prev.Body.String())

		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/purge", token, nil)
		Expect(rec.Code).NotTo(Equal(http.StatusInternalServerError),
			"purge answered 500 for a rule whose preview answers 400 — the well-formed-client-mistake path is back on the server-fault ledger; body=%s",
			rec.Body.String())
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())

		// The rule must SURVIVE a rejected purge (the destructive path is
		// transactional: no rows, no rule deletion).
		listRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/curation", token, nil)
		var listOut struct {
			Rules []db.CurationRule `json:"rules"`
		}
		Expect(json.Unmarshal(listRec.Body.Bytes(), &listOut)).To(Succeed())
		var stillThere bool
		for _, r := range listOut.Rules {
			if r.ID == id {
				stillThere = true
			}
		}
		Expect(stillThere).To(BeTrue(), "a rejected purge must not delete the rule row")
	})

	It("400s (not 500s) when applying a rename rule on the entity axis", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_apl_entity")
		seedRenameableHeartbeats(hz, user)

		newVal := "renamed.py"
		id := createRule(e, token, map[string]any{
			"axis": "entity", "action": "rename", "matchType": "exact",
			"matchValue": "main.py", "newValue": newVal,
		})
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/apply", token, nil)
		Expect(rec.Code).NotTo(Equal(http.StatusInternalServerError),
			"apply answered 500 for an axis with no raw column; body=%s", rec.Body.String())
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
	})

	It("leaves the real purge path working on a registry axis (200 + rows deleted)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_pge_guard_ok")
		seedRenameableHeartbeats(hz, user)
		Expect(countHeartbeatsWithLanguage(hz, user, "Python")).To(BeNumerically(">", 0))

		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Python",
		})
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/purge", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK),
			"the guard must not reject a purgeable registry axis; body=%s", rec.Body.String())
		Expect(countHeartbeatsWithLanguage(hz, user, "Python")).To(BeZero())
	})
})
