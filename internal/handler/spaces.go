package handler

import (
	"net/http"
	"strconv"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
	"github.com/labstack/echo/v5"
)

// spaceRequest is the POST body for creating a Space.
type spaceRequest struct {
	Name string `json:"name"`
}

// spacePatchRequest is the PATCH body for renaming/reordering a Space.
type spacePatchRequest struct {
	Name     *string `json:"name"`
	Position *int    `json:"position"`
}

// spaceRuleRequest is the POST body for adding a membership rule.
type spaceRuleRequest struct {
	Axis       string `json:"axis"`
	MatchValue string `json:"matchValue"`
	MatchType  string `json:"matchType"` // "exact" (default) | "regex"
}

// ListSpaces: GET /api/v1/users/current/spaces → {spaces:[Space]}.
func (h *Handler) ListSpaces(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	spaces, err := h.DB.ListSpaces(c.Request().Context(), owner)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, map[string]any{"spaces": spaces})
}

// CreateSpace: POST /api/v1/users/current/spaces body {"name":...} → {space:Space}.
func (h *Handler) CreateSpace(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	var req spaceRequest
	if err := c.Bind(&req); err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid request body", nil))
	}
	if req.Name == "" {
		return respondErr(c, apierr.New(http.StatusBadRequest, "name is required", nil))
	}
	space, err := h.DB.CreateSpace(c.Request().Context(), owner, req.Name)
	if err != nil {
		h.Logger.Error("create space failed", "err", err)
		return respondErr(c, apierr.New(http.StatusBadRequest, "Could not create space (name may already exist)", nil))
	}
	h.invalidateOwnerCache(owner)
	return c.JSON(http.StatusOK, map[string]any{"space": space})
}

// UpdateSpace: PATCH /api/v1/users/current/spaces/:id body {"name"?,"position"?}.
func (h *Handler) UpdateSpace(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid space id", nil))
	}
	var req spacePatchRequest
	if err := c.Bind(&req); err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid request body", nil))
	}
	n, err := h.DB.RenameSpace(c.Request().Context(), owner, id, req.Name, req.Position)
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Could not update space", nil))
	}
	if n == 0 {
		return respondErr(c, apierr.New(http.StatusNotFound, "Space not found", nil))
	}
	h.invalidateOwnerCache(owner)
	return noContent(c)
}

// DeleteSpace: DELETE /api/v1/users/current/spaces/:id → 204.
func (h *Handler) DeleteSpace(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid space id", nil))
	}
	n, err := h.DB.DeleteSpace(c.Request().Context(), owner, id)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if n == 0 {
		return respondErr(c, apierr.New(http.StatusNotFound, "Space not found", nil))
	}
	h.invalidateOwnerCache(owner)
	return noContent(c)
}

// GetSpace: GET /api/v1/users/current/spaces/:id →
// {id,name,position,rules:[{id,axis,matchValue,matchType}]}.
func (h *Handler) GetSpace(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid space id", nil))
	}
	space, rules, err := h.DB.GetSpace(c.Request().Context(), owner, id)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if space == nil {
		return respondErr(c, apierr.New(http.StatusNotFound, "Space not found", nil))
	}
	return c.JSON(http.StatusOK, map[string]any{
		"id":       space.ID,
		"name":     space.Name,
		"position": space.Position,
		"rules":    rules,
	})
}

// AddSpaceRule: POST /api/v1/users/current/spaces/:id/rules
// body {"axis","matchValue","matchType"} → {rule:SpaceRule}.
func (h *Handler) AddSpaceRule(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid space id", nil))
	}
	var req spaceRuleRequest
	if err := c.Bind(&req); err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid request body", nil))
	}
	// Validate the axis whitelist up front for a clear 400 (AddSpaceRule also guards).
	if _, ok := db.ExploreColumn(req.Axis); !ok {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Unknown axis: "+req.Axis, nil))
	}
	if req.MatchValue == "" {
		return respondErr(c, apierr.New(http.StatusBadRequest, "matchValue is required", nil))
	}
	matchType := req.MatchType
	if matchType == "" {
		matchType = db.MatchExact
	}
	if matchType != db.MatchExact && matchType != db.MatchRegex {
		return respondErr(c, apierr.New(http.StatusBadRequest, "matchType must be 'exact' or 'regex'", nil))
	}
	rule, err := h.DB.AddSpaceRule(c.Request().Context(), owner, id, req.Axis, req.MatchValue, matchType)
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, err.Error(), nil))
	}
	if rule == nil {
		return respondErr(c, apierr.New(http.StatusNotFound, "Space not found", nil))
	}
	h.invalidateOwnerCache(owner)
	return c.JSON(http.StatusOK, map[string]any{"rule": rule})
}

// DeleteSpaceRule: DELETE /api/v1/users/current/spaces/:id/rules/:rid → 204.
func (h *Handler) DeleteSpaceRule(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid space id", nil))
	}
	rid, err := strconv.Atoi(c.Param("rid"))
	if err != nil {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Invalid rule id", nil))
	}
	n, err := h.DB.DeleteSpaceRule(c.Request().Context(), owner, id, rid)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if n == 0 {
		return respondErr(c, apierr.New(http.StatusNotFound, "Space rule not found", nil))
	}
	h.invalidateOwnerCache(owner)
	return noContent(c)
}

// SpacePreview: GET /api/v1/users/current/spaces/preview?axis=&matchValue=&matchType=
// → {"values":[{"value","count"}],"truncated":bool}. Live preview of the RAW values
// (with heartbeat counts) an unsaved membership rule would match. Owner-scoped.
func (h *Handler) SpacePreview(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	axis := c.QueryParam("axis")
	matchValue := c.QueryParam("matchValue")
	matchType := c.QueryParam("matchType")
	if _, ok := db.ExploreColumn(axis); !ok {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Unknown axis: "+axis, nil))
	}
	if matchValue == "" {
		return respondErr(c, apierr.New(http.StatusBadRequest, "matchValue is required", nil))
	}
	if matchType == "" {
		matchType = db.MatchExact
	}
	if matchType != db.MatchExact && matchType != db.MatchRegex {
		return respondErr(c, apierr.New(http.StatusBadRequest, "matchType must be 'exact' or 'regex'", nil))
	}
	ctx := c.Request().Context()
	if matchType == db.MatchRegex {
		if err := h.DB.ValidateRegex(ctx, matchValue); err != nil {
			return respondErr(c, apierr.New(http.StatusBadRequest, "invalid regex pattern", nil))
		}
	}
	values, truncated, err := h.DB.SpacePreviewValues(ctx, owner, axis, matchValue, matchType, 200)
	if err != nil {
		h.Logger.Error("space preview failed", "err", err)
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, map[string]any{"values": values, "truncated": truncated})
}
