// admin_label_images.go: authed regeneration endpoints for the FE Admin
// tab (gaka-myv, evolved by gaka-8bz).
//
// Post-gaka-8bz architecture: the FE no longer runs its own concurrency
// pool. Instead the server owns an in-memory imagejobs.Registry (queue) +
// worker pool; the FE POSTs an enqueue request that returns 202 with the
// jobIDs, and a durable WebSocket at /admin/label-images/ws streams the
// full lifecycle (queued -> running -> done|error -> removed after
// retention). Any admin session connecting to the WS receives an
// immediate snapshot of what's already in flight, so refreshing the
// browser or opening a new tab never orphans a run.
//
// Auth model:
//   - resolveUser => valid API token (401 otherwise, matches the rest of
//     the /api/v1/users/current/* tree)
//   - config.IsAdmin(username) => 403 for non-admins. Admin list is set
//     via BOOM_ADMIN_USERS (comma-separated). Empty list = nobody is
//     admin (the safe default).
//   - the WS endpoint auths via the HttpOnly refresh_token cookie because
//     WS handshakes can't set the Authorization header.
//
// Endpoints:
//
//	GET  /api/v1/admin/label-images
//	  -> {enabled: bool, model: string, shimUrl: string,
//	      admin: bool, count: int, items, baseline}
//
//	POST /api/v1/admin/label-images/regenerate
//	  Body: {entries: [{id, prompt, model?, size?, seed?}, ...],
//	         ids?: [...], all?: bool, truncate?: bool}
//	  -> 202 {jobs: [{jobId, labelId, existing}, ...]}
//	  Idempotent per label: if the label already has a queued/running
//	  job the response carries the existing jobId + existing=true. The
//	  server pool absorbs the concurrency the FE previously enforced
//	  client-side.
//
//	GET  /api/v1/admin/label-images/ws
//	  WebSocket. On connect the server writes an initial snapshot then
//	  streams every added/updated/removed event forever.
package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/labelcatalog"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/imagejobs"
	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/worker/labelimages"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/labstack/echo/v5"
)

// AdminLabelImagesInfo: GET /api/v1/admin/label-images.
// Returns feature status + shim config + current row count so the Admin
// tab can render a "regenerate" button plus a small dashboard.
//
// Post gaka-364.3 the response also carries `baseline` = every id from the
// DB labels catalog. The admin table renders one row per catalog id
// (present-or-missing image), and the compiled labelcatalog baseline is
// kept as a fallback for the "brand new DB before migrations apply" case.
func (h *Handler) AdminLabelImagesInfo(c *echo.Context) error {
	_, aerr := h.requireAdmin(c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	ctx := c.Request().Context()
	items, err := h.DB.ListLabelImagesMeta(ctx)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "list label images failed", err)
	}
	baseline := labelcatalog.IDs()
	if labels, lerr := h.DB.ListLabels(ctx); lerr == nil && len(labels) > 0 {
		ids := make([]string, 0, len(labels))
		for _, l := range labels {
			ids = append(ids, l.ID)
		}
		baseline = ids
	}
	// gaka-8bz worker-topology follow-up: surface which transport is
	// actually running regens so the Admin tab can distinguish "the local
	// in-process pool" from "the decoupled boomtime-worker pod via
	// RabbitMQ" — and, when it's the latter, how deep the broker queue
	// currently is + a link to its management UI. Both extra fields are
	// best-effort: a depth-check failure is logged and simply omitted
	// rather than failing the whole info request.
	broker := "inprocess"
	resp := map[string]any{
		"enabled":  h.Cfg.LabelImagesEnabled(),
		"model":    h.Cfg.ComfyUIModel,
		"shimUrl":  h.Cfg.ComfyUIShimURL,
		"count":    len(items),
		"items":    items,
		"baseline": baseline,
	}
	if h.Cfg.BrokerRabbit() {
		broker = "rabbitmq"
		if mgmtURL := strings.TrimSpace(h.Cfg.RabbitMgmtURL); mgmtURL != "" {
			resp["mgmtUrl"] = mgmtURL
		}
		if qi, ok := h.ImageJobQueue.(imagejobs.QueueInspector); ok {
			if n, derr := qi.QueueDepth(); derr != nil {
				h.Logger.Warn("admin label-images: queue depth check failed", "err", derr)
			} else {
				resp["queueDepth"] = n
			}
		}
	}
	resp["broker"] = broker
	return c.JSON(http.StatusOK, resp)
}

