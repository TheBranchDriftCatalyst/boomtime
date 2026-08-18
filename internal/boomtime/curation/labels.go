// labels.go — HTTP surface for the DB-backed labels catalog (gaka-364.3).
//
// Split cleanly by auth model:
//
//   - PUBLIC:  GET /api/v1/labels/catalog
//     Returns {systemPrompt, labels: [...]}. Consumed by the FE
//     useLabelsCatalog hook on every mount that needs to render awards
//     (public profile, hero widget, showcase widget, admin table).
//     No auth — the catalog isn't per-user, no secret data leaks (the
//     systemPrompt IS a global authored prompt fragment, not a token or
//     secret; if the operator wants to hide it, they simply don't set
//     one). Cached client-side via staleTime 60s.
//
//   - ADMIN-GATED (requireAdmin): create/update/delete labels + edit the
//     singleton gen-config. Same admin allowlist as the label-images regen
//     endpoints (BOOM_ADMIN_USERS). Non-admins get 403.
//
//   - ADMIN utility: GET /api/v1/admin/labels/seed.sql
//     Dumps the current DB state as a `-- +goose Up` SQL body suitable
//     for committing back as a fresh migration (so a hand-tuned catalog
//     on prod can be captured as code for a fresh install to replay).
//     Not intended for the everyday flow — the admin CRUD is the
//     everyday flow — but useful for backporting operator edits into a
//     reviewable diff.
package curation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/labels"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/labstack/echo/v5"
)

// LabelsCatalog: GET /api/v1/labels/catalog (PUBLIC).
// Response: {systemPrompt: string, labels: [Label]}. The FE's evaluator
// consumes labels; the admin editor also uses systemPrompt for the
// "preview effective prompt" hint.
func (h *Handler) LabelsCatalog(c *echo.Context) error {
	ctx := c.Request().Context()
	labels, err := h.DB.ListLabels(ctx)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "labels list failed", err)
	}
	systemPrompt, err := h.DB.GetGenConfig(ctx)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "label gen-config load failed", err)
	}
	// Short TTL — the catalog changes rarely but when the admin does edit
	// a row we want the change visible within a minute.
	c.Response().Header().Set("Cache-Control", "public, max-age=60")
	return c.JSON(http.StatusOK, map[string]any{
		"systemPrompt": systemPrompt,
		"labels":       labels,
	})
}

// labelBody is the request shape for PATCH/POST admin/labels. Fields default
// to their existing DB values on PATCH — a partial body only overwrites what
// it names. `Condition` is opaque JSONB (validated by the FE evaluator on
// load); we require it to be non-empty on POST but leave it optional on
// PATCH.
type labelBody struct {
	ID              *string         `json:"id,omitempty"`
	Kind            *string         `json:"kind,omitempty"`
	Label           *string         `json:"label,omitempty"`
	Glyph           *string         `json:"glyph,omitempty"`
	Description     *string         `json:"description,omitempty"`
	OptimizedPrompt *string         `json:"optimizedPrompt,omitempty"`
	Rank            *int            `json:"rank,omitempty"`
	Tier            *string         `json:"tier,omitempty"`
	Condition       json.RawMessage `json:"condition,omitempty"`
}

// applyLabelBody merges the request body onto `into`. Nil pointers = "not
// sent, keep existing"; non-nil = overwrite (even with empty string, so
// clearing a glyph via `glyph: ""` works).
func applyLabelBody(into *db.Label, body *labelBody) {
	if body.Kind != nil {
		into.Kind = *body.Kind
	}
	if body.Label != nil {
		into.Label = *body.Label
	}
	if body.Glyph != nil {
		into.Glyph = *body.Glyph
	}
	if body.Description != nil {
		into.Description = *body.Description
	}
	if body.OptimizedPrompt != nil {
		into.OptimizedPrompt = *body.OptimizedPrompt
	}
	if body.Rank != nil {
		into.Rank = *body.Rank
	}
	if body.Tier != nil {
		into.Tier = *body.Tier
	}
	if len(body.Condition) > 0 {
		into.Condition = body.Condition
	}
}

