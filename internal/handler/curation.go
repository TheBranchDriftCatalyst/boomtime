package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
)

// curationRequest is the POST body for creating a rule.
type curationRequest struct {
	Axis       string  `json:"axis"`
	Action     string  `json:"action"`
	MatchType  string  `json:"matchType"` // "exact" (default) | "regex"
	MatchValue string  `json:"matchValue"`
	NewValue   *string `json:"newValue"`
}

// ListCuration: GET /api/v1/users/current/curation → {rules:[CurationRule]}.
func (h *Handler) ListCuration(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	rules, err := h.DB.ListCurationRules(c.Request().Context(), owner)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, map[string]any{"rules": rules})
}

// CreateCuration: POST /api/v1/users/current/curation → {rule:CurationRule}.
// Validates axis (whitelist) + action, creates the rule, and applies it
// immediately (rename → backfill + rollup rebuild; hide → store only).
func (h *Handler) CreateCuration(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	var req curationRequest
	// gaka-bi2: 64 KiB cap — curation rules are compact JSON (axis, action,
	// matchType, matchValue, optional newValue); pattern strings should never
	// approach this bound.
	if aerr := BindJSONWithLimit(c, &req, BodyLimitMedium); aerr != nil {
		return respondErr(c, aerr)
	}

	// axis must be in the Heartbeats Explorer whitelist.
	if _, ok := db.ExploreColumn(req.Axis); !ok {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Unknown axis: "+req.Axis, nil))
	}
	if req.Action != db.CurationHide && req.Action != db.CurationRename {
		return respondErr(c, apierr.New(http.StatusBadRequest, "action must be 'hide' or 'rename'", nil))
	}
	if req.MatchValue == "" {
		return respondErr(c, apierr.New(http.StatusBadRequest, "matchValue is required", nil))
	}
	matchType := req.MatchType
	if matchType == "" {
		matchType = db.MatchExact
	}
	if matchType != db.MatchExact && matchType != db.MatchRegex && matchType != db.MatchTemplate {
		return respondErr(c, apierr.New(http.StatusBadRequest, "matchType must be 'exact', 'regex', or 'template'", nil))
	}
	// A template rule's target is a capture-group replacement template — it only
	// makes sense for rename (hide has no target).
	if matchType == db.MatchTemplate && req.Action != db.CurationRename {
		return respondErr(c, apierr.New(http.StatusBadRequest, "matchType 'template' is only valid for a rename rule", nil))
	}
	newValue := req.NewValue
	if req.Action == db.CurationRename {
		if newValue == nil || *newValue == "" {
			return respondErr(c, apierr.New(http.StatusBadRequest, "newValue is required for a rename rule", nil))
		}
		if req.Axis == "day" {
			return respondErr(c, apierr.New(http.StatusBadRequest, "the day axis cannot be renamed", nil))
		}
		// Accept both Postgres `\1` and shell-style `$1` backrefs in a template;
		// normalize `$N` -> `\N` before storing/using so either works.
		if matchType == db.MatchTemplate {
			normalized := db.NormalizeTemplate(*newValue)
			newValue = &normalized
		}
	}

	ctx := c.Request().Context()
	// For a regex rule, validate the pattern compiles (Postgres regex) up front.
	if matchType == db.MatchRegex {
		if err := h.DB.ValidateRegex(ctx, req.MatchValue); err != nil {
			return respondErr(c, apierr.New(http.StatusBadRequest, "invalid regex pattern", nil))
		}
	}
	// For a template rule, validate the pattern compiles AND the template is a
	// valid regexp_replace replacement (guards bad backrefs like `\9`).
	if matchType == db.MatchTemplate {
		if err := h.DB.ValidateTemplate(ctx, req.MatchValue, *newValue); err != nil {
			return respondErr(c, apierr.New(http.StatusBadRequest, "invalid template rename", nil))
		}
	}

	// Both hide and rename are stored as rules and applied at QUERY TIME — creating
	// the rule mutates no raw data. Rename is a non-destructive, reversible remap:
	// heartbeats keep their original values and dashboards show the merged value.
	rule, err := h.DB.CreateCurationRule(ctx, owner, req.Axis, req.Action, matchType, req.MatchValue, newValue)
	if err != nil {
		h.Logger.Error("create curation rule failed", "err", err)
		return respondErr(c, apierr.Generic())
	}

	// Both hide and rename change what dashboards show → drop this user's cached
	// aggregations so the new rule takes effect immediately.
	h.invalidateOwnerCache(owner)

	return c.JSON(http.StatusOK, map[string]any{"rule": rule})
}

