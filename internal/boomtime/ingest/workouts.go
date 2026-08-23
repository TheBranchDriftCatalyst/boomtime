package ingest

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/labstack/echo/v5"
)

// Workouts ingests a single workout: POST /api/v1/users/current/workouts.
// Wraps the same singleton-vs-bulk split as heartbeats so the companion app
// can start with one-off POSTs before batching. Body capped at apihelpers.BodyLimitLarge
// (boom-d6x.handler critique fix).
func (h *Handler) Workouts(c *echo.Context) error {
	var w model.WorkoutPayload
	if aerr := apihelpers.BindJSONWithLimit(c, &w, apihelpers.BodyLimitLarge); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	return h.storeWorkouts(c, []model.WorkoutPayload{w})
}

// WorkoutsBulk ingests many workouts: POST /api/v1/users/current/workouts.bulk.
// Accepts the {"data": [...]} envelope the Swift companion produces from
// HKAnchoredObjectQuery batches; a bare array is also tolerated for parity
// with the heartbeats endpoint (some ad-hoc callers may prefer it).
//
// Body is buffered ONCE (under a MaxBytesReader cap) so both parse attempts
// (envelope form, then bare array) see the same bytes — echo's DefaultBinder
// reads c.Request().Body directly (json.NewDecoder), so the second Bind would
// otherwise see an empty reader and 400 on any bare-array payload
// (boom-d6x.handler critique). The MaxBytesReader also prevents OOM via
// oversized ingest.
func (h *Handler) WorkoutsBulk(c *echo.Context) error {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, apihelpers.BodyLimitLarge)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// http.MaxBytesReader signals oversize via "http: request body too
		// large"; render 413 rather than 400 so the client can distinguish.
		if err.Error() == "http: request body too large" {
			return apihelpers.RespondErr(c, apierr.New(http.StatusRequestEntityTooLarge, "payload too large", nil))
		}
		return apihelpers.RespondErr(c, apierr.BadRequest("Invalid request body"))
	}
	var env model.WorkoutBulkRequest
	if err := json.Unmarshal(body, &env); err != nil || env.Data == nil {
		// Fall back to a bare array — callers using curl -d "[...]" won't
		// wrap in {"data":...} and there's no reason to reject them.
		var arr []model.WorkoutPayload
		if err2 := json.Unmarshal(body, &arr); err2 != nil {
			return apihelpers.RespondErr(c, apierr.BadRequest("Invalid request body"))
		}
		env.Data = arr
	}
	return h.storeWorkouts(c, env.Data)
}

func (h *Handler) storeWorkouts(c *echo.Context, ws []model.WorkoutPayload) error {
	// auth-dry Phase 2: CapIngestHeartbeats is enforced by RequireCap route
	// middleware (ingest/routes.go) before this runs — the handler just needs
	// the owner. IdentifyOwner is a cached read (Phase 1).
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	ctx := c.Request().Context()

	ids, err := h.DB.SaveWorkouts(ctx, owner, ws)
	if err != nil {
		h.Logger.Error("failed to store workouts", "err", err)
		return apihelpers.RespondErr(c, apierr.Generic())
	}

	// Bust cached dashboard payloads so the Wellness card / Overview picks up
	// the new workouts on the next fetch instead of waiting out the 30s TTL.
	apihelpers.InvalidateOwnerCache(h.Cache, owner)

	responses := make([][]any, len(ids))
	for i, id := range ids {
		responses[i] = []any{
			model.HeartbeatData{Data: model.HeartbeatID{ID: strconv.FormatInt(id, 10)}},
			201,
		}
	}
	return c.JSON(http.StatusAccepted, model.BulkHeartbeatData{Responses: responses})
}
