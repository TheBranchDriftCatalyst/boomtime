// pin_http_test.go — the create-rule HTTP path accepts action="pin"
// (canonical entities) while still rejecting an unknown action. Complements
// the existing "action whitelist gates create" spec in curation_http_test.go.
package curation_test

import (
	"encoding/json"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("CreateCuration pin action (canonical entities)", func() {
	It("accepts action=pin and persists it as a pin rule", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_pin_ok")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "pin", "matchType": "exact", "matchValue": "Go",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK),
			"pin action must be accepted: body=%s", rec.Body.String())

		var out struct {
			Rule struct {
				Action     string `json:"action"`
				Axis       string `json:"axis"`
				MatchValue string `json:"matchValue"`
				Enabled    bool   `json:"enabled"`
			} `json:"rule"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Rule.Action).To(Equal("pin"))
		Expect(out.Rule.Axis).To(Equal("language"))
		Expect(out.Rule.MatchValue).To(Equal("Go"))
		Expect(out.Rule.Enabled).To(BeTrue())
	})

	It("still rejects an unknown action with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_pin_bad_action")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "bogus", "matchType": "exact", "matchValue": "Go",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"an unknown action must still 400: body=%s", rec.Body.String())
	})
})
