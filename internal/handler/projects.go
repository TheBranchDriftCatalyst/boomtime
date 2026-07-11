package handler

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	"github.com/labstack/echo/v5"
)

// ProjectStats: GET /api/v1/users/current/projects/:project?start&end&timeLimit.
func (h *Handler) ProjectStats(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 7)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	project := c.Param("project")

	// `project` is a DISPLAY name — validate ownership through the rename remap so
	// a merged/regex display name (which has no raw projects row) still resolves.
	// Renames load EAGERLY here (the owner check needs them before the cache).
	renames, err := h.DB.LoadRenameSets(s.ctx, s.owner)
	if err != nil {
		return h.internalErr(c, "rename sets load failed", err)
	}
	ok, err := h.DB.CheckProjectDisplayOwner(s.ctx, s.owner, project, renames)
	if err != nil {
		return h.internalErr(c, "project owner check failed", err)
	}
	if !ok {
		return respondErr(c, apierr.InvalidRelation(s.owner, project))
	}

	return h.cachedJSON(c, s.cacheKey("project", project, s.t0, s.t1, s.limit), func() (any, error) {
		l, err := s.load(loadHidden)
		if err != nil {
			return nil, err
		}
		// `project` is a DISPLAY name; GetProjectStats remap-matches it so a merged
		// name aggregates all its source projects.
		rows, err := h.DB.GetProjectStats(s.ctx, s.owner, project, s.t0, s.t1, s.limit, l.hidden, renames, l.members, l.spaceRequested)
		if err != nil {
			return nil, err
		}
		extras, err := h.DB.GetProjectExtras(s.ctx, s.owner, project, s.t0, s.t1, s.limit, renames)
		if err != nil {
			return nil, err
		}
		return stats.ToProjectStatistics(s.t0, s.t1, rows, extras), nil
	})
}

// ProjectList: GET /api/v1/projects?start&end.
func (h *Handler) ProjectList(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 30)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	// Not cached, so the sets load eagerly. Exclude hidden values (a project
	// surfaces only if it has non-hidden heartbeats) and relabel renamed projects
	// (merged names collapse to one), both reversible query-time transforms.
	l, err := s.load(loadHidden | loadRenames)
	if err != nil {
		return h.internalErr(c, "project list curation load failed", err)
	}
	projects, err := h.DB.GetAllProjects(s.ctx, s.owner, s.t0, s.t1, l.hidden, l.renames, l.members, l.spaceRequested)
	if err != nil {
		return h.internalErr(c, "project list query failed", err)
	}
	return c.JSON(http.StatusOK, model.ProjectListPayload{Projects: projects})
}
