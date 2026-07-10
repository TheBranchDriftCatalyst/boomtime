package handler

import (
	"github.com/TheBranchDriftCatalyst/gakatime/internal/stats"
	"github.com/labstack/echo/v5"
)

// Leaderboards: GET /api/v1/leaderboards?start&end (default last month).
func (h *Handler) Leaderboards(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	t0, t1 := defaultMonthRange(c)
	ctx := c.Request().Context()
	// Leaderboards are cross-user, but the requester's own hidden values are
	// excluded from their rows, so the response is per-owner — cache per owner.
	return h.cachedJSON(c, cacheKey(owner, "leaderboards", t0, t1), func() (any, error) {
		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		rows, err := h.DB.GetLeaderboards(ctx, t0, t1, owner, hidden)
		if err != nil {
			return nil, err
		}
		return stats.ToLeaderboardsPayload(rows), nil
	})
}
