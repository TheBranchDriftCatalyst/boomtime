// helpers_ginkgo_test.go — shared HTTP + JSON helpers for the ginkgo mirror
// tests in this package. Kept separate from the stdlib helpers (`do`,
// `decode`, `extractRefreshCookie`, `recordFor`, `itoa`) in
// integration_test.go / widgets_test.go / openapi_test.go so both suites
// coexist without symbol collisions. Every helper here fails via
// gomega Expect(...) instead of t.Fatalf so it can be called from
// inside an It.
package testutil_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// doG mirrors `do` but reports failures via gomega. Safe to call from any It.
func doG(e http.Handler, method, target, token string, body any) *httptest.ResponseRecorder {
	GinkgoHelper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred(), "marshal body")
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// decodeG mirrors `decode`.
func decodeG(rec *httptest.ResponseRecorder, v any) {
	GinkgoHelper()
	err := json.Unmarshal(rec.Body.Bytes(), v)
	Expect(err).NotTo(HaveOccurred(),
		"decode (status %d): body=%s", rec.Code, rec.Body.String())
}

// extractRefreshCookieG mirrors extractRefreshCookie.
func extractRefreshCookieG(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" {
			return c.Value
		}
	}
	return ""
}

// itoaG is a straight int→string; strconv.Itoa is fine here (the stdlib
// file rolled its own; parity with strconv makes the ginkgo file smaller).
func itoaG(n int) string { return strconv.Itoa(n) }
