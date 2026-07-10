package handler

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
	"github.com/labstack/echo/v5"
)

// reservedExploreParams are query keys that are NOT equality filters.
var reservedExploreParams = map[string]struct{}{
	"groupBy": {}, "start": {}, "end": {}, "page": {}, "limit": {}, "entity": {},
}

// collectExploreFilters builds validated equality filters from repeated query
// params whose key is a whitelisted axis. A non-whitelisted key (excluding the
// reserved params) is a 400. A repeated axis uses its last value. An empty value
// is treated as an explicit IS NULL match.
func collectExploreFilters(c *echo.Context) ([]db.ExploreFilter, *apierr.Error) {
	var filters []db.ExploreFilter
	for key, vals := range c.QueryParams() {
		if _, reserved := reservedExploreParams[key]; reserved {
			continue
		}
		col, ok := db.ExploreColumn(key)
		if !ok {
			return nil, apierr.New(http.StatusBadRequest, "Unknown filter axis: "+key, nil)
		}
		if len(vals) == 0 {
			continue
		}
		v := vals[len(vals)-1]
		f := db.ExploreFilter{Column: col}
		if v != "" {
			val := v
			f.Value = &val
		} // empty string => Value stays nil => IS NULL
		filters = append(filters, f)
	}
	return filters, nil
}

const (
	exploreGroupLimit   = 500
	exploreRowsDefault  = 100
	exploreRowsMaxLimit = 500
)

// HeartbeatsGroup: GET /api/v1/users/current/heartbeats/group
// Groups the user's heartbeats by one whitelisted axis with accumulated equality
// filters. Read-only, owner-scoped.
func (h *Handler) HeartbeatsGroup(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}

	groupBy := c.QueryParam("groupBy")
	groupCol, ok := db.ExploreColumn(groupBy)
	if !ok {
		return respondErr(c, apierr.New(http.StatusBadRequest, "Unknown groupBy axis: "+groupBy, nil))
	}

	filters, ferr := collectExploreFilters(c)
	if ferr != nil {
		return respondErr(c, ferr)
	}

	t0, t1 := defaultWeekRange(c)
	groups, truncated, err := h.DB.GroupHeartbeats(c.Request().Context(), owner, groupCol, t0, t1, filters, exploreGroupLimit)
	if err != nil {
		h.Logger.Error("heartbeats group query failed", "err", err)
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, map[string]any{
		"groupBy":   groupBy,
		"groups":    groups,
		"truncated": truncated,
	})
}

// HeartbeatsList: GET /api/v1/users/current/heartbeats
// Returns a page of raw heartbeat records for the given whitelist filters,
// optional entity substring, and time range. Read-only, owner-scoped.
func (h *Handler) HeartbeatsList(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}

	filters, ferr := collectExploreFilters(c)
	if ferr != nil {
		return respondErr(c, ferr)
	}

	page := int(queryInt64(c, "page", 1))
	if page < 1 {
		page = 1
	}
	limit := int(queryInt64(c, "limit", exploreRowsDefault))
	if limit < 1 {
		limit = exploreRowsDefault
	}
	if limit > exploreRowsMaxLimit {
		limit = exploreRowsMaxLimit
	}
	entity := c.QueryParam("entity")

	t0, t1 := defaultWeekRange(c)
	items, total, err := h.DB.ListHeartbeats(c.Request().Context(), owner, t0, t1, filters, entity, page, limit)
	if err != nil {
		h.Logger.Error("heartbeats list query failed", "err", err)
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, map[string]any{
		"items": items,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}
