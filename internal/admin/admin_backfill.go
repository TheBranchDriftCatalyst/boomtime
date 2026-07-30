// admin_backfill.go: HTTP handlers for the git-history backfill flow
// (gaka-vh8). All routes admin-gated via requireAdmin (see admin_label_
// images.go).
//
// Endpoints registered by server.go:
//
//   GET    /api/v1/admin/backfill/config
//   PATCH  /api/v1/admin/backfill/config
//   GET    /api/v1/admin/backfill/stats
//   POST   /api/v1/admin/backfill/jobs
//   PATCH  /api/v1/admin/backfill/jobs/:id
//   POST   /api/v1/admin/backfill/jobs/:id/heartbeats
//   POST   /api/v1/admin/backfill/jobs/:id/preview
//   DELETE /api/v1/admin/backfill/heartbeats
//   GET    /api/v1/admin/backfill/ws
//
// Auth:
//   - JSON endpoints: bearer token → resolved owner → admin allowlist.
//     Same shape as the label-images admin tree.
//   - WS endpoint: refresh_token cookie (WS handshake can't set
//     Authorization) → resolved owner → admin allowlist.
//
// Wire-shape rule of thumb: the request body is the shape the CLI (or
// FE) sends. The server never trusts a body-supplied username or
// sourceTag — both come from resolved auth + persisted config.

package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/backfilljobs"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/labstack/echo/v5"
)

// AdminBackfillConfig: GET /api/v1/admin/backfill/config.
func (h *Handler) AdminBackfillConfig(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	cfg, err := h.DB.GetBackfillConfig(c.Request().Context(), owner)
	if err != nil {
		return h.internalErr(c, "backfill config get failed", err)
	}
	return c.JSON(http.StatusOK, cfg)
}

// AdminBackfillConfigUpdate: PATCH /api/v1/admin/backfill/config.
// Body is a partial config; every zero-value field is left unchanged
// against the current persisted row (fetched fresh under the same
// request).
type backfillConfigPatch struct {
	ClusterGapSec     *int              `json:"clusterGapSec,omitempty"`
	PreCommitLeadSec  *int              `json:"preCommitLeadSec,omitempty"`
	PostCommitTailSec *int              `json:"postCommitTailSec,omitempty"`
	HeartbeatRateSec  *int              `json:"heartbeatRateSec,omitempty"`
	AuthorEmails      *[]string         `json:"authorEmails,omitempty"`
	SourceTag         *string           `json:"sourceTag,omitempty"`
	LangMap           map[string]string `json:"langMap,omitempty"`
}

// AdminBackfillConfigUpdate handles the PATCH.
func (h *Handler) AdminBackfillConfigUpdate(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	var p backfillConfigPatch
	if aerr := BindJSONWithLimit(c, &p, BodyLimitMedium); aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()
	cur, err := h.DB.GetBackfillConfig(ctx, owner)
	if err != nil {
		return h.internalErr(c, "backfill config get failed", err)
	}
	if p.ClusterGapSec != nil {
		cur.ClusterGapSec = *p.ClusterGapSec
	}
	if p.PreCommitLeadSec != nil {
		cur.PreCommitLeadSec = *p.PreCommitLeadSec
	}
	if p.PostCommitTailSec != nil {
		cur.PostCommitTailSec = *p.PostCommitTailSec
	}
	if p.HeartbeatRateSec != nil {
		cur.HeartbeatRateSec = *p.HeartbeatRateSec
	}
	if p.AuthorEmails != nil {
		cur.AuthorEmails = *p.AuthorEmails
	}
	if p.SourceTag != nil {
		cur.SourceTag = *p.SourceTag
	}
	if p.LangMap != nil {
		cur.LangMap = p.LangMap
	}
	cur.Username = owner
	if err := h.DB.SetBackfillConfig(ctx, cur); err != nil {
		return h.internalErr(c, "backfill config set failed", err)
	}
	// Re-read to reflect any clamping applied in SetBackfillConfig, so
	// the FE displays what actually persisted.
	updated, err := h.DB.GetBackfillConfig(ctx, owner)
	if err != nil {
		return h.internalErr(c, "backfill config re-read failed", err)
	}
	return c.JSON(http.StatusOK, updated)
}

