// books_monitor_integration_test.go — real WebSocket handshake coverage of the
// reading monitor's AUTH gate (AdminBooksReadingMonitorWS). Uses httptest with
// the handler mounted directly (the route is BooksEnabled-gated in production
// routes.go; here we mount it unconditionally to exercise the in-handler
// cookie-auth + requireAdmin gate itself). Mirrors admin_ws_integration_test.go.
package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/coder/websocket"
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

const readingMonitorPath = "/api/v1/admin/books/reading-monitor/ws"

// mountReadingMonitor returns an httptest server exposing ONLY the reading
// monitor WS against the harness handler.
func mountReadingMonitor(hz *testutil.Harness) *httptest.Server {
	e := echo.New()
	e.GET(readingMonitorPath, hz.H.Admin.AdminBooksReadingMonitorWS)
	return httptest.NewServer(e)
}

var _ = Describe("AdminBooksReadingMonitorWS: requireAdmin gate", func() {
	It("a NON-admin authed user is refused (403, no upgrade)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bookmon"))
		ctx := context.Background()
		user, _ := hz.MintUser("bookmon_plain")
		// Deliberately NOT added to hz.Cfg.AdminUsers → not an admin.
		hz.Cfg.AdminUsers = map[string]struct{}{"someone_else": {}}

		refresh := fmt.Sprintf("refresh-bookmon-plain-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(user, refresh), 24)).To(Succeed())

		srv := mountReadingMonitor(hz)
		DeferCleanup(srv.Close)

		conn, resp, err := dialAdminWS(srv.URL, readingMonitorPath, refresh)
		if conn != nil {
			DeferCleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "test end") })
		}
		Expect(err).To(HaveOccurred(), "non-admin must NOT complete the WS handshake")
		Expect(resp).NotTo(BeNil())
		Expect(resp.StatusCode).To(Equal(403), "non-admin should be Forbidden before the upgrade")
	})

	It("an ADMIN authed user PASSES the gate → upgrades (101); with no Amazon credential the first frame is an error", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bookmon"))
		ctx := context.Background()
		user, _ := hz.MintUser("bookmon_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		refresh := fmt.Sprintf("refresh-bookmon-admin-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(user, refresh), 24)).To(Succeed())

		srv := mountReadingMonitor(hz)
		DeferCleanup(srv.Close)

		conn, _, err := dialAdminWS(srv.URL, readingMonitorPath, refresh)
		Expect(err).NotTo(HaveOccurred(), "admin should complete the handshake")
		DeferCleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "test end") })

		// The admin has no connected Amazon device in this fresh DB, so the
		// handler streams a single `error` frame explaining that and closes —
		// which proves it got PAST requireAdmin into the monitor body.
		readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, raw, rerr := conn.Read(readCtx)
		Expect(rerr).NotTo(HaveOccurred(), "read first frame: %v", rerr)
		var frame map[string]any
		Expect(json.Unmarshal(raw, &frame)).To(Succeed(), "frame not JSON: %s", string(raw))
		Expect(frame["type"]).To(Equal("error"),
			"admin with no Amazon credential should get an error frame first, got %v", frame)
		Expect(frame["error"]).To(ContainSubstring("Amazon credential"))
	})
})
