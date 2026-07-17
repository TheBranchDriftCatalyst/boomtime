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

// AIActivity: GET /api/v1/users/current/stats/ai?start&end.
// Per-day AI-assistance metrics (input/output tokens, AI vs human line
// changes, distinct sessions) + range summary + latest subscription plan.
// Returns {hasData: false} when the user has no AI-tagged heartbeats in
// the range so the FE card can skip render. Not affected by curation
// (audit-first metric — AI usage is inherently the user's own signal) or
// space scoping (AI usage is cross-cutting).
func (h *Handler) AIActivity(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 30)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	return h.cachedJSON(c, s.cacheKey("ai-activity", s.t0, s.t1), func() (any, error) {
		return h.DB.GetAIActivity(s.ctx, s.owner, s.t0, s.t1)
	})
}

// HealthActivity: GET /api/v1/users/current/stats/health?start&end.
// Per-day workout counts + HealthKit metric aggregates (HR, steps, kcal,
// sleep, HRV, mindful). Returns {hasData: false} on an empty range so the FE
// Wellness card can skip render. Not affected by curation or space scoping —
// health metrics are cross-cutting personal signals, same as AI activity.
func (h *Handler) HealthActivity(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 30)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	return h.cachedJSON(c, s.cacheKey("health-activity", s.t0, s.t1), func() (any, error) {
		return h.DB.GetHealthActivity(s.ctx, s.owner, s.t0, s.t1)
	})
}

// WorkoutList: GET /api/v1/users/current/workouts?start&end.
// Per-workout event list + per-label breakdown for the Wellness page. Not a
// /stats/ endpoint because it returns raw event rows (with timestamps and
// heartbeat ids), not aggregates. Named WorkoutList (not Workouts) so it
// doesn't collide with the POST ingest handler in handler/workouts.go.
// 30s cached like the other dashboard feeds.
func (h *Handler) WorkoutList(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 30)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	return h.cachedJSON(c, s.cacheKey("workouts", s.t0, s.t1), func() (any, error) {
		return h.DB.GetWorkouts(s.ctx, s.owner, s.t0, s.t1)
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
