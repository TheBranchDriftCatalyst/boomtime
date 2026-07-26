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
//   GET  /api/v1/admin/label-images
//     -> {enabled: bool, model: string, shimUrl: string,
//         admin: bool, count: int, items, baseline}
//
//   POST /api/v1/admin/label-images/regenerate
//     Body: {entries: [{id, prompt, model?, size?, seed?}, ...],
//            ids?: [...], all?: bool, truncate?: bool}
//     -> 202 {jobs: [{jobId, labelId, existing}, ...]}
//     Idempotent per label: if the label already has a queued/running
//     job the response carries the existing jobId + existing=true. The
//     server pool absorbs the concurrency the FE previously enforced
//     client-side.
//
//   GET  /api/v1/admin/label-images/ws
//     WebSocket. On connect the server writes an initial snapshot then
//     streams every added/updated/removed event forever.
package handler

import (
	"context"
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/labelcatalog"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/imagejobs"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/labstack/echo/v5"
)

// requireAdmin: 401 without a token, 403 when not on the admin allowlist.
// Returns the resolved owner on success. The 403 path deliberately does
// NOT distinguish "unknown admin config" from "not on the list" — both
// look like a plain 403 to the client.
func (h *Handler) requireAdmin(c *echo.Context) (string, *apierr.Error) {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return "", aerr
	}
	if !h.Cfg.IsAdmin(owner) {
		return "", apierr.New(http.StatusForbidden, "admin only", nil)
	}
	return owner, nil
}

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
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()
	items, err := h.DB.ListLabelImagesMeta(ctx)
	if err != nil {
		return h.internalErr(c, "list label images failed", err)
	}
	baseline := labelcatalog.IDs()
	if labels, lerr := h.DB.ListLabels(ctx); lerr == nil && len(labels) > 0 {
		ids := make([]string, 0, len(labels))
		for _, l := range labels {
			ids = append(ids, l.ID)
		}
		baseline = ids
	}
	return c.JSON(http.StatusOK, map[string]any{
		"enabled":  h.Cfg.LabelImagesEnabled(),
		"model":    h.Cfg.ComfyUIModel,
		"shimUrl":  h.Cfg.ComfyUIShimURL,
		"count":    len(items),
		"items":    items,
		"baseline": baseline,
	})
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
		return respondErr(c, aerr)
	}
	if h.LabelImagesWorker == nil || h.ImageJobQueue == nil {
		return respondErr(c, apierr.New(http.StatusServiceUnavailable,
			"label-images feature is disabled — set BOOM_FEATURE_LABEL_IMAGES=on and BOOM_COMFYUI_SHIM_URL, then restart", nil))
	}

	var req regenReq
	if aerr := BindJSONWithLimit(c, &req, 256*1024); aerr != nil {
		return respondErr(c, aerr)
	}
	if len(req.Entries) == 0 {
		return respondErr(c, apierr.BadRequest("`entries` is required (send [{id, prompt}, ...])"))
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
		return respondErr(c, apierr.BadRequest("either `all: true` or a non-empty `ids` array is required"))
	}
	if len(toRun) == 0 {
		return respondErr(c, apierr.BadRequest("nothing to regenerate — verify `ids` match the `entries` you sent"))
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
			return h.internalErr(c, "label images truncate failed", err)
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
	owner, aerr := h.resolveOwnerFromCookie(c, apierr.ExpiredRefreshToken())
	if aerr != nil {
		return respondErr(c, aerr)
	}
	if !h.Cfg.IsAdmin(owner) {
		return respondErr(c, apierr.New(http.StatusForbidden, "admin only", nil))
	}
	if h.ImageJobQueue == nil {
		return respondErr(c, apierr.New(http.StatusServiceUnavailable,
			"label-images feature is disabled", nil))
	}

	conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		InsecureSkipVerify: true, // same-origin; CORS is handled elsewhere
	})
	if err != nil {
		return nil
	}
	defer conn.CloseNow()

	// Background context so the stream survives after the HTTP handler
	// returns (echo tears down the request context on return).
	ctx := context.Background()

	// Subscribe BEFORE the snapshot: an event fired between snapshot and
	// subscribe would otherwise be missed. Duplicates are cheap (the FE
	// map keyed by jobId absorbs them).
	sub, unsub := h.ImageJobQueue.Subscribe()
	defer unsub()

	if err := wsjson.Write(ctx, conn, map[string]any{
		"kind": "snapshot",
		"jobs": h.ImageJobQueue.Snapshot(),
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
