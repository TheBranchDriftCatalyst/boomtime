// awards.go: label-award streak ledger endpoints (gaka-mwp-streaks).
//
// Two handlers:
//
//   - POST /api/v1/users/current/awards/log — client sends the label ids
//     that just fired for the caller (from the JIT client-side
//     evaluator) plus each label's PeriodType. Server computes the
//     current period bounds in the user's timezone and upserts one row
//     per (username, label_id, period_start). Idempotent — a repeat
//     visit inside the same period is a no-op.
//
//   - GET /api/v1/users/current/awards/streaks — returns
//     {[labelId]: streakCount} for every label with an ACTIVE streak
//     (i.e. fired in the current period). Streak counts back until the
//     first gap; a break resets the count to 0 next period.
//
// Public variant lives alongside the /p/:slug endpoints (see
// profile.go for the pattern) so profile viewers see the streak
// badges too. Timezone always comes from the RESOLVED user tz
// (gaka-dg7 resolveUserTZ), never assumed UTC.

package handler

import (
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
)

// awardLogReq is the wire body for POST /awards/log. `Items` may be
// empty (no-op). Each item's PeriodType is trusted from the client
// only when it's a known value — server ignores unknowns silently to
// avoid making a batch fail on one bad row.
type awardLogReq struct {
	Items []struct {
		LabelID    string `json:"labelId"`
		PeriodType string `json:"periodType"`
	} `json:"items"`
	// gaka-mwp-streaks backfill: when set, the server buckets the log
	// against THIS timestamp instead of time.Now(). Enables a client-
	// side tool to walk N historical days, evaluate against that day's
	// stats, and populate the ledger so streak badges immediately show
	// real recorded history instead of "starts today". ISO-8601.
	At string `json:"at,omitempty"`
}

// AwardsLog: POST /api/v1/users/current/awards/log
func (h *Handler) AwardsLog(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	var req awardLogReq
	if aerr := BindJSONWithLimit(c, &req, 128*1024); aerr != nil {
		return respondErr(c, aerr)
	}
	// Filter to known period types; drop lifetime (not ledger-eligible).
	items := make([]db.AwardLogItem, 0, len(req.Items))
	for _, it := range req.Items {
		pt := db.PeriodType(it.PeriodType)
		if pt != db.PeriodDaily && pt != db.PeriodWeekly && pt != db.PeriodMonthly {
			continue
		}
		if it.LabelID == "" {
			continue
		}
		items = append(items, db.AwardLogItem{LabelID: it.LabelID, PeriodType: pt})
	}
	tzName := h.resolveUserTZ(c.Request().Context(), owner)
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.UTC
	}
	// Explicit `at` for historical backfill; otherwise use wall clock.
	// Reject a future `at` (nonsensical + could poison the streak walker).
	at := time.Now()
	if req.At != "" {
		parsed, perr := time.Parse(time.RFC3339, req.At)
		if perr != nil {
			return respondErr(c, apierr.BadRequest("`at` must be RFC3339"))
		}
		if parsed.After(time.Now().Add(time.Hour)) {
			return respondErr(c, apierr.BadRequest("`at` cannot be in the future"))
		}
		at = parsed
	}
	written, err := h.DB.LogAwards(c.Request().Context(), owner, items, loc, at)
	if err != nil {
		return h.internalErr(c, "award log write failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"received": len(req.Items),
		"written":  written,
	})
}

// AwardsStreaks: GET /api/v1/users/current/awards/streaks
func (h *Handler) AwardsStreaks(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	return h.awardsStreaksFor(c, owner)
}

// PublicAwardsStreaks: GET /api/public/profile/:slug/awards/streaks
// — same shape, target user derived from the public slug.
func (h *Handler) PublicAwardsStreaks(c *echo.Context) error {
	slug := c.Param("slug")
	if slug == "" {
		return respondErr(c, apierr.BadRequest("slug is required"))
	}
	owner, err := h.DB.LookupUsernameBySlug(c.Request().Context(), slug)
	if err != nil || owner == "" {
		return respondErr(c, apierr.New(http.StatusNotFound, "profile not found", nil))
	}
	return h.awardsStreaksFor(c, owner)
}

// AwardsLedger: GET /api/v1/users/current/awards/ledger?label=<id>&limit=<n>
// — inspector endpoint that returns the raw ledger rows (with label
// name + kind joined in) for debug/admin viewing on the AdminTab.
func (h *Handler) AwardsLedger(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	label := c.QueryParam("label")
	limit := 500
	if s := c.QueryParam("limit"); s != "" {
		if n, err := parsePositiveInt(s, 500); err == nil {
			limit = n
		}
	}
	rows, err := h.DB.ListAwardLedger(c.Request().Context(), owner, label, limit)
	if err != nil {
		return h.internalErr(c, "ledger query failed", err)
	}
	c.Response().Header().Set("Cache-Control", "private, max-age=30")
	return c.JSON(http.StatusOK, map[string]any{
		"rows":  rows,
		"limit": limit,
	})
}

// parsePositiveInt is a tiny query-param helper — parses `s` as a
// positive int with an upper bound (`max`). Returns max on any parse
// error so a bad ?limit=abc doesn't 400 the request.
func parsePositiveInt(s string, max int) (int, error) {
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			return max, nil
		}
		n = n*10 + int(r-'0')
		if n > max {
			return max, nil
		}
	}
	if n <= 0 {
		return max, nil
	}
	return n, nil
}

func (h *Handler) awardsStreaksFor(c *echo.Context, owner string) error {
	tzName := h.resolveUserTZ(c.Request().Context(), owner)
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.UTC
	}
	streaks, err := h.DB.GetLabelStreaks(c.Request().Context(), owner, loc, time.Now())
	if err != nil {
		return h.internalErr(c, "streak query failed", err)
	}
	// Response is a flat map keyed by label id — small payload, easiest
	// for the FE to look up by chip.
	out := make(map[string]int, len(streaks))
	for _, s := range streaks {
		out[s.LabelID] = s.StreakCount
	}
	// Cache header: streaks change at most once per period, so a short
	// browser cache is safe. 60s balances "reload after evaluate" with
	// "don't hammer the DB from every mount".
	c.Response().Header().Set("Cache-Control", "private, max-age=60")
	return c.JSON(http.StatusOK, out)
}
