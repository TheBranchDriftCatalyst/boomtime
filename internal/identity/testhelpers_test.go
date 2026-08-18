// testhelpers_test.go — identity-package copy of the external ginkgo test
// helpers originally defined in internal/handler/testhelpers_test.go. Kept
// in sync with the handler + widgets copies on purpose: each file is
// private to its _test package, so a shared testutil-side location would
// need to lift the gomega dependency into the harness. Follow-up phase 8
// will move these to a shared package once the domain extracts settle.
package identity_test

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
// harness router. Copied verbatim from internal/handler/testhelpers_test.go
// so identity tests keep their existing call sites.
func doRawG(e http.Handler, method, target, token string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// mintUserWithPasswordG mirrors password_test.go's helper of the same name.
// Copied verbatim from internal/handler/testhelpers_test.go so identity
// specs (auth_cluster_coverage_test.go, password_test.go) keep working
// after the package move.
func mintUserWithPasswordG(hz *testutil.Harness, prefix, password string) (user, plain, token string) {
	ctx := context.Background()
	user, token = hz.MintUser(prefix)
	hash, salt, err := auth.HashPassword(password)
	Expect(err).NotTo(HaveOccurred())
	Expect(hz.DB.UpdatePassword(ctx, user, hash, salt)).To(Succeed())
	return user, password, token
}

// verifyLoginG mirrors password_test.go's helper — POSTs to /auth/login and
// returns the status code. Copied verbatim from
// internal/handler/testhelpers_test.go.
func verifyLoginG(e http.Handler, user, password string) int {
	rec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
		"username": user, "password": password,
	})
	return rec.Code
}

// decodeJSONBody unmarshals a JSON payload into dst. Kept as a shared
// helper so per-test decoders stay one line. Mirror of the widgets +
// handler versions.
func decodeJSONBody(b []byte, dst any) error { return json.Unmarshal(b, dst) }
