// loc.go — HTTP surface for the Lines-of-Code dashboard (gaka-yfg).
//
// GET /api/v1/users/current/stats/loc returns total + per-project lines of
// code (current snapshot) plus a bounded total-LOC-over-time growth curve, all
// derived from heartbeats.file_lines with the generated/vendored ignore filter
// applied in the DB layer (internal/db/loc.go). NO GitHub dependency.
//
// Scope wiring mirrors Stats: dashboardScope resolves owner + range, CachedJSON
// caches the payload under the same range/space cache-key convention, and the
// hide-exclusion + Space scope are applied query-side (loadHidden). Renames are
// not applied (documented on the DB query). Empty data degrades to an empty
// payload — the FE renders a gentle empty state, never an error.
package stats

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/labstack/echo/v5"
)

// Loc: GET /api/v1/users/current/stats/loc?start&end&space.
func (h *Handler) Loc(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 7)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	return apihelpers.CachedJSON(h.Cache, h.Logger, c, s.cacheKey("loc", s.t0, s.t1), func() (any, error) {
		// Hide exclusion + Space scope (query-side). No rename set: LOC groups on
		// the raw project name, so loadRenames would be a no-op cost here.
		l, err := s.load(loadHidden)
		if err != nil {
			return nil, err
		}
		perProject, total, err := h.DB.GetProjectLoc(s.ctx, s.owner, s.t0, s.t1, l.hidden, l.members, l.spaceRequested)
		if err != nil {
			return nil, err
		}
		overTime, err := h.DB.GetLocOverTime(s.ctx, s.owner, s.t0, s.t1, l.hidden, l.members, l.spaceRequested)
		if err != nil {
			return nil, err
		}
		return ToLocPayload(total, perProject, overTime), nil
	})
}

// ToLocPayload assembles the LOC response, normalizing nil slices to empty ones
// so the JSON always carries `perProject: []` / `overTime: []` (never null) —
// the FE treats an empty payload as the gentle "no file-lines data yet" state.
func ToLocPayload(total int64, perProject []model.LocProject, overTime []model.LocPoint) model.LocPayload {
	if perProject == nil {
		perProject = []model.LocProject{}
	}
	if overTime == nil {
		overTime = []model.LocPoint{}
	}
	return model.LocPayload{
		TotalLoc:   total,
		PerProject: perProject,
		OverTime:   overTime,
	}
}
