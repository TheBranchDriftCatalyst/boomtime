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
	apiroute.GET(e, "/api/v1/users/current/curation", h.ListCuration)
	// BodyLimitMedium (64 KiB): a curation rule can carry a long condition blob,
	// and this endpoint bound at Medium before the seam.
	apiroute.POSTLimit(e, "/api/v1/users/current/curation", apihelpers.BodyLimitMedium, h.CreateCuration)
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/curation/:id", h.DeleteCuration)
	apiroute.GET(e, "/api/v1/users/current/curation/:id/affected", h.CurationAffected)
	// NOT on the typed seam: /preview answers two DIFFERENT payload shapes
	// discriminated on `action` — the rename branch carries
	// sqlUpdate/sqlDelete with []db.AffectedRowDiff rows, the hide branch
	// carries sqlDeleteRows/sqlDeleteRule with []db.PurgeRowDiff rows. One
	// Go struct cannot express both honestly (`affectedRows` has a different
	// element TYPE per branch), so it stays on plain echo rather than being
	// flattened into a type that lies.
	e.GET("/api/v1/users/current/curation/:id/preview", h.ApplyRenamePreview)
	apiroute.POSTNoBody(e, "/api/v1/users/current/curation/:id/apply", h.ApplyRename)
	apiroute.POSTNoBody(e, "/api/v1/users/current/curation/:id/purge", h.PurgeHidden)
	// boom-dfd: pause/resume a rule without deleting it. Body optional —
	// empty POST flips, {"enabled":true|false} sets an exact value.
	apiroute.POST(e, "/api/v1/users/current/curation/:id/toggle", h.ToggleCuration)

	// boom-364.3: DB-backed labels catalog. Public GET returns the whole
	// catalog for the FE evaluator + admin table; admin CRUD lets a
	// whitelisted operator edit labels + the global gen-config live.
	apiroute.GET(e, "/api/v1/labels/catalog", h.LabelsCatalog)
	// NOT on the typed seam — the three body-binding admin routes below keep
	// h.requireAdmin BEFORE the body read (see the package doc in handler.go:
	// "a non-admin request never costs a body allocation"). The seam binds
	// first, which would turn a non-admin's malformed body from 403 into 400
	// and an oversized one from 403 into 413. POST /admin/labels additionally
	// answers 201 Created, which no registrar expresses (POST is 200,
	// Accepted is 202).
	e.POST("/api/v1/admin/labels", h.AdminCreateLabel)
	e.PATCH("/api/v1/admin/labels/:id", h.AdminUpdateLabel)
	// DELETE binds no body, so requireAdmin still runs first — it moves onto
	// the seam.
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/admin/labels/:id", h.AdminDeleteLabel)
	e.PATCH("/api/v1/admin/label-gen-config", h.AdminUpdateLabelGenConfig)
	// text/plain via c.Blob — off the seam by rule.
	e.GET("/api/v1/admin/labels/seed.sql", h.AdminLabelsSeedSQL)
}
