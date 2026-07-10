package handler

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/model"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/stats"
	"github.com/labstack/echo/v5"
)

// Stats: GET /api/v1/users/current/stats?start&end&tag&timeLimit.
func (h *Handler) Stats(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()
	t0, t1 := defaultWeekRange(c)
	limit := timeLimit(c)
	tag := c.QueryParam("tag")

	return h.cachedJSON(c, cacheKey(owner, "stats", t0, t1, limit, tag), func() (any, error) {
		// Apply the user's query-time hide exclusions (reversible; audit views
		// stay unfiltered).
		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		var rows []db.StatRow
		switch {
		case tag != "":
			// Tag stats are scoped to a chosen tag; leave hide out of this path.
			rows, err = h.DB.GetUserActivityByTag(ctx, owner, t0, t1, tag, limit)
		case limit == 15:
			// Fast path: pre-aggregated rollup (default 15-min limit, no tag).
			rows, err = h.DB.GetUserActivityRollup(ctx, owner, t0, t1, hidden)
		default:
			// Non-default limit: recompute from raw gap_seconds.
			rows, err = h.DB.GetUserActivity(ctx, owner, t0, t1, limit, hidden)
		}
		if err != nil {
			return nil, err
		}
		return stats.ToStatsPayload(t0, t1, rows), nil
	})
}

// Timeline: GET /api/v1/users/current/timeline?start&end&timeLimit.
func (h *Handler) Timeline(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()
	t0, t1 := defaultWeekRange(c)
	limit := timeLimit(c)
	return h.cachedJSON(c, cacheKey(owner, "timeline", t0, t1, limit), func() (any, error) {
		rows, err := h.DB.GetTimeline(ctx, owner, t0, t1, limit)
		if err != nil {
			return nil, err
		}
		return stats.ToTimelinePayload(rows), nil
	})
}

// StatusbarToday: GET /api/v1/users/current/statusbar/today.
func (h *Handler) StatusbarToday(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	total, err := h.DB.GetTotalTimeToday(c.Request().Context(), owner)
	if err != nil {
		h.Logger.Error("statusbar query failed", "err", err)
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, model.StatusBarPayload{
		Data: model.DayGrandTotal{
			Categories: []string{},
			GrandTotal: model.DayTextValue{Text: stats.CompoundDuration(&total)},
		},
	})
}
