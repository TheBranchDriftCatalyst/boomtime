package spaces

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
)

// Register wires the spaces + dashboard-layout domain's routes onto e. Called
// from internal/server/server.go as `spaces.Register(e, h.Spaces)` — replacing
// the inline registerSpaceRoutes helper plus the dashboard-layout routes that
// used to be scattered inside registerAuthRoutes. Registration order preserved
// verbatim from the pre-refactor server.go so tests + traffic see the same
// matching (in particular, the static /spaces/preview must register BEFORE
// /spaces/:id — Echo picks the first matcher for overlapping patterns).
//
// Every route goes through apiroute so its request/response TYPES reach the
// OpenAPI generator, and every route carries its prose at the registration via
// .Doc(...) so the documentation cannot outlive the route.
//
// Two of them use a non-default seam form on purpose:
//
//   - POST /spaces/:id/rules uses POSTLimit with apihelpers.BodyLimitMedium
//     (64 KiB) because that is the cap the handler applied by hand. The plain
//     POST form binds at 4 KiB and would silently start 413-ing bodies that
//     succeed today.
//   - PUT /dashboard/:scope uses WritesJSON: the handler hand-rolls its decode
//     to stay Content-Type-lenient and to keep its own 400 text, so the seam
//     must NOT own the bind. WritesJSON still DECLARES the response type, which
//     is what the spec needs.
func Register(e *echo.Echo, h *Handler) {
	// Spaces (named, scoped dashboards). Static /preview registered BEFORE
	// param /:id so it is not shadowed.
	apiroute.GET(e, "/api/v1/users/current/spaces", h.ListSpaces).
		Doc("Space list",
			"Every space the caller owns, as {spaces:[Space]}. A space is a named, "+
				"user-scoped filter — a set of membership rules that dashboards and stats "+
				"queries can be narrowed to via the ?space= query parameter. This endpoint "+
				"returns the space rows only (id, name, position); fetch one by id to see "+
				"its membership rules.")

	apiroute.POST(e, "/api/v1/users/current/spaces", h.CreateSpace).
		Doc("Space creation",
			"Creates an empty space from {name} and returns it as {space:Space} — note the "+
				"nesting, which the single-space GET deliberately does NOT use. `name` is "+
				"required (400 when empty) and unique per owner; a collision surfaces as a "+
				"400 (\"Could not create space (name may already exist)\"), NOT a 409, "+
				"because the DB error is deliberately not inspected before being flattened. "+
				"A successful create invalidates the owner's cached aggregations so "+
				"dashboards pick the new space up immediately. Body cap: 4 KiB.")

	apiroute.GET(e, "/api/v1/users/current/spaces/preview", h.SpacePreview).
		Doc("Membership rule preview",
			"Dry-runs a membership rule that has NOT been saved and returns the raw values "+
				"it would match together with their heartbeat counts, as "+
				"{values:[{value,count}], truncated}. Takes ?axis, ?matchValue and optional "+
				"?matchType ('exact' — the default — or 'regex'; anything else is 400). "+
				"`axis` must be one of the whitelisted explore columns, `matchValue` must be "+
				"non-empty, and a regex is compiled by Postgres first so a bad pattern is a "+
				"400 rather than a 500. At most 200 rows come back; `truncated` reports that "+
				"the limit clipped the result, so an empty-looking preview and a clipped one "+
				"are distinguishable. Owner-scoped — it never sees another user's heartbeats.")

	apiroute.GET(e, "/api/v1/users/current/spaces/:id", h.GetSpace).
		Doc("Space detail",
			"One space plus its membership rules. The space's own columns are INLINED at "+
				"the top level ({id, name, position, rules:[SpaceRule]}) rather than nested "+
				"under a \"space\" key the way the create response is — the two shapes "+
				"differ on purpose and clients must not assume one from the other. A "+
				"non-numeric id is 400; an id that is not the caller's is 404, the same "+
				"answer as an id that does not exist, so the endpoint cannot be used to "+
				"enumerate another user's space ids.")

	apiroute.NoContentBody(e, http.MethodPatch, "/api/v1/users/current/spaces/:id", h.UpdateSpace).
		Doc("Space rename and reorder",
			"Renames a space and/or moves it in the sidebar ordering from "+
				"{name?, position?}. Both fields are optional and omitting one leaves it "+
				"untouched. Answers 204 with no body — the updated row is NOT echoed back, "+
				"so re-read the space if you need it. A non-numeric id is 400, an id that is "+
				"not the caller's is 404, and a rejected write (for example a name that "+
				"collides) is a flat 400. A successful update invalidates the owner's cached "+
				"aggregations. Body cap: 4 KiB.")

	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/spaces/:id", h.DeleteSpace).
		Doc("Space deletion",
			"Permanently deletes one space AND every membership rule attached to it (the "+
				"rules cascade — there is no orphan cleanup step and no undo). Answers 204 "+
				"with no body. A non-numeric id is 400; an id that does not exist, and one "+
				"that belongs to another user, are both 404. Heartbeats are never touched: "+
				"a space is only a saved filter, so deleting it removes the view, not the "+
				"data. Invalidates the owner's cached aggregations.")

	// POSTLimit (not POST) — AddSpaceRule bound under BodyLimitMedium (64 KiB)
	// before it moved onto the seam, and the plain POST form would cap it at
	// 4 KiB. Passing the original limit keeps the wire contract identical.
	apiroute.POSTLimit(e, "/api/v1/users/current/spaces/:id/rules",
		apihelpers.BodyLimitMedium, h.AddSpaceRule).
		Doc("Membership rule creation",
			"Adds one membership rule to a space from {axis, matchValue, matchType} and "+
				"returns it as {rule:SpaceRule}. `axis` must be a whitelisted explore column "+
				"(400 with the offending value otherwise) and `matchValue` is required. "+
				"`matchType` defaults to 'exact' when omitted and accepts ONLY 'exact' or "+
				"'regex' — in particular the curation-only 'template' type is rejected here "+
				"with a 400. An id that is not the caller's is 404. A rule the database "+
				"refuses (an uncompilable regex being the usual cause) collapses to a fixed "+
				"400, \"Could not add space rule (invalid pattern?)\"; the raw Postgres "+
				"diagnostics are logged server-side and never returned. A successful add "+
				"invalidates the owner's cached aggregations. Body cap: 64 KiB — larger than "+
				"the API default so a long regex or template-style pattern still fits.")

	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/spaces/:id/rules/:rid", h.DeleteSpaceRule).
		Doc("Membership rule deletion",
			"Removes one membership rule from a space, narrowing what the space matches "+
				"from the next query onward. Answers 204 with no body. A non-numeric space "+
				"id or rule id is 400; a rule that does not exist, belongs to a different "+
				"space, or belongs to another user is 404 in every case. Invalidates the "+
				"owner's cached aggregations.")

	// Dashboard layout persistence (boom-keb). Per-user, per-scope. Scope
	// today is "public_profile"; the handler enforces the small allowlist so
	// a stale FE can't squat rows for future scopes.
	apiroute.GET(e, "/api/v1/users/current/dashboard/:scope", h.GetDashboardLayout).
		Doc("Saved dashboard layout",
			"The caller's persisted widget placement for one dashboard scope, wrapped as "+
				"{layout: <opaque JSON>}. The layout bytes are returned VERBATIM as they "+
				"were stored — the server never rewrites or validates the widget-kind ids "+
				"inside them, so a renderer must tolerate kinds it does not know. `scope` "+
				"must be one of the allowlist 'public_profile' or 'overview'; anything else "+
				"is 400 rather than an empty result, so a stale client cannot squat rows for "+
				"scopes that do not exist yet. When the caller has never saved a layout for "+
				"the scope the answer is 404 (\"no layout saved\"), which is the signal to "+
				"fall back to the built-in default layout — not an error to report.")

	// WritesJSON (not PUT) — PutDashboardLayout hand-rolls its decode to stay
	// Content-Type-lenient and keep its own 400 text; the seam's binder would
	// change both. WritesJSON leaves the write to the handler while still
	// declaring the response type for the spec.
	apiroute.WritesJSON[dashboardLayoutResponse](e, http.MethodPut, "/api/v1/users/current/dashboard/:scope", h.PutDashboardLayout).
		Doc("Dashboard layout upsert",
			"Stores the caller's widget placement for one dashboard scope from "+
				"{layout: <opaque JSON>} and returns the persisted envelope read back from "+
				"the database, so a client can settle its cache on exactly what was saved. "+
				"The inner `layout` value is preserved byte-for-byte; the widget-kind ids "+
				"inside are deliberately NOT validated server-side. `scope` must be "+
				"'public_profile' or 'overview' (400 otherwise) and the row is keyed "+
				"(owner, scope), so a PUT replaces the caller's own layout and can never "+
				"touch anyone else's. A missing or null `layout` key is 400 "+
				"(\"missing 'layout' field\") and unparseable input is 400 (\"invalid JSON "+
				"body\") — note that unlike the rest of the API this endpoint decodes "+
				"regardless of Content-Type. Bodies over 4 KiB are 413.")

	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/dashboard/:scope", h.DeleteDashboardLayout).
		Doc("Dashboard layout reset",
			"Drops the caller's saved layout row for one scope so subsequent renders fall "+
				"back to the built-in default. Idempotent: 204 with no body whether or not a "+
				"row existed, so a client need not check first. `scope` must be "+
				"'public_profile' or 'overview'; anything else is 400.")
}
