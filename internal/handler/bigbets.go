package handler

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	"github.com/labstack/echo/v5"
)

// Punchcard: GET /api/v1/users/current/stats/punchcard?start&end&timeLimit.
// Day-of-week x hour-of-day intensity (UTC). Excludes all hidden axis values.
func (h *Handler) Punchcard(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 7)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	return h.cachedJSON(c, s.cacheKey("punchcard", s.t0, s.t1, s.limit), func() (any, error) {
		l, err := s.load(loadHidden)
		if err != nil {
			return nil, err
		}
		cells, err := h.DB.GetPunchcard(s.ctx, s.owner, s.t0, s.t1, s.limit, l.hidden, l.members, l.spaceRequested)
		if err != nil {
			return nil, err
		}
		return stats.ToPunchcardPayload(cells), nil
	})
}

// Sessions: GET /api/v1/users/current/stats/sessions?start&end&timeLimit.
// Sessionized activity (summary + daily + histogram). Excludes all hidden axis values.
func (h *Handler) Sessions(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 7)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	return h.cachedJSON(c, s.cacheKey("sessions", s.t0, s.t1, s.limit), func() (any, error) {
		l, err := s.load(loadHidden)
		if err != nil {
			return nil, err
		}
		rows, err := h.DB.GetSessions(s.ctx, s.owner, s.t0, s.t1, s.limit, l.hidden, l.members, l.spaceRequested)
		if err != nil {
			return nil, err
		}
		return stats.ToSessionsPayload(s.t0, s.t1, rows), nil
	})
}

// Momentum: GET /api/v1/users/current/stats/momentum?start&end&timeLimit&top=8.
// Top-N projects' weekly time series. Excludes all hidden axis values.
func (h *Handler) Momentum(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 7)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	top := int(queryInt64(c, "top", 8))
	if top < 1 {
		top = 8
	}
	return h.cachedJSON(c, s.cacheKey("momentum", s.t0, s.t1, s.limit, top), func() (any, error) {
		l, err := s.load(loadHidden | loadRenames)
		if err != nil {
			return nil, err
		}
		rows, err := h.DB.GetMomentum(s.ctx, s.owner, s.t0, s.t1, s.limit, l.hidden, l.renames, l.members, l.spaceRequested)
		if err != nil {
			return nil, err
		}
		return stats.ToMomentumPayload(s.t0, s.t1, rows, top), nil
	})
}
