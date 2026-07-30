// testhelpers_test.go — widgets-package copy of the external ginkgo test
// helpers originally defined in internal/handler/testhelpers_test.go. Kept in
// sync with the handler copy on purpose: both files are private to their
// _test package, so a shared testutil-side location would need to lift the
// gomega dependency into the harness. Follow-up cleanup phase 8 will move
// these to a shared package once the domain extracts settle.
package widgets_test

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

// decodeJSONBody unmarshals a JSON payload into dst. Kept as a shared
// helper so per-test decoders stay one line.
func decodeJSONBody(b []byte, dst any) error { return json.Unmarshal(b, dst) }

// lastSegment returns the trailing path segment after the final '/'. Used
// by the badges tests to extract the uuid from `<BadgeURL>/badge/svg/<uuid>`
// without pulling in net/url just to peel off one path element.
func lastSegment(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[i+1:]
		}
	}
	return s
}