// AdminCreateLabel: POST /api/v1/admin/labels.
// Body: full label shape (id required, kind required, label required,
// condition required). 409-shaped 400 if id already exists (upsert would
// silently overwrite — we want create-vs-update to be intentional here so
// the admin doesn't blast a live label by accident).
func (h *Handler) AdminCreateLabel(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	var body labelBody
	if aerr := apihelpers.BindJSONWithLimit(c, &body, apihelpers.BodyLimitMedium); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if body.ID == nil || strings.TrimSpace(*body.ID) == "" {
		return apihelpers.RespondErr(c, apierr.BadRequest("`id` is required"))
	}
	if body.Kind == nil || strings.TrimSpace(*body.Kind) == "" {
		return apihelpers.RespondErr(c, apierr.BadRequest("`kind` is required"))
	}
	if body.Label == nil || strings.TrimSpace(*body.Label) == "" {
		return apihelpers.RespondErr(c, apierr.BadRequest("`label` is required"))
	}
	if len(body.Condition) == 0 {
		return apihelpers.RespondErr(c, apierr.BadRequest("`condition` JSONB is required"))
	}
	// gaka-6uf: schema-validate the condition BEFORE the DB write. Without
	// this, malformed conditions (bad op, missing required field, out-of-
	// range enum) sit in the DB until evaluator load and either always- or
	// never-fire silently. The rich JSON-pointer path helps the FE surface
	// the offending field inline.
	if err := labels.ValidateCondition(body.Condition); err != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("condition: "+err.Error()))
	}
	// Fail loud if the id already exists — the admin should hit PATCH, not
	// POST-that-silently-overwrites.
	existing, err := h.DB.GetLabel(c.Request().Context(), *body.ID)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "labels lookup failed", err)
	}
	if existing != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest(fmt.Sprintf("label %q already exists — use PATCH to update", *body.ID)))
	}

	l := db.Label{ID: *body.ID}
	applyLabelBody(&l, &body)
	if err := h.DB.UpsertLabel(c.Request().Context(), l); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "label upsert failed", err)
	}
	created, err := h.DB.GetLabel(c.Request().Context(), l.ID)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "post-create fetch failed", err)
	}
	return c.JSON(http.StatusCreated, created)
}

// AdminUpdateLabel: PATCH /api/v1/admin/labels/:id.
// Partial body — only fields present in the JSON are overwritten. 404 if
// the id doesn't exist (admin should use POST to create).
func (h *Handler) AdminUpdateLabel(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	id := c.Param("id")
	if id == "" {
		return apihelpers.RespondErr(c, apierr.BadRequest("missing label id in URL"))
	}
	var body labelBody
	if aerr := apihelpers.BindJSONWithLimit(c, &body, apihelpers.BodyLimitMedium); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	existing, err := h.DB.GetLabel(c.Request().Context(), id)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "labels lookup failed", err)
	}
	if existing == nil {
		return apihelpers.RespondErr(c, apierr.NotFound("label not found"))
	}
	// gaka-6uf: when the PATCH body includes a condition, schema-validate it
	// before the write. Partial-PATCH-omit-condition still allowed (nil = no
	// change). Same rich JSON-pointer path surfaces on the FE.
	if len(body.Condition) > 0 {
		if err := labels.ValidateCondition(body.Condition); err != nil {
			return apihelpers.RespondErr(c, apierr.BadRequest("condition: "+err.Error()))
		}
	}
	// Never allow id-rename via PATCH — an id change breaks label_images
	// FKs + persisted award history. If the operator needs to rename,
	// they can DELETE + POST.
	body.ID = nil
	applyLabelBody(existing, &body)
	if err := h.DB.UpsertLabel(c.Request().Context(), *existing); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "label upsert failed", err)
	}
	fresh, err := h.DB.GetLabel(c.Request().Context(), id)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "post-update fetch failed", err)
	}
	return c.JSON(http.StatusOK, fresh)
}

// AdminDeleteLabel: DELETE /api/v1/admin/labels/:id.
// Idempotent — 204 whether or not the row was there. The corresponding
// label_images row (if any) is also removed so stale bytes don't
// out-live their catalog entry.
func (h *Handler) AdminDeleteLabel(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	id := c.Param("id")
	if id == "" {
		return apihelpers.RespondErr(c, apierr.BadRequest("missing label id in URL"))
	}
	ctx := c.Request().Context()
	if err := h.DB.DeleteLabel(ctx, id); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "label delete failed", err)
	}
	// Best-effort: cascade the image row. A failure here doesn't block
	// the DELETE — the catalog row is already gone and orphan image bytes
	// are harmless (nothing renders them without a matching label).
	if err := h.DB.DeleteLabelImage(ctx, id); err != nil {
		h.Logger.Warn("label image cascade delete failed",
			"id", id, "err", err)
	}
	return c.NoContent(http.StatusNoContent)
}

