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
		// Apply the user's query-time hide exclusions + rename remaps (both
		// reversible; audit views stay unfiltered/un-remapped).
		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		renames, err := h.DB.LoadRenameSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		var rows []db.StatRow
		switch {
		case tag != "":
			// /stats?tag= is scoped to a chosen tag; apply all-axis hides + remaps.
			rows, err = h.DB.GetUserActivityByTag(ctx, owner, t0, t1, tag, limit, hidden, renames)
		case limit == 15 && !hidden.HasHiddenOutside(db.RollupAxes):
			// Fast path: pre-aggregated rollup (default 15-min limit, no tag). Only
			// gated on hide (rename needs no rollup fallback — it relabels output
			// columns without removing rows, and the rollup stores exactly the
			// remappable output axes).
			rows, err = h.DB.GetUserActivityRollup(ctx, owner, t0, t1, hidden, renames)
		default:
			// Raw gap_seconds scan (non-default limit, or a hide the rollup can't apply).
			rows, err = h.DB.GetUserActivity(ctx, owner, t0, t1, limit, hidden, renames)
		}
		if err != nil {
			return nil, err
		}
		// Categories are fetched separately (no category column in the rollup) and
		// respect the same all-axis hide exclusion + rename remap + timeLimit.
		categories, err := h.DB.GetCategoryDaily(ctx, owner, t0, t1, limit, hidden, renames)
		if err != nil {
			return nil, err
		}
		return stats.ToStatsPayload(t0, t1, rows, categories), nil
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
	ctx := c.Request().Context()
	hidden, err := h.DB.LoadHiddenSets(ctx, owner)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	total, err := h.DB.GetTotalTimeToday(ctx, owner, hidden)
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