// regenReq is the POST body shape. The FE sends BOTH `entries` (the full
// {id, prompt} catalog snapshot) and either `all=true` OR `ids=[...]` to
// pick which subset to generate.
type regenReq struct {
	Entries  []regenEntry `json:"entries"`
	IDs      []string     `json:"ids,omitempty"`
	All      bool         `json:"all,omitempty"`
	Truncate bool         `json:"truncate,omitempty"`
}
type regenEntry struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	// Optional per-request overrides — the Admin tab's per-label editor
	// threads user tweaks through here (prompt iteration, pipeline swap,
	// deterministic seed). Empty strings + nil seed fall back to worker
	// defaults (Model=env, Size=1024x1024, Seed=random).
	Model string `json:"model,omitempty"`
	Size  string `json:"size,omitempty"`
	Seed  *int64 `json:"seed,omitempty"`
}

// regenResponseJob is the per-enqueue result the FE sees. `existing=true`
// means the label already had an in-flight job; the FE treats jobId as
// the canonical handle either way.
type regenResponseJob struct {
	JobID    string `json:"jobId"`
	LabelID  string `json:"labelId"`
	Existing bool   `json:"existing"`
}

// AdminLabelImagesRegenerate: POST /api/v1/admin/label-images/regenerate.
// Enqueues each requested entry into the imagejobs registry and returns
// 202 with the resulting jobIDs. The server's worker pool absorbs
// concurrency; the FE just fires enqueues and watches the WS.
func (h *Handler) AdminLabelImagesRegenerate(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	// Unified (gaka-hney Stage 3): route regen through catalyst-go-jobs (the DB
	// queue + KEDA ScaledJob) instead of the in-memory imagejobs registry. Needs
	// the worker + the DB-jobs enqueuer/store; falls back to imagejobs when off.
	unified := h.Cfg != nil && h.Cfg.JobsUnified && h.JobEnqueuer != nil && h.JobStore != nil
	if h.LabelImagesWorker == nil || (!unified && h.ImageJobQueue == nil) {
		return apihelpers.RespondErr(c, apierr.New(http.StatusServiceUnavailable,
			"label-images feature is disabled — set BOOM_FEATURE_LABEL_IMAGES=on and BOOM_COMFYUI_SHIM_URL, then restart", nil))
	}

	var req regenReq
	if aerr := apihelpers.BindJSONWithLimit(c, &req, 256*1024); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if len(req.Entries) == 0 {
		return apihelpers.RespondErr(c, apierr.BadRequest("`entries` is required (send [{id, prompt}, ...])"))
	}

	// Build the id -> entry map, preserving per-entry overrides end-to-end
	// so the Executor honors user tweaks from the Admin tab's per-label
	// editor. Descriptions come from a per-request DB lookup below rather
	// than the wire body because the admin editor's "Save + regen" saves
	// the label FIRST (see AdminTab.tsx), so a fresh DB read at regen
	// time picks up the just-saved narrative without threading it into
	// every FE payload.
	byID := make(map[string]labelcatalog.Entry, len(req.Entries))
	for _, e := range req.Entries {
		if e.ID == "" || e.Prompt == "" {
			continue
		}
		byID[e.ID] = labelcatalog.Entry{
			ID:     e.ID,
			Prompt: e.Prompt,
			Model:  e.Model,
			Size:   e.Size,
			Seed:   e.Seed,
		}
	}

	var toRun []labelcatalog.Entry
	if req.All {
		for _, entry := range byID {
			toRun = append(toRun, entry)
		}
	} else if len(req.IDs) > 0 {
		for _, id := range req.IDs {
			if entry, ok := byID[id]; ok {
				toRun = append(toRun, entry)
			}
		}
	} else {
		return apihelpers.RespondErr(c, apierr.BadRequest("either `all: true` or a non-empty `ids` array is required"))
	}
	if len(toRun) == 0 {
		return apihelpers.RespondErr(c, apierr.BadRequest("nothing to regenerate — verify `ids` match the `entries` you sent"))
	}

	// Truncate is a destructive DB op — preserve the pre-gaka-8bz
	// behavior where `all=true` + `truncate=true` wipes label_images
	// first so deleted-in-FE labels also fall out of the DB. The
	// individual per-entry delete (previous batch delete) is dropped
	// because the Executor (labelimages.Worker.RegenerateEntry) already
	// deletes the row before saving the fresh one — doing it here too
	// was a redundant pre-flight that also caused the "row is missing
	// for 5 minutes while regen runs" flicker.
	reqCtx := c.Request().Context()
	if req.All && req.Truncate {
		if err := h.DB.TruncateLabelImages(reqCtx); err != nil {
			return apihelpers.InternalErr(h.Logger, c, "label images truncate failed", err)
		}
	}

	out := make([]regenResponseJob, 0, len(toRun))
	for _, e := range toRun {
		// Pull the latest description from the DB label row so the shim
		// prompt reflects any narrative edit the operator just saved via
		// the Sheet editor's "Save + regen" flow. A missing row (unknown
		// id) or DB blip is not fatal — we log and fall back to an empty
		// description, which produces the pre-gaka-8bz {sys, prompt}
		// composition.
		desc := ""
		if row, err := h.DB.GetLabel(reqCtx, e.ID); err != nil {
			h.Logger.Warn("labelimages: description lookup failed, falling back to empty",
				"id", e.ID, "err", err)
		} else if row != nil {
			desc = row.Description
		}
		if unified {
			// Dedup per label (owner==labelID) so a double "regen all" doesn't
			// double-fire ComfyUI — mirrors the imagejobs registry idempotency.
			if pending, derr := h.JobStore.HasPending(reqCtx, labelimages.RegenJobKind, e.ID); derr == nil && pending {
				out = append(out, regenResponseJob{JobID: e.ID, LabelID: e.ID, Existing: true})
				continue
			}
			payload, merr := labelimages.RegenJobPayload{
				LabelID: e.ID, Description: desc, Prompt: e.Prompt,
				Model: e.Model, Size: e.Size, Seed: e.Seed,
			}.JSON()
			if merr != nil {
				return apihelpers.InternalErr(h.Logger, c, "label-image payload marshal failed", merr)
			}
			id, eerr := h.JobEnqueuer.Enqueue(reqCtx, labelimages.RegenJobKind, payload,
				jobs.Owner(e.ID), jobs.MaxAttempts(1))
			if eerr != nil {
				return apihelpers.InternalErr(h.Logger, c, "label-image enqueue failed", eerr)
			}
			out = append(out, regenResponseJob{JobID: strconv.FormatInt(id, 10), LabelID: e.ID, Existing: false})
			continue
		}

		job, existing := h.ImageJobQueue.Enqueue(imagejobs.EnqueueInput{
			LabelID:     e.ID,
			Description: desc,
			Prompt:      e.Prompt,
			Model:       e.Model,
			Size:        e.Size,
			Seed:        e.Seed,
		})
		out = append(out, regenResponseJob{
			JobID:    job.ID,
			LabelID:  job.LabelID,
			Existing: existing,
		})
	}

	return c.JSON(http.StatusAccepted, map[string]any{
		"queued": len(out),
		"jobs":   out,
	})
}