// genConfigBody is the request shape for PATCH admin/label-gen-config.
type genConfigBody struct {
	SystemPrompt *string `json:"systemPrompt,omitempty"`
}

// AdminUpdateLabelGenConfig: PATCH /api/v1/admin/label-gen-config.
// Body: {systemPrompt: string}. Empty string clears the prompt — the
// worker treats "" as "no prefix" and sends only the per-label
// optimizedPrompt to comfyui.
func (h *Handler) AdminUpdateLabelGenConfig(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	var body genConfigBody
	if aerr := apihelpers.BindJSONWithLimit(c, &body, apihelpers.BodyLimitMedium); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if body.SystemPrompt == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("`systemPrompt` is required"))
	}
	if err := h.DB.SetGenConfig(c.Request().Context(), *body.SystemPrompt); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "label gen-config update failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"systemPrompt": *body.SystemPrompt,
	})
}

// AdminLabelsSeedSQL: GET /api/v1/admin/labels/seed.sql.
// Dumps the current DB state as a `-- +goose Up` SQL body — an operator
// can drop the response into internal/db/migrations/00NNN_labels_seed.sql
// as a fresh migration so hand-tuned edits on prod get captured as
// reviewable code for fresh installs.
//
// Response is text/plain so the browser prompts a download when the URL
// is hit directly.
func (h *Handler) AdminLabelsSeedSQL(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	ctx := c.Request().Context()
	labels, err := h.DB.ListLabels(ctx)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "labels list failed", err)
	}
	systemPrompt, err := h.DB.GetGenConfig(ctx)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "gen config load failed", err)
	}
	var sb strings.Builder
	sb.WriteString("-- +goose Up\n-- +goose StatementBegin\n\n")
	sb.WriteString("-- Regenerated from live DB via GET /api/v1/admin/labels/seed.sql.\n")
	sb.WriteString(fmt.Sprintf("-- %d labels + 1 gen-config row.\n\n", len(labels)))
	sb.WriteString("INSERT INTO labels (id, kind, label, glyph, description, optimized_prompt, rank, tier, condition) VALUES\n")
	for i, l := range labels {
		cond := string(l.Condition)
		if cond == "" {
			cond = "{}"
		}
		fmt.Fprintf(&sb,
			"  (%s, %s, %s, %s, %s, %s, %d, %s, %s::jsonb)",
			sqlStr(l.ID),
			sqlStr(l.Kind),
			sqlStr(l.Label),
			sqlStrOrNull(l.Glyph),
			sqlStrOrNull(l.Description),
			sqlStrOrNull(l.OptimizedPrompt),
			l.Rank,
			sqlStrOrNull(l.Tier),
			sqlStr(cond),
		)
		if i < len(labels)-1 {
			sb.WriteString(",\n")
		} else {
			sb.WriteString("\n")
		}
	}
	sb.WriteString("ON CONFLICT (id) DO UPDATE SET\n")
	sb.WriteString("  kind = EXCLUDED.kind, label = EXCLUDED.label, glyph = EXCLUDED.glyph,\n")
	sb.WriteString("  description = EXCLUDED.description, optimized_prompt = EXCLUDED.optimized_prompt,\n")
	sb.WriteString("  rank = EXCLUDED.rank, tier = EXCLUDED.tier, condition = EXCLUDED.condition,\n")
	sb.WriteString("  updated_at = now();\n\n")
	fmt.Fprintf(&sb,
		"UPDATE label_gen_config SET system_prompt = %s, updated_at = now() WHERE singleton = true;\n",
		sqlStr(systemPrompt))
	sb.WriteString("\n-- +goose StatementEnd\n")

	c.Response().Header().Set("Content-Disposition", `attachment; filename="labels_seed.sql"`)
	return c.Blob(http.StatusOK, "text/plain; charset=utf-8", []byte(sb.String()))
}

// sqlStr renders 'x' with ” escaping. Never returns NULL — caller uses
// sqlStrOrNull for nullable columns.
func sqlStr(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// sqlStrOrNull renders 'x' or NULL when the input is empty. Mirrors the
// migration seed's NULLIF pattern so the dump round-trips cleanly.
func sqlStrOrNull(s string) string {
	if s == "" {
		return "NULL"
	}
	return sqlStr(s)
}
