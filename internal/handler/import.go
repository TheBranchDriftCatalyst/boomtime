package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/labstack/echo/v5"
)

// effectiveImportToken returns the apiToken to use for an import: the one in the
// request body, or (when blank) the server-configured Wakatime key. This lets the
// import run without ever putting the secret in the browser.
func (h *Handler) effectiveImportToken(bodyToken string) string {
	if bodyToken != "" {
		return bodyToken
	}
	return h.Cfg.WakatimeAPIKey
}

// ImportRequest: POST /import — create + start a durable import job.
// If a job is already queued/running for this user, returns that job instead of
// starting a second one (one active job per owner).
func (h *Handler) ImportRequest(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	var payload model.ImportRequestPayload
	if err := c.Bind(&payload); err != nil {
		return respondErr(c, apierr.Generic())
	}
	payload.APIToken = h.effectiveImportToken(payload.APIToken)
	ctx := c.Request().Context()

	// One active job per owner: return the existing running/queued job if any.
	if existing, err := h.DB.GetRunningJobByOwner(ctx, owner); err != nil {
		return respondErr(c, apierr.Generic())
	} else if existing != nil {
		return c.JSON(http.StatusOK, map[string]any{
			"jobId":     existing.ID,
			"jobStatus": importer.JobSubmitted,
			"job":       existing,
		})
	}

	item := importer.QueueItem{Requester: owner, ReqPayload: payload}
	raw, err := json.Marshal(item)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}

	total := importer.TotalDays(payload.StartDate, payload.EndDate)
	job, err := h.DB.CreateImportJob(ctx, owner, raw, payload.StartDate, payload.EndDate, total)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}

	h.Worker.StartJob(job, item)

	return c.JSON(http.StatusOK, map[string]any{
		"jobId":     job.ID,
		"jobStatus": importer.JobSubmitted,
		"job":       job,
	})
}

// ImportJobs: GET /import/jobs — list this user's jobs, newest first.
func (h *Handler) ImportJobs(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	jobs, err := h.DB.GetJobsByOwner(c.Request().Context(), owner)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, map[string]any{"jobs": jobs})
}

// ImportJob: GET /import/jobs/:id — one job plus its logs (owner-scoped).
func (h *Handler) ImportJob(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, ok := parseJobID(c)
	if !ok {
		return respondErr(c, apierr.Generic())
	}
	ctx := c.Request().Context()

	job, err := h.DB.GetJobByID(ctx, id)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if job == nil || job.Owner != owner {
		return respondErr(c, apierr.New(http.StatusNotFound, "Import job not found", nil))
	}
	logs, err := h.DB.GetJobLogs(ctx, id, 0, 1000)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, map[string]any{"job": job, "logs": logs})
}

// ImportJobLogs: GET /import/jobs/:id/logs?afterId=<n> — REST fallback tail.
func (h *Handler) ImportJobLogs(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, ok := parseJobID(c)
	if !ok {
		return respondErr(c, apierr.Generic())
	}
	ctx := c.Request().Context()

	job, err := h.DB.GetJobByID(ctx, id)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if job == nil || job.Owner != owner {
		return respondErr(c, apierr.New(http.StatusNotFound, "Import job not found", nil))
	}
	afterID := queryInt64(c, "afterId", 0)
	logs, err := h.DB.GetJobLogs(ctx, id, afterID, 1000)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, map[string]any{"logs": logs})
}

// ImportJobCancel: POST /import/jobs/:id/cancel — cancel a running job.
func (h *Handler) ImportJobCancel(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, ok := parseJobID(c)
	if !ok {
		return respondErr(c, apierr.Generic())
	}
	ctx := c.Request().Context()

	job, err := h.DB.GetJobByID(ctx, id)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if job == nil || job.Owner != owner {
		return respondErr(c, apierr.New(http.StatusNotFound, "Import job not found", nil))
	}

	// Signal the in-process worker (if running here); the worker records the
	// cancelled terminal state. If it isn't running in this process (e.g. queued
	// but not yet started, or already terminal), cancel durably in the DB.
	if !h.Worker.Cancel(id) {
		updated, err := h.DB.CancelJob(ctx, id)
		if err != nil {
			return respondErr(c, apierr.Generic())
		}
		if updated != nil {
			job = updated
		}
	} else {
		// Give the worker a brief moment to persist the cancelled state, then
		// re-read so the response reflects it.
		time.Sleep(150 * time.Millisecond)
		if fresh, err := h.DB.GetJobByID(ctx, id); err == nil && fresh != nil {
			job = fresh
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"job": job})
}

