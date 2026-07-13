// entities.go: Entity Explorer endpoints (gaka-90x). Read a flat per-ty list
// of every entity value the owner has, plus a REDACT that blanks the entity
// column on individually-selected rows — the heartbeat rows themselves stay
// (contributing to project/language/machine totals), only the specific
// entity value is scrubbed from audit views. Guarded by ?confirm=<magic>,
// same belt-and-braces the DB restore endpoint uses so a stray fetch can't
// silently scrub rows.
package handler

import (
	"errors"
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
)

// entityRedactConfirm is the ?confirm= sentinel required to hit the redact
// endpoint. Kept short + descriptive so a curl user sees exactly what
// they'd be doing.
const entityRedactConfirm = "redact-entities"

// Entity list bounds. maxLimit protects both the DB (a single sender with
// hundreds of thousands of URL heartbeats would return one row per unique
// URL) and the FE (past a few thousand rows a virtualized table starts
// creaking). redactBatchMax caps one request so an accidental "select all"
// doesn't scrub the world in one shot.
const (
	entityListDefaultLimit = 500
	entityListMaxLimit     = 5000
	entityRedactBatchMax   = 500
)

// validEntityTypes is the whitelist the handler accepts for ?type= and the
// redact body's ty. Mirrors internal/db entity type constants.
var validEntityTypes = map[string]bool{
	db.EntityTypeFile:   true,
	db.EntityTypeApp:    true,
	db.EntityTypeDomain: true,
	db.EntityTypeURL:    true,
}

// ListEntitiesByType: GET /api/v1/users/current/heartbeats/entities?type=file&limit=500.
func (h *Handler) ListEntitiesByType(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ty := c.QueryParam("type")
	if !validEntityTypes[ty] {
		return respondErr(c, apierr.BadRequest("type must be one of file/app/domain/url"))
	}
	limit := int(queryInt64(c, "limit", entityListDefaultLimit))
	if limit < 1 {
		limit = entityListDefaultLimit
	}
	if limit > entityListMaxLimit {
		limit = entityListMaxLimit
	}

	entities, truncated, err := h.DB.ListEntitiesByType(c.Request().Context(), owner, ty, limit)
	if err != nil {
		return h.internalErr(c, "list entities failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"entities":  entities,
		"truncated": truncated,
	})
}

// redactEntitiesBody is the JSON payload for the redact endpoint.
type redactEntitiesBody struct {
	Ty       string   `json:"ty"`
	Entities []string `json:"entities"`
}

// RedactEntities: POST /api/v1/users/current/heartbeats/entities/redact?confirm=redact-entities.
// Body: {ty, entities[]}. Blanks the entity column ('') on every matching row,
// owner-scoped. The heartbeat still counts toward every other axis; only the
// entity value is scrubbed. Rollup unaffected (entity isn't a rollup axis).
func (h *Handler) RedactEntities(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	if c.QueryParam("confirm") != entityRedactConfirm {
		return respondErr(c, apierr.BadRequest("missing confirm=redact-entities — this endpoint scrubs the entity column on heartbeat rows"))
	}
	var body redactEntitiesBody
	if err := c.Bind(&body); err != nil {
		return respondErr(c, apierr.BadRequest("invalid JSON body"))
	}
	if !validEntityTypes[body.Ty] {
		return respondErr(c, apierr.BadRequest("ty must be one of file/app/domain/url"))
	}
	if len(body.Entities) == 0 {
		return respondErr(c, apierr.BadRequest("entities is required and must be non-empty"))
	}
	if len(body.Entities) > entityRedactBatchMax {
		return respondErr(c, apierr.BadRequest("entities batch too large; redact in chunks of at most 500"))
	}

	redacted, err := h.DB.RedactEntities(c.Request().Context(), owner, body.Ty, body.Entities)
	if err != nil {
		// Rare edge: two selected entities share the same (sender, time_sent)
		// so both would land on the same (sender, time_sent, '') unique key.
		// Surface as a friendly 400 so the FE can prompt "try one entity at
		// a time".
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return respondErr(c, apierr.BadRequest("timestamp collision: two of the selected entities share the same (sender, time_sent). Try redacting one entity at a time."))
		}
		return h.internalErr(c, "redact entities failed", err)
	}
	// Aggregations grouped by entity are stale; explore views need refresh.
	h.invalidateOwnerCache(owner)
	return c.JSON(http.StatusOK, map[string]any{"redacted": redacted})
}
