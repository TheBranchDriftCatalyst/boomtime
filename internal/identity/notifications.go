package identity

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
)

// notifications.go — the durable-notification read API (migration 00079). Durable
// events are written by the notify hub (SaveNotification); these endpoints let the
// FE REPLAY them on session start (so a book-finished fired while the user was
// offline isn't lost) and mark them read.

// ListNotifications handles GET /api/v1/notifications?limit=. Returns the owner's
// recent durable notifications (newest first) + the unread count. Fetched by the FE
// on mount to seed the notification panel.
func (h *Handler) ListNotifications(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	limit := 50
	if v := c.QueryParam("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	items, unread, err := h.DB.ListNotifications(c.Request().Context(), owner, limit)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "list notifications failed", err)
	}
	if items == nil {
		items = nil // JSON-encodes to null; the FE treats null/[] the same
	}
	return c.JSON(http.StatusOK, map[string]any{
		"notifications": items,
		"unreadCount":   unread,
	})
}

// MarkNotificationsRead handles POST /api/v1/notifications/read. Flips all of the
// owner's unread notifications to read and returns how many changed.
func (h *Handler) MarkNotificationsRead(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	n, err := h.DB.MarkNotificationsRead(c.Request().Context(), owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "mark notifications read failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{"marked": n})
}