// AdminBackfillStats: GET /api/v1/admin/backfill/stats.
func (h *Handler) AdminBackfillStats(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	stats, err := h.DB.BackfillStatsFor(c.Request().Context(), owner)
	if err != nil {
		return h.internalErr(c, "backfill stats failed", err)
	}
	return c.JSON(http.StatusOK, stats)
}

// enqueueJobReq is the body for POST /admin/backfill/jobs.
type enqueueJobReq struct {
	RepoName     string `json:"repoName"`
	RepoPath     string `json:"repoPath"`
	TotalCommits int    `json:"totalCommits"`
}

// AdminBackfillEnqueueJob: POST /api/v1/admin/backfill/jobs.
// Returns {jobId} 202 and adds a queued row to the registry.
func (h *Handler) AdminBackfillEnqueueJob(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	if h.BackfillJobQueue == nil {
		return respondErr(c, apierr.New(http.StatusServiceUnavailable,
			"backfill queue not initialized", nil))
	}
	var req enqueueJobReq
	if aerr := BindJSONWithLimit(c, &req, BodyLimitSmall); aerr != nil {
		return respondErr(c, aerr)
	}
	if strings.TrimSpace(req.RepoName) == "" {
		return respondErr(c, apierr.BadRequest("`repoName` is required"))
	}
	if req.TotalCommits < 0 {
		return respondErr(c, apierr.BadRequest("`totalCommits` must be >= 0"))
	}
	job := h.BackfillJobQueue.Enqueue(backfilljobs.EnqueueInput{
		Owner:    owner,
		RepoName: strings.TrimSpace(req.RepoName),
		RepoPath: strings.TrimSpace(req.RepoPath),
		Total:    req.TotalCommits,
	})
	return c.JSON(http.StatusAccepted, map[string]any{
		"jobId": job.ID,
		"job":   job,
	})
}

// jobPatchReq is the body for PATCH /admin/backfill/jobs/:id. Used by
// the CLI to report status transitions (queued → running is automatic
// on first heartbeats POST, but done/error are explicit).
type jobPatchReq struct {
	Status    string  `json:"status,omitempty"`
	Error     *string `json:"error,omitempty"`
	Processed *int    `json:"processed,omitempty"`
	Written   *int    `json:"written,omitempty"`
	Skipped   *int    `json:"skipped,omitempty"`
}

// AdminBackfillJobPatch: PATCH /api/v1/admin/backfill/jobs/:id.
func (h *Handler) AdminBackfillJobPatch(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	if h.BackfillJobQueue == nil {
		return respondErr(c, apierr.New(http.StatusServiceUnavailable,
			"backfill queue not initialized", nil))
	}
	id := c.Param("id")
	if id == "" {
		return respondErr(c, apierr.BadRequest("job id required"))
	}
	cur, ok := h.BackfillJobQueue.Get(id)
	if !ok {
		return respondErr(c, apierr.New(http.StatusNotFound, "job not found", nil))
	}
	// Cross-owner protection: an admin can only touch their own jobs.
	// 404 (not 403) to avoid an oracle for other admins' job IDs.
	if cur.Owner != owner {
		return respondErr(c, apierr.New(http.StatusNotFound, "job not found", nil))
	}

	var req jobPatchReq
	if aerr := BindJSONWithLimit(c, &req, BodyLimitSmall); aerr != nil {
		return respondErr(c, aerr)
	}
	patch := backfilljobs.UpdatePatch{
		Error:     req.Error,
		Processed: req.Processed,
		Written:   req.Written,
		Skipped:   req.Skipped,
	}
	switch strings.ToLower(strings.TrimSpace(req.Status)) {
	case "":
		// no status change
	case "queued", "running", "done", "error":
		patch.Status = backfilljobs.JobStatus(strings.ToLower(req.Status))
	default:
		return respondErr(c, apierr.BadRequest("unknown status; use queued|running|done|error"))
	}
	job, _ := h.BackfillJobQueue.Update(id, patch)
	return c.JSON(http.StatusOK, job)
}

