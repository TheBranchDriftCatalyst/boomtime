// ws_origin_test.go — regression for the websocket Origin finding in the
// 2026-09-06 audit (internal/identity/jobs_ws.go, notify_ws.go).
//
// Both push streams called websocket.Accept with
// `InsecureSkipVerify: true, // same-origin` — a comment that asserts the exact
// OPPOSITE of what the flag does. InsecureSkipVerify DISABLES coder/websocket's
// Origin verification, so any origin could complete the handshake. The
// refresh_token cookie is SameSite=Strict, which blunts a classic CSWSH, but
// SameSite treats SUBDOMAINS of the same registrable domain as same-site: any
// page served from a sibling app under the shared parent domain (the homelab
// layout — many services under one domain) could open
// wss://boomtime…/api/v1/notify/ws or /jobs/ws, have the browser attach the
// cookie, and stream the victim's job/notification events.
package identity_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("Push-stream WebSocket origin check", func() {
	// dialWS attempts a handshake against path with an explicit Origin and the
	// caller's session cookie, returning the handshake error (nil = upgraded).
	dialWS := func(serverURL, path, origin, refreshCookie string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hdr := http.Header{}
		hdr.Set("Origin", origin)
		hdr.Set("Cookie", "refresh_token="+refreshCookie)
		conn, _, err := websocket.Dial(ctx,
			"ws"+strings.TrimPrefix(serverURL, "http")+path,
			&websocket.DialOptions{HTTPHeader: hdr})
		if conn != nil {
			conn.Close(websocket.StatusNormalClosure, "")
		}
		return err
	}

	// newAuthedWSServer boots an httptest server carrying both push routes and
	// returns it plus a valid session cookie value.
	newAuthedWSServer := func(hz *testutil.Harness, prefix string) (*httptest.Server, string) {
		e := hz.Router()
		apiroute.WebSocket(e, "/api/v1/jobs/ws", hz.H.Identity.JobEventsWS)
		apiroute.WebSocket(e, "/api/v1/notify/ws", hz.H.Identity.NotifyWS)

		user, _ := hz.MintUser(prefix)
		td := db.TokenData{
			Owner:        user,
			Token:        auth.ToBase64(auth.NewRawToken()),
			RefreshToken: auth.ToBase64(auth.NewRawToken()),
		}
		Expect(hz.DB.CreateAccessTokens(context.Background(), td, 24)).To(Succeed())

		srv := httptest.NewServer(e)
		DeferCleanup(srv.Close)
		return srv, td.RefreshToken
	}

	It("refuses a FOREIGN origin in prod, and still accepts the app's own origin", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.Env = "prod"
		srv, refreshCookie := newAuthedWSServer(hz, "ws_origin_prod_g")

		for _, path := range []string{"/api/v1/jobs/ws", "/api/v1/notify/ws"} {
			Expect(dialWS(srv.URL, path, "https://evil.knowledgedump.space", refreshCookie)).
				To(HaveOccurred(),
					"%s upgraded a handshake from a foreign origin — websocket.Accept runs with "+
						"InsecureSkipVerify, so the Origin header is never checked and any sibling "+
						"subdomain page can stream this user's events with their cookie attached", path)

			// Same-origin must keep working, or the fix is a denial of service.
			Expect(dialWS(srv.URL, path, srv.URL, refreshCookie)).NotTo(HaveOccurred(),
				"%s rejected its OWN origin", path)
		}
	})

	It("stays permissive outside prod, where dev proxies rewrite Host", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.Env = "dev"
		srv, refreshCookie := newAuthedWSServer(hz, "ws_origin_dev_g")

		// vite's dev proxy sets changeOrigin:true, which rewrites Host to the
		// backend while leaving Origin on the vite port — a permanent mismatch
		// that a strict check would turn into a broken dev/e2e stack.
		Expect(dialWS(srv.URL, "/api/v1/jobs/ws", "http://localhost:5173", refreshCookie)).
			NotTo(HaveOccurred(), "dev handshake through a Host-rewriting proxy was rejected")
	})
})
