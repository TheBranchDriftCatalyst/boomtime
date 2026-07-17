package handler

import (
	"net/http"
	"strconv"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/labstack/echo/v5"
)

// Workouts ingests a single workout: POST /api/v1/users/current/workouts.
// Wraps the same singleton-vs-bulk split as heartbeats so the companion app
// can start with one-off POSTs before batching.
func (h *Handler) Workouts(c *echo.Context) error {
	var w model.WorkoutPayload
	if err := c.Bind(&w); err != nil {
		return respondErr(c, apierr.BadRequest("Invalid request body"))
	}
	return h.storeWorkouts(c, []model.WorkoutPayload{w})
}

// WorkoutsBulk ingests many workouts: POST /api/v1/users/current/workouts.bulk.
// Accepts the {"data": [...]} envelope the Swift companion produces from
// HKAnchoredObjectQuery batches; a bare array is also tolerated for parity
// with the heartbeats endpoint (some ad-hoc callers may prefer it).
func (h *Handler) WorkoutsBulk(c *echo.Context) error {
	var env model.WorkoutBulkRequest
	if err := c.Bind(&env); err != nil {
		// Fall back to a bare array — callers using curl -d "[...]" won't
		// wrap in {"data":...} and there's no reason to reject them.
		var arr []model.WorkoutPayload
		if err2 := c.Bind(&arr); err2 != nil {
			return respondErr(c, apierr.BadRequest("Invalid request body"))
		}
		env.Data = arr
	}
	return h.storeWorkouts(c, env.Data)
}

func (h *Handler) storeWorkouts(c *echo.Context, ws []model.WorkoutPayload) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()

	ids, err := h.DB.SaveWorkouts(ctx, owner, ws)
	if err != nil {
		h.Logger.Error("failed to store workouts", "err", err)
		return respondErr(c, apierr.Generic())
	}

	// Bust cached dashboard payloads so the Wellness card / Overview picks up
	// the new workouts on the next fetch instead of waiting out the 30s TTL.
	h.invalidateOwnerCache(owner)

	responses := make([][]any, len(ids))
	for i, id := range ids {
		responses[i] = []any{
			model.HeartbeatData{Data: model.HeartbeatID{ID: strconv.FormatInt(id, 10)}},
			201,
		}
	}
	return c.JSON(http.StatusAccepted, model.BulkHeartbeatData{Responses: responses})
}
