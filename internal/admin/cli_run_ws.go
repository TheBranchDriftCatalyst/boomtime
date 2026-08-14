// cli_run_ws.go — GET /api/v1/admin/cli/run/ws (gaka-hney.5): the STREAMING
// twin of CLIRun. The client opens the socket, sends ONE cliRunRequest frame,
// and receives live "output" frames as the command runs, then a terminal
// "done" — so the operator watches a run (e.g. backfill github-stats across N
// users) instead of waiting on a synchronous buffer.
//
// It shares the exact allowlist + dry-run/confirm gating + audit as the sync
// path (h.cliResolve / h.invokeCLI); the ONLY difference is the sink: a
// wsLineWriter streaming to the socket instead of a cappedWriter buffering to
// the response. Still zero-subprocess. Auth is cookie-based (a WS handshake
// can't set an Authorization header) + admin-gated in-handler, mirroring
// ImportJobWS / AdminLabelImagesWS.
package admin

import (
	"context"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
)

// cliStreamMsg is one frame of the CLI run stream:
//
//	start  — run accepted (Command/DryRun set)
//	output — a chunk of the command's output (Data set)
//	done   — finished (ExitError ""=ok, DurationMs, Truncated set)
//	error  — refused before it started (Error set)
type cliStreamMsg struct {
	Type       string `json:"type"`
	Command    string `json:"command,omitempty"`
	DryRun     bool   `json:"dryRun,omitempty"`
	Data       string `json:"data,omitempty"`
	ExitError  string `json:"exitError,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	Error      string `json:"error,omitempty"`
}

// wsLineWriter is the streaming sink for a run: each Write is forwarded as an
// "output" frame (the FE terminal appends it), capped at max total bytes so a
// runaway command can't flood the client. Write never short-writes (mirrors
// cappedWriter) so command bodies never see a write error; a send failure
// (client gone) is swallowed — the disconnect reader cancels the run context.
type wsLineWriter struct {
	ctx       context.Context
	conn      *websocket.Conn
	max       int
	written   int
	truncated bool
}

func (w *wsLineWriter) Write(p []byte) (int, error) {
	room := w.max - w.written
	if room <= 0 {
		if len(p) > 0 {
			w.truncated = true
		}
		return len(p), nil
	}
	chunk := p
	if len(chunk) > room {
		chunk = chunk[:room]
		w.truncated = true
	}
	w.written += len(chunk)
	_ = wsjson.Write(w.ctx, w.conn, cliStreamMsg{Type: "output", Data: string(chunk)})
	return len(p), nil
}

// CLIRunWS streams a single allowlisted command run over a WebSocket.
func (h *Handler) CLIRunWS(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwnerFromCookie(h.DB, h.Logger, c, apierr.ExpiredRefreshToken())
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if !h.Cfg.IsAdmin(owner) {
		return apihelpers.RespondErr(c, apierr.Forbidden("admin only"))
	}

	conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		InsecureSkipVerify: true, // same-origin
	})
	if err != nil {
		return nil // handshake failed; nothing more to do
	}
	defer conn.CloseNow()
	metrics.WSActiveConnections.WithLabelValues("cli").Inc()
	defer metrics.WSActiveConnections.WithLabelValues("cli").Dec()

	ctx := context.Background()

	// First frame is the run request. Bound the read so a socket that opens
	// but never speaks can't hold the handler open.
	var req cliRunRequest
	readCtx, readCancel := context.WithTimeout(ctx, 10*time.Second)
	rerr := wsjson.Read(readCtx, conn, &req)
	readCancel()
	if rerr != nil {
		conn.Close(websocket.StatusPolicyViolation, "expected a run request")
		return nil
	}

	spec, entry, args, dryRun, aerr := h.cliResolve(owner, req)
	if aerr != nil {
		_ = wsjson.Write(ctx, conn, cliStreamMsg{Type: "error", Error: aerr.Message})
		conn.Close(websocket.StatusPolicyViolation, "run refused")
		return nil
	}

	runCtx, cancel := context.WithTimeout(ctx, cliRunTimeout)
	defer cancel()

	// A reader goroutine cancels the run if the client disconnects mid-stream.
	go func() {
		for {
			if _, _, e := conn.Read(runCtx); e != nil {
				cancel()
				return
			}
		}
	}()

	_ = wsjson.Write(runCtx, conn, cliStreamMsg{Type: "start", Command: req.Command, DryRun: dryRun})

	w := &wsLineWriter{ctx: runCtx, conn: conn, max: cliMaxOutputBytes}
	start := time.Now()
	runErr := h.invokeCLI(runCtx, entry, args, w)
	duration := time.Since(start)

	outcome := "ok"
	exitError := ""
	if runErr != nil {
		outcome = "error"
		exitError = runErr.Error()
	}
	h.Logger.Info("admin cli run",
		"actor", owner,
		"command", req.Command,
		"flags", maskFlags(spec, req.Flags),
		"classification", entry.Classification,
		"dryRun", dryRun,
		"outcome", outcome,
		"durationMs", duration.Milliseconds(),
		"transport", "ws",
	)

	_ = wsjson.Write(runCtx, conn, cliStreamMsg{
		Type:       "done",
		ExitError:  exitError,
		DurationMs: duration.Milliseconds(),
		Truncated:  w.truncated,
	})
	conn.Close(websocket.StatusNormalClosure, "done")
	return nil
}
