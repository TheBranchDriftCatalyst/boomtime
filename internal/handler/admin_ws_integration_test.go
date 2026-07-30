// admin_ws_integration_test.go — real WebSocket handshake coverage for
// AdminBackfillWS + AdminLabelImagesWS (gaka-d6x.handler). Uses
// httptest.NewServer so the coder/websocket client can Dial + receive
// the initial snapshot frame — this covers the post-Accept branches of
// both handlers (subscribe + snapshot write + reader-goroutine cleanup)
// that the httptest.NewRecorder path (which can't hijack) never reaches.
package handler_test

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

	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/backfilljobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/imagejobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
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

var _ = Describe("AdminBackfillWS: full handshake + snapshot frame (post-Accept coverage)", func() {
	It("admin cookie + queue wired → 101 Switching Protocols; first frame is snapshot filtered to owner", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "wsbfup"))
		ctx := context.Background()

		// Two admins on the same allowlist + shared queue. A enqueues a job;
		// the WS client is B — the snapshot must NOT leak A's job.
		userA, _ := hz.MintUser("wsbf_a")
		userB, _ := hz.MintUser("wsbf_b")
		hz.Cfg.AdminUsers = map[string]struct{}{userA: {}, userB: {}}
		reg := backfilljobs.NewRegistry(nil)
		hz.H.SetBackfillJobQueue(reg)

		// A enqueues out-of-band (bypasses HTTP).
		reg.Enqueue(backfilljobs.EnqueueInput{Owner: userA, RepoName: "A-repo", Total: 1})

		// Mint a refresh cookie for B.
		refreshB := fmt.Sprintf("refresh-wsbf-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(userB, refreshB), 24)).To(Succeed())

		srv := httptest.NewServer(hz.Router())
		DeferCleanup(srv.Close)

		conn, resp, err := dialAdminWS(srv.URL, "/api/v1/admin/backfill/ws", refreshB)
		Expect(err).NotTo(HaveOccurred(), "dial: %v (resp=%v)", err, resp)
		DeferCleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "test end") })

		// Read the initial snapshot frame with a bounded deadline.
		readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, raw, rerr := conn.Read(readCtx)
		Expect(rerr).NotTo(HaveOccurred(), "read snapshot: %v", rerr)
		var frame map[string]any
		Expect(json.Unmarshal(raw, &frame)).To(Succeed(), "frame not JSON: %s", string(raw))
		Expect(frame["kind"]).To(Equal("snapshot"))
		jobs, _ := frame["jobs"].([]any)
		// INVARIANT: snapshot for B contains 0 jobs (A's job filtered out).
		Expect(jobs).To(BeEmpty(),
			"B's WS snapshot leaked A's job(s): %v", jobs)
	})

	It("live event: after connect, an EventAdded for THIS admin is streamed; other admin's job is filtered", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "wsbfup"))
		ctx := context.Background()
		userA, _ := hz.MintUser("wsbf_live_a")
		userB, _ := hz.MintUser("wsbf_live_b")
		hz.Cfg.AdminUsers = map[string]struct{}{userA: {}, userB: {}}
		reg := backfilljobs.NewRegistry(nil)
		hz.H.SetBackfillJobQueue(reg)

		refreshA := fmt.Sprintf("refresh-wsbfliveA-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(userA, refreshA), 24)).To(Succeed())

		srv := httptest.NewServer(hz.Router())
		DeferCleanup(srv.Close)

		conn, _, err := dialAdminWS(srv.URL, "/api/v1/admin/backfill/ws", refreshA)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "done") })

		// Drain the initial snapshot.
		readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _, _ = conn.Read(readCtx)

		// Fire two events on the queue: one for B (filtered out for A) and one for A.
		reg.Enqueue(backfilljobs.EnqueueInput{Owner: userB, RepoName: "B-repo"})
		reg.Enqueue(backfilljobs.EnqueueInput{Owner: userA, RepoName: "A-repo"})

		// Read the next frame; must be the A-repo event (B filtered).
		readCtx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel2()
		_, raw, rerr := conn.Read(readCtx2)
		Expect(rerr).NotTo(HaveOccurred(), "read live: %v", rerr)
		var frame map[string]any
		Expect(json.Unmarshal(raw, &frame)).To(Succeed())
		Expect(frame["kind"]).To(Equal("added"))
		job, _ := frame["job"].(map[string]any)
		Expect(job["repoName"]).To(Equal("A-repo"),
			"A got B's event across the owner filter: %v", frame)
		Expect(job["owner"]).To(Equal(userA))
	})
})

var _ = Describe("AdminLabelImagesWS: full handshake + snapshot frame (post-Accept coverage)", func() {
	It("admin cookie + queue wired → 101; first frame is snapshot (may be empty on fresh registry)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "wsliup"))
		ctx := context.Background()
		user, _ := hz.MintUser("wsli_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		reg := imagejobs.NewRegistry(nil)
		hz.H.SetImageJobQueue(reg)

		refresh := fmt.Sprintf("refresh-wsli-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(user, refresh), 24)).To(Succeed())

		srv := httptest.NewServer(hz.Router())
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
		Expect(frame).To(HaveKey("jobs"))
	})

	It("live event: after enqueue, an added frame with the labelId reaches the connected admin", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "wsliup"))
		ctx := context.Background()
		user, _ := hz.MintUser("wsli_live")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		reg := imagejobs.NewRegistry(nil)
		hz.H.SetImageJobQueue(reg)

		refresh := fmt.Sprintf("refresh-wslilive-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(user, refresh), 24)).To(Succeed())

		srv := httptest.NewServer(hz.Router())
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
