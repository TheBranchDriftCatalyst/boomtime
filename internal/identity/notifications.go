package identity

import (
	"fmt"
	"strconv"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// notifications.go — the durable-notification read API (migration 00079). Durable
// events are written by the notify hub (SaveNotification); these endpoints let the
// FE REPLAY them on session start (so a book-finished fired while the user was
// offline isn't lost) and mark them read.

// listNotificationsResponse is GET /api/v1/notifications.
type listNotificationsResponse struct {
	// Notifications is nil-able on purpose: a nil slice JSON-encodes to null
	// and the FE treats null/[] the same. Preserved from the pre-typed
	// map[string]any shape.
	Notifications []db.Notification `json:"notifications"`
	UnreadCount   int               `json:"unreadCount"`
}

// ListNotifications handles GET /api/v1/notifications?limit=. Returns the owner's
// recent durable notifications (newest first) + the unread count. Fetched by the FE
// on mount to seed the notification panel.
func (h *Handler) ListNotifications(c *echo.Context) (listNotificationsResponse, error) {
	var out listNotificationsResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	limit := 50
	if v := c.QueryParam("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	items, unread, err := h.DB.ListNotifications(c.Request().Context(), owner, limit)
	if err != nil {
		return out, fmt.Errorf("list notifications failed: %w", err)
	}
	if items == nil {
		items = nil // JSON-encodes to null; the FE treats null/[] the same
	}
	return listNotificationsResponse{
		Notifications: items,
		UnreadCount:   unread,
	}, nil
}

// markNotificationsReadResponse is POST /api/v1/notifications/read.
type markNotificationsReadResponse struct {
	Marked int64 `json:"marked"`
}

// MarkNotificationsRead handles POST /api/v1/notifications/read. Flips all of the
// owner's unread notifications to read and returns how many changed.
func (h *Handler) MarkNotificationsRead(c *echo.Context) (markNotificationsReadResponse, error) {
	var out markNotificationsReadResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	n, err := h.DB.MarkNotificationsRead(c.Request().Context(), owner)
	if err != nil {
		return out, fmt.Errorf("mark notifications read failed: %w", err)
	}
	return markNotificationsReadResponse{Marked: n}, nil
}
