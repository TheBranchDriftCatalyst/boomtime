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

	ok, err := h.DB.CheckProjectOwner(ctx, owner, project)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if !ok {
		return respondErr(c, apierr.InvalidRelation(owner, project))
	}

	t0, t1 := defaultWeekRange(c)
	limit := timeLimit(c)
	return h.cachedJSON(c, cacheKey(owner, "project", project, t0, t1, limit), func() (any, error) {
		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		rows, err := h.DB.GetProjectStats(ctx, owner, project, t0, t1, limit, hidden)
		if err != nil {
			return nil, err
		}
		extras, err := h.DB.GetProjectExtras(ctx, owner, project, t0, t1, limit)
		if err != nil {
			return nil, err
		}
		return stats.ToProjectStatistics(t0, t1, rows, extras), nil
	})
}

// TagStats: GET /api/v1/users/current/tags/:tag?start&end&timeLimit.
func (h *Handler) TagStats(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	tag := c.Param("tag")
	ctx := c.Request().Context()

	ok, err := h.DB.CheckTagOwner(ctx, owner, tag)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if !ok {
		return respondErr(c, apierr.InvalidTagRelation(owner, tag))
	}

	t0, t1 := defaultWeekRange(c)
	limit := timeLimit(c)
	return h.cachedJSON(c, cacheKey(owner, "tag", tag, t0, t1, limit), func() (any, error) {
		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		rows, err := h.DB.GetTagStats(ctx, owner, tag, t0, t1, limit, hidden)
		if err != nil {
			return nil, err
		}
		// Tag path keeps the extras nil for now (per-project viz metrics only).
		return stats.ToProjectStatistics(t0, t1, rows, nil), nil
	})
}

// SetProjectTags: POST /api/v1/projects/:project/tags.
func (h *Handler) SetProjectTags(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	project := c.Param("project")
	ctx := c.Request().Context()

	ok, err := h.DB.CheckProjectOwner(ctx, owner, project)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if !ok {
		return respondErr(c, apierr.InvalidRelation(owner, project))
	}

	var payload model.TagsPayload
	if err := c.Bind(&payload); err != nil {
		return respondErr(c, apierr.Generic())
	}
	if _, err := h.DB.SetTags(ctx, owner, project, payload.Tags); err != nil {
		h.Logger.Error("set tags failed", "err", err)
		return respondErr(c, apierr.Generic())
	}
	return noContent(c)
}

// GetProjectTags: GET /api/v1/projects/:project/tags.
func (h *Handler) GetProjectTags(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	project := c.Param("project")
	ctx := c.Request().Context()

	ok, err := h.DB.CheckProjectOwner(ctx, owner, project)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	if !ok {
		return respondErr(c, apierr.InvalidRelation(owner, project))
	}

	tags, err := h.DB.GetTags(ctx, owner, project)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, model.TagsPayload{Tags: tags})
}

// GetUserTags: GET /api/v1/tags.
func (h *Handler) GetUserTags(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	tags, err := h.DB.GetAllTags(c.Request().Context(), owner)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, model.TagsPayload{Tags: tags})
}

// ProjectList: GET /api/v1/projects?start&end.
func (h *Handler) ProjectList(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()
	t0, t1 := defaultMonthRange(c)
	// Exclude the user's hidden values across all axes (reversible query-time
	// hide): a project surfaces only if it has non-hidden heartbeats in range.
	hidden, err := h.DB.LoadHiddenSets(ctx, owner)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	projects, err := h.DB.GetAllProjects(ctx, owner, t0, t1, hidden)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, model.ProjectListPayload{Projects: projects})
}