// DeleteCuration: DELETE /api/v1/users/current/curation/:id → 204.
// Both hide and rename are query-time and fully reversible: deleting a hide rule
// un-hides, and deleting a rename rule instantly reverts the dashboards to the
// raw (un-merged) values (raw records were never mutated).
func (h *Handler) DeleteCuration(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid rule id", nil))
	}
	n, err := h.DB.DeleteCurationRule(c.Request().Context(), owner, id)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if n == 0 {
		return respondErr(c, apierr.New(http.StatusNotFound, "Curation rule not found", nil))
	}
	h.invalidateOwnerCache(owner)
	return noContent(c)
}

// toggleCurationRequest is the optional POST body for the toggle endpoint.
// When Enabled is nil the current value is flipped; when non-nil the exact
// value is written (idempotent — no-op if already at the requested value).
type toggleCurationRequest struct {
	Enabled *bool `json:"enabled"`
}

// ToggleCuration: POST /api/v1/users/current/curation/:id/toggle → {enabled:bool}.
// gaka-dfd. Pauses / resumes a curation rule without deleting it. Owner-
// scoped. Body is optional: omit to flip, or pass {"enabled":true|false} to
// set an exact state. Both flip and set are idempotent — sending the same
// state twice still returns 200 with the current value.
//
// A disabled rule stays in ListCurationRules (so the UI can surface it) but
// is filtered out of LoadHiddenSets / LoadRenameSets — its effect is
// paused. Apply and Purge reject disabled rules with 400 (see below).
func (h *Handler) ToggleCuration(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid rule id", nil))
	}
	// Body is optional — an empty POST flips. When present it must be tiny
	// (a single boolean); reuse the small body cap.
	var req toggleCurationRequest
	if c.Request().ContentLength > 0 {
		if aerr := BindJSONWithLimit(c, &req, BodyLimitSmall); aerr != nil {
			return respondErr(c, aerr)
		}
	}
	ctx := c.Request().Context()
	var newEnabled bool
	var found bool
	if req.Enabled != nil {
		found, err = h.DB.SetCurationRuleEnabled(ctx, owner, id, *req.Enabled)
		newEnabled = *req.Enabled
	} else {
		newEnabled, found, err = h.DB.ToggleCurationRule(ctx, owner, id)
	}
	if err != nil {
		h.Logger.Error("toggle curation rule failed", "err", err, "ruleId", id)
		return respondErr(c, apierr.Generic())
	}
	if !found {
		return respondErr(c, apierr.New(http.StatusNotFound, "Curation rule not found", nil))
	}
	// Enabling/disabling a rule changes what dashboards render → drop the
	// owner's cached aggregations so the next fetch reflects the new state.
	h.invalidateOwnerCache(owner)
	return c.JSON(http.StatusOK, map[string]any{"enabled": newEnabled})
}

// CurationAffected: GET /api/v1/users/current/curation/:id/affected →
// {values:[{value,count}], truncated}. The DISTINCT RAW values (with heartbeat
// counts) a rule matches on its axis — the one literal for an exact rule, every
// matching value for a regex rule. Owner-scoped, UNFILTERED (audit).
func (h *Handler) CurationAffected(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid rule id", nil))
	}
	ctx := c.Request().Context()

	rule, ruleOwner, err := h.DB.GetCurationRule(ctx, id)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if rule == nil || ruleOwner != owner {
		return respondErr(c, apierr.New(http.StatusNotFound, "Curation rule not found", nil))
	}

	values, truncated, err := h.DB.CurationAffectedValues(ctx, owner, rule, 200)
	if err != nil {
		h.Logger.Error("curation affected values failed", "err", err)
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, map[string]any{"values": values, "truncated": truncated})
}

