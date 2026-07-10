package handler

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/apierr"
	"github.com/labstack/echo/v5"
)

// DerivedStatus: GET /api/v1/users/current/derived/status — health of the
// precomputed gap_seconds column and hb_rollup_daily rollup for the user.
func (h *Handler) DerivedStatus(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	s, err := h.DB.GetDerivedStatus(c.Request().Context(), owner)
	if err != nil {
		h.Logger.Error("derived status failed", "err", err)
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, s)
}

// DerivedResync: POST /api/v1/users/current/derived/resync — rebuild gap_seconds
// + rollup from raw heartbeats, then return the refreshed status.
func (h *Handler) DerivedResync(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()
	if err := h.DB.ResyncDerived(ctx, owner); err != nil {
		h.Logger.Error("derived resync failed", "err", err)
		return respondErr(c, apierr.Generic())
	}
	// Bust cached aggregates so the dashboard reflects the resynced data.
	h.Cache.InvalidatePrefix(owner + "|")
	s, err := h.DB.GetDerivedStatus(ctx, owner)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, s)
}
