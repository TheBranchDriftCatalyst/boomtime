package handler

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/model"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/stats"
	"github.com/labstack/echo/v5"
)

// ProjectStats: GET /api/v1/users/current/projects/:project?start&end&timeLimit.
func (h *Handler) ProjectStats(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	project := c.Param("project")
	ctx := c.Request().Context()

	// `project` is a DISPLAY name — validate ownership through the rename remap so
	// a merged/regex display name (which has no raw projects row) still resolves.
	renames, err := h.DB.LoadRenameSets(ctx, owner)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	ok, err := h.DB.CheckProjectDisplayOwner(ctx, owner, project, renames)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if !ok {
		return respondErr(c, apierr.InvalidRelation(owner, project))
	}

	t0, t1 := defaultWeekRange(c)
	limit := timeLimit(c)
	spaceParam := c.QueryParam("space")
	return h.cachedJSON(c, cacheKey(owner, "project", project, t0, t1, limit, "space:"+spaceParam), func() (any, error) {
		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		members, spaceRequested, err := h.loadSpace(ctx, spaceParam)
		if err != nil {
			return nil, err
		}
		// `project` is a DISPLAY name; GetProjectStats remap-matches it so a merged
		// name aggregates all its source projects.
		rows, err := h.DB.GetProjectStats(ctx, owner, project, t0, t1, limit, hidden, renames, members, spaceRequested)
		if err != nil {
			return nil, err
		}
		extras, err := h.DB.GetProjectExtras(ctx, owner, project, t0, t1, limit, renames)
		if err != nil {
			return nil, err
		}
		return stats.ToProjectStatistics(t0, t1, rows, extras), nil
	})
}

// ProjectList: GET /api/v1/projects?start&end.
func (h *Handler) ProjectList(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()
	t0, t1 := defaultMonthRange(c)
	// Exclude hidden values (a project surfaces only if it has non-hidden
	// heartbeats) and relabel renamed projects (merged names collapse to one),
	// both reversible query-time transforms.
	hidden, err := h.DB.LoadHiddenSets(ctx, owner)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	renames, err := h.DB.LoadRenameSets(ctx, owner)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	members, spaceRequested, err := h.loadSpace(ctx, c.QueryParam("space"))
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	projects, err := h.DB.GetAllProjects(ctx, owner, t0, t1, hidden, renames, members, spaceRequested)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, model.ProjectListPayload{Projects: projects})
}
