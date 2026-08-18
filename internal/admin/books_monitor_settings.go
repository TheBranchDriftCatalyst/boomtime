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
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/reading"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// readingMonitorView is the GET/PUT response: the persisted toggle + mode plus
// the engine's live-ish state (books currently in L2, last poll time) and the
// calibration-window status.
type readingMonitorView struct {
	Enabled     bool    `json:"enabled"`
	Mode        string  `json:"mode"`        // "debounced" | "verbose"
	ActiveBooks int     `json:"activeBooks"` // owner's books currently in fine-capture (L2)
	LastPingAt  *string `json:"lastPingAt"`  // RFC3339 of the most recent poll, or null
	// Calibration window (PART 2): whether a high-fidelity burst is currently
	// active + when it expires (RFC3339, null when not calibrating).
	Calibrating      bool    `json:"calibrating"`
	CalibratingUntil *string `json:"calibratingUntil"`
	// Recommendation is the derived optimal-interval answer + sync-pattern
	// classification the panel STATES in plain English; null until enough
	// advances are observed to calibrate.
	Recommendation *reading.Recommendation `json:"recommendation"`
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

// readingMonitorPutReq is the PUT body: all fields optional (pointers) so a client
// can toggle enable, change mode, or start/stop calibration independently.
type readingMonitorPutReq struct {
	Enabled   *bool   `json:"enabled"`
	Mode      *string `json:"mode"`
	Calibrate *bool   `json:"calibrate"` // true → start a burst; false → cancel it
}

// AdminBooksReadingMonitorPut: PUT /api/v1/admin/books/reading-monitor — update
// enabled and/or mode and/or start/stop a calibration burst. Body
// {enabled?:bool, mode?:'debounced'|'verbose', calibrate?:bool}.
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
	if req.Enabled == nil && req.Mode == nil && req.Calibrate == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("nothing to update — send enabled, mode, and/or calibrate"))
	}
	ctx := c.Request().Context()
	if req.Enabled != nil || req.Mode != nil {
		if err := h.DB.SetReadingMonitorSettings(ctx, owner, req.Enabled, req.Mode); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apihelpers.RespondErr(c, apierr.NotFound("user not found"))
			}
			h.Logger.Error("admin reading-monitor: save failed", "actor", owner, "err", err)
			return apihelpers.RespondErr(c, apierr.Generic())
		}
	}
	// Calibration: true → open a window of MonitorConfig.CalibrationDuration from
	// now; false → clear it (NULL). The engine auto-reverts to L1/L2 when the
	// window expires — false is only an early cancel.
	if req.Calibrate != nil {
		var until *time.Time
		if *req.Calibrate {
			t := time.Now().UTC().Add(reading.LoadMonitorConfig().CalibrationDuration)
			until = &t
		}
		if err := h.DB.SetReadingMonitorCalibration(ctx, owner, until); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apihelpers.RespondErr(c, apierr.NotFound("user not found"))
			}
			h.Logger.Error("admin reading-monitor: calibration save failed", "actor", owner, "err", err)
			return apihelpers.RespondErr(c, apierr.Generic())
		}
	}
	view, err := h.readingMonitorView(c, owner)
	if err != nil {
		h.Logger.Error("admin reading-monitor: view failed", "actor", owner, "err", err)
		return apihelpers.RespondErr(c, apierr.Generic())
	}
	h.Logger.Info("admin reading-monitor settings updated", "actor", owner,
		"enabled", view.Enabled, "mode", view.Mode, "calibrating", view.Calibrating)
	return c.JSON(http.StatusOK, view)
}

// rawKindleSample is one raw Kindle heartbeat for the diagnostic page: the
// last-page-read position + Amazon's creationTime, plus the Δlocation and interval
// to the SAME book's previous sample (0 for a book's first sample). Derived from
// kindle_reading_positions — the raw position stream the two-level engine samples.
type rawKindleSample struct {
	ASIN         string  `json:"asin"`
	Title        string  `json:"title"`
	Location     int64   `json:"location"`
	DLoc         int64   `json:"dloc"`
	CreationTime string  `json:"creationTime"` // RFC3339
	IntervalSecs float64 `json:"intervalSecs"`
}

// rawAudibleSample is one raw Audible listening bucket (per-day seconds) — the
// aggregate Audible reading source, for side-by-side comparison with the Kindle
// position stream on the diagnostic page.
type rawAudibleSample struct {
	Title            string `json:"title"`
	Day              string `json:"day"` // RFC3339 (UTC midnight)
	ListeningSeconds int64  `json:"listeningSeconds"`
}

