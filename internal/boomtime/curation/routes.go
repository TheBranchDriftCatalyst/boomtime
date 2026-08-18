// routes.go — Echo route registrations for the curation domain
// (gaka-8tn phase 5b). Extracted from internal/server/server.go's
// registerCurationRoutes + the admin-labels chunk inside
// registerMiscRoutes so those functions collapse toward N domain-
// Register calls.
//
// URL patterns are byte-identical to the pre-refactor set — this is a
// pure package move, not a route rename. The tests already assert
// specific 404s / 400s / status-code invariants against these strings;
// changing any of them is out of scope for phase 5b.
package curation

import "github.com/labstack/echo/v5"

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
	e.GET("/api/v1/users/current/curation", h.ListCuration)
	e.POST("/api/v1/users/current/curation", h.CreateCuration)
	e.DELETE("/api/v1/users/current/curation/:id", h.DeleteCuration)
	e.GET("/api/v1/users/current/curation/:id/affected", h.CurationAffected)
	e.GET("/api/v1/users/current/curation/:id/preview", h.ApplyRenamePreview)
	e.POST("/api/v1/users/current/curation/:id/apply", h.ApplyRename)
	e.POST("/api/v1/users/current/curation/:id/purge", h.PurgeHidden)
	// gaka-dfd: pause/resume a rule without deleting it. Body optional —
	// empty POST flips, {"enabled":true|false} sets an exact value.
	e.POST("/api/v1/users/current/curation/:id/toggle", h.ToggleCuration)

	// gaka-364.3: DB-backed labels catalog. Public GET returns the whole
	// catalog for the FE evaluator + admin table; admin CRUD lets a
	// whitelisted operator edit labels + the global gen-config live.
	e.GET("/api/v1/labels/catalog", h.LabelsCatalog)
	e.POST("/api/v1/admin/labels", h.AdminCreateLabel)
	e.PATCH("/api/v1/admin/labels/:id", h.AdminUpdateLabel)
	e.DELETE("/api/v1/admin/labels/:id", h.AdminDeleteLabel)
	e.PATCH("/api/v1/admin/label-gen-config", h.AdminUpdateLabelGenConfig)
	e.GET("/api/v1/admin/labels/seed.sql", h.AdminLabelsSeedSQL)
}
