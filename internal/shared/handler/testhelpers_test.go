// testhelpers_ginkgo_test.go — shared ginkgo test helpers for the
// external (package handler_test) mirror suite. Mirrors of doJSONReq /
// doRaw / mintUserWithPassword defined across the stdlib test files
// (password_test.go, backup_test.go). Naming is *_G to make it obvious
// these are the ginkgo-facing variants and to avoid duplicate-symbol
// collisions in the same test binary.
package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
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

// mintUserWithPasswordG mirrors password_test.go's helper of the same name.
func mintUserWithPasswordG(hz *testutil.Harness, prefix, password string) (user, plain, token string) {
	ctx := context.Background()
	user, token = hz.MintUser(prefix)
	hash, salt, err := auth.HashPassword(password)
	Expect(err).NotTo(HaveOccurred())
	Expect(hz.DB.UpdatePassword(ctx, user, hash, salt)).To(Succeed())
	return user, password, token
}

// verifyLoginG mirrors password_test.go's helper — POSTs to /auth/login and
// returns the status code.
func verifyLoginG(e http.Handler, user, password string) int {
	rec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
		"username": user, "password": password,
	})
	return rec.Code
}

// getJSONG mirrors the same-named helper that lived in
// internal/handler/awards_eval_test.go before boom-8tn phase 4b moved the
// awards cluster into internal/awards/. Kept here as a local copy so
// stats tests (bigbets_test.go — its sole caller outside of the moved
// awards suite) keep the byte-identical invocation shape. Follow-up
// (phase 8): promote to internal/testutil/handlerhelpers/.
func getJSONG(e http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}
