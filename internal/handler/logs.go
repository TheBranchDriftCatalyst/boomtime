package handler

import (
	"context"
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logging"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/labstack/echo/v5"
)

// ServerLogs: GET /api/v1/logs?afterId=<n> — REST tail fallback for the server's
// own log records (from the in-memory LogHub ring buffer). Owner-gated via the
// standard Authorization header.
func (h *Handler) ServerLogs(c *echo.Context) error {
	if _, _, aerr := h.resolveUser(c); aerr != nil {
		return respondErr(c, aerr)
	}
	afterID := queryInt64(c, "afterId", 0)
	var logs []logging.LogEntry
	if h.LogHub != nil {
		logs = h.LogHub.Backfill(afterID)
	}
	if logs == nil {
		logs = []logging.LogEntry{}
	}
	return c.JSON(http.StatusOK, map[string]any{"logs": logs})
}

// ServerLogsWS: GET /api/v1/logs/ws?afterId=<n> — live, resumable stream of
// the server's own log records.
//
// Auth uses the HttpOnly refresh_token cookie (WS handshakes can't set an
// Authorization header, and a query-param access token would leak into
// server/proxy logs). On connect the server backfills the ring buffer after
// afterId (making reload/resume seamless), then tails live entries.
//
// NOTE: any authenticated user sees ALL server logs (no per-user filtering).
func (h *Handler) ServerLogsWS(c *echo.Context) error {
	if _, aerr := h.resolveOwnerFromCookie(c, apierr.ExpiredRefreshToken()); aerr != nil {
		return respondErr(c, aerr)
	}
	if h.LogHub == nil {
		return respondErr(c, apierr.Generic())
	}

	afterID := queryInt64(c, "afterId", 0)

	conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		InsecureSkipVerify: true, // same-origin; CORS handled elsewhere
	})
	if err != nil {
		return nil // handshake failed; nothing more to do
	}
	defer conn.CloseNow()

	// A background context so streaming outlives the HTTP handler return.
	ctx := context.Background()

	// Subscribe BEFORE reading the backfill so no live entry is missed between
	// backfill and live tail. Entries already in the backfill are de-duplicated
	// by the client via LogEntry.id (monotonic).
	sub := h.LogHub.Subscribe()
	defer h.LogHub.Unsubscribe(sub)

	backfill := h.LogHub.Backfill(afterID)
	if backfill == nil {
		backfill = []logging.LogEntry{}
	}
	if err := wsjson.Write(ctx, conn, map[string]any{
		"type": "snapshot", "logs": backfill,
	}); err != nil {
		return nil
	}

	// Detect client disconnect: a reader goroutine cancels streaming.
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
		case entry, alive := <-sub:
			if !alive {
				return nil
			}
			if err := wsjson.Write(streamCtx, conn, map[string]any{
				"type": "log", "log": entry,
			}); err != nil {
				return nil
			}
		}
	}
}