// ImportConfig: GET /import/config — reports whether a server key is configured.
func (h *Handler) ImportConfig(c *echo.Context) error {
	return c.JSON(http.StatusOK, map[string]bool{"hasServerKey": h.Cfg.HasServerWakatimeKey()})
}

// WakatimeRange: POST /import/wakatime-range — discover how far back the user's
// wakatime.com data goes so the UI can auto-populate the import date range.
func (h *Handler) WakatimeRange(c *echo.Context) error {
	if _, _, aerr := h.resolveUser(c); aerr != nil {
		return respondErr(c, aerr)
	}
	var body struct {
		APIToken string `json:"apiToken"`
	}
	// Body is optional; ignore bind errors and fall back to the server key.
	_ = c.Bind(&body)

	token := h.effectiveImportToken(body.APIToken)
	if token == "" {
		// No effective key: friendly "no data" instead of an error (never leaks the key).
		return c.JSON(http.StatusOK, map[string]any{"hasData": false})
	}

	// Keep it snappy: a single request with a short timeout.
	ctx, cancel := context.WithTimeout(c.Request().Context(), 15*time.Second)
	defer cancel()

	rng, err := importer.FetchAllTimeRange(ctx, token)
	if err != nil {
		h.Logger.Warn("wakatime range lookup failed", "err", err)
		return c.JSON(http.StatusOK, map[string]any{"hasData": false})
	}
	return c.JSON(http.StatusOK, rng)
}

// ImportJobWS: GET /import/jobs/:id/ws — live, resumable job stream.
// Auth uses the HttpOnly refresh_token cookie (WS handshakes can't set headers).
func (h *Handler) ImportJobWS(c *echo.Context) error {
	owner, ok, err := h.resolveUserFromCookie(c)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if !ok {
		return respondErr(c, apierr.ExpiredRefreshToken())
	}
	id, valid := parseJobID(c)
	if !valid {
		return respondErr(c, apierr.Generic())
	}

	reqCtx := c.Request().Context()
	job, err := h.DB.GetJobByID(reqCtx, id)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if job == nil || job.Owner != owner {
		return respondErr(c, apierr.New(http.StatusNotFound, "Import job not found", nil))
	}

	conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		InsecureSkipVerify: true, // same-origin; CORS handled elsewhere
	})
	if err != nil {
		return nil // handshake failed; nothing more to do
	}
	defer conn.CloseNow()

	// A background context so streaming outlives the HTTP handler return.
	ctx := context.Background()

	// Subscribe BEFORE reading the snapshot so no live event is missed between
	// snapshot and live tail. Events for logs already in the snapshot are
	// de-duplicated by the client via LogLine.id (monotonic).
	sub := h.Hub.Subscribe(id)
	defer h.Hub.Unsubscribe(id, sub)

	// Snapshot from the DB (this is what makes reload/resume work).
	logs, err := h.DB.GetJobLogs(ctx, id, 0, 1000)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "snapshot failed")
		return nil
	}
	if err := wsjson.Write(ctx, conn, map[string]any{
		"type": "snapshot", "job": job, "logs": logs,
	}); err != nil {
		return nil
	}

	// If already terminal, the snapshot carries everything; close cleanly.
	if isTerminal(job.State) {
		conn.Close(websocket.StatusNormalClosure, "job terminal")
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
		case ev, alive := <-sub:
			if !alive {
				return nil
			}
			if err := wsjson.Write(streamCtx, conn, ev); err != nil {
				return nil
			}
			if ev.Type == "state" && ev.Job != nil && isTerminal(ev.Job.State) {
				conn.Close(websocket.StatusNormalClosure, "job terminal")
				return nil
			}
		}
	}
}

func isTerminal(state string) bool {
	switch state {
	case db.JobStateCompleted, db.JobStateFailed, db.JobStateCancelled:
		return true
	}
	return false
}

func parseJobID(c *echo.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return 0, false
	}
	return id, true
}
