// Package awards owns the label-award streak ledger + evaluator HTTP surface
// (boom-8tn phase 4b). Extracted from internal/handler/ so the awards domain
// (log/streaks/ledger inspection + server-side evaluation + historical
// backfill) owns its handler struct, routes, and tests as one folder.
//
// Three clusters land in this package:
//
//   - handler.go — the streak ledger endpoints (this file):
//     POST /awards/log, GET /awards/streaks, GET /awards/ledger, and the
//     public /p/:slug/awards/streaks mirror. Timezone always comes from the
//     RESOLVED user tz (boom-dg7 resolveUserTZ), never assumed UTC.
//
//   - eval.go — server-side award evaluation (boom-hc6.3):
//     GET /awards (own, WRITES ledger) and GET /public/profile/:slug/awards
//     (public, does NOT write). Runs the labels evaluator against the
//     dashboard payload.
//
//   - backfill.go — historical replay (boom-hc6.5.1):
//     POST /awards/backfill. Walks N days back computing each day's payload
//     snapshot + writing ledger rows at that day. Idempotent.
//
// The award_ledger DB methods stay on *db.DB (internal/db/award_ledger.go)
// because they're used cross-package by goals + identity.
package awards

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/labstack/echo/v5"
)

// Handler bundles the SUBSET of the god-type handler.Handler's dependencies
// that the awards domain actually reads. Everything else stays out of this
// package.
//
//   - DB     — award_ledger + labels catalog + public-profile lookup +
//     dashboard-payload query chain (GetUserActivity/Rollup, GetPunchcard,
//     GetCategoryDaily, LoadHiddenSets, LoadRenameSets, GetUserTimezone)
//   - Cfg    — DefaultTimezone (used by the resolveUserTZ 3-level chain)
//   - Logger — internal log target for eval / ledger-write / backfill errors
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
}

// New constructs an awards.Handler with the passed-in shared deps.
func New(database *db.DB, cfg *config.Config, logger *slog.Logger) *Handler {
	return &Handler{
		DB:     database,
		Cfg:    cfg,
		Logger: logger,
	}
}

// awardLogReq is the wire body for POST /awards/log. `Items` may be
// empty (no-op). Each item's PeriodType is trusted from the client
// only when it's a known value — server ignores unknowns silently to
// avoid making a batch fail on one bad row.
type awardLogReq struct {
	Items []struct {
		LabelID    string `json:"labelId"`
		PeriodType string `json:"periodType"`
	} `json:"items"`
	// boom-mwp-streaks backfill: when set, the server buckets the log
	// against THIS timestamp instead of time.Now(). Enables a client-
	// side tool to walk N historical days, evaluate against that day's
	// stats, and populate the ledger so streak badges immediately show
	// real recorded history instead of "starts today". ISO-8601.
	At string `json:"at,omitempty"`
}

// AwardsLog: POST /api/v1/users/current/awards/log
func (h *Handler) AwardsLog(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	var req awardLogReq
	if aerr := apihelpers.BindJSONWithLimit(c, &req, 128*1024); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
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
	tzName := apihelpers.ResolveUserTZ(h.DB, h.Logger, c.Request().Context(), owner, h.Cfg.DefaultTimezone)
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
			return apihelpers.RespondErr(c, apierr.BadRequest("`at` must be RFC3339"))
		}
		if parsed.After(time.Now().Add(time.Hour)) {
			return apihelpers.RespondErr(c, apierr.BadRequest("`at` cannot be in the future"))
		}
		at = parsed
	}
	written, err := h.DB.LogAwards(c.Request().Context(), owner, items, loc, at)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "award log write failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"received": len(req.Items),
		"written":  written,
	})
}

// AwardsStreaks: GET /api/v1/users/current/awards/streaks
func (h *Handler) AwardsStreaks(c *echo.Context) (map[string]int, error) {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return nil, aerr
	}
	return h.awardsStreaksFor(c, owner)
}

// PublicAwardsStreaks: GET /api/public/profile/:slug/awards/streaks
// — same shape, target user derived from the public slug.
func (h *Handler) PublicAwardsStreaks(c *echo.Context) (map[string]int, error) {
	slug := c.Param("slug")
	if slug == "" {
		return nil, apierr.BadRequest("slug is required")
	}
	owner, err := h.DB.LookupUsernameBySlug(c.Request().Context(), slug)
	if err != nil || owner == "" {
		return nil, apierr.New(http.StatusNotFound, "profile not found", nil)
	}
	return h.awardsStreaksFor(c, owner)
}

// AwardsLedger: GET /api/v1/users/current/awards/ledger?label=<id>&limit=<n>
// — inspector endpoint that returns the raw ledger rows (with label
// name + kind joined in) for debug/admin viewing on the AdminTab.
func (h *Handler) AwardsLedger(c *echo.Context) (awardsLedgerResponse, error) {
	var out awardsLedgerResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
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
		return out, fmt.Errorf("ledger query failed: %w", err)
	}
	c.Response().Header().Set("Cache-Control", "private, max-age=30")
	return awardsLedgerResponse{
		Rows:  rows,
		Limit: limit,
	}, nil
}

// awardsLedgerResponse is GET /api/v1/users/current/awards/ledger. Field
// names mirror the map keys this handler used to build by hand ("rows",
// "limit") — the AdminTab inspector reads both.
type awardsLedgerResponse struct {
	// Rows are the raw ledger rows with the label name + kind joined in.
	Rows []db.LedgerRow `json:"rows"`
	// Limit echoes the effective row cap after clamping, so the caller can
	// tell "that is all of it" from "there is more behind a bigger limit".
	Limit int `json:"limit"`
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

func (h *Handler) awardsStreaksFor(c *echo.Context, owner string) (map[string]int, error) {
	tzName := apihelpers.ResolveUserTZ(h.DB, h.Logger, c.Request().Context(), owner, h.Cfg.DefaultTimezone)
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.UTC
	}
	streaks, err := h.DB.GetLabelStreaks(c.Request().Context(), owner, loc, time.Now())
	if err != nil {
		return nil, fmt.Errorf("streak query failed: %w", err)
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
	return out, nil
}
