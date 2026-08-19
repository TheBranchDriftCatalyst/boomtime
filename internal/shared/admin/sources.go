package admin

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/labstack/echo/v5"
)

// SourceHealth: GET /api/v1/users/current/sources/health
// Lists every ingestion source (editor/plugin/machine value) with its last
// check-in (raw MAX(time_sent)) and heartbeat count, stalest-first. Powers the
// Heartbeats "Source health" panel — the "is my wakatime plugin still
// reporting" view. Read-only, owner-scoped, and cached like other reads. The
// active/idle/stale/silent status is derived CLIENT-side from lastSeen.
func (h *Handler) SourceHealth(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	key := apihelpers.CacheKey(owner, "sources-health")
	return apihelpers.CachedJSON(h.Cache, h.Logger, c, key, func() (any, error) {
		sources, err := h.DB.ListSourceHealth(c.Request().Context(), owner)
		if err != nil {
			return nil, err
		}
		return map[string]any{"sources": sources}, nil
	})
}
