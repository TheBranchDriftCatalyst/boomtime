package ingest

import (
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/labstack/echo/v5"
)

// reservedExploreParams are query keys that are NOT equality filters.
var reservedExploreParams = map[string]struct{}{
	"groupBy": {}, "start": {}, "end": {}, "page": {}, "limit": {}, "entity": {}, "timeLimit": {},
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

type heartbeatsLatestResponse struct {
	// LastHeartbeat is the owner's most recent heartbeat timestamp (RFC3339
	// UTC), or null when they have never ingested one. NOT omitempty: the FE
	// distinguishes "no heartbeats yet" from a missing field.
	LastHeartbeat *string `json:"lastHeartbeat"`
	// Count is the owner's total heartbeat count.
	Count int64 `json:"count"`
}

// HeartbeatsLatest: GET /api/v1/users/current/heartbeats/latest
// Returns the owner's most recent heartbeat timestamp (RFC3339 UTC, or null) and
// total count. Powers the import "backfill from last heartbeat" button.
func (h *Handler) HeartbeatsLatest(c *echo.Context) (heartbeatsLatestResponse, error) {
	var out heartbeatsLatestResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	last, count, err := h.DB.LatestHeartbeat(c.Request().Context(), owner)
	if err != nil {
		h.Logger.Error("latest heartbeat query failed", "err", err)
		return out, apierr.Generic()
	}
	var lastStr *string
	if last != nil {
		s := last.Format(time.RFC3339)
		lastStr = &s
	}
	return heartbeatsLatestResponse{
		LastHeartbeat: lastStr,
		Count:         count,
	}, nil
}

type heartbeatsGroupResponse struct {
	// GroupBy echoes the requested axis.
	GroupBy string `json:"groupBy"`
	// Groups is one row per distinct value on that axis, count desc.
	Groups []db.ExploreGroup `json:"groups"`
	// Truncated reports that the result hit exploreGroupLimit.
	Truncated bool `json:"truncated"`
}

// HeartbeatsGroup: GET /api/v1/users/current/heartbeats/group
// Groups the user's heartbeats by one whitelisted axis with accumulated equality
// filters. Read-only, owner-scoped.
func (h *Handler) HeartbeatsGroup(c *echo.Context) (heartbeatsGroupResponse, error) {
	var out heartbeatsGroupResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}

	groupBy := c.QueryParam("groupBy")
	groupCol, ok := db.ExploreColumn(groupBy)
	if !ok {
		return out, apierr.New(http.StatusBadRequest, "Unknown groupBy axis: "+groupBy, nil)
	}

	filters, ferr := collectExploreFilters(c)
	if ferr != nil {
		return out, ferr
	}

	t0, t1 := apihelpers.DefaultWeekRange(c)
	// timeLimit (default 15) is the gap cutoff for the per-group attributed time.
	// entity is an optional ILIKE substring on the entity column; empty is a
	// no-op. Threading it here fixes the Explorer search box, which previously
	// only narrowed the leaf-row endpoint (boom-90x sibling fix).
	entity := c.QueryParam("entity")
	groups, truncated, err := h.DB.GroupHeartbeats(c.Request().Context(), owner, groupCol, t0, t1, filters, entity, exploreGroupLimit, apihelpers.TimeLimit(c))
	if err != nil {
		h.Logger.Error("heartbeats group query failed", "err", err)
		return out, apierr.Generic()
	}
	return heartbeatsGroupResponse{
		GroupBy:   groupBy,
		Groups:    groups,
		Truncated: truncated,
	}, nil
}

type heartbeatsListResponse struct {
	// Items is the requested page of raw heartbeat rows.
	Items []db.ExploreRow `json:"items"`
	// Total is the unpaged match count.
	Total int64 `json:"total"`
	// Page and Limit echo the clamped pagination actually applied.
	Page  int `json:"page"`
	Limit int `json:"limit"`
}

// HeartbeatsList: GET /api/v1/users/current/heartbeats
// Returns a page of raw heartbeat records for the given whitelist filters,
// optional entity substring, and time range. Read-only, owner-scoped.
func (h *Handler) HeartbeatsList(c *echo.Context) (heartbeatsListResponse, error) {
	var out heartbeatsListResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}

	filters, ferr := collectExploreFilters(c)
	if ferr != nil {
		return out, ferr
	}

	page := int(apihelpers.QueryInt64(c, "page", 1))
	if page < 1 {
		page = 1
	}
	limit := int(apihelpers.QueryInt64(c, "limit", exploreRowsDefault))
	if limit < 1 {
		limit = exploreRowsDefault
	}
	if limit > exploreRowsMaxLimit {
		limit = exploreRowsMaxLimit
	}
	entity := c.QueryParam("entity")

	t0, t1 := apihelpers.DefaultWeekRange(c)
	items, total, err := h.DB.ListHeartbeats(c.Request().Context(), owner, t0, t1, filters, entity, page, limit)
	if err != nil {
		h.Logger.Error("heartbeats list query failed", "err", err)
		return out, apierr.Generic()
	}
	return heartbeatsListResponse{
		Items: items,
		Total: total,
		Page:  page,
		Limit: limit,
	}, nil
}
