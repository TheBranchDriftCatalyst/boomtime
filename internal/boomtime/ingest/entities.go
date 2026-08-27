// entities.go: Entity Explorer endpoints (boom-90x). Read a flat per-ty list
// of every entity value the owner has, plus a REDACT that blanks the entity
// column on individually-selected rows — the heartbeat rows themselves stay
// (contributing to project/language/machine totals), only the specific
// entity value is scrubbed from audit views. Guarded by ?confirm=<magic>,
// same belt-and-braces the DB restore endpoint uses so a stray fetch can't
// silently scrub rows.
package ingest

import (
	"errors"
	"fmt"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
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

// listEntitiesResponse is the 200 body for ListEntitiesByType.
type listEntitiesResponse struct {
	// Entities is one row per distinct non-empty entity value, count desc.
	Entities []db.EntitySummary `json:"entities"`
	// Truncated reports that the result hit the limit — the FE prompts the
	// user to filter rather than silently showing a partial list.
	Truncated bool `json:"truncated"`
}

// ListEntitiesByType: GET /api/v1/users/current/heartbeats/entities?type=file&limit=500.
func (h *Handler) ListEntitiesByType(c *echo.Context) (listEntitiesResponse, error) {
	var out listEntitiesResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	ty := c.QueryParam("type")
	if !validEntityTypes[ty] {
		return out, apierr.BadRequest("type must be one of file/app/domain/url")
	}
	limit := int(apihelpers.QueryInt64(c, "limit", entityListDefaultLimit))
	if limit < 1 {
		limit = entityListDefaultLimit
	}
	if limit > entityListMaxLimit {
		limit = entityListMaxLimit
	}

	entities, truncated, err := h.DB.ListEntitiesByType(c.Request().Context(), owner, ty, limit)
	if err != nil {
		return out, fmt.Errorf("list entities failed: %w", err)
	}
	return listEntitiesResponse{
		Entities:  entities,
		Truncated: truncated,
	}, nil
}

// redactEntitiesBody is the JSON payload for the redact endpoint.
type redactEntitiesBody struct {
	Ty       string   `json:"ty"`
	Entities []string `json:"entities"`
}

// redactEntitiesResponse is the 200 body for RedactEntities.
type redactEntitiesResponse struct {
	// Redacted is the number of heartbeat rows whose entity column was blanked.
	Redacted int64 `json:"redacted"`
}

// RedactEntities: POST /api/v1/users/current/heartbeats/entities/redact?confirm=redact-entities.
// Body: {ty, entities[]}. Blanks the entity column (”) on every matching row,
// owner-scoped. The heartbeat still counts toward every other axis; only the
// entity value is scrubbed. Rollup unaffected (entity isn't a rollup axis).
func (h *Handler) RedactEntities(c *echo.Context) (redactEntitiesResponse, error) {
	var out redactEntitiesResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	if c.QueryParam("confirm") != entityRedactConfirm {
		return out, apierr.BadRequest("missing confirm=redact-entities — this endpoint scrubs the entity column on heartbeat rows")
	}
	var body redactEntitiesBody
	// boom-bi2: 64 KiB cap — batches are bounded to 500 entities; each entity
	// is a short URL/path/app name. Medium fits comfortably (500 * ~120 chars).
	// Bound HERE and not by the apiroute seam, which binds at
	// apihelpers.BodyLimitSmall (4 KiB) — too small for a 500-entity batch.
	if aerr := apihelpers.BindJSONWithLimit(c, &body, apihelpers.BodyLimitMedium); aerr != nil {
		return out, aerr
	}
	if !validEntityTypes[body.Ty] {
		return out, apierr.BadRequest("ty must be one of file/app/domain/url")
	}
	if len(body.Entities) == 0 {
		return out, apierr.BadRequest("entities is required and must be non-empty")
	}
	if len(body.Entities) > entityRedactBatchMax {
		return out, apierr.BadRequest("entities batch too large; redact in chunks of at most 500")
	}

	redacted, err := h.DB.RedactEntities(c.Request().Context(), owner, body.Ty, body.Entities)
	if err != nil {
		// Rare edge: two selected entities share the same (sender, time_sent)
		// so both would land on the same (sender, time_sent, '') unique key.
		// Surface as a friendly 400 so the FE can prompt "try one entity at
		// a time".
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return out, apierr.BadRequest("timestamp collision: two of the selected entities share the same (sender, time_sent). Try redacting one entity at a time.")
		}
		return out, fmt.Errorf("redact entities failed: %w", err)
	}
	// Aggregations grouped by entity are stale; explore views need refresh.
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
	return redactEntitiesResponse{Redacted: redacted}, nil
}
