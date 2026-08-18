// testhelpers_test.go — curation-package copy of the external ginkgo
// test helpers originally defined in internal/handler/testhelpers_test.go
// (doJSONReqG) and internal/handler/bigbets_test.go (mapKeys). Kept in
// sync with the ingest / identity / widgets copies on purpose: each
// file is private to its _test package, so a shared testutil-side
// location would need to lift the gomega dependency into the harness.
// Follow-up phase 8 will move these to a shared package once the
// domain extracts settle.
package curation_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/gomega"
)

// doJSONReqG issues a JSON request against the harness router. Ginkgo variant:
// panics via Fail rather than *testing.T.Fatalf so failures inside helpers
// still show up as spec failures. Copied byte-identically from
// internal/handler/testhelpers_test.go.
func doJSONReqG(e http.Handler, method, target, token string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		Expect(json.NewEncoder(&buf).Encode(body)).To(Succeed())
	}
	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// mapKeys returns the sorted key list of a JSON-decoded map — used in
// diagnostic messages so a failing shape assertion names WHICH keys the
// endpoint actually returned instead of a bare mismatch. Copied
// byte-identically from internal/handler/bigbets_test.go.
func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
