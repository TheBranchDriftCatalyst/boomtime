package widgets

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
)

// Register wires the widget + widget-def + badge endpoints onto the passed-in
// Echo instance. The route strings + registration ORDER are byte-identical
// to the pre-Phase-3 mix in internal/server/server.go (registerMiscRoutes) —
// the /widget/svg/:uuid/named route MUST be registered BEFORE the generic
// /widget/svg/:uuid/:kind so it wins path matching (Echo picks the first
// registered matcher for overlapping patterns).
//
// Every route goes through apiroute so the OpenAPI spec is generated from the
// Go types rather than stubbed. The three SVG endpoints use apiroute.Raw with
// the real media type — they answer image/svg+xml via c.Blob /
// apihelpers.CachedBlob, and documenting them as a JSON object would be
// actively wrong rather than merely vague. The two widget-def WRITE routes use
// the explicit-limit registrars: both bound their body at
// apihelpers.BodyLimitMedium (64 KiB) because the inline widget spec is allowed
// up to widgetDefMax (32 KiB), and the plain POST/PATCH forms would bind at
// BodyLimitSmall (4 KiB) — silently 413ing compositions that are legal today.
func Register(e *echo.Echo, h *Handler) {
	// Badges
	apiroute.GET(e, "/badge/link/:project", h.BadgeLink).
		Doc("Badge URL for a project",
			"Upserts the caller's badge link for the project named in the path "+
				"and returns its stable public URL as {badgeUrl}. Idempotent: "+
				"re-minting the same project returns the same uuid. The project "+
				"string is NOT validated against the caller's heartbeats — any "+
				"value mints a link, and a project with no activity simply "+
				"renders a zero badge.")
	// BadgeSvg proxies shields.io and answers c.Blob(image/svg+xml) — not JSON.
	apiroute.Raw(e, http.MethodGet, "/badge/svg/:svg", "image/svg+xml", http.StatusOK, h.BadgeSvg).
		Doc("Public project-time badge",
			"Renders the badge for a minted badge uuid by proxying shields.io "+
				"with the project as the label and the last `days` (default 7) of "+
				"coding time as the message. PUBLIC — the uuid is the capability, "+
				"no auth header is read. Answers image/svg+xml. 400 on an "+
				"unparseable uuid; 404 both when the uuid is unknown AND when the "+
				"badge's project is on the owner's hide list, so an outsider "+
				"cannot tell curated-away badges from nonexistent ones; 502 when "+
				"shields.io is unreachable or answers non-200 (the upstream body "+
				"is never echoed back).")

	// Embeddable widgets (boom-hsj)
	apiroute.GET(e, "/api/v1/users/current/widgets/link", h.WidgetLink).
		Doc("Widget link for a scope",
			"Upserts the caller's embeddable-widget link for one scope and "+
				"returns {widgetBaseUrl, linkId}; append /<kind> to the base URL "+
				"to get a renderable SVG. Query `scopeType` must be one of "+
				"user | project | space. `user` ignores scopeRef (account-wide); "+
				"`project` requires a scopeRef that exists for the caller, or is "+
				"the target of an exact rename rule (the renderer expands it back "+
				"to the source project at query time); `space` requires a numeric "+
				"space id the caller owns. Idempotent per (owner, scopeType, "+
				"scopeRef). 400 on an unknown scopeType or a non-numeric space id; "+
				"404 on an unknown project or space.")
	apiroute.GET(e, "/api/v1/users/current/widgets/links", h.WidgetLinkList).
		Doc("Minted widget links",
			"Every widget link the caller has minted, as {links: [...]}, newest "+
				"first. Each entry carries its scope (with the space's display "+
				"name resolved for space scopes), creation time, the last time the "+
				"public SVG was requested, and the referring origins that fetched "+
				"it with per-origin counts (retained for the 20 most recent "+
				"origins). Powers the Settings widgets panel. Owner-scoped.")
	// Roll takes no request body — the link id is in the path.
	apiroute.POSTNoBody(e, "/api/v1/users/current/widgets/link/:id/roll", h.WidgetLinkRoll).
		Doc("Widget link uuid rotation",
			"Mints a fresh uuid for the same (owner, scope) and returns the new "+
				"{widgetBaseUrl, linkId}. DESTRUCTIVE for anything already "+
				"embedded: the old uuid starts 404ing immediately, which is "+
				"exactly the point — it is how a leaked or screenshotted README "+
				"embed is revoked. The scope and created-at are preserved, so the "+
				"link keeps its meaning in Settings. Owner-scoped: another "+
				"account's link id 404s rather than rolling. Also sweeps the "+
				"owner's cached SVG bytes. 400 on an unparseable id.")

	// Named/saved custom widget defs (boom-3nu) — /named MUST come before /:kind.
	// Both render SVG through apihelpers.CachedBlob; no JSON body either way.
	apiroute.Raw(e, http.MethodGet, "/widget/svg/:uuid/named", "image/svg+xml", http.StatusOK, h.WidgetDefSvg).
		Doc("Public saved-composition SVG",
			"Renders a saved widget-def by its DEF uuid — the defId returned by "+
				"POST /api/v1/users/current/widget-defs — not by a widget link. "+
				"PUBLIC, no auth. Answers image/svg+xml with "+
				"`Cache-Control: public, max-age=300, s-maxage=300` for camo/CDNs, "+
				"plus an in-process cache keyed by owner + def + params + the "+
				"def's updated-at, so editing the def re-renders on the next "+
				"fetch. Query: `days` (default 30, clamped to 1..366), `theme`, "+
				"`title`. v1 is USER-SCOPED only — no project or space slicing — "+
				"and always applies the owner's hide/rename curation, including "+
				"scrubbing hidden names out of the collapsed \"Other (N more)\" "+
				"tail. 400 on an unparseable uuid, 404 when no def has that id, "+
				"500 when a stored spec no longer validates against the current "+
				"layout/panel whitelist.")
	apiroute.Raw(e, http.MethodGet, "/widget/svg/:uuid/:kind", "image/svg+xml", http.StatusOK, h.WidgetSvg).
		Doc("Public embeddable widget SVG",
			"Renders one widget kind for a minted widget link. PUBLIC — the uuid "+
				"is the capability. `kind` is any target:\"both\" kind in the "+
				"widget spec registry (stats-card, stats-card-with-grade, "+
				"top-langs, top-projects, badge, activity-heatmap, punchcard, "+
				"momentum, profile-summary, social-card, cumulative-area, "+
				"deep-work, heatmap-projects, heatmap-languages, total-time-stat, "+
				"daily-avg-stat, current-streak-stat, longest-streak-stat, "+
				"active-days-stat, categories-chart, editors-chips, "+
				"platforms-chips, goal-progress, goal-ring, goal-list), or the "+
				"literal \"custom\" with the composition passed inline as "+
				"?spec=<base64 widget.Def>. Query: `days` (default 30, clamped to "+
				"1..366), `theme`, `title`. The session gap is fixed at the app "+
				"default (15 min) — public embeds accept no timeLimit override. "+
				"Answers image/svg+xml with "+
				"`Cache-Control: public, max-age=300, s-maxage=300` plus an "+
				"owner-prefixed in-process cache, so a curation change busts it. "+
				"Every render applies the owner's hide/rename rules and scrubs the "+
				"collapsed \"Other (N more)\" tail. 400 on an unparseable uuid or "+
				"an undecodable custom spec. 404 — never a partial render — when "+
				"the uuid is unknown, the kind is unknown, a project-scoped link's "+
				"project has been hidden, or a goal-* kind is requested on a "+
				"project/space-scoped link (goals are account-wide, so serving "+
				"them under a project URL would over-disclose). Each successful "+
				"lookup is recorded as a hit for the Settings origins view.")

	// Widget-def CRUD
	apiroute.GET(e, "/api/v1/users/current/widget-defs", h.ListWidgetDefs).
		Doc("Saved widget compositions",
			"Every widget-def the caller has saved, as {defs: [...]} — def id, "+
				"name, the raw JSONB spec, and created/updated timestamps. "+
				"Owner-scoped: another account's compositions are never returned.")
	apiroute.POSTLimit[widgetDefBody, widgetDefCreateResponse](
		e, "/api/v1/users/current/widget-defs", apihelpers.BodyLimitMedium, h.CreateWidgetDef).
		Doc("Save a widget composition",
			"Persists a builder composition under a caller-owned name and returns "+
				"{defId, url}, where url is the public "+
				"/widget/svg/<defId>/named render endpoint. Body is {name, spec} "+
				"with the spec inline as JSON (a widget.Def), so a client can "+
				"round-trip a builder Def without base64-encoding it. The spec is "+
				"validated against the same layout/panel whitelist the renderer "+
				"uses, before any insert. Request body cap is 64 KiB; the spec "+
				"itself is capped at 32 KiB (a 33 KiB spec inside a legal envelope "+
				"is a 400, not a 413). 400 on a blank name, an invalid or "+
				"oversized spec, or a duplicate name — names are unique per "+
				"account, so iterate on a saved composition with PATCH rather than "+
				"re-POSTing it.")
	apiroute.NoContentBodyLimit[widgetDefBody](
		e, http.MethodPatch, "/api/v1/users/current/widget-defs/:name",
		apihelpers.BodyLimitMedium, h.UpdateWidgetDef).
		Doc("Replace a saved composition's spec",
			"Replaces the spec of the caller's widget-def named in the path. The "+
				"body is the same {name, spec} shape as create, but only `spec` is "+
				"read — the URL identifies the row, so a name in the body is "+
				"ignored and this endpoint cannot rename a def. Answers 204 with "+
				"no body. Request body cap is 64 KiB, spec cap 32 KiB, validated "+
				"before the update runs. 400 on an invalid or oversized spec; 404 "+
				"when the CALLER owns no def by that name — the key is (owner, "+
				"name), so another account's def is invisible rather than "+
				"writable. On success the owner's cached SVG bytes are swept, "+
				"since they are keyed by def id and would otherwise keep serving "+
				"the pre-edit render.")
	// Delete carries no body, so the 204 registrar fits as-is.
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/widget-defs/:name", h.DeleteWidgetDef).
		Doc("Delete a saved composition",
			"Removes the caller's widget-def by name and answers 204. Owner-keyed: "+
				"a def another account owns under the same name 404s and is left "+
				"untouched. DESTRUCTIVE for anything embedding it — the def's "+
				"public /widget/svg/<defId>/named URL 404s immediately afterwards. "+
				"Sweeps the owner's cached SVG bytes.")
}
