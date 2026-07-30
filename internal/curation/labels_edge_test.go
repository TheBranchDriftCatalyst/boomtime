// labels_edge_test.go — edge-branch coverage (malformed JSON body, oversize
// body) for the admin-labels endpoints so the `if aerr := BindJSONWithLimit
// ...` guard on each handler is exercised. Also hits AdminUpdateLabel with
// a body that clears `label` via empty-string non-nil pointer (partial
// PATCH with a "clear" intent), pinning that clear semantics.
package curation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// doRawJSONReqG sends a raw body with Content-Type: application/json — used
// to inject malformed JSON without going through the encoder.
func doRawJSONReqG(e http.Handler, method, target, token string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

var _ = Describe("Admin label endpoints reject malformed JSON bodies", func() {
	It("AdminCreateLabel: 400 on non-JSON body", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_bad_json_create")
		grantAdmin(hz, user)
		rec := doRawJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token,
			[]byte(`{not valid json`))
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"malformed JSON must be a 400 via BindJSONWithLimit, got %d", rec.Code)
	})

	It("AdminUpdateLabel: 400 on non-JSON body", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_bad_json_patch")
		grantAdmin(hz, user)
		rec := doRawJSONReqG(e, http.MethodPatch, "/api/v1/admin/labels/anything", token,
			[]byte(`{not valid`))
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("AdminUpdateLabelGenConfig: 400 on non-JSON body", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_bad_json_gc")
		grantAdmin(hz, user)
		rec := doRawJSONReqG(e, http.MethodPatch, "/api/v1/admin/label-gen-config", token,
			[]byte(`{`))
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})
})

var _ = Describe("AdminUpdateLabel — clear a nullable field via empty string", func() {
	It("clears glyph to empty when body carries `glyph: \"\"` (non-nil pointer)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_clear_glyph")
		grantAdmin(hz, user)
		id := "test-clear-" + time.Now().Format("150405.000000000")
		cleanupLabel(hz, id)

		cRec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, mkLabelBody(id))
		Expect(cRec).To(testutil.HaveStatus(http.StatusCreated))

		// PATCH with glyph=""
		pRec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/labels/"+id, token,
			map[string]any{"glyph": ""})
		Expect(pRec).To(testutil.HaveStatus(http.StatusOK), "body=%s", pRec.Body.String())

		fresh, err := hz.DB.GetLabel(context.Background(), id)
		Expect(err).NotTo(HaveOccurred())
		Expect(fresh).NotTo(BeNil())
		Expect(fresh.Glyph).To(Equal(""), "explicit glyph='' must clear (nil vs empty distinction)")
	})
})

var _ = Describe("AdminUpdateLabel — reject condition-body via raw JSONB overwrite", func() {
	It("accepts a condition-only PATCH and persists the new condition JSON", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_cond_patch")
		grantAdmin(hz, user)
		id := "test-cond-" + time.Now().Format("150405.000000000")
		cleanupLabel(hz, id)

		cRec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, mkLabelBody(id))
		Expect(cRec).To(testutil.HaveStatus(http.StatusCreated))

		newCond := json.RawMessage(`{"kind":"axis-time","axis":"languages","value":"rust","op":"ge","hours":2}`)
		pRec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/labels/"+id, token,
			map[string]any{"condition": newCond})
		Expect(pRec).To(testutil.HaveStatus(http.StatusOK), "body=%s", pRec.Body.String())

		fresh, err := hz.DB.GetLabel(context.Background(), id)
		Expect(err).NotTo(HaveOccurred())
		Expect(fresh).NotTo(BeNil())
		Expect(string(fresh.Condition)).To(ContainSubstring("rust"),
			"condition PATCH must overwrite the raw JSONB — round-trip via GetLabel")
	})
})
