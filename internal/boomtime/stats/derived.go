package stats

import (
	"fmt"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/labstack/echo/v5"
)

// DerivedStatus: GET /api/v1/users/current/derived/status — health of the
// precomputed gap_seconds column and hb_rollup_daily rollup for the user.
func (h *Handler) DerivedStatus(c *echo.Context) (db.DerivedStatus, error) {
	var out db.DerivedStatus
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	s, err := h.DB.GetDerivedStatus(c.Request().Context(), owner)
	if err != nil {
		return out, fmt.Errorf("derived status failed: %w", err)
	}
	return s, nil
}

// DerivedResync: POST /api/v1/users/current/derived/resync — rebuild gap_seconds
// + rollup from raw heartbeats, then return the refreshed status.
func (h *Handler) DerivedResync(c *echo.Context) (db.DerivedStatus, error) {
	var out db.DerivedStatus
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	ctx := c.Request().Context()
	if err := h.DB.ResyncDerived(ctx, owner); err != nil {
		return out, fmt.Errorf("derived resync failed: %w", err)
	}
	// Bust cached aggregates so the dashboard reflects the resynced data.
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
	s, err := h.DB.GetDerivedStatus(ctx, owner)
	if err != nil {
		// Deliberately unlogged (matches the pre-seam handler): the resync
		// itself succeeded, only the confirmation read failed.
		return out, apierr.Generic()
	}
	return s, nil
}
