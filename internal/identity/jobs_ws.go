// jobs_ws.go — GET /api/v1/jobs/ws (gaka-hney.6): the per-user push stream for
// catalyst-go-jobs terminal events. The FE opens it once (in AppShell) and
// toasts when one of the caller's jobs completes or fails. Cookie-authed (a WS
// handshake can't set an Authorization header) and user-scoped via the hub, so
// a user only ever sees their own job events.
package identity

import (
	"context"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
)

// JobEventsWS streams the caller's terminal job events (done/failed).
func (h *Handler) JobEventsWS(c *echo.Context) error {
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
	metrics.WSActiveConnections.WithLabelValues("jobs").Inc()
	defer metrics.WSActiveConnections.WithLabelValues("jobs").Dec()

	// Feature not wired → close cleanly so the client backs off.
	if h.JobEvents == nil {
		conn.Close(websocket.StatusNormalClosure, "job events unavailable")
		return nil
	}

	ctx := context.Background()
	events, unsub := h.JobEvents.Subscribe(owner)
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