// applyPreviewRowsCap is the max number of before/after rows returned by the
// apply preview. The DB touches all rows on apply — this is only the modal
// display cap (matches the frontend "and N more…" footer).
const applyPreviewRowsCap = 100

// resolveCurationRule fetches the rule and enforces owner scoping. Returns a
// 404-wrapped apierr when the rule is missing OR owned by someone else (the
// two are indistinguishable to the caller — deliberate: never leak that
// another user's rule exists). Shared by every destructive-action handler.
func (h *Handler) resolveCurationRule(c *echo.Context, ctx context.Context, owner string, id int) (*db.CurationRule, *apierr.Error) {
	rule, ruleOwner, err := h.DB.GetCurationRule(ctx, id)
	if err != nil {
		return nil, apierr.Generic()
	}
	if rule == nil || ruleOwner != owner {
		return nil, apierr.New(http.StatusNotFound, "Curation rule not found", nil)
	}
	return rule, nil
}

// ApplyRenamePreview: GET /api/v1/users/current/curation/:id/preview.
// Dispatches on rule.action — a rename rule preview returns the apply-shaped
// payload (UPDATE + rule-delete SQL, before/after per row), a hide rule
// preview returns the purge-shaped payload (DELETE heartbeats + rule-delete
// SQL, per-row "will be deleted" info). Owner-scoped; no data is mutated.
// One preview endpoint, two payload shapes — the FE modal dispatches on the
// same `action` discriminator to render the appropriate UI.
func (h *Handler) ApplyRenamePreview(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid rule id", nil))
	}
	ctx := c.Request().Context()

	rule, aerr := h.resolveCurationRule(c, ctx, owner, id)
	if aerr != nil {
		return respondErr(c, aerr)
	}

	// Rule shape returned on both branches — kept identical so the FE
	// discriminator (`action`) is the only per-variant switch it needs.
	ruleOut := map[string]any{
		"id":         rule.ID,
		"axis":       rule.Axis,
		"action":     rule.Action,
		"matchType":  rule.MatchType,
		"matchValue": rule.MatchValue,
		"newValue":   rule.NewValue,
	}

	switch rule.Action {
	case db.CurationRename:
		updSQL, delSQL, diff, total, perr := h.DB.ApplyRenamePreview(ctx, owner, rule, applyPreviewRowsCap)
		if perr != nil {
			h.Logger.Error("apply-rename preview failed", "err", perr, "ruleId", id)
			return respondErr(c, apierr.New(http.StatusBadRequest, perr.Error(), nil))
		}
		return c.JSON(http.StatusOK, map[string]any{
			"action":        "rename",
			"sqlPlanned":    updSQL + ";\n" + delSQL + ";",
			"sqlUpdate":     updSQL,
			"sqlDelete":     delSQL,
			"affectedRows":  diff,
			"totalAffected": total,
			"rowsShown":     len(diff),
			"rule":          ruleOut,
		})
	case db.CurationHide:
		delRowsSQL, delRuleSQL, diff, total, perr := h.DB.PurgeHiddenPreview(ctx, owner, rule, applyPreviewRowsCap)
		if perr != nil {
			h.Logger.Error("purge-hidden preview failed", "err", perr, "ruleId", id)
			return respondErr(c, apierr.New(http.StatusBadRequest, perr.Error(), nil))
		}
		return c.JSON(http.StatusOK, map[string]any{
			"action":        "hide",
			"sqlPlanned":    delRowsSQL + ";\n" + delRuleSQL + ";",
			"sqlDeleteRows": delRowsSQL,
			"sqlDeleteRule": delRuleSQL,
			"affectedRows":  diff,
			"totalAffected": total,
			"rowsShown":     len(diff),
			"rule":          ruleOut,
		})
	default:
		return respondErr(c, apierr.New(http.StatusBadRequest, "unknown rule action: "+rule.Action, nil))
	}
}

