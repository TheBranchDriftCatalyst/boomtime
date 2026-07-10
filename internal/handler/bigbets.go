package handler

import (
	"github.com/TheBranchDriftCatalyst/gakatime/internal/stats"
	"github.com/labstack/echo/v5"
)

// Punchcard: GET /api/v1/users/current/stats/punchcard?start&end&timeLimit.
// Day-of-week x hour-of-day intensity (UTC). Excludes all hidden axis values.
func (h *Handler) Punchcard(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()
	t0, t1 := defaultWeekRange(c)
	limit := timeLimit(c)
	spaceParam := c.QueryParam("space")
	return h.cachedJSON(c, cacheKey(owner, "punchcard", t0, t1, limit, "space:"+spaceParam), func() (any, error) {
		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		members, spaceRequested, err := h.loadSpace(ctx, spaceParam)
		if err != nil {
			return nil, err
		}
		cells, err := h.DB.GetPunchcard(ctx, owner, t0, t1, limit, hidden, members, spaceRequested)
		if err != nil {
			return nil, err
		}
		return stats.ToPunchcardPayload(cells), nil
	})
}

// Sessions: GET /api/v1/users/current/stats/sessions?start&end&timeLimit.
// Sessionized activity (summary + daily + histogram). Excludes all hidden axis values.
func (h *Handler) Sessions(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()
	t0, t1 := defaultWeekRange(c)
	limit := timeLimit(c)
	spaceParam := c.QueryParam("space")
	return h.cachedJSON(c, cacheKey(owner, "sessions", t0, t1, limit, "space:"+spaceParam), func() (any, error) {
		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		members, spaceRequested, err := h.loadSpace(ctx, spaceParam)
		if err != nil {
			return nil, err
		}
		rows, err := h.DB.GetSessions(ctx, owner, t0, t1, limit, hidden, members, spaceRequested)
		if err != nil {
			return nil, err
		}
		return stats.ToSessionsPayload(t0, t1, rows), nil
	})
}

// Momentum: GET /api/v1/users/current/stats/momentum?start&end&timeLimit&top=8.
// Top-N projects' weekly time series. Excludes all hidden axis values.
func (h *Handler) Momentum(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()
	t0, t1 := defaultWeekRange(c)
	limit := timeLimit(c)
	top := int(queryInt64(c, "top", 8))
	if top < 1 {
		top = 8
	}
	spaceParam := c.QueryParam("space")
	return h.cachedJSON(c, cacheKey(owner, "momentum", t0, t1, limit, top, "space:"+spaceParam), func() (any, error) {
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
		rows, err := h.DB.GetMomentum(ctx, owner, t0, t1, limit, hidden, renames, members, spaceRequested)
		if err != nil {
			return nil, err
		}
		return stats.ToMomentumPayload(t0, t1, rows, top), nil
	})
}
