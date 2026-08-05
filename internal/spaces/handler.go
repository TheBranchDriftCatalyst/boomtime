// Package spaces owns the spaces + dashboard-layout HTTP surface. Extracted
// from the god-type handler.Handler as part of gaka-8tn phase 2a so a domain
// (spaces) owns its handler struct + routes + tests as one folder.
//
// A Space is a named, user-scoped filter (see internal/db/spaces.go for the
// membership-rule query engine — those types stay on *db.DB because they are
// used by every aggregation package in internal/db/). This package holds only
// the HTTP-facing CRUD + preview endpoints; the SQL logic remains in
// internal/db/ under the same *db.DB methods it always used.
//
// Dashboard layouts (per-user JSONB placement metadata) share this package
// because they render inside a Space and rise/fall on the same auth/session
// context. Both features register together via Register(e, h).
package spaces

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
)

// Handler bundles the SUBSET of the god-type handler.Handler's dependencies
// that the spaces domain actually reads. Everything else stays out of this
// package.
//
//   - DB     — spaces / space_rules / dashboard_layouts CRUD + preview
//   - Logger — internal log target for create/preview error paths
//   - Cache  — invalidated on every write so dashboards see rule/name changes
//     immediately (owner-prefixed keys; see apihelpers.InvalidateOwnerCache)
type Handler struct {
	DB     *db.DB
	Logger *slog.Logger
	Cache  *cache.TTL
}

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
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	spaces, err := h.DB.ListSpaces(c.Request().Context(), owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "list spaces failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{"spaces": spaces})
}

// CreateSpace: POST /api/v1/users/current/spaces body {"name":...} → {space:Space}.
func (h *Handler) CreateSpace(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	var req spaceRequest
	// gaka-bi2: 4 KiB cap — space create body is a single short name string.
	if aerr := apihelpers.BindJSONWithLimit(c, &req, apihelpers.BodyLimitSmall); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if req.Name == "" {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "name is required", nil))
	}
	space, err := h.DB.CreateSpace(c.Request().Context(), owner, req.Name)
	if err != nil {
		h.Logger.Error("create space failed", "err", err)
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "Could not create space (name may already exist)", nil))
	}
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
	return c.JSON(http.StatusOK, map[string]any{"space": space})
}

// UpdateSpace: PATCH /api/v1/users/current/spaces/:id body {"name"?,"position"?}.
func (h *Handler) UpdateSpace(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "Invalid space id", nil))
	}
	var req spacePatchRequest
	// gaka-bi2: 4 KiB cap — patch body is optional name + position int.
	if aerr := apihelpers.BindJSONWithLimit(c, &req, apihelpers.BodyLimitSmall); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	n, err := h.DB.RenameSpace(c.Request().Context(), owner, id, req.Name, req.Position)
	if err != nil {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "Could not update space", nil))
	}
	if n == 0 {
		return apihelpers.RespondErr(c, apierr.New(http.StatusNotFound, "Space not found", nil))
	}
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
	return apihelpers.NoContent(c)
}

// DeleteSpace: DELETE /api/v1/users/current/spaces/:id → 204.
func (h *Handler) DeleteSpace(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "Invalid space id", nil))
	}
	n, err := h.DB.DeleteSpace(c.Request().Context(), owner, id)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "delete space failed", err)
	}
	if n == 0 {
		return apihelpers.RespondErr(c, apierr.New(http.StatusNotFound, "Space not found", nil))
	}
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
	return apihelpers.NoContent(c)
}

// GetSpace: GET /api/v1/users/current/spaces/:id →
// {id,name,position,rules:[{id,axis,matchValue,matchType}]}.
func (h *Handler) GetSpace(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "Invalid space id", nil))
	}
	space, rules, err := h.DB.GetSpace(c.Request().Context(), owner, id)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "get space failed", err)
	}
	if space == nil {
		return apihelpers.RespondErr(c, apierr.New(http.StatusNotFound, "Space not found", nil))
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
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "Invalid space id", nil))
	}
	var req spaceRuleRequest
	// gaka-bi2: 64 KiB cap — rule adds carry an axis + matchValue (regex or
	// literal); Medium leaves headroom for template rules without allowing a
	// runaway body.
	if aerr := apihelpers.BindJSONWithLimit(c, &req, apihelpers.BodyLimitMedium); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	// Validate the axis whitelist up front for a clear 400 (AddSpaceRule also guards).
	if _, ok := db.ExploreColumn(req.Axis); !ok {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "Unknown axis: "+req.Axis, nil))
	}
	if req.MatchValue == "" {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "matchValue is required", nil))
	}
	matchType := req.MatchType
	if matchType == "" {
		matchType = db.MatchExact
	}
	if matchType != db.MatchExact && matchType != db.MatchRegex {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "matchType must be 'exact' or 'regex'", nil))
	}
	rule, err := h.DB.AddSpaceRule(c.Request().Context(), owner, id, req.Axis, req.MatchValue, matchType)
	if err != nil {
		// Fixed message only — the raw DB error (e.g. pg regex diagnostics) is
		// logged, never sent to the client.
		h.Logger.Error("add space rule failed", "err", err)
		return apihelpers.RespondErr(c, apierr.BadRequest("Could not add space rule (invalid pattern?)"))
	}
	if rule == nil {
		return apihelpers.RespondErr(c, apierr.New(http.StatusNotFound, "Space not found", nil))
	}
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
	return c.JSON(http.StatusOK, map[string]any{"rule": rule})
}

// DeleteSpaceRule: DELETE /api/v1/users/current/spaces/:id/rules/:rid → 204.
func (h *Handler) DeleteSpaceRule(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "Invalid space id", nil))
	}
	rid, err := strconv.Atoi(c.Param("rid"))
	if err != nil {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "Invalid rule id", nil))
	}
	n, err := h.DB.DeleteSpaceRule(c.Request().Context(), owner, id, rid)
	if err != nil {
		return apihelpers.RespondErr(c, apierr.Generic())
	}
	if n == 0 {
		return apihelpers.RespondErr(c, apierr.New(http.StatusNotFound, "Space rule not found", nil))
	}
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
	return apihelpers.NoContent(c)
}

// SpacePreview: GET /api/v1/users/current/spaces/preview?axis=&matchValue=&matchType=
// → {"values":[{"value","count"}],"truncated":bool}. Live preview of the RAW values
// (with heartbeat counts) an unsaved membership rule would match. Owner-scoped.
func (h *Handler) SpacePreview(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	axis := c.QueryParam("axis")
	matchValue := c.QueryParam("matchValue")
	matchType := c.QueryParam("matchType")
	if _, ok := db.ExploreColumn(axis); !ok {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "Unknown axis: "+axis, nil))
	}
	if matchValue == "" {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "matchValue is required", nil))
	}
	if matchType == "" {
		matchType = db.MatchExact
	}
	if matchType != db.MatchExact && matchType != db.MatchRegex {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "matchType must be 'exact' or 'regex'", nil))
	}
	ctx := c.Request().Context()
	if matchType == db.MatchRegex {
		if err := h.DB.ValidateRegex(ctx, matchValue); err != nil {
			return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "invalid regex pattern", nil))
		}
	}
	values, truncated, err := h.DB.SpacePreviewValues(ctx, owner, axis, matchValue, matchType, 200)
	if err != nil {
		h.Logger.Error("space preview failed", "err", err)
		return apihelpers.RespondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, map[string]any{"values": values, "truncated": truncated})
}
