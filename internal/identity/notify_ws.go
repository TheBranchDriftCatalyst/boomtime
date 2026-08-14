// notify_ws.go — GET /api/v1/notify/ws: the per-user push stream for the
// domain-agnostic internal/notify hub. The FE opens it once (in AppShell) and
// toasts on each Event. Cookie-authed (a WS handshake can't set an
// Authorization header) and user-scoped via the hub, so a user only ever sees
// their own notifications. Mirrors JobEventsWS / jobs_ws.go.
package identity

import (
	"context"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
)

// NotifyWS streams the caller's notification events.
func (h *Handler) NotifyWS(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwnerFromCookie(h.DB, h.Logger, c, apierr.ExpiredRefreshToken())
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		InsecureSkipVerify: true, // same-origin
	})
	if err != nil {
		return nil
	}
	defer conn.CloseNow()
	metrics.WSActiveConnections.WithLabelValues("notify").Inc()
	defer metrics.WSActiveConnections.WithLabelValues("notify").Dec()

	// Feature not wired → close cleanly so the client backs off.
	if h.Notify == nil {
		conn.Close(websocket.StatusNormalClosure, "notifications unavailable")
		return nil
	}

	ctx := context.Background()
	events, unsub := h.Notify.Subscribe(owner)
	defer unsub()

	// A reader goroutine detects client disconnect and cancels the stream.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		for {
			if _, _, rerr := conn.Read(streamCtx); rerr != nil {
				cancel()
				return
			}
		}
	}()

	for {
		select {
		case <-streamCtx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if werr := wsjson.Write(streamCtx, conn, ev); werr != nil {
				return nil
			}
		}
	}
}
