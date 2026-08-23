package ingest

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/labstack/echo/v5"
)

// HealthSamples ingests a single HealthKit sample:
// POST /api/v1/users/current/health_samples. Body capped at apihelpers.BodyLimitLarge
// (boom-d6x.handler critique fix).
func (h *Handler) HealthSamples(c *echo.Context) error {
	var s model.HealthSamplePayload
	if aerr := apihelpers.BindJSONWithLimit(c, &s, apihelpers.BodyLimitLarge); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
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
// (boom-d6x.handler critique). MaxBytesReader also prevents OOM via
// oversized ingest.
func (h *Handler) HealthSamplesBulk(c *echo.Context) error {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, apihelpers.BodyLimitLarge)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if err.Error() == "http: request body too large" {
			return apihelpers.RespondErr(c, apierr.New(http.StatusRequestEntityTooLarge, "payload too large", nil))
		}
		return apihelpers.RespondErr(c, apierr.BadRequest("Invalid request body"))
	}
	var env model.HealthSampleBulkRequest
	if err := json.Unmarshal(body, &env); err != nil || env.Data == nil {
		var arr []model.HealthSamplePayload
		if err2 := json.Unmarshal(body, &arr); err2 != nil {
			return apihelpers.RespondErr(c, apierr.BadRequest("Invalid request body"))
		}
		env.Data = arr
	}
	return h.storeSamples(c, env.Data)
}

func (h *Handler) storeSamples(c *echo.Context, ss []model.HealthSamplePayload) error {
	// auth-dry Phase 2: CapIngestHeartbeats is enforced by RequireCap route
	// middleware (ingest/routes.go); the handler only needs the owner.
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	ctx := c.Request().Context()

	n, err := h.DB.SaveHealthSamples(ctx, owner, ss)
	if err != nil {
		h.Logger.Error("failed to store health samples", "err", err)
		return apihelpers.RespondErr(c, apierr.Generic())
	}

	apihelpers.InvalidateOwnerCache(h.Cache, owner)

	return c.JSON(http.StatusAccepted, map[string]any{"accepted": n})
}
