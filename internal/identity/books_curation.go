package identity

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
)

// books_curation.go — PATCH /api/v1/books/items/:externalId/curation?source=<src>.
// The user-driven writer of the reading_items curation OVERRIDE layer (migration
// 00069): it stamps the chosen status/rating/finish onto the override columns
// (never the Amazon-derived layer) and enqueues a per-item Hardcover push so the
// choice mirrors out. Owner-scoped; the row is keyed by owner + ?source= (kindle|
// audible) + :externalId (the ASIN) — the same key ingest/pull use.

// curationBody is the PATCH request body. Each field is OPTIONAL and json.RawMessage
// so absence (leave the override untouched) is distinguishable from an explicit
// null (clear the override back to the Amazon-derived value):
//
//	{}                         → no-op curation (still stamps curation_updated_at)
//	{"status":"dnf"}           → set the status override
//	{"rating":null}            → clear the rating override
//	{"finishedAt":"2024-..."}  → set the finish-date override
type curationBody struct {
	Status     *json.RawMessage `json:"status"`
	Rating     *json.RawMessage `json:"rating"`
	FinishedAt *json.RawMessage `json:"finishedAt"`
}

// SetBookCuration handles PATCH /api/v1/books/items/:externalId/curation?source=.
func (h *Handler) SetBookCuration(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	externalID := c.Param("externalId")
	if externalID == "" {
		return apierr.New(http.StatusBadRequest, "missing item externalId", nil).Write(c)
	}
	source := c.QueryParam("source")
	if source == "" {
		return apierr.New(http.StatusBadRequest, "missing `source` query param (kindle|audible)", nil).Write(c)
	}

	var body curationBody
	if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
		return apierr.New(http.StatusBadRequest, "invalid request body", nil).Write(c)
	}

	patch, verr := body.toPatch()
	if verr != nil {
		return apierr.New(http.StatusBadRequest, verr.Error(), nil).Write(c)
	}

	it, err := h.DB.SetReadingItemCuration(c.Request().Context(), owner, source, externalID, patch)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.New(http.StatusNotFound, "reading item not found", nil).Write(c)
		}
		return apihelpers.InternalErr(h.Logger, c, "set book curation failed", err)
	}

	// Enqueue the outbound Hardcover curation push (dry-run-gated). Best-effort: the
	// override is already persisted, so an enqueue miss only delays the mirror (a
	// later sync reconciles); never fail the user's write on it.
	h.enqueueCurationPush(c, owner, it.Source, it.ExternalID)

	return c.JSON(http.StatusOK, toReadingItemDTO(it))
}

// toPatch converts the request body into a db.ReadingItemCurationPatch, decoding
// each present field and validating the status enum. A present field is written
// (Set*=true); an explicit null clears the override; an absent field is left alone.
func (b curationBody) toPatch() (db.ReadingItemCurationPatch, error) {
	var p db.ReadingItemCurationPatch

	if b.Status != nil {
		p.SetStatus = true
		if !isJSONNull(*b.Status) {
			var s string
			if err := json.Unmarshal(*b.Status, &s); err != nil {
				return p, errors.New("status must be a string")
			}
			if !db.CurationStatuses[s] {
				return p, errors.New("status must be one of want, reading, read, paused, dnf")
			}
			p.Status = &s
		}
	}

	if b.Rating != nil {
		p.SetRating = true
		if !isJSONNull(*b.Rating) {
			var r float64
			if err := json.Unmarshal(*b.Rating, &r); err != nil {
				return p, errors.New("rating must be a number")
			}
			p.Rating = &r
		}
	}

	if b.FinishedAt != nil {
		p.SetFinishedAt = true
		if !isJSONNull(*b.FinishedAt) {
			var s string
			if err := json.Unmarshal(*b.FinishedAt, &s); err != nil {
				return p, errors.New("finishedAt must be an RFC3339 string")
			}
			t, terr := time.Parse(time.RFC3339, s)
			if terr != nil {
				return p, errors.New("finishedAt must be an RFC3339 timestamp")
			}
			p.FinishedAt = &t
		}
	}

	return p, nil
}

// enqueueCurationPush best-effort enqueues the per-item Hardcover curation push.
func (h *Handler) enqueueCurationPush(c *echo.Context, owner, source, externalID string) {
	if h.JobEnqueuer == nil {
		return
	}
	payload, err := json.Marshal(hardcover.CurationPushPayload{Owner: owner, Source: source, ExternalID: externalID})
	if err != nil {
		h.Logger.Warn("curation push: marshal payload failed", "user", owner, "externalId", externalID, "err", err)
		return
	}
	if _, eerr := h.JobEnqueuer.Enqueue(c.Request().Context(), hardcover.CurationPushKind, payload,
		jobs.Owner(owner), jobs.MaxAttempts(3)); eerr != nil {
		h.Logger.Warn("curation push: enqueue failed", "user", owner, "externalId", externalID, "err", eerr)
	}
}

// isJSONNull reports whether a raw JSON value is the literal null.
func isJSONNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}
