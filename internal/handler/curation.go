package handler

import (
	"net/http"
	"strconv"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
	"github.com/labstack/echo/v5"
)

// curationRequest is the POST body for creating a rule.
type curationRequest struct {
	Axis       string  `json:"axis"`
	Action     string  `json:"action"`
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
	if err := c.Bind(&req); err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid request body", nil))
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
	if req.Action == db.CurationRename {
		if req.NewValue == nil || *req.NewValue == "" {
			return respondErr(c, apierr.New(http.StatusBadRequest, "newValue is required for a rename rule", nil))
		}
		if req.Axis == "day" {
			return respondErr(c, apierr.New(http.StatusBadRequest, "the day axis cannot be renamed", nil))
		}
	}

	ctx := c.Request().Context()
	// Both hide and rename are stored as rules and applied at QUERY TIME — creating
	// the rule mutates no raw data. Rename is a non-destructive, reversible remap:
	// heartbeats keep their original values and dashboards show the merged value.
	rule, err := h.DB.CreateCurationRule(ctx, owner, req.Axis, req.Action, req.MatchValue, req.NewValue)
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

// invalidateOwnerCache drops all cached aggregation payloads for a user so hide/
// rename changes take effect immediately.
func (h *Handler) invalidateOwnerCache(owner string) {
	if h.Cache != nil {
		h.Cache.InvalidatePrefix(owner + "|")
	}
}