// ApplyRename: POST /api/v1/users/current/curation/:id/apply →
// {rowsAffected:int, sqlRun:string}. DESTRUCTIVELY rewrites every heartbeat
// row that the rename rule matches on the target column, then removes the
// rule row itself, ATOMICALLY in one transaction. Owner-scoped. Rejects
// non-rename rules with 400 (hide rules go through /purge instead — a
// destructive-delete UX is scarier and gets its own path).
//
// Idempotent-in-effect: if the mapping is already applied and 0 rows match,
// still succeeds with rowsAffected=0 and the rule row is still removed.
func (h *Handler) ApplyRename(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid rule id", nil))
	}
	ctx := c.Request().Context()

	rule, aerr := h.resolveCurationRule(c, ctx, owner, id)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	if rule.Action != db.CurationRename {
		return respondErr(c, apierr.New(http.StatusBadRequest, "only rename rules can be applied", nil))
	}
	// gaka-dfd: refuse to run a destructive action against a paused rule —
	// applying-a-rule-you-just-paused is confusing and probably a mistake.
	// The user should re-enable, verify it still matches what they expect,
	// and then apply.
	if !rule.Enabled {
		return respondErr(c, apierr.New(http.StatusBadRequest, "cannot apply a disabled rule; enable it first", nil))
	}

	rows, sqlUpd, sqlDel, err := h.DB.ApplyRenameRule(ctx, owner, rule)
	if err != nil {
		h.Logger.Error("apply-rename failed", "err", err, "ruleId", id)
		return respondErr(c, apierr.Generic())
	}

	// The apply mutated raw heartbeats and removed a rule → dashboards, the
	// explorer, and per-axis values all change. Drop the owner's cached
	// aggregations so the next fetch is fresh.
	h.invalidateOwnerCache(owner)

	return c.JSON(http.StatusOK, map[string]any{
		"rowsAffected": rows,
		"sqlRun":       sqlUpd + ";\n" + sqlDel + ";",
		"sqlUpdate":    sqlUpd,
		"sqlDelete":    sqlDel,
	})
}

// PurgeHidden: POST /api/v1/users/current/curation/:id/purge →
// {rowsAffected:int, sqlRun:string, sqlDeleteRows:string, sqlDeleteRule:string}.
// DESTRUCTIVELY deletes every heartbeat row a HIDE rule matches, then removes
// the rule itself, ATOMICALLY in one transaction. Owner-scoped. Rejects
// non-hide rules with 400 — rename rules go through /apply, which preserves
// raw data under a rewrite (this endpoint destroys raw data). Idempotent-in-
// effect: 0 matches still deletes the rule and returns rowsAffected=0.
//
// This is the scariest endpoint in the curation family — the FE modal MUST
// gate it behind a "type rule id N to confirm" input, and the icon in the
// row list gets a redder destructive tint than /apply's Zap.
func (h *Handler) PurgeHidden(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid rule id", nil))
	}
	ctx := c.Request().Context()

	rule, aerr := h.resolveCurationRule(c, ctx, owner, id)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	if rule.Action != db.CurationHide {
		return respondErr(c, apierr.New(http.StatusBadRequest, "only hide rules can be purged", nil))
	}
	// gaka-dfd: refuse to purge against a paused rule — the same reasoning
	// as the apply guard, and purge is the more dangerous of the two.
	if !rule.Enabled {
		return respondErr(c, apierr.New(http.StatusBadRequest, "cannot purge a disabled rule; enable it first", nil))
	}

	rows, sqlDelRows, sqlDelRule, err := h.DB.PurgeHiddenRule(ctx, owner, rule)
	if err != nil {
		h.Logger.Error("purge-hidden failed", "err", err, "ruleId", id)
		return respondErr(c, apierr.Generic())
	}

	// Purge deleted raw heartbeats + removed a rule → dashboards, the
	// explorer, and per-axis values all change. Drop the owner's cached
	// aggregations so the next fetch is fresh.
	h.invalidateOwnerCache(owner)

	return c.JSON(http.StatusOK, map[string]any{
		"rowsAffected":  rows,
		"sqlRun":        sqlDelRows + ";\n" + sqlDelRule + ";",
		"sqlDeleteRows": sqlDelRows,
		"sqlDeleteRule": sqlDelRule,
	})
}
