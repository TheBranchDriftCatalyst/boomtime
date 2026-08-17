package identity

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// books_work.go — GET /api/v1/books/work?bookId=<hcid>&asin=<amazonAsin>.
// Backs the Book detail side panel: returns EVERY edition of one canonical Work
// for the owner (Audible / Kindle / Hardcover rows that share a Hardcover book id,
// or — for unmatched books — an amazon_asin). The panel groups editions of the
// same book so duplicates collapse into one view. Owner-scoped, read-only.
func (h *Handler) GetBookWork(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	var bookID *int64
	if raw := strings.TrimSpace(c.QueryParam("bookId")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return apierr.New(http.StatusBadRequest, "bookId must be a positive integer", nil).Write(c)
		}
		bookID = &id
	}
	asin := strings.TrimSpace(c.QueryParam("asin"))
	if bookID == nil && asin == "" {
		return apierr.New(http.StatusBadRequest, "one of bookId or asin is required", nil).Write(c)
	}

	items, err := h.DB.ListReadingItemsForWork(c.Request().Context(), owner, bookID, asin)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "load book work failed", err)
	}

	editions := make([]readingItemDTO, 0, len(items))
	for _, it := range items {
		editions = append(editions, toReadingItemDTO(it))
	}

	// Read history (migration 00078): every discrete read of this Work — a book can
	// be read more than once. Best-effort; a load miss just omits the history.
	reads := []readEventDTO{}
	if evs, rerr := h.DB.ListReadingEventsForWork(c.Request().Context(), owner, bookID, "", asin); rerr == nil {
		for _, ev := range evs {
			reads = append(reads, toReadEventDTO(ev))
		}
	} else {
		h.Logger.Warn("load reading events failed", "user", owner, "err", rerr)
	}

	return c.JSON(http.StatusOK, map[string]any{"editions": editions, "reads": reads})
}

// readEventDTO is one discrete read in the Book panel's history.
type readEventDTO struct {
	Origin          string  `json:"origin"`
	Source          string  `json:"source,omitempty"`
	StartedAt       *string `json:"startedAt,omitempty"`
	FinishedAt      *string `json:"finishedAt,omitempty"`
	ProgressPages   *int    `json:"progressPages,omitempty"`
	ProgressSeconds *int    `json:"progressSeconds,omitempty"`
}

func toReadEventDTO(ev db.ReadingEvent) readEventDTO {
	d := readEventDTO{
		Origin: ev.Origin, Source: ev.Source,
		ProgressPages: ev.ProgressPages, ProgressSeconds: ev.ProgressSeconds,
	}
	if ev.StartedAt != nil {
		s := ev.StartedAt.UTC().Format(time.RFC3339)
		d.StartedAt = &s
	}
	if ev.FinishedAt != nil {
		s := ev.FinishedAt.UTC().Format(time.RFC3339)
		d.FinishedAt = &s
	}
	return d
}
