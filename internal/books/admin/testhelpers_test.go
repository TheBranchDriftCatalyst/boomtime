// testhelpers_test.go — books-admin copy of the external ginkgo test helpers
// (dialAdminWS / testutilTokenData / doJSONReqG). These moved here with the
// books-admin handlers (boom-zp2s); the internal/admin copies stay put for the
// admin-package tests that remain there. Each file is private to its _test
// package, so a shared testutil-side location would need to lift the gomega
// dependency into the harness — deferred to the same follow-up as the other
// per-domain copies.
package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// testutilTokenData builds a db.TokenData with a throwaway (unused) access token
// and the caller-supplied refresh token — a valid refresh_token cookie without
// the public Login flow.
func testutilTokenData(user, refresh string) db.TokenData {
	return db.TokenData{Owner: user, Token: strings.Repeat("a", 32), RefreshToken: refresh}
}

// dialAdminWS opens a WS connection using an existing refresh_token cookie value.
func dialAdminWS(srvURL, path, refreshCookie string) (*websocket.Conn, *http.Response, error) {
	wsURL := strings.Replace(srvURL, "http://", "ws://", 1) + path
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{"refresh_token=" + refreshCookie}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, wsURL, opts)
}

// doJSONReqG issues a JSON request against a router. Ginkgo variant: fails via
// gomega rather than *testing.T so failures inside the helper surface as spec
// failures.
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