// labelJobStatus is one label's latest regen job, mapped to the imagejobs
// status vocab the FE already renders (done/error — not the jobs 'failed').
type labelJobStatus struct {
	LabelID    string  `json:"labelId"`
	Status     string  `json:"status"` // queued|running|done|error
	Error      string  `json:"error,omitempty"`
	StartedAt  *string `json:"startedAt,omitempty"`
	FinishedAt *string `json:"finishedAt,omitempty"`
}

// AdminLabelImagesStatus: GET /api/v1/admin/label-images/status — the latest
// label-image job per label from the DB queue (gaka-hney Stage 3). Under
// BOOM_JOBS_UNIFIED this replaces the imagejobs WS as the FE's per-label status
// source; the admin tab polls it. Returns [] when the jobs subsystem isn't
// wired (feature off) so the FE degrades to "no in-flight jobs" rather than
// erroring.
func (h *Handler) AdminLabelImagesStatus(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	out := []labelJobStatus{}
	if h.JobStore != nil {
		rows, err := h.JobStore.ListLatestPerOwner(c.Request().Context(), labelimages.RegenJobKind)
		if err != nil {
			return apihelpers.InternalErr(h.Logger, c, "label-image status query failed", err)
		}
		for _, j := range rows {
			// Match the imagejobs registry's retention so a done/error badge
			// shows briefly then clears, instead of a permanent green check on
			// every label ever regenerated (DB jobs persist forever). Done=5m,
			// error=15m; in-flight always shown.
			if j.FinishedAt != nil {
				age := time.Since(*j.FinishedAt)
				if j.Status == jobs.StatusDone && age > 5*time.Minute {
					continue
				}
				if j.Status == jobs.StatusFailed && age > 15*time.Minute {
					continue
				}
			}
			out = append(out, labelJobStatus{
				LabelID:    j.Owner,
				Status:     mapJobStatus(j.Status),
				Error:      j.Error,
				StartedAt:  rfcPtr(j.StartedAt),
				FinishedAt: rfcPtr(j.FinishedAt),
			})
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"jobs": out})
}

