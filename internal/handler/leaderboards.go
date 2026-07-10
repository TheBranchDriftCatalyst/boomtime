package handler

import (
	"github.com/TheBranchDriftCatalyst/gakatime/internal/stats"
	"github.com/labstack/echo/v5"
)

// Leaderboards: GET /api/v1/leaderboards?start&end (default last month).
func (h *Handler) Leaderboards(c *echo.Context) error {
	// Requires a valid token (owner unused beyond auth, matching hakatime).
	if _, _, aerr := h.resolveUser(c); aerr != nil {
		return respondErr(c, aerr)
	}
	t0, t1 := defaultMonthRange(c)
	ctx := c.Request().Context()
	// Leaderboards are global (all users); cache under a shared key.
	return h.cachedJSON(c, cacheKey("*", "leaderboards", t0, t1), func() (any, error) {
		rows, err := h.DB.GetLeaderboards(ctx, t0, t1)
		if err != nil {
			return nil, err
		}
		return stats.ToLeaderboardsPayload(rows), nil
	})
}
