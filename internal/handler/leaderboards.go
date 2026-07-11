package handler

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	"github.com/labstack/echo/v5"
)

// Leaderboards: GET /api/v1/leaderboards?start&end (default last month).
func (h *Handler) Leaderboards(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 30)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	// Leaderboards are cross-user, but the requester's own hide + rename + space
	// scope apply to THEIR rows only, so the response is per-owner — cache per
	// owner. Note: no timeLimit in the key (the query doesn't take one).
	return h.cachedJSON(c, s.cacheKey("leaderboards", s.t0, s.t1), func() (any, error) {
		l, err := s.load(loadHidden | loadRenames)
		if err != nil {
			return nil, err
		}
		rows, err := h.DB.GetLeaderboards(s.ctx, s.t0, s.t1, s.owner, l.hidden, l.renames, l.members, l.spaceRequested)
		if err != nil {
			return nil, err
		}
		return stats.ToLeaderboardsPayload(rows), nil
	})
}
