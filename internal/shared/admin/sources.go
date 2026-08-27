package admin

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/labstack/echo/v5"
)

// sourceHealthResponse is GET /api/v1/users/current/sources/health.
//
// It was a map[string]any{"sources": ...} literal inside the CachedJSON
// compute closure; naming it is what lets the OpenAPI spec carry a real
// schema for the "Source health" panel payload. The json tag is byte-identical
// to the old map key, and Sources is populated from ListSourceHealth, which
// returns a non-nil empty slice — so an owner with no heartbeats still
// serialises as {"sources":[]} rather than {"sources":null}, the contract
// sources_test.go asserts.
type sourceHealthResponse struct {
	// Sources is one entry per (plugin, machine) pair, stalest-first.
	Sources []db.SourceHealth `json:"sources"`
}

// SourceHealth: GET /api/v1/users/current/sources/health
// Lists every ingestion source (editor/plugin/machine value) with its last
// check-in (raw MAX(time_sent)) and heartbeat count, stalest-first. Powers the
// Heartbeats "Source health" panel — the "is my wakatime plugin still
// reporting" view. Read-only, owner-scoped, and cached like other reads. The
// active/idle/stale/silent status is derived CLIENT-side from lastSeen.
//
// TYPED SEAM NOTE (routes.go registers this via apiroute.WritesJSON): the
// handler keeps writing its own JSON because apihelpers.CachedJSON serves
// PRE-MARSHALLED bytes straight from the TTL cache. WritesJSON declares the
// response type for the spec without adding an unmarshal/re-marshal round trip
// on every cache hit.
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
		return sourceHealthResponse{Sources: sources}, nil
	})
}
