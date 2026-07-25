// admin_label_images.go: authed regeneration endpoints for the FE Admin
// tab (gaka-myv).
//
// The Admin tab lets a whitelisted operator kick a full or per-label
// image regeneration without shelling into the box. Since the source-of-
// truth label catalog lives in TypeScript, the FE POSTs the FULL list of
// {id, prompt} pairs it wants generated — the Go side doesn't need to
// mirror the ever-changing memecore/kawaii/space-marine expansions.
//
// Auth model:
//   - resolveUser => valid API token (401 otherwise, matches the rest of
//     the /api/v1/users/current/* tree)
//   - config.IsAdmin(username) => 403 for non-admins. Admin list is set
//     via BOOM_ADMIN_USERS (comma-separated). Empty list = nobody is
//     admin (the safe default).
//
// Endpoints:
//
//   GET  /api/v1/admin/label-images
//     -> {enabled: bool, model: string, shimUrl: string,
//         admin: bool, count: int}
//     Returns config + row count. Non-admins get 403 (no oracle for
//     whether the feature is on — the shape of the config is not a
//     secret but the endpoint keeps a consistent gate).
//
//   POST /api/v1/admin/label-images/regenerate
//     Body: {ids: ["late-night-coder"], entries: [{id,prompt}, ...],
//            all: bool, truncate: bool}
//     - all=true: rebuilds every entry the FE sent. Optionally
//       truncate=true wipes the table first (guarantees deleted-in-FE
//       labels are also removed from the DB).
//     - ids: regenerate a specific subset (validated against entries).
//     Returns {generated, failed}.
//
// The endpoint intentionally does NOT stream progress — a full 68-label
// SDXL regeneration takes ~15 minutes and holding an HTTP connection for
// that long defeats every reverse proxy in existence. Instead the endpoint
// generates SEQUENTIALLY inside the request (short-run correctness), and
// for the common case (--all with the whole catalog) the FE either shows
// a "kicked off" indicator + polls the /admin/label-images GET for a
// count-changed signal, or (simpler MVP) the FE just fires the request
// and lets the browser hold the connection for the duration.
package handler

import (
	"context"
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/labelcatalog"
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
func (h *Handler) AdminLabelImagesInfo(c *echo.Context) error {
	_, aerr := h.requireAdmin(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	var count int
	// Row count is cheap (single COUNT); we don't cache — the tab is
	// staff-only and rarely hit.
	_ = h.DB.Pool.QueryRow(c.Request().Context(),
		`SELECT COUNT(*) FROM label_images`).Scan(&count)
	return c.JSON(http.StatusOK, map[string]any{
		"enabled":  h.Cfg.LabelImagesEnabled(),
		"model":    h.Cfg.ComfyUIModel,
		"shimUrl":  h.Cfg.ComfyUIShimURL,
		"count":    count,
		"baseline": labelcatalog.IDs(),
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
}

// AdminLabelImagesRegenerate: POST /api/v1/admin/label-images/regenerate.
// Blocking generation — the FE is expected to show a spinner + hold the
// request. For very large regenerations the operator can prefer running
// the CLI `boomtime label-images regenerate --all` server-side.
func (h *Handler) AdminLabelImagesRegenerate(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return respondErr(c, aerr)
	}
	if h.LabelImagesWorker == nil {
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

	// Pick the subset.
	byID := make(map[string]string, len(req.Entries))
	for _, e := range req.Entries {
		if e.ID == "" || e.Prompt == "" {
			continue
		}
		byID[e.ID] = e.Prompt
	}

	var toRun []labelcatalog.Entry
	if req.All {
		for id, prompt := range byID {
			toRun = append(toRun, labelcatalog.Entry{ID: id, Prompt: prompt})
		}
	} else if len(req.IDs) > 0 {
		for _, id := range req.IDs {
			if p, ok := byID[id]; ok {
				toRun = append(toRun, labelcatalog.Entry{ID: id, Prompt: p})
			}
		}
	} else {
		return respondErr(c, apierr.BadRequest("either `all: true` or a non-empty `ids` array is required"))
	}
	if len(toRun) == 0 {
		return respondErr(c, apierr.BadRequest("nothing to regenerate — verify `ids` match the `entries` you sent"))
	}

	// The DELETE step is fast + needs to happen synchronously so a follow-up
	// GET /admin/label-images shows the correct empty count while regen runs.
	// Batched now (single query instead of len(toRun) round trips — was tripping
	// the N+1 detector at ~70 rows per click).
	reqCtx := c.Request().Context()
	if req.All && req.Truncate {
		if err := h.DB.TruncateLabelImages(reqCtx); err != nil {
			return h.internalErr(c, "label images truncate failed", err)
		}
	} else {
		ids := make([]string, len(toRun))
		for i, e := range toRun {
			ids[i] = e.ID
		}
		if err := h.DB.DeleteLabelImages(reqCtx, ids); err != nil {
			return h.internalErr(c, "label images batch delete failed", err)
		}
	}

	// Async generation. Chroma-HD / SDXL Illustrious take ~20-30s per image;
	// a full 68-label regenerate is ~30+ minutes. No HTTP client / reverse
	// proxy holds a connection open that long — the earlier synchronous
	// version got context-canceled at 125s and dropped a 500 back to the FE
	// even though prior images had saved fine. Detach from the request
	// context so shutting the browser tab doesn't kill the run.
	//
	// FE polls GET /admin/label-images (count field) to observe progress.
	// Server logs stream per-label status via labelimages worker at INFO.
	bgCtx := context.Background()
	go func() {
		gen, failed, err := h.LabelImagesWorker.RegenerateList(bgCtx, toRun)
		if err != nil {
			h.Logger.Error("label images regenerate background run failed",
				"err", err, "requested", len(toRun), "generated", gen, "failed", failed)
			return
		}
		h.Logger.Info("label images regenerate background run complete",
			"requested", len(toRun), "generated", gen, "failed", failed)
	}()

	return c.JSON(http.StatusAccepted, map[string]any{
		"queued":    len(toRun),
		"async":     true,
		"note":      "generation runs in background; poll GET /api/v1/admin/label-images (count) or watch server logs (labelimages: generating / saved)",
	})
}
