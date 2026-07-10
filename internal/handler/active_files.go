package handler

import (
	"github.com/TheBranchDriftCatalyst/gakatime/internal/stats"
	"github.com/labstack/echo/v5"
)

// activeFilesDefaultLimit / activeFilesMaxLimit bound the ?limit param.
const (
	activeFilesDefaultLimit = 20
	activeFilesMaxLimit     = 100
)

// ActiveFiles: GET /api/v1/users/current/files?start&end&timeLimit&limit.
// Top FILES across ALL of the owner's projects, each with its attributed time
// and how many DISTINCT projects touch it — surfacing shared interfaces /
// lynchpins that span projects (projects>1). Curation-aware: hidden projects are
// excluded and rename rules are applied before the distinct-project count, so the
// aggregate matches the dashboards.
func (h *Handler) ActiveFiles(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()
	t0, t1 := defaultWeekRange(c)
	limitMin := timeLimit(c)

	limit := int(queryInt64(c, "limit", activeFilesDefaultLimit))
	if limit < 1 {
		limit = activeFilesDefaultLimit
	}
	if limit > activeFilesMaxLimit {
		limit = activeFilesMaxLimit
	}

	spaceParam := c.QueryParam("space")
	return h.cachedJSON(c, cacheKey(owner, "files", t0, t1, limitMin, limit, "space:"+spaceParam), func() (any, error) {
		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		renames, err := h.DB.LoadRenameSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		members, spaceRequested, err := h.loadSpace(ctx, spaceParam)
		if err != nil {
			return nil, err
		}
		rows, truncated, err := h.DB.GetActiveFiles(ctx, owner, t0, t1, limitMin, limit, hidden, renames, members, spaceRequested)
		if err != nil {
			return nil, err
		}
		return stats.ToActiveFilesPayload(rows, truncated), nil
	})
}
