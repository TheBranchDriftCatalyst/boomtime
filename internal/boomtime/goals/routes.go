// routes.go — Echo route registrations for the goals domain
// (boom-8tn phase 2b). Extracted from internal/server/server.go's
// registerGoalRoutes so the server's route file collapses to N
// domain-Register calls.
//
// URL patterns are byte-identical to the pre-refactor set. The
// /goals/progress batched endpoint MUST be registered BEFORE the
// /goals/:id param route so Echo picks the exact-match handler for
// path collisions (spaces/preview pins the same invariant).
//
// Registration goes through internal/shared/apiroute rather than the
// bare e.GET/e.POST helpers: that is what captures each handler's
// request and response TYPES for the OpenAPI spec. Walking the router
// afterwards yields only paths, so a plain e.POST can never be
// documented as more than `{"type":"object"}`.
//
// Each registration also carries its own prose via .Doc(summary,
// description). The documentation and the route are THE SAME CALL, so
// deleting a route deletes its docs and a route that was never
// registered was never documented — there is no parallel spec file to
// forget.
package goals

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
)

// Register wires the goals domain endpoints onto e. Handler must be
// non-nil.
func Register(e *echo.Echo, h *Handler) {
	apiroute.GET(e, "/api/v1/users/current/goals", h.ListGoals).
		Doc("Goal list",
			"Every composite goal the caller owns, newest first (created_at DESC). A goal "+
				"is a named predicate tree over the caller's activity: the row carries its "+
				"opaque `spec` JSONB, the `enabled` flag, the `public` flag that opts it into "+
				"the embeddable goal widgets, and the two progress-cache columns "+
				"(last_progress / last_evaluated_at, either of which may be null when the "+
				"goal has never been evaluated). Progress itself is NOT computed here — use "+
				"/goals/progress for the batch or /goals/{id}/progress for one.")

	apiroute.POST(e, "/api/v1/users/current/goals", h.CreateGoal).
		Doc("Goal creation",
			"Creates a goal from {name, description?, spec, public?}. `name` and `spec` are "+
				"both required (400 otherwise). The spec is parsed and validated strictly "+
				"before any DB write and the validator's own error text is returned verbatim "+
				"in the 400 so the author can correct the predicate tree. A duplicate "+
				"(owner, name) is 409, not 400. `public` defaults to false — a private goal "+
				"is never rendered by the public goal widgets. Request body is capped at "+
				"4 KiB: a spec that needs more than that has too many predicates.")

	apiroute.GET(e, "/api/v1/users/current/goals/progress", h.GetAllGoalProgress).
		Doc("Batched goal progress",
			"Evaluated progress for every ENABLED goal, keyed by goal id — one HTTP round "+
				"trip per dashboard render instead of one per tile. Disabled goals are "+
				"omitted, and so is any goal whose spec fails to evaluate: a broken spec is "+
				"logged and skipped rather than failing the whole batch, so a missing key "+
				"means \"unknown / no data\" rather than \"zero\". Freshness is decided per "+
				"goal against its own last_evaluated_at (60s TTL); stale rows are recomputed "+
				"during this call and their cache written back.")

	apiroute.GET(e, "/api/v1/users/current/goals/:id", h.GetGoal).
		Doc("Goal detail",
			"One goal by id, wrapped as {goal}. Owner-scoped: an id belonging to another "+
				"user returns 404 rather than 403, so the response cannot be used as an "+
				"existence oracle for someone else's goal ids.")

	apiroute.PATCH(e, "/api/v1/users/current/goals/:id", h.UpdateGoal).
		Doc("Goal update",
			"Partial update — every body field is optional and only the fields actually "+
				"present are written, so omitting one leaves it untouched (distinct from "+
				"sending an empty string or false). Supplying `spec` re-runs strict "+
				"validation (400 carrying the validator text on failure) and clears the "+
				"cached progress atomically, so the next progress read recomputes. An empty "+
				"`name` is 400; a rename colliding with another of the caller's goals is "+
				"409; an id that is not the caller's is 404. Request body is capped at 4 KiB.")

	// 204 on success — DeleteGoal writes no body, so it registers through
	// the NoContent form rather than inventing a response type it never
	// returns.
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/goals/:id", h.DeleteGoal).
		Doc("Goal deletion",
			"Permanently removes one goal and its cached progress. Answers 204 with no body "+
				"on success. An id that does not exist — and an id that exists but belongs "+
				"to another user — are both 404, deliberately indistinguishable so the "+
				"endpoint is not an existence oracle for other users' goal ids.")

	apiroute.POST(e, "/api/v1/users/current/goals/:id/toggle", h.ToggleGoal).
		Doc("Goal enable toggle",
			"Pauses or resumes a goal and returns {enabled} — the state AFTER the write. "+
				"The body is optional: send nothing to FLIP the current state, or "+
				"{\"enabled\": bool} to SET it exactly. The exact-set form is idempotent "+
				"(setting the value it already holds still answers 200). A disabled goal is "+
				"skipped by the batched progress endpoint. 404 for an id that is not the "+
				"caller's.")

	apiroute.GET(e, "/api/v1/users/current/goals/:id/progress", h.GetGoalProgress).
		Doc("Goal progress",
			"Evaluated progress for one goal. The body is a BARE Progress object — no "+
				"{goal:...} style envelope, unlike the other single-goal routes. Served from "+
				"cache while last_evaluated_at is within 60 seconds, otherwise recomputed "+
				"and the cache written back; a spec PATCH or a heartbeat ingest nulls "+
				"last_evaluated_at so the next read always recomputes. Unlike the batch "+
				"endpoint this does NOT swallow evaluation failures: a spec that no longer "+
				"validates comes back as 400 carrying the validator's message. 404 for an "+
				"id that is not the caller's.")
}
