// testhelpers_test.go — ginkgo test helpers shared by the external
// (package spaces_test) suite. Duplicated from
// internal/handler/testhelpers_test.go to keep the spaces domain self-
// contained; phase 8 of the refactor promotes these to a shared
// internal/testutil/handlerhelpers/ package.
package spaces_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/gomega"
)

// doJSONReqG issues a JSON request against the harness router. Ginkgo variant:
// panics via Fail rather than *testing.T.Fatalf so failures inside helpers
// still show up as spec failures.
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
