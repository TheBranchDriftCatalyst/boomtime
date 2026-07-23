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
