// reading_monitor.go — the user-scoped reading-monitor STATUS endpoint
// (catalyst-books §5.1, PART 2). The admin GET/PUT
// (internal/admin/books_monitor_settings.go) is admin-gated; this is the thin
// self-only read the global nav indicator polls to show "monitor on" / "calibrating"
// without granting the caller the admin surface. Read-only: it never mutates.
package identity

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
)

// readingMonitorStatusResponse is the nav-indicator payload: is the persistent
// monitor on for me, and am I in a calibration burst (and until when).
type readingMonitorStatusResponse struct {
	Enabled          bool    `json:"enabled"`
	Calibrating      bool    `json:"calibrating"`
	CalibratingUntil *string `json:"calibratingUntil"` // RFC3339, or null
}

// ReadingMonitorStatus: GET /api/v1/books/reading-monitor/status — the caller's
// own persistent-monitor enable + calibration status. Auth'd (self-only via
// IdentifyOwner); registered only when BOOM_FEATURE_BOOKS is on.
func (h *Handler) ReadingMonitorStatus(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	ctx := c.Request().Context()

	enabled, _, err := h.DB.GetReadingMonitorSettings(ctx, owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "reading-monitor status lookup failed", err)
	}
	calUntil, err := h.DB.GetReadingMonitorCalibration(ctx, owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "reading-monitor calibration lookup failed", err)
	}

	resp := readingMonitorStatusResponse{Enabled: enabled}
	if calUntil != nil {
		resp.Calibrating = time.Now().Before(*calUntil)
		s := calUntil.UTC().Format(time.RFC3339)
		resp.CalibratingUntil = &s
	}
	return c.JSON(http.StatusOK, resp)
}