// rawView is the diagnostic raw-data response: recent raw samples for BOTH reading
// sources.
type rawView struct {
	Kindle  []rawKindleSample  `json:"kindle"`
	Audible []rawAudibleSample `json:"audible"`
}

// AdminBooksReadingMonitorRaw: GET /api/v1/admin/books/reading-monitor/raw — the
// diagnostic page's raw heartbeat/position stream for BOTH reading sources. Kindle
// = recent last-page-read position samples (with derived Δloc + interval); Audible
// = recent per-day listening buckets. Admin-only.
func (h *Handler) AdminBooksReadingMonitorRaw(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	ctx := c.Request().Context()

	const rawCap = 200
	positions, err := h.DB.ListRecentKindleReadingPositions(ctx, owner, rawCap)
	if err != nil {
		h.Logger.Error("admin reading-monitor raw: kindle positions failed", "actor", owner, "err", err)
		return apihelpers.RespondErr(c, apierr.Generic())
	}

	// Derive per-book Δlocation + interval by walking each ASIN's samples oldest-
	// first (the query returns newest-first, so index the previous sample per ASIN
	// as we go from the end). Simpler: bucket by ASIN, sort ascending, diff.
	byAsin := map[string][]db.KindleReadingPositionRow{}
	for _, p := range positions {
		byAsin[p.ASIN] = append(byAsin[p.ASIN], p)
	}
	kindle := make([]rawKindleSample, 0, len(positions))
	for _, rows := range byAsin {
		// rows are newest-first (query order); reverse to oldest-first for diffing.
		sort.Slice(rows, func(i, j int) bool { return rows[i].SampledAt.Before(rows[j].SampledAt) })
		for i, r := range rows {
			s := rawKindleSample{
				ASIN:         r.ASIN,
				Title:        r.Title,
				Location:     r.Position,
				CreationTime: r.SampledAt.UTC().Format(time.RFC3339),
			}
			if i > 0 {
				prev := rows[i-1]
				s.DLoc = r.Position - prev.Position
				s.IntervalSecs = r.SampledAt.Sub(prev.SampledAt).Seconds()
			}
			kindle = append(kindle, s)
		}
	}
	// Present newest-first across all books.
	sort.Slice(kindle, func(i, j int) bool { return kindle[i].CreationTime > kindle[j].CreationTime })

	// Audible: recent per-day listening buckets over the same 30d window.
	from := time.Now().UTC().AddDate(0, 0, -30)
	to := time.Now().UTC()
	acts, err := h.DB.ListReadingActivity(ctx, owner, "audible", from, to)
	if err != nil {
		h.Logger.Error("admin reading-monitor raw: audible activity failed", "actor", owner, "err", err)
		return apihelpers.RespondErr(c, apierr.Generic())
	}
	audible := make([]rawAudibleSample, 0, len(acts))
	for _, a := range acts {
		audible = append(audible, rawAudibleSample{
			Day:              a.BucketDate.UTC().Format(time.RFC3339),
			ListeningSeconds: a.ListeningSeconds,
		})
	}

	return c.JSON(http.StatusOK, rawView{Kindle: kindle, Audible: audible})
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

	// Derive the interval recommendation + sync-pattern classification from the
	// advance samples observed over the lookback window (nil until enough land),
	// using the consolidated MonitorConfig coefficients.
	rmCfg := reading.LoadMonitorConfig()
	pairs, err := h.DB.ListRecentReadingMonitorAdvances(ctx, owner, time.Now().Add(-rmCfg.RecommendLookback), rmCfg.WindowCap)
	if err != nil {
		return readingMonitorView{}, err
	}
	rec := reading.RecommendIntervals(pairs, rmCfg)

	// Calibration-window status.
	calUntil, err := h.DB.GetReadingMonitorCalibration(ctx, owner)
	if err != nil {
		return readingMonitorView{}, err
	}

	view := readingMonitorView{Enabled: enabled, Mode: mode, ActiveBooks: active, Recommendation: rec}
	if last != nil {
		s := last.UTC().Format(time.RFC3339)
		view.LastPingAt = &s
	}
	if calUntil != nil {
		view.Calibrating = time.Now().Before(*calUntil)
		s := calUntil.UTC().Format(time.RFC3339)
		view.CalibratingUntil = &s
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
