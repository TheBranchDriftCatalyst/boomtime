package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
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
// The body is bound by the typed seam (apiroute.PATCH), which also caps it at
// apihelpers.BodyLimitSmall — a curation patch is three optional scalars.
func (h *Handler) SetBookCuration(c *echo.Context, body curationBody) (readingItemDTO, error) {
	var out readingItemDTO
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}

	externalID := c.Param("externalId")
	if externalID == "" {
		return out, apierr.New(http.StatusBadRequest, "missing item externalId", nil)
	}
	source := c.QueryParam("source")
	if source == "" {
		return out, apierr.New(http.StatusBadRequest, "missing `source` query param (kindle|audible)", nil)
	}

	patch, verr := body.toPatch()
	if verr != nil {
		return out, apierr.New(http.StatusBadRequest, verr.Error(), nil)
	}

	it, err := h.DB.SetReadingItemCuration(c.Request().Context(), owner, source, externalID, patch)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, apierr.New(http.StatusNotFound, "reading item not found", nil)
		}
		return out, fmt.Errorf("set book curation failed: %w", err)
	}

	// Enqueue the outbound Hardcover curation push (dry-run-gated). Best-effort: the
	// override is already persisted, so an enqueue miss only delays the mirror (a
	// later sync reconciles); never fail the user's write on it.
	h.enqueueCurationPush(c, owner, it.Source, it.ExternalID)

	return toReadingItemDTO(it), nil
}

// PushBookToHardcover handles POST /api/v1/books/items/:externalId/push?source=.
// A push-only sibling of SetBookCuration: it changes NOTHING in the DB, it just
// re-enqueues the SAME per-item Hardcover curation push (reusing enqueueCurationPush
// — the exact path a curation edit takes) so the row's CURRENT effective state
// (status/finish/rating) mirrors out to Hardcover on demand. This is the per-row
// "sync this book to Hardcover now" button. Owner-scoped; keyed by owner + ?source=
// + :externalId. The push itself is dry-run-gated by BOOM_HARDCOVER_DRYRUN.
//
// NOT on the typed seam (internal/shared/apiroute), deliberately: this handler
// answers 200 with the full readingItemDTO on the inline path and 202
// {"enqueued":true} on the queue fallback. Two statuses AND two shapes cannot be
// expressed by one (Resp, status) registration, and inventing a merged struct
// would document a payload the handler never writes. Stays on plain e.POST.
func (h *Handler) PushBookToHardcover(c *echo.Context) error {
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

	// Confirm the row exists AND is matched — an unmatched book has no Hardcover
	// target, so a push would be a no-op; 409 tells the UI to disable the button.
	it, err := h.DB.GetReadingItem(c.Request().Context(), owner, source, externalID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.New(http.StatusNotFound, "reading item not found", nil).Write(c)
		}
		return apihelpers.InternalErr(h.Logger, c, "push book: load item failed", err)
	}
	if it.HardcoverBookID == nil {
		return apierr.New(http.StatusConflict, "book is not matched to Hardcover — match it first", nil).Write(c)
	}

	// Manual single-row sync: push INLINE (bypass the job queue) so the click gets an
	// immediate real result and the response carries the updated row (hardcover_status
	// advanced → the out-of-sync badge clears without a reload). Falls back to the
	// async queue when the inline service isn't wired.
	if h.HardcoverPush != nil {
		if perr := h.HardcoverPush.PushCuration(c.Request().Context(),
			hardcover.CurationPushPayload{Owner: owner, Source: it.Source, ExternalID: it.ExternalID}); perr != nil {
			return apihelpers.InternalErr(h.Logger, c, "hardcover sync push failed", perr)
		}
		// Re-read so the response reflects the just-advanced hardcover_status mirror.
		if fresh, gerr := h.DB.GetReadingItem(c.Request().Context(), owner, it.Source, it.ExternalID); gerr == nil {
			it = fresh
		}
		return c.JSON(http.StatusOK, toReadingItemDTO(it))
	}

	h.enqueueCurationPush(c, owner, it.Source, it.ExternalID)
	return c.JSON(http.StatusAccepted, map[string]any{"enqueued": true})
}

// DeleteReadingEvent handles DELETE /api/v1/books/reads/:id. Removes one read from
// the local reading_events history AND, when it originated on Hardcover, deletes the
// corresponding user_book_read on the user's Hardcover account (dry-run-gated). This
// is how a user prunes junk/empty reads (or a finish they undid) — the delete
// propagates both ways. Owner-scoped.

// deleteReadingEventResponse is DELETE /api/v1/books/reads/:id. hardcoverDeleted
// reports whether the Hardcover-side user_book_read was pruned too (false when
// the read did not originate there, or the remote delete was a best-effort miss).
type deleteReadingEventResponse struct {
	Deleted          bool `json:"deleted"`
	HardcoverDeleted bool `json:"hardcoverDeleted"`
}

func (h *Handler) DeleteReadingEvent(c *echo.Context) (deleteReadingEventResponse, error) {
	var out deleteReadingEventResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	id, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil || id <= 0 {
		return out, apierr.New(http.StatusBadRequest, "invalid read id", nil)
	}

	origin, externalReadID, ok, err := h.DB.DeleteReadingEvent(c.Request().Context(), owner, id)
	if err != nil {
		return out, fmt.Errorf("delete reading event failed: %w", err)
	}
	if !ok {
		return out, apierr.New(http.StatusNotFound, "read not found", nil)
	}

	// Propagate to Hardcover when the read came from there. Best-effort: the local
	// row is already gone; a remote miss is logged, not surfaced as a failure.
	hardcoverDeleted := false
	if origin == db.ReadingEventOriginHardcover && h.HardcoverPush != nil {
		if rid, cErr := strconv.ParseInt(externalReadID, 10, 64); cErr == nil && rid > 0 {
			if dErr := h.HardcoverPush.DeleteHardcoverRead(c.Request().Context(), owner, rid); dErr != nil {
				h.Logger.Warn("delete read: hardcover propagation failed", "user", owner, "readId", rid, "err", dErr)
			} else {
				hardcoverDeleted = true
			}
		}
	}
	return deleteReadingEventResponse{Deleted: true, HardcoverDeleted: hardcoverDeleted}, nil
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
