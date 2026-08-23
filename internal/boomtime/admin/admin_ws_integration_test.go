// admin_ws_integration_test.go — real WebSocket handshake coverage for
// AdminLabelImagesWS (boom-d6x.handler). Uses httptest.NewServer so the
// coder/websocket client can Dial + receive the initial snapshot frame —
// this covers the post-Accept branches of the handler (subscribe + snapshot
// write + reader-goroutine cleanup) that the httptest.NewRecorder path
// (which can't hijack) never reaches.
package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/coder/websocket"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/queue/imagejobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// dialAdminWS opens a WS connection using an existing refresh_token cookie
// value. Returns the live connection + a cleanup func the caller must defer.
func dialAdminWS(srvURL, path, refreshCookie string) (*websocket.Conn, *http.Response, error) {
	wsURL := strings.Replace(srvURL, "http://", "ws://", 1) + path
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{"refresh_token=" + refreshCookie}},
	}
	// Bounded dial deadline so a wedged handshake never hangs the suite.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, wsURL, opts)
}

var _ = Describe("AdminLabelImagesWS: full handshake + snapshot frame (post-Accept coverage)", func() {
	It("admin cookie + queue wired → 101; first frame is snapshot (may be empty on fresh registry)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "wsliup"))
		ctx := context.Background()
		user, _ := hz.MintUser("wsli_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		reg := imagejobs.NewRegistry(nil)
		e, bh := boomtimeRouterH(hz)
		bh.SetImageJobQueue(reg)

		refresh := fmt.Sprintf("refresh-wsli-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(user, refresh), 24)).To(Succeed())

		srv := httptest.NewServer(e)
		DeferCleanup(srv.Close)

		conn, _, err := dialAdminWS(srv.URL, "/api/v1/admin/label-images/ws", refresh)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "test end") })

		readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, raw, rerr := conn.Read(readCtx)
		Expect(rerr).NotTo(HaveOccurred(), "read snapshot: %v", rerr)
		var frame map[string]any
		Expect(json.Unmarshal(raw, &frame)).To(Succeed(), "frame not JSON: %s", string(raw))
		Expect(frame["kind"]).To(Equal("snapshot"),
			"first frame must be snapshot, got %v", frame)
		// INVARIANT: `jobs` is an array-or-nil, not a scalar/string/garbage.
		// HaveKey alone would let `{"jobs": "surprise"}` pass — this pins
		// the wire type so a future refactor can't quietly change the shape.
		Expect(frame).To(HaveKey("jobs"))
		Expect(frame["jobs"]).To(SatisfyAny(BeNil(), BeAssignableToTypeOf([]any{})),
			"jobs must be array-or-nil; got %T=%v", frame["jobs"], frame["jobs"])
	})

	It("TWO admins BOTH see each other's label-images jobs (WS is intentionally NOT owner-filtered)", func() {
		// LOCKS IN: the label-images WS is global — every connected admin
		// sees every enqueued label-image job (NOT per-owner filtered). This
		// design decision was silent in the code; this test makes it a
		// load-bearing invariant so an accidental owner-filter would fail here.
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "wsliup"))
		ctx := context.Background()
		userA, _ := hz.MintUser("wsli_2a")
		userB, _ := hz.MintUser("wsli_2b")
		hz.Cfg.AdminUsers = map[string]struct{}{userA: {}, userB: {}}
		reg := imagejobs.NewRegistry(nil)
		e, bh := boomtimeRouterH(hz)
		bh.SetImageJobQueue(reg)

		refreshB := fmt.Sprintf("refresh-wsli2b-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(userB, refreshB), 24)).To(Succeed())

		srv := httptest.NewServer(e)
		DeferCleanup(srv.Close)

		conn, _, err := dialAdminWS(srv.URL, "/api/v1/admin/label-images/ws", refreshB)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "done") })

		// Drain the snapshot.
		snapCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _, _ = conn.Read(snapCtx)

		// Enqueue AS-IF admin A (owner tracking isn't per-user on this
		// registry — LabelID is the natural key). B's WS still must see it.
		_, _ = reg.Enqueue(imagejobs.EnqueueInput{LabelID: "cross-admin-label", Prompt: "p"})

		liveCtx, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel2()
		_, raw, rerr := conn.Read(liveCtx)
		Expect(rerr).NotTo(HaveOccurred(), "read live: %v", rerr)
		var frame map[string]any
		Expect(json.Unmarshal(raw, &frame)).To(Succeed())
		Expect(frame["kind"]).To(Equal("added"))
		job, _ := frame["job"].(map[string]any)
		Expect(job["labelId"]).To(Equal("cross-admin-label"),
			"admin B did NOT see the cross-admin enqueue — label-images WS accidentally acquired owner filtering")
	})

	It("live event: after enqueue, an added frame with the labelId reaches the connected admin", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "wsliup"))
		ctx := context.Background()
		user, _ := hz.MintUser("wsli_live")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		reg := imagejobs.NewRegistry(nil)
		e, bh := boomtimeRouterH(hz)
		bh.SetImageJobQueue(reg)

		refresh := fmt.Sprintf("refresh-wslilive-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(user, refresh), 24)).To(Succeed())

		srv := httptest.NewServer(e)
		DeferCleanup(srv.Close)

		conn, _, err := dialAdminWS(srv.URL, "/api/v1/admin/label-images/ws", refresh)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "done") })

		// Drain the snapshot.
		snapCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _, _ = conn.Read(snapCtx)

		// Enqueue AFTER the snapshot to hit the live-event branch.
		_, _ = reg.Enqueue(imagejobs.EnqueueInput{LabelID: "wired-label", Prompt: "p"})

		liveCtx, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel2()
		_, raw, rerr := conn.Read(liveCtx)
		Expect(rerr).NotTo(HaveOccurred(), "read live event: %v", rerr)
		var frame map[string]any
		Expect(json.Unmarshal(raw, &frame)).To(Succeed())
		Expect(frame["kind"]).To(Equal("added"))
		job, _ := frame["job"].(map[string]any)
		Expect(job["labelId"]).To(Equal("wired-label"))
	})
})
