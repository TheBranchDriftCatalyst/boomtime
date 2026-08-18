// backfill.go: server-side historical award-replay (gaka-hc6.5.1).
//
// Unblocks the full delete of the client-side evaluator (gaka-hc6.5) by
// moving the last runtime call site — AdminTab's StreakBackfillSection —
// off the browser. The FE now POSTs {days: N} and the server walks N days
// backwards computing the payload as-if-EndDate=D for each and writing
// ledger rows with at=D.
//
// Semantics:
//   - `days` clamped [1, 365]. > 365 is nonsense (per-day payload rebuild
//     × 365 days already stresses the aggregation cache; longer is
//     "restore from a backup" territory).
//   - Each day rebuilds the caller's payload as-of that day, evaluates
//     the full catalog, and calls LogAwards with at=D. Idempotent —
//     re-running is a no-op via the existing (username, label_id,
//     period_start) PK.
//   - Timezone comes from gaka-dg7 resolveUserTZ, same as the /awards
//     handlers. A user in Pacific gets Pacific day-boundaries; the
//     server clock's tz doesn't leak in.
//   - Response is a summary: {daysProcessed, rowsWritten, skipped, tookMs}.
//     Deliberately does NOT return per-day awards — the response would
//     balloon and the FE has no consumer for it (it just wants to know
//     "did the batch finish and how many rows landed").
package awards

import (
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/labels"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/labstack/echo/v5"
)

type awardsBackfillReq struct {
	Days int `json:"days"`
}

type awardsBackfillResp struct {
	DaysProcessed int   `json:"daysProcessed"`
	RowsWritten   int   `json:"rowsWritten"`
	Skipped       int   `json:"skipped"`
	TookMs        int64 `json:"tookMs"`
}

// AwardsBackfill: POST /api/v1/users/current/awards/backfill
// Body: {days: N}. Server walks N days back to today, computing each
// day's payload snapshot + writing ledger rows at that day.
func (h *Handler) AwardsBackfill(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	var req awardsBackfillReq
	if aerr := apihelpers.BindJSONWithLimit(c, &req, 4*1024); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if req.Days < 1 {
		return apihelpers.RespondErr(c, apierr.BadRequest("days must be ≥ 1"))
	}
	if req.Days > 365 {
		req.Days = 365 // hard clamp; message-less silent floor is fine
	}

	ctx := c.Request().Context()
	tzName := apihelpers.ResolveUserTZ(h.DB, h.Logger, ctx, owner, h.Cfg.DefaultTimezone)
	loc, terr := time.LoadLocation(tzName)
	if terr != nil {
		loc = time.UTC
	}

	// Load catalog ONCE — same shape every day, no reason to reparse per
	// day. Reject the whole request if a catalog row is malformed
	// (safer than partially backfilling a corrupt catalog snapshot).
	catalog, err := h.loadEvaluatorCatalog(ctx)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "awards backfill catalog load failed", err)
	}

	start := time.Now()
	resp := awardsBackfillResp{}
	now := time.Now().UTC()
	// Walk oldest → newest so the streak walker (called on the next
	// /awards read) sees a monotonically-growing history rather than
	// backwards-in-time inserts. Both orders work — the PK dedupe is
	// order-independent — but this order also means partial failures
	// leave a contiguous prefix instead of scattered rows.
	for d := req.Days - 1; d >= 0; d-- {
		at := now.AddDate(0, 0, -d)
		payload, err := h.buildAwardsPayloadAt(ctx, owner, at)
		if err != nil {
			// One bad day shouldn't kill the whole batch; log + skip.
			h.Logger.Warn("awards backfill payload build failed",
				"user", owner, "at", at, "err", err)
			resp.Skipped++
			continue
		}
		awards := labels.EvaluateAll(payload, catalog)
		items := make([]db.AwardLogItem, 0, len(awards))
		for _, a := range awards {
			pt := db.ResolvePeriod(string(a.Kind), specPeriodDefault(catalog, a.ID))
			if pt == db.PeriodLifetime || pt == db.PeriodAuto {
				continue
			}
			items = append(items, db.AwardLogItem{LabelID: a.ID, PeriodType: pt})
		}
		if len(items) == 0 {
			resp.DaysProcessed++
			continue
		}
		wrote, err := h.DB.LogAwards(ctx, owner, items, loc, at)
		if err != nil {
			// Log + skip; don't abort — the FE tool historically kept
			// going through per-day errors too.
			h.Logger.Warn("awards backfill ledger write failed",
				"user", owner, "at", at, "err", err)
			resp.Skipped++
			continue
		}
		resp.DaysProcessed++
		resp.RowsWritten += wrote
	}
	resp.TookMs = time.Since(start).Milliseconds()
	return c.JSON(http.StatusOK, resp)
}