// heartbeatsBatchReq is the body for POST /admin/backfill/jobs/:id/
// heartbeats and /preview. `sessions[].heartbeats` is a straight list
// of model.HeartbeatPayload — the CLI marshals git.Materialize output
// into that shape directly.
type heartbeatsBatchReq struct {
	Sessions []struct {
		Start      time.Time                `json:"start"`
		End        time.Time                `json:"end"`
		Heartbeats []model.HeartbeatPayload `json:"heartbeats"`
	} `json:"sessions"`
}

// AdminBackfillJobHeartbeats: POST /api/v1/admin/backfill/jobs/:id/
// heartbeats. Runs the overlap check + insert for every session,
// increments the job's per-session counts, and returns the per-session
// result.
func (h *Handler) AdminBackfillJobHeartbeats(c *echo.Context) error {
	return h.handleBackfillJobBatch(c, /* insert */ true)
}

// AdminBackfillJobPreview: POST /api/v1/admin/backfill/jobs/:id/preview.
// Same wire shape, no writes.
func (h *Handler) AdminBackfillJobPreview(c *echo.Context) error {
	return h.handleBackfillJobBatch(c, /* insert */ false)
}

func (h *Handler) handleBackfillJobBatch(c *echo.Context, insert bool) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	if h.BackfillJobQueue == nil {
		return respondErr(c, apierr.New(http.StatusServiceUnavailable,
			"backfill queue not initialized", nil))
	}
	id := c.Param("id")
	if id == "" {
		return respondErr(c, apierr.BadRequest("job id required"))
	}
	cur, ok := h.BackfillJobQueue.Get(id)
	if !ok {
		return respondErr(c, apierr.New(http.StatusNotFound, "job not found", nil))
	}
	if cur.Owner != owner {
		return respondErr(c, apierr.New(http.StatusNotFound, "job not found", nil))
	}

	// Bigger cap here — a single batch can carry hundreds of KiB of
	// materialized heartbeats. 4 MiB is enough for a couple thousand
	// sessions and still small enough that a hostile CLI can't wedge
	// the server on parse alone.
	var req heartbeatsBatchReq
	if aerr := BindJSONWithLimit(c, &req, 4*1024*1024); aerr != nil {
		return respondErr(c, aerr)
	}
	if len(req.Sessions) == 0 {
		return c.JSON(http.StatusOK, db.BackfillResult{})
	}

	// Pull the current source tag from persisted config so the CLI
	// can't forge one. Fall back to "backfill:git" if the row is
	// missing (defaultBackfillConfig handles this).
	cfg, err := h.DB.GetBackfillConfig(c.Request().Context(), owner)
	if err != nil {
		return h.internalErr(c, "backfill config get failed", err)
	}
	sessions := make([]db.BackfillSession, 0, len(req.Sessions))
	for _, s := range req.Sessions {
		sessions = append(sessions, db.BackfillSession{
			Start:      s.Start,
			End:        s.End,
			Heartbeats: s.Heartbeats,
		})
	}
	// job.processed is incremented by session count, not commit count.
	// The FE displays "processed/total"; total was set at enqueue time
	// (commit count from the CLI's scan), so the ratio is
	// sessions-so-far / commits-total. This is intentionally
	// approximate — the operator sees "we're moving through the repo"
	// and by the end processed==total minus filtered-out-during-scan
	// commits. An exact per-commit counter would require the CLI to
	// PATCH per commit, which is too chatty.
	sessionCount := len(sessions)
	batch := db.BackfillBatch{
		Username:  owner,
		SourceTag: cfg.SourceTag,
		Sessions:  sessions,
	}
	var res db.BackfillResult
	if insert {
		res, err = h.DB.InsertBackfillBatch(c.Request().Context(), batch)
	} else {
		res, err = h.DB.PreviewBackfillBatch(c.Request().Context(), batch)
	}
	if err != nil {
		return h.internalErr(c, "backfill batch failed", err)
	}
	if insert {
		// Auto-flip queued → running and increment counters. Uses the
		// registry's atomic IncrementCounts so a burst of parallel
		// batches doesn't lose any updates.
		h.BackfillJobQueue.IncrementCounts(id, sessionCount, res.AcceptedHeartbeats, res.SkippedHeartbeats)
	}
	// Invalidate cached aggregations so the new rows show up on the
	// next dashboard poll. Backfill writes can span months, so cached
	// stats for "last 7 days" would still be technically correct — but
	// a fresh purge run invalidates old backfill row counts too, so we
	// flush unconditionally for correctness.
	h.invalidateOwnerCache(owner)
	return c.JSON(http.StatusOK, res)
}