// mapJobStatus maps a catalyst-go-jobs status to the imagejobs vocab the FE
// renders: 'failed' → 'error'; queued/running/done pass through.
func mapJobStatus(s jobs.Status) string {
	if s == jobs.StatusFailed {
		return "error"
	}
	return string(s)
}

// rfcPtr renders an optional timestamp as an RFC3339 string pointer (nil stays
// nil). The jobs-admin HTTP surface that used to share this helper moved to the
// jobs package (internal/jobs/adminhttp.go); this copy serves the imagejobs
// status endpoint above.
func rfcPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// AdminLabelImagesWS: GET /api/v1/admin/label-images/ws — durable stream of
// registry events to the Admin tab.
//
// Auth uses the HttpOnly refresh_token cookie because WS handshakes cannot
// set the Authorization header; the resolved owner is then checked against
// the admin allowlist. Non-admin cookies get 403 pre-upgrade.
//
// Wire protocol (JSON per frame):
//   - initial: {"kind":"snapshot","jobs":[...Job]}
//   - live:    {"kind":"added"|"updated"|"removed","job":{...Job}}
//
// The reader goroutine consumes pings + close frames and cancels the write
// loop on the first read error, so a dropped client tears the WS down
// promptly. A subscriber that can't keep up drops OLDEST events (see
// Registry.broadcastLocked) so a wedged FE never blocks the emitter — the
// FE reconnect + snapshot recovers the true state.
func (h *Handler) AdminLabelImagesWS(c *echo.Context) error {
	// Cookie auth first — a bad cookie should never even trigger the
	// upgrade handshake.
	owner, aerr := apihelpers.IdentifyOwnerFromCookie(h.DB, h.Logger, c, apierr.ExpiredRefreshToken())
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if !h.Cfg.IsAdmin(owner) {
		return apihelpers.RespondErr(c, apierr.New(http.StatusForbidden, "admin only", nil))
	}
	if h.ImageJobEvents == nil {
		return apihelpers.RespondErr(c, apierr.New(http.StatusServiceUnavailable,
			"label-images feature is disabled", nil))
	}

	conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		InsecureSkipVerify: true, // same-origin; CORS is handled elsewhere
	})
	if err != nil {
		return nil
	}
	defer conn.CloseNow()
	metrics.WSActiveConnections.WithLabelValues("label-images").Inc()
	defer metrics.WSActiveConnections.WithLabelValues("label-images").Dec()

	// Background context so the stream survives after the HTTP handler
	// returns (echo tears down the request context on return).
	ctx := context.Background()

	// Subscribe BEFORE the snapshot: an event fired between snapshot and
	// subscribe would otherwise be missed. Duplicates are cheap (the FE
	// map keyed by jobId absorbs them).
	sub, unsub := h.ImageJobEvents.Subscribe()
	defer unsub()

	if err := wsjson.Write(ctx, conn, map[string]any{
		"kind": "snapshot",
		"jobs": h.ImageJobEvents.Snapshot(),
	}); err != nil {
		return nil
	}

	// Client-disconnect detector: a reader goroutine watches for close /
	// error, and cancels streamCtx so the write loop bails.
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
			if err := wsjson.Write(streamCtx, conn, event2json(ev)); err != nil {
				return nil
			}
		}
	}
}

// event2json unwraps an imagejobs.Event into the wire shape the FE hook
// expects. Kept separate from Event so the internal type can evolve
// without breaking the FE contract.
func event2json(ev imagejobs.Event) map[string]any {
	return map[string]any{
		"kind": string(ev.Kind),
		"job":  ev.Job,
	}
}
