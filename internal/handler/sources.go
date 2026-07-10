package handler

import (
	"github.com/labstack/echo/v5"
)

// SourceHealth: GET /api/v1/users/current/sources/health
// Lists every ingestion source (editor/plugin/machine value) with its last
// check-in (raw MAX(time_sent)) and heartbeat count, stalest-first. Powers the
// Heartbeats "Source health" panel — the "is my wakatime plugin still
// reporting" view. Read-only, owner-scoped, and cached like other reads. The
// active/idle/stale/silent status is derived CLIENT-side from lastSeen.
func (h *Handler) SourceHealth(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	key := cacheKey(owner, "sources-health")
	return h.cachedJSON(c, key, func() (any, error) {
		sources, err := h.DB.SourceHealth(c.Request().Context(), owner)
		if err != nil {
			return nil, err
		}
		return map[string]any{"sources": sources}, nil
	})
}
