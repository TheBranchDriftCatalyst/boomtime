package stats

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/labstack/echo/v5"
)

// activeFilesDefaultLimit / activeFilesMaxLimit bound the ?limit param.
const (
	activeFilesDefaultLimit = 20
	activeFilesMaxLimit     = 100
)

// ActiveFiles: GET /api/v1/users/current/files?start&end&timeLimit&limit.
// Top FILES across ALL of the owner's projects, each with its attributed time
// and how many DISTINCT projects touch it — surfacing shared interfaces /
// lynchpins that span projects (projects>1). Curation-aware: hidden projects are
// excluded and rename rules are applied before the distinct-project count, so the
// aggregate matches the dashboards.
func (h *Handler) ActiveFiles(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 7)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	limit := int(apihelpers.QueryInt64(c, "limit", activeFilesDefaultLimit))
	if limit < 1 {
		limit = activeFilesDefaultLimit
	}
	if limit > activeFilesMaxLimit {
		limit = activeFilesMaxLimit
	}

	return apihelpers.CachedJSON(h.Cache, h.Logger, c, s.cacheKey("files", s.t0, s.t1, s.limit, limit), func() (any, error) {
		l, err := s.load(loadHidden | loadRenames)
		if err != nil {
			return nil, err
		}
		rows, truncated, err := h.DB.GetActiveFiles(s.ctx, s.owner, s.t0, s.t1, s.limit, limit, l.hidden, l.renames, l.members, l.spaceRequested)
		if err != nil {
			return nil, err
		}
		return ToActiveFilesPayload(rows, truncated), nil
	})
}
