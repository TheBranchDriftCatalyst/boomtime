package handler

import (
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

// ApplyRenamePreview: GET /api/v1/users/current/remappings/:id/preview →
// {sqlPlanned:string, affectedRows:[{id,before,after}], totalAffected:int}.
// Returns the exact SQL that a destructive apply would run PLUS a capped
// diff of every heartbeat row that would be rewritten. Owner-scoped; no
// data is mutated. Feeds the frontend confirm modal.
//
// The endpoint lives under /remappings/ (not /curation/) because the concept
// belongs to the remappings sub-domain — only rename rules are apply-able,
// and the frontend surface is the Remappings tab.
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

	rule, ruleOwner, err := h.DB.GetCurationRule(ctx, id)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	// Owner scoping: never leak that another user's rule exists.
	if rule == nil || ruleOwner != owner {
		return respondErr(c, apierr.New(http.StatusNotFound, "Remapping not found", nil))
	}
	if rule.Action != db.CurationRename {
		return respondErr(c, apierr.New(http.StatusBadRequest, "only rename rules can be applied", nil))
	}

	updSQL, delSQL, diff, total, err := h.DB.ApplyRenamePreview(ctx, owner, rule, applyPreviewRowsCap)
	if err != nil {
		h.Logger.Error("apply-rename preview failed", "err", err, "ruleId", id)
		return respondErr(c, apierr.New(http.StatusBadRequest, err.Error(), nil))
	}

	return c.JSON(http.StatusOK, map[string]any{
		"sqlPlanned":    updSQL + ";\n" + delSQL + ";",
		"sqlUpdate":     updSQL,
		"sqlDelete":     delSQL,
		"affectedRows":  diff,
		"totalAffected": total,
		"rowsShown":     len(diff),
		"rule": map[string]any{
			"id":         rule.ID,
			"axis":       rule.Axis,
			"matchType":  rule.MatchType,
			"matchValue": rule.MatchValue,
			"newValue":   rule.NewValue,
		},
	})
}

// ApplyRename: POST /api/v1/users/current/remappings/:id/apply →
// {rowsAffected:int, sqlRun:string}. DESTRUCTIVELY rewrites every heartbeat
// row that the mapping matches on the target column, then removes the mapping
// row itself, ATOMICALLY in one transaction. Owner-scoped.
//
// Idempotent-in-effect: if the mapping is already applied and 0 rows match,
// still succeeds with rowsAffected=0 and the mapping row is still removed.
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

	rule, ruleOwner, err := h.DB.GetCurationRule(ctx, id)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if rule == nil || ruleOwner != owner {
		return respondErr(c, apierr.New(http.StatusNotFound, "Remapping not found", nil))
	}
	if rule.Action != db.CurationRename {
		return respondErr(c, apierr.New(http.StatusBadRequest, "only rename rules can be applied", nil))
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
