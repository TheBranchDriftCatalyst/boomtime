package identity

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
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
	return c.JSON(http.StatusOK, map[string]any{"editions": editions})
}
