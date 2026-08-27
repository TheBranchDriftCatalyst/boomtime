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
func (h *Handler) HealthSamples(c *echo.Context) (healthSamplesResponse, error) {
	var out healthSamplesResponse
	var s model.HealthSamplePayload
	// Bound here rather than by the apiroute seam: the seam binds at
	// apihelpers.BodyLimitSmall (4 KiB) and this endpoint keeps its 8 MiB cap.
	if aerr := apihelpers.BindJSONWithLimit(c, &s, apihelpers.BodyLimitLarge); aerr != nil {
		return out, aerr
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
func (h *Handler) HealthSamplesBulk(c *echo.Context) (healthSamplesResponse, error) {
	var out healthSamplesResponse
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, apihelpers.BodyLimitLarge)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if err.Error() == "http: request body too large" {
			return out, apierr.New(http.StatusRequestEntityTooLarge, "payload too large", nil)
		}
		return out, apierr.BadRequest("Invalid request body")
	}
	var env model.HealthSampleBulkRequest
	if err := json.Unmarshal(body, &env); err != nil || env.Data == nil {
		var arr []model.HealthSamplePayload
		if err2 := json.Unmarshal(body, &arr); err2 != nil {
			return out, apierr.BadRequest("Invalid request body")
		}
		env.Data = arr
	}
	return h.storeSamples(c, env.Data)
}

// healthSamplesResponse is the 202 body for POST
// /api/v1/users/current/health_samples and .../health_samples.bulk.
type healthSamplesResponse struct {
	// Accepted is the number of samples persisted by SaveHealthSamples.
	Accepted int `json:"accepted"`
}

func (h *Handler) storeSamples(c *echo.Context, ss []model.HealthSamplePayload) (healthSamplesResponse, error) {
	var out healthSamplesResponse
	// auth-dry Phase 2: CapIngestHeartbeats is enforced by RequireCap route
	// middleware (ingest/routes.go); the handler only needs the owner.
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	ctx := c.Request().Context()

	n, err := h.DB.SaveHealthSamples(ctx, owner, ss)
	if err != nil {
		h.Logger.Error("failed to store health samples", "err", err)
		return out, apierr.Generic()
	}

	apihelpers.InvalidateOwnerCache(h.Cache, owner)

	return healthSamplesResponse{Accepted: n}, nil
}
