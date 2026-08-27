package stats

import (
	"fmt"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/labstack/echo/v5"
)

// Stats: GET /api/v1/users/current/stats?start&end&tag&timeLimit.
func (h *Handler) Stats(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 7)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	return apihelpers.CachedJSON(h.Cache, h.Logger, c, s.cacheKey("stats", s.t0, s.t1, s.limit), func() (any, error) {
		// Apply the user's query-time hide exclusions + rename remaps (both
		// reversible; audit views stay unfiltered/un-remapped).
		l, err := s.load(loadHidden | loadRenames)
		if err != nil {
			return nil, err
		}
		var rows []db.StatRow
		switch {
		case s.limit == 15 &&
			!l.hidden.HasHiddenOutside(db.RollupAxes) &&
			(!l.spaceRequested || !l.members.HasMemberOutside(db.RollupAxes)):
			// Fast path: pre-aggregated rollup (default 15-min limit, no rollup-
			// external hide, and no rollup-external Space rule). The rollup stores
			// project/language/editor/platform/machine + category/plugin/branch, so
			// only a Space rule on entity (or a future non-rollup axis) forces raw.
			// spaceRequested is passed through — the rollup query splices the same
			// inclusion predicate (or ` AND FALSE` for a rule-less requested space).
			// Rename needs no rollup fallback: rename relabels output columns without
			// removing rows, and the rollup's output columns match the remappable set.
			rows, err = h.DB.GetUserActivityRollup(s.ctx, s.owner, s.t0, s.t1, l.hidden, l.renames, l.members, l.spaceRequested)
		default:
			// Raw gap_seconds scan (non-default limit, a hide the rollup can't
			// apply — impossible today, all hiddenAxes are rollup axes — or a Space
			// rule on entity/other non-rollup axis).
			rows, err = h.DB.GetUserActivity(s.ctx, s.owner, s.t0, s.t1, s.limit, s.tz, l.hidden, l.renames, l.members, l.spaceRequested)
		}
		if err != nil {
			return nil, err
		}
		// Categories are fetched separately (get_user_activity_rollup collapses
		// category back to the 5-axis output grain; a category-broken-down series
		// still needs its own scan) and respect the same all-axis hide exclusion +
		// rename remap + timeLimit + space. boom-dg7: same tz as the activity
		// scan so both series bucket on the same day boundary.
		//
		// boom-o4m: same rollup fast-path gate as the activity scan above — at
		// the default 15-min limit with no hide / no Space rule outside
		// RollupAxes, serve the pre-aggregated hb_rollup_daily instead of scanning
		// raw heartbeats. Category IS a rollup axis so the pie's output shape is
		// unchanged (day, category, total_seconds, pct, daily_pct).
		var categories []db.CategoryDailyRow
		switch {
		case s.limit == 15 &&
			!l.hidden.HasHiddenOutside(db.RollupAxes) &&
			(!l.spaceRequested || !l.members.HasMemberOutside(db.RollupAxes)):
			categories, err = h.DB.GetCategoryDailyRollup(s.ctx, s.owner, s.t0, s.t1, l.hidden, l.renames, l.members, l.spaceRequested)
		default:
			categories, err = h.DB.GetCategoryDaily(s.ctx, s.owner, s.t0, s.t1, s.limit, s.tz, l.hidden, l.renames, l.members, l.spaceRequested)
		}
		if err != nil {
			return nil, err
		}
		// boom-csx P3: OPTIONAL GitHub contribution overlay. A CHEAP local read
		// of the owner's cached grid (no external GitHub call) — when absent
		// (never connected / feature off / never synced) the grid is nil and
		// GithubDailyTotal is omitted, so the payload is byte-identical to today.
		// A read error is non-fatal: log-and-continue with a nil grid rather than
		// letting the GitHub overlay ever block the Overview.
		var ghGrid []model.GithubContributionDay
		if cache, ok, gerr := h.DB.GetGithubStatsCache(s.ctx, s.owner); gerr != nil {
			h.Logger.Warn("github stats cache read failed; omitting overlay", "err", gerr)
		} else if ok {
			ghGrid = cache.ContributionGrid
		}
		return ToStatsPayload(s.t0, s.t1, rows, categories, ghGrid), nil
	})
}

// Timeline: GET /api/v1/users/current/timeline?start&end&timeLimit.
func (h *Handler) Timeline(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 7)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	return apihelpers.CachedJSON(h.Cache, h.Logger, c, s.cacheKey("timeline", s.t0, s.t1, s.limit), func() (any, error) {
		// Timeline intentionally applies neither hide nor rename — only the space scope.
		l, err := s.load(loadNone)
		if err != nil {
			return nil, err
		}
		rows, err := h.DB.GetTimeline(s.ctx, s.owner, s.t0, s.t1, s.limit, l.members, l.spaceRequested)
		if err != nil {
			return nil, err
		}
		return ToTimelinePayload(rows), nil
	})
}

// StatusbarToday: GET /api/v1/users/current/statusbar/today.
func (h *Handler) StatusbarToday(c *echo.Context) (model.StatusBarPayload, error) {
	var out model.StatusBarPayload
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	ctx := c.Request().Context()
	hidden, err := h.DB.LoadHiddenSets(ctx, owner)
	if err != nil {
		// Deliberately unlogged (matches the pre-seam handler).
		return out, apierr.Generic()
	}
	// boom-dg7: "today" bounded by the user's local midnight (per user tz +
	// server default resolver), not UTC midnight — a 23:59 PT status bar
	// refresh previously showed the next UTC day's (empty) window.
	tz := apihelpers.ResolveUserTZ(h.DB, h.Logger, ctx, owner, h.Cfg.DefaultTimezoneValue())
	total, err := h.DB.GetTotalTimeToday(ctx, owner, tz, hidden)
	if err != nil {
		return out, fmt.Errorf("statusbar query failed: %w", err)
	}
	return model.StatusBarPayload{
		Data: model.DayGrandTotal{
			Categories: []string{},
			GrandTotal: model.DayTextValue{Text: CompoundDuration(&total)},
		},
	}, nil
}