// AdminBackfillDeleteHeartbeats: DELETE /api/v1/admin/backfill/heartbeats.
// Query params:
//   ?source=<sender>     — delete only rows matching this exact source
//                          (must start with "backfill:")
//   ?all=true            — delete every backfill:% row for this owner
// One of the two is required (400 otherwise).
func (h *Handler) AdminBackfillDeleteHeartbeats(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	source := c.QueryParam("source")
	all := c.QueryParam("all") == "true"
	if source == "" && !all {
		return respondErr(c, apierr.BadRequest("either ?source=<tag> or ?all=true is required"))
	}
	if source != "" && !strings.HasPrefix(source, "backfill:") {
		return respondErr(c, apierr.BadRequest("source must start with 'backfill:'"))
	}
	pattern := source
	if all {
		pattern = "backfill:%"
	}
	n, err := h.DB.DeleteBackfilledHeartbeats(c.Request().Context(), owner, pattern)
	if err != nil {
		return h.internalErr(c, "backfill delete failed", err)
	}
	h.invalidateOwnerCache(owner)
	return c.JSON(http.StatusOK, map[string]any{"deleted": n})
}

// AdminBackfillWS: GET /api/v1/admin/backfill/ws — durable stream of
// backfill job events, filtered to the connecting admin's own jobs.
// Wire protocol matches the label-images WS (same {kind, job} events)
// so the FE hook is a small copy-paste of useImageJobQueue.
func (h *Handler) AdminBackfillWS(c *echo.Context) error {
	owner, aerr := h.resolveOwnerFromCookie(c, apierr.ExpiredRefreshToken())
	if aerr != nil {
		return respondErr(c, aerr)
	}
	if !h.Cfg.IsAdmin(owner) {
		return respondErr(c, apierr.New(http.StatusForbidden, "admin only", nil))
	}
	if h.BackfillJobQueue == nil {
		return respondErr(c, apierr.New(http.StatusServiceUnavailable,
			"backfill queue not initialized", nil))
	}
	conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return nil
	}
	defer conn.CloseNow()
	ctx := context.Background()

	sub, unsub := h.BackfillJobQueue.Subscribe()
	defer unsub()
	// Initial snapshot (owner-filtered) before draining live events.
	if err := wsjson.Write(ctx, conn, map[string]any{
		"kind": "snapshot",
		"jobs": h.BackfillJobQueue.SnapshotFor(owner),
	}); err != nil {
		return nil
	}

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
			// Owner filter at the boundary: an event for another admin's
			// job (or an EventRemoved for a job this admin never saw)
			// is silently skipped rather than forwarded.
			if ev.Job.Owner != "" && ev.Job.Owner != owner {
				continue
			}
			if err := wsjson.Write(streamCtx, conn, backfillEvent2json(ev)); err != nil {
				return nil
			}
		}
	}
}

// backfillEvent2json is the WS wire shape (mirrors event2json for
// label-images). Kept as a separate function so a future field addition
// on backfilljobs.Event doesn't leak into the label-images path.
func backfillEvent2json(ev backfilljobs.Event) map[string]any {
	return map[string]any{
		"kind": string(ev.Kind),
		"job":  ev.Job,
	}
}
