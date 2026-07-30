package ingest

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/labstack/echo/v5"
)

// HealthSamples ingests a single HealthKit sample:
// POST /api/v1/users/current/health_samples. Body capped at BodyLimitLarge
// (gaka-d6x.handler critique fix).
func (h *Handler) HealthSamples(c *echo.Context) error {
	var s model.HealthSamplePayload
	if aerr := BindJSONWithLimit(c, &s, BodyLimitLarge); aerr != nil {
		return respondErr(c, aerr)
	}
	return h.storeSamples(c, []model.HealthSamplePayload{s})
}

// HealthSamplesBulk ingests many samples:
// POST /api/v1/users/current/health_samples.bulk.
// Envelope-or-array tolerant, same as WorkoutsBulk.
//
// Body is buffered ONCE (under a MaxBytesReader cap) so both parse attempts
// (envelope form, then bare array) see the same bytes — echo's DefaultBinder
// reads c.Request().Body directly (json.NewDecoder), so the second Bind
// would otherwise see an empty reader and 400 on any bare-array payload
// (gaka-d6x.handler critique). MaxBytesReader also prevents OOM via
// oversized ingest.
func (h *Handler) HealthSamplesBulk(c *echo.Context) error {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, BodyLimitLarge)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if err.Error() == "http: request body too large" {
			return respondErr(c, apierr.New(http.StatusRequestEntityTooLarge, "payload too large", nil))
		}
		return respondErr(c, apierr.BadRequest("Invalid request body"))
	}
	var env model.HealthSampleBulkRequest
	if err := json.Unmarshal(body, &env); err != nil || env.Data == nil {
		var arr []model.HealthSamplePayload
		if err2 := json.Unmarshal(body, &arr); err2 != nil {
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
