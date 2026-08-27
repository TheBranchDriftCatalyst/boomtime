// eval.go: server-side award evaluation (boom-hc6.3).
//
// Two endpoints:
//
//   - GET /api/v1/users/current/awards      — signed-in caller. Evaluates
//     labels against the caller's public-dashboard payload, WRITES ledger
//     rows for firing labels (server-authoritative — the FE no longer POSTs
//     /awards/log from the JIT path), returns []LabelAward.
//
//   - GET /api/public/profile/:slug/awards  — visitor viewing a public
//     profile. Same shape, scoped to the slug's owner. Respects
//     public_profile_enabled. Does NOT write to the ledger (a public visit
//     shouldn't be able to advance someone else's streak).
//
// The old POST /awards/log endpoint stays alive — the admin backfill tool
// (StreakBackfillSection) uses it with an explicit `at` parameter to seed
// historical periods. The JIT client-eval path is the caller that's going
// away (see boom-hc6.4).

package awards

import (
	"errors"
	"fmt"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/labels"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/widget"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
)

// OwnAwards: GET /api/v1/users/current/awards
func (h *Handler) OwnAwards(c *echo.Context) ([]labels.LabelAward, error) {
	var out []labels.LabelAward
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	ctx := c.Request().Context()
	payload, err := h.buildAwardsPayload(ctx, owner)
	if err != nil {
		return out, fmt.Errorf("awards payload build failed: %w", err)
	}
	catalog, err := h.loadEvaluatorCatalog(ctx)
	if err != nil {
		return out, fmt.Errorf("awards catalog load failed: %w", err)
	}
	awards := labels.EvaluateAll(payload, catalog)

	// Server-authoritative ledger write. Idempotent inside a period via the
	// existing (username, label_id, period_start) PK. Use the caller's
	// resolved tz (boom-dg7) so a user in Pacific gets their day-boundary
	// right even when the server clock is UTC.
	tzName := apihelpers.ResolveUserTZ(h.DB, h.Logger, ctx, owner, h.Cfg.DefaultTimezone)
	loc, terr := time.LoadLocation(tzName)
	if terr != nil {
		loc = time.UTC
	}
	items := make([]db.AwardLogItem, 0, len(awards))
	for _, a := range awards {
		pt := db.ResolvePeriod(string(a.Kind), specPeriodDefault(catalog, a.ID))
		if pt == db.PeriodLifetime || pt == db.PeriodAuto {
			continue // lifetime labels don't get ledger rows
		}
		items = append(items, db.AwardLogItem{LabelID: a.ID, PeriodType: pt})
	}
	if len(items) > 0 {
		if _, err := h.DB.LogAwards(ctx, owner, items, loc, time.Now()); err != nil {
			// Log-and-continue: a ledger write failure shouldn't fail the
			// award response — the client already has what to display.
			h.Logger.Warn("server-side award ledger write failed", "user", owner, "err", err)
		}
	}

	c.Response().Header().Set("Cache-Control", "private, max-age=30")
	return awards, nil
}

// PublicAwards: GET /api/public/profile/:slug/awards
func (h *Handler) PublicAwards(c *echo.Context) ([]labels.LabelAward, error) {
	var out []labels.LabelAward
	slug := c.Param("slug")
	if slug == "" {
		return out, apierr.BadRequest("slug is required")
	}
	ctx := c.Request().Context()
	username, err := h.DB.LookupUsernameBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, apierr.NotFound("This profile isn't public")
		}
		return out, fmt.Errorf("public awards slug lookup failed: %w", err)
	}
	enabled, _, err := h.DB.GetPublicProfile(ctx, username)
	if err != nil {
		return out, fmt.Errorf("public awards enabled check failed: %w", err)
	}
	if !enabled {
		return out, apierr.NotFound("This profile isn't public")
	}
	payload, err := h.buildAwardsPayload(ctx, username)
	if err != nil {
		return out, fmt.Errorf("public awards payload build failed: %w", err)
	}
	catalog, err := h.loadEvaluatorCatalog(ctx)
	if err != nil {
		return out, fmt.Errorf("public awards catalog load failed: %w", err)
	}
	awards := labels.EvaluateAll(payload, catalog)
	// Deliberately NO ledger write here — a public visit must not pollute
	// the profile owner's streaks. Their own /awards call is the write path.
	c.Response().Header().Set("Cache-Control", "public, max-age=180")
	return awards, nil
}

