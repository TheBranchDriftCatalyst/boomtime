package awards

import (
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
)

// Register wires the awards domain's routes onto e. Called from
// internal/server/server.go as `awards.Register(e, h.Awards)` — replacing
// the awards-cluster lines that used to be inline inside registerAuthRoutes.
// Registration order is preserved verbatim from the pre-refactor server.go
// so tests + traffic see the exact same matching.
//
// Route inventory (three clusters — streak ledger + evaluator + backfill):
//
//	POST   /api/v1/users/current/awards/log        (h.AwardsLog)
//	GET    /api/v1/users/current/awards/streaks    (h.AwardsStreaks)
//	GET    /api/v1/users/current/awards/ledger     (h.AwardsLedger)
//	GET    /api/public/profile/:slug/awards/streaks (h.PublicAwardsStreaks)
//	GET    /api/v1/users/current/awards            (h.OwnAwards)          [boom-hc6.3]
//	GET    /api/public/profile/:slug/awards        (h.PublicAwards)       [boom-hc6.3]
//	POST   /api/v1/users/current/awards/backfill   (h.AwardsBackfill)     [boom-hc6.5.1]
func Register(e *echo.Echo, h *Handler) {
	// boom-mwp-streaks: award-ledger endpoints. FE evaluator POSTs the
	// firing labels after each evaluate() run; server upserts one row
	// per (user, label, period_start) so the streak walker can render
	// "3x NIGHT WATCH" badges on the LabelChip. Public variant so
	// profile viewers see the same badges.
	//
	// /awards/log stays on plain e.POST: it binds its body at 128 KiB
	// deliberately (a historical batch carries one item per firing label
	// per replayed day), and apiroute.POST hardcodes the 4 KiB
	// apihelpers.BodyLimitSmall. Moving it would shrink the accepted body
	// 32x — a wire-behaviour change, not a typing refactor.
	e.POST("/api/v1/users/current/awards/log", h.AwardsLog)
	apiroute.GET(e, "/api/v1/users/current/awards/streaks", h.AwardsStreaks)
	apiroute.GET(e, "/api/v1/users/current/awards/ledger", h.AwardsLedger)
	apiroute.GET(e, "/api/public/profile/:slug/awards/streaks", h.PublicAwardsStreaks)
	// boom-hc6.3: server-side award evaluation. Replaces the client-side
	// evaluate() call. Own variant WRITES the ledger; public variant does not.
	apiroute.GET(e, "/api/v1/users/current/awards", h.OwnAwards)
	apiroute.GET(e, "/api/public/profile/:slug/awards", h.PublicAwards)
	// boom-hc6.5.1: historical replay. Unblocks the full delete of the
	// client-side evaluator (which was the AdminTab backfill's last use).
	apiroute.POST(e, "/api/v1/users/current/awards/backfill", h.AwardsBackfill)
}
