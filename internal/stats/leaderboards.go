package stats

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/labstack/echo/v5"
)

// Leaderboards: GET /api/v1/leaderboards?start&end (default last month).
func (h *Handler) Leaderboards(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 30)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	// Leaderboards are cross-user, but the requester's own hide + rename + space
	// scope apply to THEIR rows only, so the response is per-owner — cache per
	// owner. Note: no timeLimit in the key (the query doesn't take one).
	return apihelpers.CachedJSON(h.Cache, h.Logger, c, s.cacheKey("leaderboards", s.t0, s.t1), func() (any, error) {
		l, err := s.load(loadHidden | loadRenames)
		if err != nil {
			return nil, err
		}
		// gaka-o4m: the raw leaderboards query hardcodes a 15-min gap cutoff
		// (no timeLimit param), which is exactly what the rollup captured at
		// ingest — so summing rollup total_seconds reproduces the raw sum
		// byte-for-byte whenever the requester's hide + Space rules stay
		// within the rollup axes. The multi-user machinery (requester-only
		// hide/rename via `sender = $req`, `sender <> $req` bypass on the
		// scope) is column-independent and works identically on both tables.
		var rows []db.LeaderboardRow
		switch {
		case !l.hidden.HasHiddenOutside(db.RollupAxes) &&
			(!l.spaceRequested || !l.members.HasMemberOutside(db.RollupAxes)):
			rows, err = h.DB.GetLeaderboardsRollup(s.ctx, s.t0, s.t1, s.owner, l.hidden, l.renames, l.members, l.spaceRequested)
		default:
			rows, err = h.DB.GetLeaderboards(s.ctx, s.t0, s.t1, s.owner, l.hidden, l.renames, l.members, l.spaceRequested)
		}
		if err != nil {
			return nil, err
		}
		return ToLeaderboardsPayload(rows), nil
	})
}