// buildAwardsPayload runs the same query chain the public-profile handler
// uses, projected into the shape the labels evaluator reads. Same 60-day
// window + 15-min gap-cutoff as the public dashboard — so a label seen on
// /p/:slug and a label seen on /awards are computed from the same data.
//
// Kept as a Handler method so it can be shared between the two award
// endpoints without exporting all the tuning constants.
func (h *Handler) buildAwardsPayload(ctx echoContext, username string) (*labels.Payload, error) {
	return h.buildAwardsPayloadAt(ctx, username, time.Now().UTC())
}

// buildAwardsPayloadAt is the historical variant — computes the payload
// as if EndDate=at instead of Now(). Used by the /awards/backfill flow
// (boom-hc6.5.1) to walk N days back and evaluate each day's snapshot
// against the historical window that ended THAT day.
func (h *Handler) buildAwardsPayloadAt(ctx echoContext, username string, at time.Time) (*labels.Payload, error) {
	t1 := at.UTC()
	t0 := apihelpers.RemoveDays(t1, publicProfilePayloadDays)

	hidden, err := h.DB.LoadHiddenSets(ctx, username)
	if err != nil {
		return nil, err
	}
	renames, err := h.DB.LoadRenameSets(ctx, username)
	if err != nil {
		return nil, err
	}
	tz := apihelpers.ResolveUserTZ(h.DB, h.Logger, ctx, username, h.Cfg.DefaultTimezone)

	var members db.MemberSets
	var rows []db.StatRow
	if !hidden.HasHiddenOutside(db.RollupAxes) {
		rows, err = h.DB.GetUserActivityRollup(ctx, username, t0, t1, hidden, renames, members, false)
	} else {
		rows, err = h.DB.GetUserActivity(ctx, username, t0, t1, publicProfileTimeLimit, tz, hidden, renames, members, false)
	}
	if err != nil {
		return nil, err
	}
	categories, err := h.DB.GetCategoryDaily(ctx, username, t0, t1, publicProfileTimeLimit, tz, hidden, renames, members, false)
	if err != nil {
		return nil, err
	}
	payload := stats.ToStatsPayload(t0, t1, rows, categories, nil)
	scrubbed := widget.Scrub(&payload, hidden)

	pcCells, err := h.DB.GetPunchcard(ctx, username, t0, t1, publicProfileTimeLimit, tz, hidden, members, false)
	if err != nil {
		return nil, err
	}
	return &labels.Payload{
		Languages:  scrubbed.Languages,
		Editors:    scrubbed.Editors,
		Projects:   scrubbed.Projects,
		Categories: scrubbed.Categories,
		Platforms:  scrubbed.Platforms,
		Punchcard:  stats.ToPunchcardPayload(pcCells),
		DailyTotal: scrubbed.DailyTotal,
		DailyAvg:   scrubbed.DailyAvg,
	}, nil
}

// echoContext is a tiny local alias so buildAwardsPayload's signature reads
// clean without dragging the whole echo import into every DB helper's
// signature. The underlying context.Context is what every DB call needs.
type echoContext = interface {
	Deadline() (time.Time, bool)
	Done() <-chan struct{}
	Err() error
	Value(key any) any
}

// loadEvaluatorCatalog reads the full labels catalog + converts each DB row
// into a labels.LabelSpec. A single decode error stops the world — a bad
// seed row is a "louder is better" situation.
func (h *Handler) loadEvaluatorCatalog(ctx echoContext) ([]labels.LabelSpec, error) {
	dbRows, err := h.DB.ListLabels(ctx)
	if err != nil {
		return nil, err
	}
	adapted := make([]labels.DBRow, 0, len(dbRows))
	for _, r := range dbRows {
		adapted = append(adapted, labels.DBRow{
			ID:            r.ID,
			Kind:          r.Kind,
			Label:         r.Label,
			Glyph:         r.Glyph,
			Description:   r.Description,
			Rank:          r.Rank,
			Tier:          r.Tier,
			PeriodDefault: r.PeriodDefault,
			Condition:     r.Condition,
		})
	}
	return labels.SpecsFromDBRows(adapted)
}

// specPeriodDefault returns the per-label period override string for a
// given awardID. Empty means "use kind default". Called during ledger-item
// build in OwnAwards.
func specPeriodDefault(catalog []labels.LabelSpec, awardID string) string {
	for _, s := range catalog {
		if s.ID == awardID {
			return s.PeriodDefault
		}
	}
	return ""
}
