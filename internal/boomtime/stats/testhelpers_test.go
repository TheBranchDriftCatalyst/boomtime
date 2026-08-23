// testhelpers_test.go — stats-package copy of the external ginkgo test
// helpers originally defined in internal/handler/testhelpers_test.go.
// Kept in sync with the identity + widgets + ingest + curation copies on
// purpose: each file is private to its _test package, so a shared
// testutil-side location would need to lift the gomega dependency into
// the harness. Follow-up phase 8 will move these to a shared package
// once the domain extracts settle.
package stats_test

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

// getJSONG mirrors the same-named helper that lived in
// internal/handler/testhelpers_test.go before boom-8tn phase 6 moved the
// stats-owned tests into internal/stats/. Kept as a stats-package copy so
// bigbets_handler_test.go's call sites (`getJSONG(e, url, tok)`) stay
// byte-identical. Follow-up (phase 8): promote to
// internal/testutil/handlerhelpers/.
func getJSONG(e http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}
