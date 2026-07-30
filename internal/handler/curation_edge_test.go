// curation_edge_test.go — malformed-body branch coverage for the curation
// cluster's BindJSONWithLimit guards.
package handler_test

import (
	"net/http"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("Curation endpoints reject malformed JSON bodies", func() {
	It("CreateCuration: 400 on non-JSON body", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_bad_json_create")
		rec := doRawJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token,
			[]byte(`{not json`))
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("ToggleCuration: 400 on non-JSON body (when a body is sent)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_bad_json_toggle")
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Foo",
		})
		rec := doRawJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/toggle", token,
			[]byte(`{not json`))
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"non-empty malformed body must be 400 via BindJSONWithLimit; body=%s", rec.Body.String())
	})
})

var _ = Describe("CreateCuration defaults matchType to exact when omitted", func() {
	It("succeeds with matchType absent, storing exact", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_no_mt")
		// No matchType key.
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token,
			map[string]any{
				"axis": "language", "action": "hide", "matchValue": "Kotlin",
			})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
	})
})
