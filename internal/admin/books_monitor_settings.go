// books_monitor_settings.go — the admin GET/PUT for the PERSISTENT server-side
// reading-monitor toggle (catalyst-books §5.1). Distinct from the live WS probe
// (books_monitor.go): these endpoints only read/write the per-user
// reading_monitor_enabled / reading_monitor_mode flags + report the engine's
// current state (active book count, last poll time). The engine itself runs on
// the leader-singleton scheduler (books-reading-monitor kind) whether or not this
// tab is open — the panel is a thin view+toggle over persisted state, not the
// engine.
//
// Admin-only (requireAdmin) and registered only when BOOM_FEATURE_BOOKS is on
// (routes.go), so the path 404s like any unknown route when the feature is off.
package admin

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/domains/books"
)

// readingMonitorView is the GET/PUT response: the persisted toggle + mode plus
// the engine's live-ish state (books currently in L2, last poll time).
type readingMonitorView struct {
	Enabled     bool    `json:"enabled"`
	Mode        string  `json:"mode"`        // "debounced" | "verbose"
	ActiveBooks int     `json:"activeBooks"` // owner's books currently in fine-capture (L2)
	LastPingAt  *string `json:"lastPingAt"`  // RFC3339 of the most recent poll, or null
	// Recommendation is the derived optimal-interval answer + sync-pattern
	// classification the panel STATES in plain English; null until enough
	// advances are observed to calibrate.
	Recommendation *books.Recommendation `json:"recommendation"`
}

// AdminBooksReadingMonitorGet: GET /api/v1/admin/books/reading-monitor — report
// the caller's persistent-monitor settings + current engine state.
func (h *Handler) AdminBooksReadingMonitorGet(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	view, err := h.readingMonitorView(c, owner)
	if err != nil {
		h.Logger.Error("admin reading-monitor: view failed", "actor", owner, "err", err)
		return apihelpers.RespondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, view)
}

// readingMonitorPutReq is the PUT body: both fields optional (pointers) so a
// client can toggle enable or change mode independently.
type readingMonitorPutReq struct {
	Enabled *bool   `json:"enabled"`
	Mode    *string `json:"mode"`
}

// AdminBooksReadingMonitorPut: PUT /api/v1/admin/books/reading-monitor — update
// enabled and/or mode. Body {enabled?:bool, mode?:'debounced'|'verbose'}.
func (h *Handler) AdminBooksReadingMonitorPut(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	var req readingMonitorPutReq
	if err := c.Bind(&req); err != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("invalid JSON body"))
	}
	if req.Mode != nil && *req.Mode != db.ReadingMonitorModeDebounced && *req.Mode != db.ReadingMonitorModeVerbose {
		return apihelpers.RespondErr(c, apierr.BadRequest("mode must be 'debounced' or 'verbose'"))
	}
	if req.Enabled == nil && req.Mode == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("nothing to update — send enabled and/or mode"))
	}
	if err := h.DB.SetReadingMonitorSettings(c.Request().Context(), owner, req.Enabled, req.Mode); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apihelpers.RespondErr(c, apierr.NotFound("user not found"))
		}
		h.Logger.Error("admin reading-monitor: save failed", "actor", owner, "err", err)
		return apihelpers.RespondErr(c, apierr.Generic())
	}
	view, err := h.readingMonitorView(c, owner)
	if err != nil {
		h.Logger.Error("admin reading-monitor: view failed", "actor", owner, "err", err)
		return apihelpers.RespondErr(c, apierr.Generic())
	}
	h.Logger.Info("admin reading-monitor settings updated", "actor", owner, "enabled", view.Enabled, "mode", view.Mode)
	return c.JSON(http.StatusOK, view)
}

// readingMonitorView assembles the response from the settings + engine state.
func (h *Handler) readingMonitorView(c *echo.Context, owner string) (readingMonitorView, error) {
	ctx := c.Request().Context()
	enabled, mode, err := h.DB.GetReadingMonitorSettings(ctx, owner)
	if err != nil {
		return readingMonitorView{}, err
	}
	active, err := h.DB.CountActiveKindleMonitorBooks(ctx, owner)
	if err != nil {
		return readingMonitorView{}, err
	}
	last, err := h.DB.LastReadingMonitorPollAt(ctx, owner)
	if err != nil {
		return readingMonitorView{}, err
	}

	// Derive the interval recommendation from the advance intervals observed over
	// the lookback window (nil until enough samples land). Reads the rolling
	// per-owner window the engine persists on each intra-session advance.
	pairs, err := h.DB.ListRecentReadingMonitorAdvances(ctx, owner, time.Now().Add(-books.RecommendLookback))
	if err != nil {
		return readingMonitorView{}, err
	}
	rec := books.RecommendIntervals(pairs)

	view := readingMonitorView{Enabled: enabled, Mode: mode, ActiveBooks: active, Recommendation: rec}
	if last != nil {
		s := last.UTC().Format(time.RFC3339)
		view.LastPingAt = &s
	}
	if rec != nil {
		// Log the derived answer once per read so the recommendation is visible
		// in the server logs, not only the admin UI.
		h.Logger.Info("reading-monitor recommendation",
			"actor", owner, "detectSecs", rec.DetectSecs, "captureSecs", rec.CaptureSecs, "idleSecs", rec.IdleSecs,
			"medianAdvanceSecs", rec.MedianAdvanceSecs, "p90AdvanceSecs", rec.P90AdvanceSecs, "samples", rec.SampleCount)
	}
	return view, nil
}
