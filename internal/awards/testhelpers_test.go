// testhelpers_test.go — ginkgo test helpers local to the awards
// package's external mirror suite (package awards_test). Mirror of the
// same-named helpers in internal/handler/testhelpers_test.go — the
// awards domain (gaka-8tn phase 4b) keeps a private copy so it's
// self-contained until phase 8 promotes these to a shared
// internal/testutil/handlerhelpers package.
package awards_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
)

// doRawG mirrors backup_test.go's doRaw — a raw-body request against the
// harness router.
func doRawG(e http.Handler, method, target, token string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}
