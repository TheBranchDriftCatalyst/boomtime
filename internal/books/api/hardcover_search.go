package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
)

// hardcover_search.go — the interactive manual match-fixer:
//   GET  /api/v1/hardcover/search?q=<text>                          (autocomplete)
//   POST /api/v1/books/items/:externalId/match?source=<src>         (apply pick)
//
// The autocomplete live-queries Hardcover's Typesense search and returns
// descriptive candidate cards; picking one writes a MANUAL reading_items linkage
// (confidence "manual") so the ~93% of books the automated ladder can't confidently
// resolve can be fixed by hand. Both owner-scoped; search is read-only, the link
// write only touches the per-row linkage (never the global match cache — a human's
// pick is authoritative for THEIR row, not necessarily correct for every user).

// hardcoverSearchResponse is GET /api/v1/hardcover/search. candidates is ALWAYS
// an array (never null) — the autocomplete iterates it unguarded.
type hardcoverSearchResponse struct {
	Candidates []hardcover.Candidate `json:"candidates"`
}

// HardcoverSearch handles GET /api/v1/hardcover/search?q=<text>&limit=<n>.
func (h *Handler) HardcoverSearch(c *echo.Context) (hardcoverSearchResponse, error) {
	var out hardcoverSearchResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	q := c.QueryParam("q")
	if len(q) < 2 {
		// Too short to search meaningfully — return an empty set, not an error, so
		// the autocomplete stays quiet until the user has typed something.
		return hardcoverSearchResponse{Candidates: []hardcover.Candidate{}}, nil
	}

	svc := hardcover.NewSyncService(h.DB, hardcover.NewStore(h.DB), h.Logger)
	cands, connected, err := svc.SearchForOwner(c.Request().Context(), owner, q, 8)
	if err != nil {
		if errors.Is(err, hardcover.ErrBadToken) {
			return out, apierr.New(http.StatusBadGateway, "hardcover token rejected — reconnect Hardcover", nil)
		}
		if errors.Is(err, hardcover.ErrRateLimited) {
			return out, apierr.New(http.StatusTooManyRequests, "hardcover rate limit — try again shortly", nil)
		}
		return out, fmt.Errorf("hardcover search failed: %w", err)
	}
	if !connected {
		return out, apierr.New(http.StatusPreconditionFailed, "connect Hardcover first to search its catalog", nil)
	}
	if cands == nil {
		cands = []hardcover.Candidate{}
	}
	return hardcoverSearchResponse{Candidates: cands}, nil
}

// manualMatchBody is the POST body: the chosen Hardcover book. editionId + slug are
// optional (the FE search card carries the slug); when absent we resolve the edition
// server-side so the linkage is complete.
type manualMatchBody struct {
	HardcoverBookID int64  `json:"hardcoverBookId"`
	EditionID       int64  `json:"editionId"`
	Slug            string `json:"slug"`
}

// SetBookManualMatch handles POST /api/v1/books/items/:externalId/match?source=.
//
// NOT on the typed seam (internal/shared/apiroute), deliberately: on success it
// answers 200 with the full readingItemDTO, but when the post-write read-back
// misses it answers 200 with the minimal ack {"matched":true,"hardcoverBookId":N}
// instead. One response struct cannot express both without documenting fields the
// handler does not always write, so this stays on plain e.POST.
func (h *Handler) SetBookManualMatch(c *echo.Context) error {
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
		return apierr.New(http.StatusBadRequest, "missing `source` query param", nil).Write(c)
	}

	var body manualMatchBody
	if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
		return apierr.New(http.StatusBadRequest, "invalid request body", nil).Write(c)
	}
	if body.HardcoverBookID <= 0 {
		return apierr.New(http.StatusBadRequest, "hardcoverBookId is required", nil).Write(c)
	}

	ctx := c.Request().Context()
	svc := hardcover.NewSyncService(h.DB, hardcover.NewStore(h.DB), h.Logger)

	// Fill in a missing edition/slug from Hardcover so the stored linkage is complete
	// (mirrors the automated path). Best-effort: if the resolve fails or the user
	// isn't connected, fall back to what the client sent (book_id alone still links).
	editionID, slug := body.EditionID, body.Slug
	if editionID == 0 || slug == "" {
		if ed, sl, connected, rerr := svc.ResolveEditionForBook(ctx, owner, body.HardcoverBookID); rerr == nil && connected {
			if editionID == 0 {
				editionID = ed
			}
			if slug == "" {
				slug = sl
			}
		} else if rerr != nil {
			h.Logger.Warn("manual match: edition resolve failed; linking on book_id only", "user", owner, "bookId", body.HardcoverBookID, "err", rerr)
		}
	}

	if err := h.DB.SetReadingItemHardcoverLink(ctx, owner, source, externalID, body.HardcoverBookID, editionID, "manual", slug); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.New(http.StatusNotFound, "reading item not found", nil).Write(c)
		}
		return apihelpers.InternalErr(h.Logger, c, "set manual hardcover match failed", err)
	}
	h.Logger.Info("manual hardcover match set", "user", owner, "source", source, "externalId", externalID, "bookId", body.HardcoverBookID)

	// Return the updated row so the FE flips the badge to Matched.
	it, err := h.DB.GetReadingItem(ctx, owner, source, externalID)
	if err != nil {
		// The link IS written; a read-back miss is non-fatal — return a minimal ack.
		return c.JSON(http.StatusOK, map[string]any{"matched": true, "hardcoverBookId": body.HardcoverBookID})
	}
	return c.JSON(http.StatusOK, toReadingItemDTO(it))
}
