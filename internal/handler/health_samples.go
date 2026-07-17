package handler

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/labstack/echo/v5"
)

// HealthSamples ingests a single HealthKit sample:
// POST /api/v1/users/current/health_samples.
func (h *Handler) HealthSamples(c *echo.Context) error {
	var s model.HealthSamplePayload
	if err := c.Bind(&s); err != nil {
		return respondErr(c, apierr.BadRequest("Invalid request body"))
	}
	return h.storeSamples(c, []model.HealthSamplePayload{s})
}

// HealthSamplesBulk ingests many samples:
// POST /api/v1/users/current/health_samples.bulk.
// Envelope-or-array tolerant, same as WorkoutsBulk.
func (h *Handler) HealthSamplesBulk(c *echo.Context) error {
	var env model.HealthSampleBulkRequest
	if err := c.Bind(&env); err != nil {
		var arr []model.HealthSamplePayload
		if err2 := c.Bind(&arr); err2 != nil {
			return respondErr(c, apierr.BadRequest("Invalid request body"))
		}
		env.Data = arr
	}
	return h.storeSamples(c, env.Data)
}

func (h *Handler) storeSamples(c *echo.Context, ss []model.HealthSamplePayload) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()

	n, err := h.DB.SaveHealthSamples(ctx, owner, ss)
	if err != nil {
		h.Logger.Error("failed to store health samples", "err", err)
		return respondErr(c, apierr.Generic())
	}

	h.invalidateOwnerCache(owner)

	return c.JSON(http.StatusAccepted, map[string]any{"accepted": n})
}
