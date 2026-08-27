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
//
// Every route registers through internal/shared/apiroute so its request and
// response Go TYPES reach the OpenAPI generator, and carries its prose at the
// registration via .Doc(...) so the docs cannot outlive the route.
func Register(e *echo.Echo, h *Handler) {
	// boom-mwp-streaks: award-ledger endpoints. FE evaluator POSTs the
	// firing labels after each evaluate() run; server upserts one row
	// per (user, label, period_start) so the streak walker can render
	// "3x NIGHT WATCH" badges on the LabelChip. Public variant so
	// profile viewers see the same badges.
	//
	// /awards/log uses POSTLimit rather than the plain POST form: it binds
	// its body at AwardsLogBodyLimit (128 KiB) deliberately — a historical
	// batch carries one item per firing label per replayed day — and plain
	// POST hard-codes the 4 KiB apihelpers.BodyLimitSmall. POSTLimit carries
	// the original ceiling across, so moving it onto the seam typed the route
	// without shrinking the accepted body 32x.
	apiroute.POSTLimit(e, "/api/v1/users/current/awards/log",
		AwardsLogBodyLimit, h.AwardsLog).
		Doc("Award ledger write",
			"Records the labels that fired for the caller into the award ledger — one "+
				"upserted row per (user, label, period start) — and answers "+
				"{received, written}. `received` counts the items as submitted, `written` "+
				"counts the rows the upsert actually inserted, so the two differ whenever "+
				"rows were dropped or already existed. The write is IDEMPOTENT within a "+
				"period: replaying the same batch writes nothing the second time. Items are "+
				"filtered, not validated: an entry with an empty labelId, or a periodType "+
				"outside 'daily'|'weekly'|'monthly' (lifetime labels are not ledger "+
				"eligible), is silently skipped so one bad row cannot fail the batch. "+
				"Optional `at` (RFC3339) buckets the batch against a historical instant "+
				"instead of now, for replaying past days; it is 400 if unparseable and 400 "+
				"if more than an hour in the future, since a future period would poison the "+
				"streak walker. Period boundaries are computed in the caller's resolved "+
				"timezone, never assumed UTC. Body cap: 128 KiB.")

	apiroute.GET(e, "/api/v1/users/current/awards/streaks", h.AwardsStreaks).
		Doc("Label streak counts",
			"Current streak length for every label the caller has ever earned, as a FLAT "+
				"map of label id to an integer — deliberately the smallest shape a chip "+
				"renderer can look up by id, not a nested per-period object. The value is "+
				"the number of consecutive periods, counting back from and INCLUDING the "+
				"current one, in which the label fired; a label that did not fire in the "+
				"current period reports 0, which is how a broken streak is represented "+
				"rather than by omitting the key. Labels with no history at all are absent "+
				"entirely, so an empty object is a valid answer. Period boundaries use the "+
				"caller's resolved timezone. Sends Cache-Control: private, max-age=60 — "+
				"streaks can only change once per period, so a fresh mount does not need to "+
				"re-hit the database.")

	apiroute.GET(e, "/api/v1/users/current/awards/ledger", h.AwardsLedger).
		Doc("Award ledger inspector",
			"Raw award_ledger rows for the caller with the label's name and kind joined in "+
				"— the debug/admin view behind the AdminTab, not a rendering endpoint. "+
				"Optional ?label=<id> narrows to a single label. ?limit caps the rows and "+
				"defaults to 500, which is also the hard maximum: a limit that is "+
				"non-numeric, zero, negative or above 500 is silently clamped to 500 rather "+
				"than rejected, so a bad value never 400s the request. The response echoes "+
				"the effective `limit` alongside `rows` so a caller can tell \"that is "+
				"everything\" from \"there is more behind a larger limit\". Sends "+
				"Cache-Control: private, max-age=30.")

	apiroute.GET(e, "/api/public/profile/:slug/awards/streaks", h.PublicAwardsStreaks).
		Doc("Label streak counts (public)",
			"The same flat label-id-to-streak-count map as the owner endpoint, for the user "+
				"behind a public profile slug, so a visitor sees the same streak badges the "+
				"owner does. The target user is resolved from the slug; an unknown slug is "+
				"404 (\"profile not found\"). Note this route gates on the slug EXISTING "+
				"only — unlike /api/public/profile/{slug}/awards it does not re-check the "+
				"public_profile_enabled flag. Streak counts are computed in the profile "+
				"OWNER's timezone, not the viewer's. Sends Cache-Control: private, "+
				"max-age=60.")

	// boom-hc6.3: server-side award evaluation. Replaces the client-side
	// evaluate() call. Own variant WRITES the ledger; public variant does not.
	apiroute.GET(e, "/api/v1/users/current/awards", h.OwnAwards).
		Doc("Award evaluation (own)",
			"Evaluates the entire label catalog against the caller's last 60 days of "+
				"activity and returns the awards that fire. This is the server-authoritative "+
				"replacement for the old client-side evaluator, so it is NOT a pure read: as "+
				"a side effect it writes an award-ledger row for every firing non-lifetime "+
				"label, which is what advances the caller's streaks. Lifetime labels are "+
				"evaluated and returned but never written to the ledger. A ledger-write "+
				"failure is logged and swallowed — the awards still come back, since the "+
				"caller already has what it needs to display. The evaluated payload is the "+
				"same scrubbed 60-day window the public profile renders, so a label seen "+
				"here and one seen on the profile agree. Sends Cache-Control: private, "+
				"max-age=30.")

	apiroute.GET(e, "/api/public/profile/:slug/awards", h.PublicAwards).
		Doc("Award evaluation (public)",
			"The same evaluated award list as the owner endpoint, for the user behind a "+
				"public profile slug. This variant is a PURE READ: it deliberately writes no "+
				"ledger rows, so a visitor loading someone's profile cannot advance — or "+
				"reset — that person's streaks. Requires both that the slug resolves and "+
				"that the owner still has public sharing enabled; either failing is the same "+
				"404 (\"This profile isn't public\"), so the endpoint does not reveal whether "+
				"a slug exists but was turned off. Sends Cache-Control: public, max-age=180 "+
				"— longer than the owner endpoint because a profile visitor tolerates "+
				"staleness that the owner would not.")

	// boom-hc6.5.1: historical replay. Unblocks the full delete of the
	// client-side evaluator (which was the AdminTab backfill's last use).
	apiroute.POST(e, "/api/v1/users/current/awards/backfill", h.AwardsBackfill).
		Doc("Historical award replay",
			"Replays award evaluation over the caller's history: for each of the last "+
				"{days} days it rebuilds the activity payload as-of that day, evaluates the "+
				"whole label catalog against it, and writes ledger rows dated to that day — "+
				"which is how a freshly-seeded account gets real streak history instead of "+
				"\"starts today\". `days` under 1 is 400; above 365 it is silently clamped "+
				"to 365 rather than rejected. Days are walked oldest to newest so a partial "+
				"run leaves a contiguous prefix, and the whole operation is idempotent — "+
				"re-running writes nothing new. A day whose payload or ledger write fails is "+
				"counted in `skipped` and the batch continues; a malformed label catalog is "+
				"the one condition that aborts before any write. The response is a summary "+
				"only ({daysProcessed, rowsWritten, skipped, tookMs}) — per-day awards are "+
				"deliberately not returned. This runs INLINE and rebuilds one aggregation "+
				"payload per day, so a 365-day request is slow and the response arrives only "+
				"when the replay has finished. Body cap: 4 KiB.")
}
