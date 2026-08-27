// routes.go — Echo route registrations for the curation domain
// (boom-8tn phase 5b). Extracted from internal/server/server.go's
// registerCurationRoutes + the admin-labels chunk inside
// registerMiscRoutes so those functions collapse toward N domain-
// Register calls.
//
// URL patterns are byte-identical to the pre-refactor set — this is a
// pure package move, not a route rename. The tests already assert
// specific 404s / 400s / status-code invariants against these strings;
// changing any of them is out of scope for phase 5b.
package curation

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// Register wires the curation-domain endpoints onto e. Handler must be
// non-nil. Registration order preserves the pre-refactor sequence inside
// registerCurationRoutes + the labels chunk of registerMiscRoutes so
// any test that hit these routes previously still hits them in the same
// order — Echo picks the first registered matcher for overlapping
// patterns, so preserving order preserves matching. In particular
// /curation/:id/preview must register BEFORE the /:id path so the
// static suffix wins against the param matcher.
//
// Route inventory (two clusters — curation rules + labels catalog admin):
//
//	GET    /api/v1/users/current/curation                   (h.ListCuration)
//	POST   /api/v1/users/current/curation                   (h.CreateCuration)
//	DELETE /api/v1/users/current/curation/:id               (h.DeleteCuration)
//	GET    /api/v1/users/current/curation/:id/affected      (h.CurationAffected)
//	GET    /api/v1/users/current/curation/:id/preview       (h.ApplyRenamePreview)
//	POST   /api/v1/users/current/curation/:id/apply         (h.ApplyRename)
//	POST   /api/v1/users/current/curation/:id/purge         (h.PurgeHidden)
//	POST   /api/v1/users/current/curation/:id/toggle        (h.ToggleCuration)
//	GET    /api/v1/labels/catalog                           (h.LabelsCatalog)     PUBLIC
//	POST   /api/v1/admin/labels                             (h.AdminCreateLabel)
//	PATCH  /api/v1/admin/labels/:id                         (h.AdminUpdateLabel)
//	DELETE /api/v1/admin/labels/:id                         (h.AdminDeleteLabel)
//	PATCH  /api/v1/admin/label-gen-config                   (h.AdminUpdateLabelGenConfig)
//	GET    /api/v1/admin/labels/seed.sql                    (h.AdminLabelsSeedSQL)
func Register(e *echo.Echo, h *Handler) {
	// Curation rules (owner-scoped CRUD + destructive path triplet + toggle).
	// /preview registered BEFORE /:id so the static suffix wins against
	// Echo's param matcher.
	apiroute.GET(e, "/api/v1/users/current/curation", h.ListCuration).
		Doc("Curation rule list",
			"Every hide / rename / pin rule the owner has authored, newest first, "+
				"as {rules:[...]}. Nothing is filtered: this is the AUTHORING view, so "+
				"paused rules (enabled=false) are included too — the query-time loaders "+
				"do their own enabled filtering, this endpoint does not, and the UI needs "+
				"the paused rows to be able to un-pause them.")
	// BodyLimitMedium (64 KiB): a curation rule can carry a long condition blob,
	// and this endpoint bound at Medium before the seam.
	apiroute.POSTLimit(e, "/api/v1/users/current/curation", apihelpers.BodyLimitMedium, h.CreateCuration).
		Doc("Create a curation rule",
			"Creates one owner-scoped rule and returns it as {rule:...}. `axis` must be a "+
				"Heartbeats Explorer column; `action` is one of hide | rename | pin; "+
				"`matchType` is exact (the default) | regex | template. A rename needs a "+
				"non-empty `newValue` and may not target the `day` axis; matchType "+
				"'template' is rename-only, and `$1`-style backrefs in its target are "+
				"normalised to `\\1` before storing. Regex and template patterns are "+
				"compile-checked against Postgres up front, and `applyAtIngest` "+
				"(rename-only) is additionally compile-checked under Go RE2 because the "+
				"ingest rewriter is RE2 and a pattern that only Postgres accepts would "+
				"save and then silently never fire. Every one of those failures is a 400. "+
				"A plain rule is applied at QUERY time and stays fully reversible; "+
				"applyAtIngest also rewrites newly-ingested heartbeats and is excluded "+
				"from the query-time remap so it cannot double-apply. Success invalidates "+
				"the owner's cached dashboard aggregations. Request body cap: 64 KiB.")
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/curation/:id", h.DeleteCuration).
		Doc("Delete a curation rule",
			"Removes one owner-scoped rule; answers 204 with no body. Reversible in "+
				"effect because a query-time rule never mutated raw heartbeats: deleting a "+
				"hide rule un-hides its rows and deleting a rename rule restores the raw "+
				"values on the next fetch. An unknown id and another user's id are both "+
				"404 — deliberately indistinguishable, so the endpoint never confirms that "+
				"someone else's rule exists. Invalidates the owner's cached aggregations.")
	apiroute.GET(e, "/api/v1/users/current/curation/:id/affected", h.CurationAffected).
		Doc("Values a rule matches",
			"The DISTINCT RAW values this rule matches on its axis with their heartbeat "+
				"counts, plus (for renames) the value each one maps to. An audit view: the "+
				"values are read UNFILTERED, so no other curation rule hides or remaps them "+
				"first. Ordered by count descending and capped at 200 rows; `truncated` "+
				"reports that the cap clipped the list. Owner-scoped — an unknown or "+
				"foreign rule id is 404.")
	// ON the seam via WritesJSON: the handler keeps ownership of the write
	// because /preview answers two DIFFERENT payload shapes discriminated on
	// `action` — the rename branch carries sqlUpdate/sqlDelete with
	// []db.AffectedRowDiff rows, the hide branch carries
	// sqlDeleteRows/sqlDeleteRule with []db.PurgeRowDiff rows. The declared
	// curationPreviewResponse is the honest SUPERSET of both (every
	// branch-specific field optional), which documents the payload without
	// letting `omitempty` take over the wire and drop a legitimately-empty
	// before/after value.
	apiroute.WritesJSON[curationPreviewResponse](e, http.MethodGet, "/api/v1/users/current/curation/:id/preview", h.ApplyRenamePreview).
		Doc("Destructive-action preview",
			"Dry run of /apply or /purge for one rule: the exact SQL both statements "+
				"would execute plus a capped before/after diff, with nothing mutated. The "+
				"payload is a union discriminated on `action` — a rename rule returns "+
				"sqlUpdate + sqlDelete and rows shaped {id,before,after}; a hide rule "+
				"returns sqlDeleteRows + sqlDeleteRule and rows shaped {id,deleted}. "+
				"`affectedRows` is capped at 100 entries while `totalAffected` is the exact "+
				"uncapped count, so the modal can render its 'and N more…' footer from the "+
				"difference. The echoed `rule` block is identical on both branches. Any "+
				"other action (a pin rule) is 400. Owner-scoped; 404 for an unknown or "+
				"foreign id.")
	apiroute.POSTNoBody(e, "/api/v1/users/current/curation/:id/apply", h.ApplyRename).
		Doc("Apply a rename destructively",
			"DESTRUCTIVE. Rewrites the target column on every heartbeat row the rename "+
				"rule matches and then deletes the rule row itself, atomically in one "+
				"transaction, returning {rowsAffected, sqlRun, sqlUpdate, sqlDelete} — the "+
				"SQL verbatim as executed. Only rename rules qualify: a hide rule is 400 "+
				"(use /purge), and a PAUSED rule is 400 until it is re-enabled, so nobody "+
				"applies a rule they just switched off. Idempotent in effect — 0 matches "+
				"still succeeds with rowsAffected=0 and still removes the rule. Takes no "+
				"request body. Invalidates the owner's cached aggregations.")
	apiroute.POSTNoBody(e, "/api/v1/users/current/curation/:id/purge", h.PurgeHidden).
		Doc("Purge every row a hide rule matches",
			"The most destructive endpoint in this domain. DELETEs every heartbeat row "+
				"the hide rule matches and then the rule row itself, atomically in one "+
				"transaction, returning {rowsAffected, sqlRun, sqlDeleteRows, sqlDeleteRule} "+
				"— the SQL verbatim as executed. Unlike /apply, which rewrites values, this "+
				"destroys raw rows and deleting the rule afterwards cannot bring them back; "+
				"the FE gates it behind a type-the-rule-id confirmation. Only hide rules "+
				"qualify (a rename rule is 400), and a PAUSED rule is 400 until re-enabled. "+
				"Idempotent in effect — 0 matches still deletes the rule and returns "+
				"rowsAffected=0. Takes no request body. Invalidates the owner's cached "+
				"aggregations.")
	// boom-dfd: pause/resume a rule without deleting it. Body optional —
	// empty POST flips, {"enabled":true|false} sets an exact value.
	apiroute.POST(e, "/api/v1/users/current/curation/:id/toggle", h.ToggleCuration).
		Doc("Pause or resume a rule",
			"Flips or sets a rule's `enabled` flag without deleting it, answering "+
				"{enabled:bool} with the resulting state. The request body is OPTIONAL: an "+
				"empty POST flips the current value, {\"enabled\":true|false} writes an "+
				"exact one, and both are idempotent — sending the same state twice still "+
				"returns 200 with the current value. A paused rule stays in the rule list "+
				"but is skipped by the query-time hide/rename loaders, and /apply and "+
				"/purge refuse to run against it with 400. Owner-scoped; 404 for an unknown "+
				"id. Request body cap: 4 KiB. Invalidates the owner's cached aggregations.")

	// boom-364.3: DB-backed labels catalog. Public GET returns the whole
	// catalog for the FE evaluator + admin table; admin CRUD lets a
	// whitelisted operator edit labels + the global gen-config live.
	apiroute.GET(e, "/api/v1/labels/catalog", h.LabelsCatalog).
		Doc("Public labels catalog",
			"The entire award-label catalog plus the global authored image-generation "+
				"`systemPrompt`, as {systemPrompt, labels:[...]}. PUBLIC — no auth: the "+
				"catalog is global rather than per-user, and the systemPrompt is authored "+
				"prose, not a credential. The FE evaluator, the public profile page and the "+
				"hero / showcase widgets all read this, as does the admin editor (which "+
				"uses systemPrompt for its 'preview effective prompt' hint). Answered with "+
				"Cache-Control: public, max-age=60, so an admin edit becomes visible within "+
				"a minute.")
	// ON the seam via WritesJSON, NOT via the binding registrars. The three
	// body-reading admin routes below must keep h.requireAdmin BEFORE the body
	// read (see the package doc in handler.go: "a non-admin request never costs
	// a body allocation"). A binding registrar binds first, which would turn a
	// non-admin's malformed body from 403 into 400 and an oversized one from
	// 403 into 413 — and for PATCH there is no *Limit form at all, so it would
	// also shrink the handlers' 64 KiB cap to the seam's 4 KiB default.
	// WritesJSON declares the RESPONSE type while leaving the handler's own
	// bind, gate order and status code untouched; POST additionally answers
	// 201 Created, which it still does because the seam writes nothing here.
	// The cost is that the REQUEST schemas on these three stay undocumented.
	apiroute.WritesJSON[db.Label](e, http.MethodPost, "/api/v1/admin/labels", h.AdminCreateLabel).
		Doc("Create a label",
			"Admin only — the BOOM_ADMIN_USERS allowlist is checked BEFORE the body is "+
				"read, so an unauthenticated caller gets 401 and a non-admin gets 403 "+
				"without the request body ever being allocated. Body is a full label: `id`, "+
				"`kind`, `label` and a `condition` JSONB object are all required, and the "+
				"condition is schema-validated up front — a malformed one is rejected with "+
				"400 carrying a JSON-pointer path to the offending field, rather than "+
				"sitting in the DB always- or never-firing until someone notices. An `id` "+
				"that already exists is a 400, never a silent overwrite: PATCH is the edit "+
				"path. On success answers 201 Created with the stored label re-read from "+
				"the DB. Request body cap: 64 KiB.")
	apiroute.WritesJSON[db.Label](e, http.MethodPatch, "/api/v1/admin/labels/:id", h.AdminUpdateLabel).
		Doc("Update a label",
			"Admin only, gate checked before the body is read. Partial merge: only the "+
				"keys present in the JSON are written, and a present-but-empty string "+
				"CLEARS the field (so {\"glyph\":\"\"} removes a glyph). An `id` in the body "+
				"is ignored — renaming would orphan label_images rows and persisted award "+
				"history, so DELETE + POST is the rename path. A `condition` in the body is "+
				"schema-validated before the write, same 400-with-JSON-pointer as create; "+
				"omitting it leaves the stored condition untouched. An unknown id is 404 "+
				"(POST creates). Returns the label freshly re-read after the write. Request "+
				"body cap: 64 KiB.")
	// DELETE binds no body, so requireAdmin still runs first — it moves onto
	// the seam.
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/admin/labels/:id", h.AdminDeleteLabel).
		Doc("Delete a label",
			"Admin only. Idempotent: 204 with no body whether or not the row existed. The "+
				"matching label_images row is deleted too, but best-effort — a failure "+
				"there is logged and does NOT fail the request, because orphaned image "+
				"bytes render nowhere once the catalog row is gone.")
	apiroute.WritesJSON[genConfigResponse](e, http.MethodPatch, "/api/v1/admin/label-gen-config", h.AdminUpdateLabelGenConfig).
		Doc("Label generation prompt",
			"Admin only, gate checked before the body is read. Writes the single global "+
				"`systemPrompt` that is prepended to every per-label image prompt and "+
				"echoes it back as {systemPrompt}. The key is required — omitting it is a "+
				"400 — but it may be the empty string, which CLEARS the prefix: the "+
				"generation worker then sends only the label's own optimizedPrompt to "+
				"comfyui. Request body cap: 64 KiB.")
	// text/plain via c.Blob — declared through apiroute.Raw so the spec
	// advertises the real media type instead of a JSON object.
	apiroute.Raw(e, http.MethodGet, "/api/v1/admin/labels/seed.sql", "text/plain; charset=utf-8", http.StatusOK, h.AdminLabelsSeedSQL).
		Doc("Labels catalog SQL dump",
			"Admin only. Renders the live labels table and the singleton gen-config row "+
				"as a `-- +goose Up` migration body — one INSERT … ON CONFLICT (id) DO "+
				"UPDATE covering every label, followed by an UPDATE of the system prompt — "+
				"so a catalog hand-tuned on a running instance can be committed back as a "+
				"reviewable migration that a fresh install replays. Not the everyday flow "+
				"(the admin CRUD is); this is for backporting operator edits into a diff. "+
				"The response is text/plain, NOT JSON, and carries Content-Disposition: "+
				"attachment; filename=\"labels_seed.sql\" so hitting the URL directly "+
				"downloads a file.")
}
