package handler

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	"github.com/labstack/echo/v5"
)

// Stats: GET /api/v1/users/current/stats?start&end&tag&timeLimit.
func (h *Handler) Stats(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 7)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	return h.cachedJSON(c, s.cacheKey("stats", s.t0, s.t1, s.limit), func() (any, error) {
		// Apply the user's query-time hide exclusions + rename remaps (both
		// reversible; audit views stay unfiltered/un-remapped).
		l, err := s.load(loadHidden | loadRenames)
		if err != nil {
			return nil, err
		}
		var rows []db.StatRow
		switch {
		case s.limit == 15 && !l.spaceRequested && !l.hidden.HasHiddenOutside(db.RollupAxes):
			// Fast path: pre-aggregated rollup (default 15-min limit, no space). Only
			// gated on hide (rename needs no rollup fallback — it relabels output
			// columns without removing rows, and the rollup stores exactly the
			// remappable output axes). A space scope may target a non-rollup axis, so
			// scoped requests always use the raw path.
			rows, err = h.DB.GetUserActivityRollup(s.ctx, s.owner, s.t0, s.t1, l.hidden, l.renames, l.members, false)
		default:
			// Raw gap_seconds scan (non-default limit, a hide the rollup can't apply,
			// or a space scope).
			rows, err = h.DB.GetUserActivity(s.ctx, s.owner, s.t0, s.t1, s.limit, l.hidden, l.renames, l.members, l.spaceRequested)
		}
		if err != nil {
			return nil, err
		}
		// Categories are fetched separately (no category column in the rollup) and
		// respect the same all-axis hide exclusion + rename remap + timeLimit + space.
		categories, err := h.DB.GetCategoryDaily(s.ctx, s.owner, s.t0, s.t1, s.limit, l.hidden, l.renames, l.members, l.spaceRequested)
		if err != nil {
			return nil, err
		}
		return stats.ToStatsPayload(s.t0, s.t1, rows, categories), nil
	})
}

// Timeline: GET /api/v1/users/current/timeline?start&end&timeLimit.
func (h *Handler) Timeline(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 7)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	return h.cachedJSON(c, s.cacheKey("timeline", s.t0, s.t1, s.limit), func() (any, error) {
		// Timeline intentionally applies neither hide nor rename — only the space scope.
		l, err := s.load(loadNone)
		if err != nil {
			return nil, err
		}
		rows, err := h.DB.GetTimeline(s.ctx, s.owner, s.t0, s.t1, s.limit, l.members, l.spaceRequested)
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
