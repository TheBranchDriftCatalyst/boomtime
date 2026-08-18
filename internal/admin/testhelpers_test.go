// testhelpers_test.go — admin-package copy of the external ginkgo test
// helpers originally defined in internal/handler/testhelpers_test.go.
// Kept in sync with the identity + widgets + ingest + curation copies
// on purpose: each file is private to its _test package, so a shared
// testutil-side location would need to lift the gomega dependency into
// the harness. Follow-up phase 8 will move these to a shared package
// once the domain extracts settle.
package admin_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	. "github.com/onsi/gomega"
)

// testutilTokenData builds a db.TokenData with a throwaway (unused) access
// token and the caller-supplied refresh token. Used by WS tests to stand up a
// valid refresh_token cookie without going through the public Login flow
// (Login requires a plaintext password the prefix-hashed MintUser users don't
// track). Relocated here from the removed admin_backfill_http_test.go.
func testutilTokenData(user, refresh string) db.TokenData {
	return db.TokenData{Owner: user, Token: strings.Repeat("a", 32), RefreshToken: refresh}
}

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
// harness router. Copied verbatim from internal/handler/testhelpers_test.go
// so admin specs keep their existing call sites after the package move.
func doRawG(e http.Handler, method, target, token string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}
